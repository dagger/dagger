package dagql

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
	set "github.com/hashicorp/go-set/v3"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/dagger/dagger/dagql/call"
)

type cacheTestOnReleaseInt struct {
	Int
	onRelease func(context.Context) error
}

type cacheTestLeaseProvider struct {
	nextLeaseID atomic.Int32
	releases    atomic.Int32
}

func (p *cacheTestLeaseProvider) WithOperationLease(ctx context.Context) (context.Context, func(context.Context) error, error) {
	leaseID := fmt.Sprintf("shared-%d", p.nextLeaseID.Add(1))
	return leases.WithLease(ctx, leaseID), func(context.Context) error {
		p.releases.Add(1)
		return nil
	}, nil
}

type cacheTestLeaseCheckedInt struct {
	Int
	onAttach func(context.Context) error
}

func (v cacheTestLeaseCheckedInt) AttachDependencyResults(
	ctx context.Context,
	_ AnyResult,
	_ func(AnyResult) (AnyResult, error),
) ([]AnyResult, error) {
	if v.onAttach == nil {
		return nil, nil
	}
	return nil, v.onAttach(ctx)
}

func (v cacheTestOnReleaseInt) OnRelease(ctx context.Context) error {
	if v.onRelease == nil {
		return nil
	}
	return v.onRelease(ctx)
}

type cacheTestOpaqueValue struct {
	value     string
	onRelease func(context.Context) error
}

func (v cacheTestOpaqueValue) OnRelease(ctx context.Context) error {
	if v.onRelease == nil {
		return nil
	}
	return v.onRelease(ctx)
}

type cacheTestSizedInt struct {
	Int
	sizeByIdentity       map[string]int64
	sizeSourceByIdentity map[string]*atomic.Int64
	usageIdentities      []string
	sizeCalls            *atomic.Int32
	sizeMayChange        bool
}

func (v cacheTestSizedInt) CacheUsageSize(_ context.Context, identity string) (int64, bool, error) {
	if v.sizeCalls != nil {
		v.sizeCalls.Add(1)
	}
	if v.sizeSourceByIdentity != nil {
		sizeSource, ok := v.sizeSourceByIdentity[identity]
		if !ok || sizeSource == nil {
			return 0, false, nil
		}
		return sizeSource.Load(), true, nil
	}
	sizeBytes, ok := v.sizeByIdentity[identity]
	if !ok {
		return 0, false, nil
	}
	return sizeBytes, true, nil
}

func (v cacheTestSizedInt) CacheUsageIdentities() []string {
	return append([]string(nil), v.usageIdentities...)
}

func (v cacheTestSizedInt) CacheUsageMayChange() bool {
	return v.sizeMayChange
}

type cacheTestOwnedDepsInt struct {
	Int
	ownedResults []AnyResult
}

type cacheTestSpanExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *cacheTestSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (*cacheTestSpanExporter) Shutdown(context.Context) error { return nil }
func (*cacheTestSpanExporter) ForceFlush(context.Context) error {
	return nil
}

type cacheTestLogExporter struct {
	mu   sync.Mutex
	logs []sdklog.Record
}

func (e *cacheTestLogExporter) Export(_ context.Context, logs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logs = append(e.logs, logs...)
	return nil
}

func (*cacheTestLogExporter) Shutdown(context.Context) error { return nil }
func (*cacheTestLogExporter) ForceFlush(context.Context) error {
	return nil
}

func (v *cacheTestOwnedDepsInt) AttachDependencyResults(
	ctx context.Context,
	_ AnyResult,
	attach func(AnyResult) (AnyResult, error),
) ([]AnyResult, error) {
	if v == nil {
		return nil, nil
	}
	attached := make([]AnyResult, 0, len(v.ownedResults))
	for i, dep := range v.ownedResults {
		if dep == nil {
			continue
		}
		attachedDep, err := attach(dep)
		if err != nil {
			return nil, err
		}
		v.ownedResults[i] = attachedDep
		attached = append(attached, attachedDep)
	}
	return attached, nil
}

func cacheTestUnwrapInt(t *testing.T, res AnyResult) int {
	t.Helper()
	v, ok := UnwrapAs[Int](res)
	assert.Assert(t, ok, "expected Int result, got %T", res)
	return int(v)
}

func cacheTestSharedResultEntryID(res AnyResult) string {
	shared := res.cacheSharedResult()
	if shared == nil || shared.id == 0 {
		return ""
	}
	return fmt.Sprintf("dagql.result.%d", shared.id)
}

type cacheTestQuery struct{}

func (cacheTestQuery) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

type cacheTestObject struct {
	Value             int
	onRelease         func(context.Context) error
	lazyEval          LazyEvalFunc
	dependencyResults []AnyResult
}

type noopTypeResolver struct{}

func (noopTypeResolver) ObjectType(string) (ObjectType, bool) {
	return nil, false
}

func (noopTypeResolver) ScalarType(string) (ScalarType, bool) {
	return nil, false
}

func TestCacheRejectsNilTypeResolver(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	key := cacheTestIntCall("nil-type-resolver")

	_, err = c.GetOrInitCall(ctx, "test-session", nil, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 1), nil
	})
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "type resolver is nil")

	_, _, err = c.lookupCacheForDigests(ctx, "test-session", nil, digest.FromString("nil-type-resolver"), nil)
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "type resolver is nil")

	_, err = c.AttachResult(ctx, "test-session", nil, cacheTestDetachedResult(key, NewInt(1)))
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "type resolver is nil")
}

func TestCacheRejectsEmptySessionIDForOwningEntrypoints(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := cacheTestIntCall("empty-session-id")

	_, err = c.GetOrInitCall(ctx, "", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 1), nil
	})
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "empty session ID")

	_, err = c.AttachResult(ctx, "", noopTypeResolver{}, cacheTestDetachedResult(key, NewInt(1)))
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "empty session ID")

	_, err = c.GetOrInitArbitrary(ctx, "", "empty-session-id", func(context.Context) (any, error) {
		return "value", nil
	})
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "empty session ID")
}

func TestAttachResultAllowsAlreadyAttachedResultWithoutFrame(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	key := cacheTestIntCall("attached-without-frame")

	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42), nil
	})
	assert.NilError(t, err)

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)
	assert.Assert(t, shared.id != 0)

	shared.storeResultCall(nil)

	attached, err := c.AttachResult(ctx, "test-session", noopTypeResolver{}, res)
	assert.NilError(t, err)
	assert.Equal(t, attached, res)
}

func TestCaptureSessionLazySpanContextFirstWriterWinsAndRelease(t *testing.T) {
	t.Parallel()

	baseCtx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctx := ContextWithCache(baseCtx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("telemetry-span-owner")
	spanCtxA := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{1},
	})
	ctxA := trace.ContextWithSpanContext(ctx, spanCtxA)
	resA, err := c.GetOrInitCall(ctxA, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 1), nil
	})
	assert.NilError(t, err)

	spanCtxB := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{2},
		SpanID:  trace.SpanID{2},
	})
	ctxB := trace.ContextWithSpanContext(ctx, spanCtxB)
	resB, err := c.GetOrInitCall(ctxB, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 2), nil
	})
	assert.NilError(t, err)

	sharedA := resA.cacheSharedResult()
	sharedB := resB.cacheSharedResult()
	assert.Assert(t, sharedA != nil)
	assert.Assert(t, sharedB != nil)
	assert.Equal(t, sharedA.id, sharedB.id)

	gotSpanCtx, ok := c.sessionLazySpanContext("test-session", sharedA.id)
	assert.Assert(t, ok)
	assert.Equal(t, gotSpanCtx.TraceID(), spanCtxA.TraceID())
	assert.Equal(t, gotSpanCtx.SpanID(), spanCtxA.SpanID())

	cacheTestReleaseSession(t, c, ctx)
	_, ok = c.sessionLazySpanContext("test-session", sharedA.id)
	assert.Assert(t, !ok)
}

func TestEvaluateLazyUsesOriginalSpanForLogsAndNestedSpans(t *testing.T) {
	t.Parallel()

	baseCtx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctx := ContextWithCache(baseCtx, cacheIface)
	c := cacheIface
	srv := cacheTestServer(t)

	spanExporter := &cacheTestSpanExporter{}
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	defer tracerProvider.Shutdown(t.Context())

	logExporter := &cacheTestLogExporter{}
	loggerProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	defer loggerProvider.Shutdown(t.Context())

	ctx = telemetry.WithLoggerProvider(ctx, loggerProvider)

	originalCtx, originalSpan := tracerProvider.Tracer("dagger.io/test").Start(ctx, "original")
	reqCall := &ResultCall{
		Type: NewResultCallType(&ast.Type{
			NamedType: "CacheTestObject",
			NonNull:   true,
		}),
		Field: "lazyResume",
	}
	res, err := c.GetOrInitCall(originalCtx, "test-session", srv, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResultWithValue(t, srv, reqCall, &cacheTestObject{
			Value: 1,
			lazyEval: func(ctx context.Context) error {
				stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
				fmt.Fprint(stdio.Stdout, "hello from lazy")
				assert.NilError(t, stdio.Close())

				_, childSpan := Tracer(ctx).Start(ctx, "lazy child")
				childSpan.End()
				return nil
			},
		}), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, HasPendingLazyEvaluation(res))

	triggerCtx, triggerSpan := tracerProvider.Tracer("dagger.io/test").Start(ctx, "trigger")
	assert.NilError(t, c.Evaluate(triggerCtx, res))
	triggerSpan.End()
	originalSpan.End()

	assert.NilError(t, tracerProvider.ForceFlush(t.Context()))
	assert.NilError(t, loggerProvider.ForceFlush(t.Context()))

	originalSpanID := originalSpan.SpanContext().SpanID()

	logExporter.mu.Lock()
	logs := append([]sdklog.Record(nil), logExporter.logs...)
	logExporter.mu.Unlock()
	assert.Assert(t, len(logs) > 0, "expected lazy evaluation to emit logs")
	assert.Equal(t, logs[0].SpanID(), originalSpanID)

	spanExporter.mu.Lock()
	spans := append([]sdktrace.ReadOnlySpan(nil), spanExporter.spans...)
	spanExporter.mu.Unlock()

	var sawResume bool
	var sawChild bool
	for _, span := range spans {
		switch span.Name() {
		case "lazy child":
			sawChild = true
			assert.Equal(t, span.Parent().SpanID(), originalSpanID)
		case "resume lazyResume":
			sawResume = true
			assert.Equal(t, span.Parent().SpanID(), triggerSpan.SpanContext().SpanID())
			assert.Equal(t, len(span.Links()), 1)
			assert.Equal(t, span.Links()[0].SpanContext.SpanID(), originalSpanID)
		}
	}
	assert.Assert(t, sawChild, "expected nested lazy child span to be recorded")
	assert.Assert(t, sawResume, "expected hidden resume span to be recorded")
}

func (*cacheTestObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CacheTestObject",
		NonNull:   true,
	}
}

func (obj *cacheTestObject) OnRelease(ctx context.Context) error {
	if obj.onRelease == nil {
		return nil
	}
	return obj.onRelease(ctx)
}

func (obj *cacheTestObject) LazyEvalFunc() LazyEvalFunc {
	if obj == nil {
		return nil
	}
	return obj.lazyEval
}

func (obj *cacheTestObject) AttachDependencyResults(
	_ context.Context,
	_ AnyResult,
	attach func(AnyResult) (AnyResult, error),
) ([]AnyResult, error) {
	if obj == nil {
		return nil, nil
	}
	deps := make([]AnyResult, 0, len(obj.dependencyResults))
	for i, dep := range obj.dependencyResults {
		if dep == nil {
			continue
		}
		attachedDep, err := attach(dep)
		if err != nil {
			return nil, err
		}
		obj.dependencyResults[i] = attachedDep
		deps = append(deps, attachedDep)
	}
	return deps, nil
}

func cacheTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newDagqlServerForTest(t, cacheTestQuery{})
	Fields[*cacheTestObject]{
		Func("value", func(_ context.Context, self *cacheTestObject, _ struct{}) (Int, error) {
			return NewInt(self.Value), nil
		}),
	}.Install(srv)
	return srv
}

func cacheTestContext(ctx context.Context) context.Context {
	return engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "dagql-test-client",
		SessionID: "test-session",
	})
}

func cacheTestSessionID(t *testing.T, ctx context.Context) string {
	t.Helper()
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	assert.NilError(t, err)
	assert.Assert(t, clientMetadata.SessionID != "")
	return clientMetadata.SessionID
}

func cacheTestReleaseSession(t *testing.T, cache *Cache, ctx context.Context) {
	t.Helper()
	assert.NilError(t, cache.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
}

func cacheTestObjectResolverServer(t *testing.T, marker int) *Server {
	t.Helper()
	srv := cacheTestServer(t)
	Fields[cacheTestQuery]{
		NodeFunc("obj", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: 0})
		}),
	}.Install(srv)
	Fields[*cacheTestObject]{
		Func("marker", func(_ context.Context, _ *cacheTestObject, _ struct{}) (Int, error) {
			return NewInt(marker), nil
		}).DoNotCache("test marker must execute against the current server class"),
	}.Install(srv)
	return srv
}

func cacheTestObjectResult(
	t *testing.T,
	srv *Server,
	frame *ResultCall,
	value int,
	onRelease func(context.Context) error,
) ObjectResult[*cacheTestObject] {
	t.Helper()
	res, err := NewObjectResultForCall(&cacheTestObject{
		Value:     value,
		onRelease: onRelease,
	}, srv, frame)
	assert.NilError(t, err)
	return res
}

func cacheTestObjectResultWithValue(
	t *testing.T,
	srv *Server,
	frame *ResultCall,
	obj *cacheTestObject,
) ObjectResult[*cacheTestObject] {
	t.Helper()
	res, err := NewObjectResultForCall(obj, srv, frame)
	assert.NilError(t, err)
	return res
}

func TestCacheConcurrent(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	keyCall := cacheTestIntCall("42")
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100
	const concurrencyKey = "42"

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	callConcKeys := callConcurrencyKeys{
		callKey:        cacheTestCallDigest(keyCall).String(),
		concurrencyKey: concurrencyKey,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:     keyCall,
			ConcurrencyKey: concurrencyKey,
		}, func(_ context.Context) (AnyResult, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return cacheTestIntResult(keyCall, 0), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
	}()

	select {
	case <-firstCallEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first caller to enter init callback")
	}

	for i := 1; i < totalCallers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: concurrencyKey,
			}, func(_ context.Context) (AnyResult, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return cacheTestIntResult(keyCall, i), nil
			})
			assert.NilError(t, err)
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.callsMu.Lock()
		oc := c.ongoingCalls[callConcKeys]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.callsMu.Unlock()

		if oc != nil && lastObservedWaiters == totalCallers {
			waiterCountReached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, waiterCountReached, "expected %d waiters, last observed %d", totalCallers, lastObservedWaiters)

	close(unblockFirstCall)

	ongoingCleared := false
	clearPollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(clearPollDeadline) {
		c.callsMu.Lock()
		_, exists := c.ongoingCalls[callConcKeys]
		c.callsMu.Unlock()
		if !exists {
			ongoingCleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, ongoingCleared, "ongoing call was not cleared")

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
	assert.Assert(t, initialized[0])
	assert.Equal(t, 1, cacheIface.Size())
}

func TestCacheEvaluate(t *testing.T) {
	t.Parallel()

	newEvalEnv := func(t *testing.T) (context.Context, *Cache, *Server) {
		t.Helper()
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		ctx = ContextWithCache(ctx, cacheIface)
		srv := cacheTestServer(t)
		return ctx, cacheIface, srv
	}

	t.Run("singleflight", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-singleflight",
		}

		var evalCalls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		var startOnce sync.Once

		resAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value: 1,
				lazyEval: func(context.Context) error {
					evalCalls.Add(1)
					startOnce.Do(func() {
						close(started)
					})
					<-release
					return nil
				},
			}), nil
		})
		assert.NilError(t, err)
		res := resAny.(ObjectResult[*cacheTestObject])

		group := new(errgroup.Group)
		for range 8 {
			group.Go(func() error {
				return c.Evaluate(ctx, res)
			})
		}

		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for lazy evaluation to start")
		}
		close(release)
		assert.NilError(t, group.Wait())
		assert.Equal(t, int32(1), evalCalls.Load())

		assert.NilError(t, c.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
	})

	t.Run("reacquires dedicated lease instead of reusing request lease", func(t *testing.T) {
		t.Parallel()
		ctx := cacheTestContext(t.Context())
		leaseProvider := &cacheTestLeaseProvider{}
		ctx = ContextWithOperationLeaseProvider(ctx, leaseProvider)
		ctx = leases.WithLease(ctx, "request-1")
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		ctx = ContextWithCache(ctx, cacheIface)
		srv := cacheTestServer(t)

		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-shared-lease",
		}

		var seenLease string
		resAny, err := cacheIface.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value: 1,
				lazyEval: func(ctx context.Context) error {
					leaseID, ok := leases.FromContext(ctx)
					if !ok || leaseID == "" {
						return fmt.Errorf("lazy evaluation missing operation lease")
					}
					if leaseID == "request-1" {
						return fmt.Errorf("lazy evaluation reused request lease %q", leaseID)
					}
					if !strings.HasPrefix(leaseID, "shared-") {
						return fmt.Errorf("lazy evaluation did not use shared lease: %q", leaseID)
					}
					seenLease = leaseID
					return nil
				},
			}), nil
		})
		assert.NilError(t, err)

		assert.NilError(t, cacheIface.Evaluate(ctx, resAny))
		assert.Assert(t, strings.HasPrefix(seenLease, "shared-"))
		assert.Assert(t, leaseProvider.nextLeaseID.Load() > 0)
		assert.Equal(t, leaseProvider.nextLeaseID.Load(), leaseProvider.releases.Load())

		assert.NilError(t, cacheIface.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
	})

	t.Run("declared dependency", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		childFrame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-child",
		}
		var childCalls atomic.Int32
		childAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: childFrame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, childFrame, &cacheTestObject{
				Value: 2,
				lazyEval: func(context.Context) error {
					childCalls.Add(1)
					return nil
				},
			}), nil
		})
		assert.NilError(t, err)
		child := childAny.(ObjectResult[*cacheTestObject])

		parentFrame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-parent",
		}
		parentAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: parentFrame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, parentFrame, &cacheTestObject{
				Value:             1,
				dependencyResults: []AnyResult{child},
				lazyEval: func(ctx context.Context) error {
					cache, err := EngineCache(ctx)
					assert.NilError(t, err)
					return cache.Evaluate(ctx, child)
				},
			}), nil
		})
		assert.NilError(t, err)
		parent := parentAny.(ObjectResult[*cacheTestObject])

		assert.NilError(t, c.Evaluate(ctx, parent))
		assert.Equal(t, int32(1), childCalls.Load())

		assert.NilError(t, c.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
	})

	t.Run("parallel multi-result", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		var running atomic.Int32
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		newLazy := func(field string) ObjectResult[*cacheTestObject] {
			frame := &ResultCall{
				Kind:  ResultCallKindField,
				Type:  NewResultCallType((&cacheTestObject{}).Type()),
				Field: field,
			}
			resAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
				return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
					Value: 1,
					lazyEval: func(context.Context) error {
						if running.Add(1) == 2 {
							started <- struct{}{}
						}
						started <- struct{}{}
						<-release
						running.Add(-1)
						return nil
					},
				}), nil
			})
			assert.NilError(t, err)
			return resAny.(ObjectResult[*cacheTestObject])
		}

		a := newLazy("lazy-parallel-a")
		b := newLazy("lazy-parallel-b")

		errCh := make(chan error, 1)
		go func() {
			errCh <- c.Evaluate(ctx, a, b)
		}()

		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for first parallel lazy callback")
		}
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for second parallel lazy callback")
		}

		close(release)
		assert.NilError(t, <-errCh)
		assert.NilError(t, c.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
	})

	t.Run("non dependency allowed while attached", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		childFrame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-undeclared-child",
		}
		var childCalls atomic.Int32
		childAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: childFrame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, childFrame, &cacheTestObject{
				Value: 2,
				lazyEval: func(context.Context) error {
					childCalls.Add(1)
					return nil
				},
			}), nil
		})
		assert.NilError(t, err)
		child := childAny.(ObjectResult[*cacheTestObject])

		parentFrame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-undeclared-parent",
		}
		parentAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: parentFrame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, parentFrame, &cacheTestObject{
				Value: 1,
				lazyEval: func(ctx context.Context) error {
					cache, err := EngineCache(ctx)
					assert.NilError(t, err)
					return cache.Evaluate(ctx, child)
				},
			}), nil
		})
		assert.NilError(t, err)
		parent := parentAny.(ObjectResult[*cacheTestObject])

		assert.NilError(t, c.Evaluate(ctx, parent))
		assert.Equal(t, int32(1), childCalls.Load())

		assert.NilError(t, c.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
	})

	t.Run("recursive", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-recursive",
		}
		var self AnyResult
		resAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value: 1,
				lazyEval: func(ctx context.Context) error {
					cache, err := EngineCache(ctx)
					assert.NilError(t, err)
					return cache.Evaluate(ctx, self)
				},
			}), nil
		})
		assert.NilError(t, err)
		self = resAny

		err = c.Evaluate(ctx, resAny)
		assert.Assert(t, err != nil)
		assert.ErrorContains(t, err, "recursive lazy evaluation detected")

		assert.NilError(t, c.ReleaseSession(ctx, cacheTestSessionID(t, ctx)))
	})

	t.Run("rejects do-not-cache lazy result", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-do-not-cache",
		}

		_, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{
			ResultCall: frame,
			DoNotCache: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value: 1,
				lazyEval: func(context.Context) error {
					return nil
				},
			}), nil
		})
		assert.Assert(t, err != nil)
		assert.ErrorContains(t, err, "cannot be lazy")
	})

	t.Run("allows already attached lazy result from do-not-cache field", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := newEvalEnv(t)

		lazyFrame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-parent",
		}
		attachedLazy, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{
			ResultCall: lazyFrame,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, lazyFrame, &cacheTestObject{
				Value: 1,
				lazyEval: func(context.Context) error {
					return nil
				},
			}), nil
		})
		assert.NilError(t, err)

		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "do-not-cache-parent",
		}
		res, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{
			ResultCall: frame,
			DoNotCache: true,
		}, func(context.Context) (AnyResult, error) {
			return attachedLazy, nil
		})
		assert.NilError(t, err)
		assert.Equal(t, res.cacheSharedResult().id, attachedLazy.cacheSharedResult().id)
	})
}

func TestCacheErrors(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	keyCall := cacheTestIntCall("42")

	myErr := errors.New("nope")
	_, err = cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return nil, myErr
	})
	assert.Assert(t, is.ErrorIs(err, myErr))

	otherErr := errors.New("nope 2")
	_, err = cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return nil, otherErr
	})
	assert.Assert(t, is.ErrorIs(err, otherErr))

	res, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyCall, 1), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))

	res, err = cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(_ context.Context) (AnyResult, error) {
		return nil, errors.New("ignored")
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))
}

func TestCacheRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	key1Call := cacheTestIntCall("1")

	// recursive calls that are guaranteed to result in deadlock should error out
	_, err = cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key1Call}, func(ctx context.Context) (AnyResult, error) {
		_, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("2"), 2), nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))

	// verify same cache can be called recursively with different keys
	key10Call := cacheTestIntCall("10")
	key11Call := cacheTestIntCall("11")
	v, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key10Call}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key11Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key11Call, 12), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key10Call, cacheTestUnwrapInt(t, res)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 12, cacheTestUnwrapInt(t, v))
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func TestCacheContextCancel(t *testing.T) {
	t.Run("cancels after all are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)

		keyCall := cacheTestIntCall("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		ctx2, cancel2 := context.WithCancel(ctx)
		ctx3, cancel3 := context.WithCancel(ctx)

		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		go func() {
			defer close(errCh1)
			_, err := cacheIface.GetOrInitCall(ctx1, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(ctx context.Context) (AnyResult, error) {
				close(started1)
				<-ctx.Done()
				return nil, fmt.Errorf("oh no 1")
			})
			errCh1 <- err
		}()
		select {
		case <-started1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for started1")
		}

		errCh2 := make(chan error, 1)
		go func() {
			defer close(errCh2)
			_, err := cacheIface.GetOrInitCall(ctx2, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(ctx context.Context) (AnyResult, error) {
				<-ctx.Done()
				return nil, fmt.Errorf("oh no 2")
			})
			errCh2 <- err
		}()

		errCh3 := make(chan error, 1)
		go func() {
			defer close(errCh3)
			_, err := cacheIface.GetOrInitCall(ctx3, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("oh no 3")
			})
			errCh3 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}
		select {
		case err := <-errCh1:
			t.Fatal("unexpected error from 1st client", err)
		case err := <-errCh3:
			t.Fatal("unexpected error from 3rd client", err)
		default:
		}

		cancel3()
		select {
		case err := <-errCh3:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh3")
		}
		select {
		case err := <-errCh1:
			t.Fatal("unexpected error from 1st client", err)
		default:
		}

		cancel1()
		select {
		case err := <-errCh1:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})

	t.Run("succeeds if others are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)

		keyCall := cacheTestIntCall("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		t.Cleanup(cancel1)
		ctx2, cancel2 := context.WithCancel(ctx)

		resCh1 := make(chan AnyResult, 1)
		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		stop1 := make(chan struct{})
		go func() {
			defer close(resCh1)
			defer close(errCh1)
			res, err := cacheIface.GetOrInitCall(ctx1, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started1)
				<-stop1
				return cacheTestIntResult(keyCall, 0), nil
			})
			resCh1 <- res
			errCh1 <- err
		}()
		select {
		case <-started1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for started1")
		}

		errCh2 := make(chan error, 1)
		go func() {
			defer close(errCh2)
			_, err := cacheIface.GetOrInitCall(ctx2, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("unexpected initializer call")
			})
			errCh2 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}

		close(stop1)
		select {
		case res := <-resCh1:
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for resCh1")
		}
		select {
		case err := <-errCh1:
			assert.NilError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})

	t.Run("shared call keeps dedicated lease across completion", func(t *testing.T) {
		t.Parallel()
		baseCtx := cacheTestContext(t.Context())
		leaseProvider := &cacheTestLeaseProvider{}
		baseCtx = ContextWithOperationLeaseProvider(baseCtx, leaseProvider)
		cacheIface, err := NewCache(baseCtx, "", nil, nil)
		assert.NilError(t, err)

		reqCall := cacheTestIntCall("shared-lease")
		callConcKeys := callConcurrencyKeys{
			callKey:        cacheTestCallDigest(reqCall).String(),
			concurrencyKey: "shared-lease",
		}

		ctx1Base := engine.ContextWithClientMetadata(leases.WithLease(baseCtx, "request-1"), &engine.ClientMetadata{
			ClientID:  "request-1-client",
			SessionID: "request-1-session",
		})
		ctx1, cancel1 := context.WithCancel(ctx1Base)
		t.Cleanup(cancel1)
		ctx2 := engine.ContextWithClientMetadata(leases.WithLease(baseCtx, "request-2"), &engine.ClientMetadata{
			ClientID:  "request-2-client",
			SessionID: "request-2-session",
		})

		started := make(chan struct{})
		allowReturn := make(chan struct{})
		errCh1 := make(chan error, 1)
		go func() {
			_, err := cacheIface.GetOrInitCall(ctx1, "request-1-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     reqCall,
				ConcurrencyKey: "shared-lease",
			}, func(ctx context.Context) (AnyResult, error) {
				leaseID, ok := leases.FromContext(ctx)
				if !ok || leaseID == "" {
					return nil, fmt.Errorf("shared call missing operation lease")
				}
				if leaseID == "request-1" || leaseID == "request-2" {
					return nil, fmt.Errorf("shared call reused request lease %q", leaseID)
				}
				close(started)
				<-allowReturn
				return cacheTestDetachedResult(reqCall, cacheTestLeaseCheckedInt{
					Int: NewInt(1),
					onAttach: func(ctx context.Context) error {
						attachLeaseID, ok := leases.FromContext(ctx)
						if !ok || attachLeaseID == "" {
							return fmt.Errorf("attach dependency results missing operation lease")
						}
						if attachLeaseID != leaseID {
							return fmt.Errorf("attach dependency results lease mismatch: got %q want %q", attachLeaseID, leaseID)
						}
						if attachLeaseID == "request-1" || attachLeaseID == "request-2" {
							return fmt.Errorf("attach dependency results reused request lease %q", attachLeaseID)
						}
						return nil
					},
				}), nil
			})
			errCh1 <- err
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for shared call start")
		}

		errCh2 := make(chan error, 1)
		resCh2 := make(chan AnyResult, 1)
		go func() {
			res, err := cacheIface.GetOrInitCall(ctx2, "request-2-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     reqCall,
				ConcurrencyKey: "shared-lease",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("unexpected initializer call")
			})
			resCh2 <- res
			errCh2 <- err
		}()

		waiterJoined := false
		waiterDeadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(waiterDeadline) {
			cacheIface.callsMu.Lock()
			oc := cacheIface.ongoingCalls[callConcKeys]
			waiters := 0
			if oc != nil {
				waiters = oc.waiters
			}
			cacheIface.callsMu.Unlock()
			if waiters == 2 {
				waiterJoined = true
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		assert.Assert(t, waiterJoined, "expected second waiter to join shared call")

		cancel1()
		select {
		case err := <-errCh1:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for first waiter cancellation")
		}

		close(allowReturn)
		select {
		case err := <-errCh2:
			assert.NilError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for second waiter result")
		}
		select {
		case <-resCh2:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for second waiter value")
		}

		assert.Equal(t, int32(1), leaseProvider.nextLeaseID.Load())
		assert.Equal(t, int32(1), leaseProvider.releases.Load())
	})

	t.Run("last waiter canceled fn returns value still releases", func(t *testing.T) {
		t.Parallel()
		// TODO: Re-enable this test once we define and implement the intended
		// last-waiter cleanup semantics for canceled waiters when fn later returns.
		t.Skip("TODO: re-enable after last-waiter canceled cleanup semantics are decided")
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)

		keyCall := cacheTestIntCall("cancel-last-waiter-release")
		ctx1, cancel1 := context.WithCancel(ctx)
		defer cancel1()

		started := make(chan struct{})
		allowReturn := make(chan struct{})
		released := make(chan struct{})

		errCh := make(chan error, 1)
		go func() {
			_, err := cacheIface.GetOrInitCall(ctx1, "test-session", noopTypeResolver{}, &CallRequest{
				ResultCall:     keyCall,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started)
				<-allowReturn
				return cacheTestIntResultWithOnRelease(keyCall, 1, func(context.Context) error {
					close(released)
					return nil
				}), nil
			})
			errCh <- err
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for call start")
		}

		cancel1()
		select {
		case err := <-errCh:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for canceled wait return")
		}

		close(allowReturn)
		select {
		case <-released:
		case <-time.After(5 * time.Second):
			t.Fatal("expected release after call returns with no waiters")
		}
	})
}

func TestSkipDedupe(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	keyCall := cacheTestIntCall("1")
	var eg errgroup.Group

	valCh1 := make(chan int, 1)
	started1 := make(chan struct{})
	stop1 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
			defer close(valCh1)
			close(started1)
			valCh1 <- 1
			<-stop1
			return cacheTestIntResult(keyCall, 1), nil
		})
		return err
	})

	valCh2 := make(chan int, 1)
	started2 := make(chan struct{})
	stop2 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
			defer close(valCh2)
			close(started2)
			valCh2 <- 2
			<-stop2
			return cacheTestIntResult(keyCall, 2), nil
		})
		return err
	})

	select {
	case <-started1:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for started1")
	}
	select {
	case <-started2:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for started2")
	}

	close(stop1)
	close(stop2)

	select {
	case val := <-valCh1:
		assert.Equal(t, 1, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for valCh1")
	}
	select {
	case val := <-valCh2:
		assert.Equal(t, 2, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for valCh2")
	}

	assert.NilError(t, eg.Wait())
}

func TestCacheNilKeyIDRejected(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	_, err = cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{}}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.ErrorContains(t, err, "missing field")
}

func TestCacheDifferentConcurrencyKeysDoNotDedupe(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	keyCall := cacheTestIntCall("different-concurrency")
	release := make(chan struct{})
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	errCh := make(chan error, 2)
	var initCalls atomic.Int32

	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:     keyCall,
			ConcurrencyKey: "a",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedA)
			<-release
			return cacheTestIntResult(keyCall, 1), nil
		})
		errCh <- err
	}()
	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:     keyCall,
			ConcurrencyKey: "b",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedB)
			<-release
			return cacheTestIntResult(keyCall, 2), nil
		})
		errCh <- err
	}()

	select {
	case <-startedA:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startedA")
	}
	select {
	case <-startedB:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startedB")
	}

	close(release)
	assert.NilError(t, <-errCh)
	assert.NilError(t, <-errCh)
	assert.Equal(t, int32(2), initCalls.Load())
}

func TestCacheNilResultIsCached(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	keyCall := cacheTestIntCall("nil-result")
	initCalls := 0

	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	assert.Assert(t, res.Unwrap() == nil)

	res, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyCall, 42), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	assert.Assert(t, res.Unwrap() == nil)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())
}

func TestEquivalencySetCacheHits(t *testing.T) {
	t.Parallel()

	// Basic case: equivalent upstream outputs enable a single downstream cache hit
	// even when the downstream recipes are distinct.
	t.Run("basic", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		ctx = ContextWithCache(ctx, cacheIface)
		c := cacheIface

		sharedEq := call.ExtraDigest{
			Digest: digest.FromString("shared-eq-basic"),
			Label:  "eq-shared",
		}
		noiseA := call.ExtraDigest{
			Digest: digest.FromString("basic-noise-a"),
			Label:  "noise-a",
		}
		noiseB := call.ExtraDigest{
			Digest: digest.FromString("basic-noise-b"),
			Label:  "noise-b",
		}
		f1OutCall := cacheTestIntCall("content-f-1", sharedEq, noiseA)
		f2OutCall := cacheTestIntCall("content-f-2", sharedEq, noiseB)

		fInitCalls := 0
		f1Call := cacheTestIntCall("content-f-1")
		f2Call := cacheTestIntCall("content-f-2")
		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f1OutCall, 11), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())

		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f2OutCall, 22), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		assert.Equal(t, 2, fInitCalls)
		assert.Assert(t, cacheTestMustRecipeID(t, ctx, f1Res).Digest() != cacheTestMustRecipeID(t, ctx, f2Res).Digest())

		g1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "content-g",
			Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(111)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "content-g",
			Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestPlainResult(NewInt(222)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 111, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Deeper chain: equivalence learned at f-level should enable hits at g-level,
	// which then propagate to h-level and i-level for distinct downstream recipes.
	t.Run("deep_chain", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		sharedEq := call.ExtraDigest{
			Digest: digest.FromString("deep-shared-eq"),
			Label:  "eq-shared",
		}
		noiseA := call.ExtraDigest{
			Digest: digest.FromString("deep-noise-a"),
			Label:  "noise-a",
		}
		noiseB := call.ExtraDigest{
			Digest: digest.FromString("deep-noise-b"),
			Label:  "noise-b",
		}
		f1Call := cacheTestIntCall("deep-f-1")
		f2Call := cacheTestIntCall("deep-f-2")
		f1OutCall := cacheTestIntCall("deep-f-1", sharedEq, noiseA)
		f2OutCall := cacheTestIntCall("deep-f-2", sharedEq, noiseB)

		fInitCalls := 0
		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f1OutCall, 21), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			fInitCalls++
			return cacheTestIntResult(f2OutCall, 22), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		assert.Equal(t, 2, fInitCalls)

		g1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-g",
			Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(121)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		h1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-h",
			Receiver: &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(221)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !h1Res.HitCache())

		i1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-i",
			Receiver: &ResultCallRef{ResultID: uint64(h1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(321)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !i1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-g",
			Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestPlainResult(NewInt(122)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 121, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

		h2InitCalls := 0
		h2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-h",
			Receiver: &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			h2InitCalls++
			return cacheTestPlainResult(NewInt(222)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, h2InitCalls)
		assert.Assert(t, h2Res.HitCache())
		assert.Equal(t, 221, cacheTestUnwrapInt(t, h2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, h1Res), cacheTestMustEncodeID(t, h2Res))

		i2InitCalls := 0
		i2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "deep-i",
			Receiver: &ResultCallRef{ResultID: uint64(h2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			i2InitCalls++
			return cacheTestPlainResult(NewInt(322)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, i2InitCalls)
		assert.Assert(t, i2Res.HitCache())
		assert.Equal(t, 321, cacheTestUnwrapInt(t, i2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, i1Res), cacheTestMustEncodeID(t, i2Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Late equivalence with noisy metadata: distinct recipes miss until h-level
	// outputs publish overlapping extra digests; once learned, downstream
	// i-level lookups should hit even with non-overlapping extras elsewhere.
	t.Run("late_extra_digests_at_h", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		f1Only := call.ExtraDigest{Digest: digest.FromString("late-f1-only"), Label: "f1-only"}
		f2Only := call.ExtraDigest{Digest: digest.FromString("late-f2-only"), Label: "f2-only"}
		g1Only := call.ExtraDigest{Digest: digest.FromString("late-g1-only"), Label: "g1-only"}
		g2Only := call.ExtraDigest{Digest: digest.FromString("late-g2-only"), Label: "g2-only"}
		sharedA := call.ExtraDigest{Digest: digest.FromString("late-shared-a"), Label: "shared-a"}
		sharedB := call.ExtraDigest{Digest: digest.FromString("late-shared-b"), Label: "shared-b"}
		h1Only := call.ExtraDigest{Digest: digest.FromString("late-h1-only"), Label: "h1-only"}
		h2Only := call.ExtraDigest{Digest: digest.FromString("late-h2-only"), Label: "h2-only"}

		f1Call := cacheTestIntCall("late-f-1")
		f2Call := cacheTestIntCall("late-f-2")
		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("late-f-1", f1Only), 41), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())

		g1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-g",
			Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-g",
				Receiver:     &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{g1Only},
			}, 141), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		h1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-h",
			Receiver: &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-h",
				Receiver:     &ResultCallRef{ResultID: uint64(g1Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{sharedA, sharedB, h1Only},
			}, 241), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !h1Res.HitCache())

		i1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-i",
			Receiver: &ResultCallRef{ResultID: uint64(h1Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(341)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !i1Res.HitCache())

		f2InitCalls := 0
		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			f2InitCalls++
			return cacheTestIntResult(cacheTestIntCall("late-f-2", f2Only), 42), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, f2InitCalls)
		assert.Assert(t, !f2Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-g",
			Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-g",
				Receiver:     &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{g2Only},
			}, 142), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, g2InitCalls)
		assert.Assert(t, !g2Res.HitCache())

		h2InitCalls := 0
		h2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-h",
			Receiver: &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			h2InitCalls++
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "late-h",
				Receiver:     &ResultCallRef{ResultID: uint64(g2Res.cacheSharedResult().id)},
				ExtraDigests: []call.ExtraDigest{sharedA, sharedB, h2Only},
			}, 242), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, h2InitCalls)
		assert.Assert(t, !h2Res.HitCache())

		i2InitCalls := 0
		i2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(Int(0).Type()),
			Field:    "late-i",
			Receiver: &ResultCallRef{ResultID: uint64(h2Res.cacheSharedResult().id)},
		}}, func(context.Context) (AnyResult, error) {
			i2InitCalls++
			return cacheTestPlainResult(NewInt(342)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, i2InitCalls)
		assert.Assert(t, i2Res.HitCache())
		assert.Equal(t, 341, cacheTestUnwrapInt(t, i2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, i1Res), cacheTestMustEncodeID(t, i2Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Multi-input case: downstream z(x,y) should hit across distinct recipes once
	// both input lanes are equivalent (x1~x2 and y1~y2) via shared extra digests.
	// Basically, same as earlier tests but with multiple inputs.
	t.Run("multi_input_all_inputs_equivalent_hit", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		xShared := call.ExtraDigest{Digest: digest.FromString("multi-x-shared"), Label: "x-shared"}
		xNoise1 := call.ExtraDigest{Digest: digest.FromString("multi-x-noise-1"), Label: "x-noise-1"}
		xNoise2 := call.ExtraDigest{Digest: digest.FromString("multi-x-noise-2"), Label: "x-noise-2"}
		yShared := call.ExtraDigest{Digest: digest.FromString("multi-y-shared"), Label: "y-shared"}
		yNoise1 := call.ExtraDigest{Digest: digest.FromString("multi-y-noise-1"), Label: "y-noise-1"}
		yNoise2 := call.ExtraDigest{Digest: digest.FromString("multi-y-noise-2"), Label: "y-noise-2"}

		x1Call := cacheTestIntCall("multi-x-1")
		x2Call := cacheTestIntCall("multi-x-2")
		y1Call := cacheTestIntCall("multi-y-1")
		y2Call := cacheTestIntCall("multi-y-2")
		x1OutCall := cacheTestIntCall("multi-x-1", xShared, xNoise1)
		x2OutCall := cacheTestIntCall("multi-x-2", xShared, xNoise2)
		y1OutCall := cacheTestIntCall("multi-y-1", yShared, yNoise1)
		y2OutCall := cacheTestIntCall("multi-y-2", yShared, yNoise2)

		x1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: x1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x1OutCall, 11), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x1Res.HitCache())
		x2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: x2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x2OutCall, 12), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x2Res.HitCache())

		y1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: y1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y1OutCall, 21), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y1Res.HitCache())
		y2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: y2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y2OutCall, 22), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y2Res.HitCache())

		zReq := func(xRes, yRes AnyResult) *CallRequest {
			xShared := xRes.cacheSharedResult()
			yShared := yRes.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "multi-z",
					Args: []*ResultCallArg{
						{Name: "x", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(xShared.id)}}},
						{Name: "y", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(yShared.id)}}},
					},
				},
			}
		}

		z1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, zReq(x1Res, y1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(501)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !z1Res.HitCache())

		z2InitCalls := 0
		z2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, zReq(x2Res, y2Res), func(context.Context) (AnyResult, error) {
			z2InitCalls++
			return cacheTestPlainResult(NewInt(502)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, z2InitCalls)
		assert.Assert(t, z2Res.HitCache())
		assert.Equal(t, 501, cacheTestUnwrapInt(t, z2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, z1Res), cacheTestMustEncodeID(t, z2Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Multi-input miss case: if only one input lane is equivalent (x1~x2) but
	// the other lane is not (y1 !~ y2), z(x,y) must miss and execute.
	t.Run("multi_input_partial_equivalence_miss", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		xShared := call.ExtraDigest{Digest: digest.FromString("multi-partial-x-shared"), Label: "x-shared"}
		xNoise1 := call.ExtraDigest{Digest: digest.FromString("multi-partial-x-noise-1"), Label: "x-noise-1"}
		xNoise2 := call.ExtraDigest{Digest: digest.FromString("multi-partial-x-noise-2"), Label: "x-noise-2"}
		yOnly1 := call.ExtraDigest{Digest: digest.FromString("multi-partial-y-only-1"), Label: "y-only-1"}
		yOnly2 := call.ExtraDigest{Digest: digest.FromString("multi-partial-y-only-2"), Label: "y-only-2"}

		x1Call := cacheTestIntCall("multi-partial-x-1")
		x2Call := cacheTestIntCall("multi-partial-x-2")
		y1Call := cacheTestIntCall("multi-partial-y-1")
		y2Call := cacheTestIntCall("multi-partial-y-2")
		x1OutCall := cacheTestIntCall("multi-partial-x-1", xShared, xNoise1)
		x2OutCall := cacheTestIntCall("multi-partial-x-2", xShared, xNoise2)
		y1OutCall := cacheTestIntCall("multi-partial-y-1", yOnly1)
		y2OutCall := cacheTestIntCall("multi-partial-y-2", yOnly2)

		x1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: x1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x1OutCall, 31), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x1Res.HitCache())
		x2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: x2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(x2OutCall, 32), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !x2Res.HitCache())

		y1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: y1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y1OutCall, 41), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y1Res.HitCache())
		y2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: y2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(y2OutCall, 42), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !y2Res.HitCache())

		zReq := func(xRes, yRes AnyResult) *CallRequest {
			xShared := xRes.cacheSharedResult()
			yShared := yRes.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "multi-partial-z",
					Args: []*ResultCallArg{
						{Name: "x", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(xShared.id)}}},
						{Name: "y", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(yShared.id)}}},
					},
				},
			}
		}

		z1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, zReq(x1Res, y1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(701)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !z1Res.HitCache())

		z2InitCalls := 0
		z2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, zReq(x2Res, y2Res), func(context.Context) (AnyResult, error) {
			z2InitCalls++
			return cacheTestPlainResult(NewInt(702)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, z2InitCalls)
		assert.Assert(t, !z2Res.HitCache())
		assert.Equal(t, 702, cacheTestUnwrapInt(t, z2Res))
		assert.Assert(t, cacheTestMustEncodeID(t, z2Res) != cacheTestMustEncodeID(t, z1Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Transitive bridge case: f1 and f3 do not share a direct digest, but f2
	// links both sides (A on f1/f2 and B on f2/f3), so equivalence should merge
	// transitively. After caching g(f1), a lookup of g(f3) should hit while still
	// returning g3Key as the request-facing ID digest.
	t.Run("transitive_extra_digest_merge_bridge_hit", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		bridgeA := call.ExtraDigest{Digest: digest.FromString("bridge-a"), Label: "bridge-a"}
		bridgeB := call.ExtraDigest{Digest: digest.FromString("bridge-b"), Label: "bridge-b"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("bridge-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("bridge-noise-2"), Label: "noise-2"}
		noise3 := call.ExtraDigest{Digest: digest.FromString("bridge-noise-3"), Label: "noise-3"}

		f1Call := cacheTestIntCall("bridge-f-1")
		f2Call := cacheTestIntCall("bridge-f-2")
		f3Call := cacheTestIntCall("bridge-f-3")
		f1OutCall := cacheTestIntCall("bridge-f-1", bridgeA, noise1)
		f2OutCall := cacheTestIntCall("bridge-f-2", bridgeA, bridgeB, noise2)
		f3OutCall := cacheTestIntCall("bridge-f-3", bridgeB, noise3)

		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f1OutCall, 101), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f2OutCall, 102), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		f3Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f3Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f3OutCall, 103), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f3Res.HitCache())

		gReq := func(parent AnyResult) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    "bridge-g",
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}

		g1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, gReq(f1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(901)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g3InitCalls := 0
		g3Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, gReq(f3Res), func(context.Context) (AnyResult, error) {
			g3InitCalls++
			return cacheTestPlainResult(NewInt(903)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g3InitCalls)
		assert.Assert(t, g3Res.HitCache())
		assert.Equal(t, 901, cacheTestUnwrapInt(t, g3Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g3Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Negative bridge case: f1 and f2 overlap on A, but f3 only overlaps with B
	// and f2 does not carry B, so there is no bridge from f1 to f3.
	// We still expect g(f2) to hit from g(f1), while g(f3) must remain a miss.
	t.Run("transitive_bridge_no_bridge_no_hit", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		bridgeA := call.ExtraDigest{Digest: digest.FromString("nobridge-a"), Label: "bridge-a"}
		bridgeB := call.ExtraDigest{Digest: digest.FromString("nobridge-b"), Label: "bridge-b"}
		other := call.ExtraDigest{Digest: digest.FromString("nobridge-other"), Label: "other"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("nobridge-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("nobridge-noise-2"), Label: "noise-2"}
		noise3 := call.ExtraDigest{Digest: digest.FromString("nobridge-noise-3"), Label: "noise-3"}

		f1Call := cacheTestIntCall("nobridge-f-1")
		f2Call := cacheTestIntCall("nobridge-f-2")
		f3Call := cacheTestIntCall("nobridge-f-3")
		f1OutCall := cacheTestIntCall("nobridge-f-1", bridgeA, noise1)
		f2OutCall := cacheTestIntCall("nobridge-f-2", bridgeA, other, noise2)
		f3OutCall := cacheTestIntCall("nobridge-f-3", bridgeB, noise3)

		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f1OutCall, 111), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f2OutCall, 112), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())
		f3Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f3Call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(f3OutCall, 113), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f3Res.HitCache())

		gReq := func(parent AnyResult) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    "nobridge-g",
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}

		g1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, gReq(f1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(911)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !g1Res.HitCache())

		g2InitCalls := 0
		g2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, gReq(f2Res), func(context.Context) (AnyResult, error) {
			g2InitCalls++
			return cacheTestPlainResult(NewInt(912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, g2InitCalls)
		assert.Assert(t, g2Res.HitCache())
		assert.Equal(t, 911, cacheTestUnwrapInt(t, g2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

		g3InitCalls := 0
		g3Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, gReq(f3Res), func(context.Context) (AnyResult, error) {
			g3InitCalls++
			return cacheTestPlainResult(NewInt(913)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, g3InitCalls)
		assert.Assert(t, !g3Res.HitCache())
		assert.Equal(t, 913, cacheTestUnwrapInt(t, g3Res))
		assert.Assert(t, cacheTestMustEncodeID(t, g3Res) != cacheTestMustEncodeID(t, g1Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	// Fanout/fanin repair case:
	//
	//   branch 1 (seeded first):
	//     f1 -> left1
	//       \-> right1
	//     join1(left1,right1)
	//
	//   branch 2 (different recipe):
	//     f2 -> left2
	//       \-> right2
	//     join2(left2,right2)
	//
	//   equivalence digest introduced later:
	//     f1 ~ f2   (shared extra digest)
	//
	// Expected repair/propagation:
	//   left1 ~ left2
	//   right1 ~ right2
	//   => join1(left1,right1) ~ join2(left2,right2)
	t.Run("fanout_fanin_join_hit_after_repair", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		shared := call.ExtraDigest{Digest: digest.FromString("fanout-shared"), Label: "fanout-shared"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("fanout-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("fanout-noise-2"), Label: "noise-2"}

		rootReq := func(field string, extras ...call.ExtraDigest) *CallRequest {
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:         ResultCallKindField,
					Type:         NewResultCallType(Int(0).Type()),
					Field:        field,
					ExtraDigests: slices.Clone(extras),
				},
			}
		}

		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-f-1"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-f-2"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(102)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())

		unaryReq := func(parent AnyResult, field string) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    field,
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}
		joinReq := func(left, right AnyResult) *CallRequest {
			leftShared := left.cacheSharedResult()
			rightShared := right.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "fanout-join",
					Args: []*ResultCallArg{
						{Name: "left", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(leftShared.id)}}},
						{Name: "right", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(rightShared.id)}}},
					},
				},
			}
		}

		left1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f1Res, "fanout-left"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(1001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left1Res.HitCache())
		right1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f1Res, "fanout-right"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(1002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right1Res.HitCache())
		join1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, joinReq(left1Res, right1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(1101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join1Res.HitCache())

		f1AliasInitCalls := 0
		f1AliasRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-f-1", shared, noise1), func(context.Context) (AnyResult, error) {
			f1AliasInitCalls++
			return cacheTestPlainResult(NewInt(1911)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f1AliasInitCalls)
		assert.Assert(t, f1AliasRes.HitCache())
		assert.Equal(t, 101, cacheTestUnwrapInt(t, f1AliasRes))

		f2AliasInitCalls := 0
		f2AliasRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-f-2", shared, noise2), func(context.Context) (AnyResult, error) {
			f2AliasInitCalls++
			return cacheTestPlainResult(NewInt(1912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f2AliasInitCalls)
		assert.Assert(t, f2AliasRes.HitCache())
		assert.Equal(t, 102, cacheTestUnwrapInt(t, f2AliasRes))

		left2InitCalls := 0
		left2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f2Res, "fanout-left"), func(context.Context) (AnyResult, error) {
			left2InitCalls++
			return cacheTestPlainResult(NewInt(2001)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, left2InitCalls)
		assert.Assert(t, left2Res.HitCache())
		assert.Equal(t, 1001, cacheTestUnwrapInt(t, left2Res))

		right2InitCalls := 0
		right2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f2Res, "fanout-right"), func(context.Context) (AnyResult, error) {
			right2InitCalls++
			return cacheTestPlainResult(NewInt(2002)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, right2InitCalls)
		assert.Assert(t, right2Res.HitCache())
		assert.Equal(t, 1002, cacheTestUnwrapInt(t, right2Res))

		join2InitCalls := 0
		join2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, joinReq(left2Res, right2Res), func(context.Context) (AnyResult, error) {
			join2InitCalls++
			return cacheTestPlainResult(NewInt(2101)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, join2InitCalls)
		assert.Assert(t, join2Res.HitCache())
		assert.Equal(t, 1101, cacheTestUnwrapInt(t, join2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, join1Res), cacheTestMustEncodeID(t, join2Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	t.Run("fanout_fanin_late_merge_enables_downstream_join_input_hit", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		shared := call.ExtraDigest{Digest: digest.FromString("fanout-late-shared"), Label: "fanout-shared"}
		noise1 := call.ExtraDigest{Digest: digest.FromString("fanout-late-noise-1"), Label: "noise-1"}
		noise2 := call.ExtraDigest{Digest: digest.FromString("fanout-late-noise-2"), Label: "noise-2"}

		rootReq := func(field string, extras ...call.ExtraDigest) *CallRequest {
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:         ResultCallKindField,
					Type:         NewResultCallType(Int(0).Type()),
					Field:        field,
					ExtraDigests: slices.Clone(extras),
				},
			}
		}

		f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-late-f-1"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(301)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f1Res.HitCache())
		f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-late-f-2"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(302)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !f2Res.HitCache())

		unaryReq := func(parent AnyResult, field string) *CallRequest {
			parentShared := parent.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    field,
					Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
				},
			}
		}
		joinReq := func(left, right AnyResult) *CallRequest {
			leftShared := left.cacheSharedResult()
			rightShared := right.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "fanout-late-join",
					Args: []*ResultCallArg{
						{Name: "left", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(leftShared.id)}}},
						{Name: "right", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(rightShared.id)}}},
					},
				},
			}
		}
		topReq := func(join AnyResult) *CallRequest {
			joinShared := join.cacheSharedResult()
			return &CallRequest{
				ResultCall: &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(Int(0).Type()),
					Field: "fanout-late-top",
					Args: []*ResultCallArg{
						{Name: "join", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(joinShared.id)}}},
					},
				},
			}
		}

		left1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f1Res, "fanout-late-left"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(3001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left1Res.HitCache())
		right1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f1Res, "fanout-late-right"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(3002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right1Res.HitCache())
		join1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, joinReq(left1Res, right1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(3101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join1Res.HitCache())

		left2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f2Res, "fanout-late-left"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(4001)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !left2Res.HitCache())
		right2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, unaryReq(f2Res, "fanout-late-right"), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(4002)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !right2Res.HitCache())
		join2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, joinReq(left2Res, right2Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(4101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !join2Res.HitCache())

		f1AliasInitCalls := 0
		f1AliasRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-late-f-1", shared, noise1), func(context.Context) (AnyResult, error) {
			f1AliasInitCalls++
			return cacheTestPlainResult(NewInt(3911)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f1AliasInitCalls)
		assert.Assert(t, f1AliasRes.HitCache())
		assert.Equal(t, 301, cacheTestUnwrapInt(t, f1AliasRes))

		f2AliasInitCalls := 0
		f2AliasRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, rootReq("fanout-late-f-2", shared, noise2), func(context.Context) (AnyResult, error) {
			f2AliasInitCalls++
			return cacheTestPlainResult(NewInt(3912)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, f2AliasInitCalls)
		assert.Assert(t, f2AliasRes.HitCache())
		assert.Equal(t, 302, cacheTestUnwrapInt(t, f2AliasRes))

		top1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, topReq(join1Res), func(context.Context) (AnyResult, error) {
			return cacheTestPlainResult(NewInt(5101)), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !top1Res.HitCache())

		top2InitCalls := 0
		top2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, topReq(join2Res), func(context.Context) (AnyResult, error) {
			top2InitCalls++
			return cacheTestPlainResult(NewInt(5201)), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, top2InitCalls)
		assert.Assert(t, top2Res.HitCache())
		assert.Equal(t, 5101, cacheTestUnwrapInt(t, top2Res))
		assert.Equal(t, cacheTestMustEncodeID(t, top1Res), cacheTestMustEncodeID(t, top2Res))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})
}

func TestDirectDigestLookupHitsWithoutTermIndex(t *testing.T) {
	t.Run("exact_recipe_digest_hit_without_term_index", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		requestID := call.New().Append(Int(0).Type(), "direct-recipe-request")
		requestCall := cacheTestIntCall("direct-recipe-request")
		outputCall := cacheTestIntCall("direct-recipe-output")
		outputID := call.New().Append(Int(0).Type(), "direct-recipe-output")
		assert.Assert(t, requestID.Digest() != outputID.Digest())

		firstRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(outputCall, 51), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !firstRes.HitCache())

		c.egraphMu.Lock()
		c.inputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.outputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
		c.egraphTermsByTermDigest = make(map[string]*set.TreeSet[egraphTermID])
		c.resultOutputEqClasses = make(map[sharedResultID]map[eqClassID]struct{})
		c.termInputProvenance = make(map[egraphTermID][]egraphInputProvenanceKind)
		c.egraphMu.Unlock()

		initCalls := 0
		hitRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			initCalls++
			return cacheTestIntResult(requestCall, 52), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, initCalls)
		assert.Assert(t, hitRes.HitCache())
		assert.Equal(t, 51, cacheTestUnwrapInt(t, hitRes))
		assert.Equal(t, cacheTestMustEncodeID(t, firstRes), cacheTestMustEncodeID(t, hitRes))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	t.Run("extra_digest_hit_without_term_index", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		shared := call.ExtraDigest{
			Digest: digest.FromString("direct-extra-hit-shared"),
			Label:  "shared",
		}
		storedRequestID := call.New().Append(Int(0).Type(), "direct-extra-stored")
		storedRequestCall := cacheTestIntCall("direct-extra-stored")
		storedOutputCall := cacheTestIntCall("direct-extra-stored", shared)
		lookupID := call.New().Append(Int(0).Type(), "direct-extra-lookup").With(call.WithExtraDigest(shared))
		lookupCall := cacheTestIntCall("direct-extra-lookup", shared)
		assert.Assert(t, storedRequestID.Digest() != lookupID.Digest())

		firstRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: storedRequestCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(storedOutputCall, 71), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !firstRes.HitCache())

		c.egraphMu.Lock()
		c.inputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.outputEqClassToTerms = make(map[eqClassID]map[egraphTermID]struct{})
		c.egraphTerms = make(map[egraphTermID]*egraphTerm)
		c.egraphTermsByTermDigest = make(map[string]*set.TreeSet[egraphTermID])
		c.resultOutputEqClasses = make(map[sharedResultID]map[eqClassID]struct{})
		c.termInputProvenance = make(map[egraphTermID][]egraphInputProvenanceKind)
		c.egraphMu.Unlock()

		initCalls := 0
		hitRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: lookupCall}, func(context.Context) (AnyResult, error) {
			initCalls++
			return cacheTestIntResult(lookupCall, 72), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, initCalls)
		assert.Assert(t, hitRes.HitCache())
		assert.Equal(t, 71, cacheTestUnwrapInt(t, hitRes))
		assert.Equal(t, cacheTestMustEncodeID(t, firstRes), cacheTestMustEncodeID(t, hitRes))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})
}

func TestIndexResultDigestsUsesExplicitRequestAndResponseIDs(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	requestExtra := call.ExtraDigest{
		Digest: digest.FromString("index-explicit-request-extra"),
		Label:  "request-extra",
	}
	responseExtra := call.ExtraDigest{
		Digest: digest.FromString("index-explicit-response-extra"),
		Label:  "response-extra",
	}
	requestCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "index-explicit-request",
		ExtraDigests: []call.ExtraDigest{
			requestExtra,
		},
	}
	responseCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "index-explicit-response",
		ExtraDigests: []call.ExtraDigest{
			responseExtra,
		},
	}

	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(NewInt(42), responseCall)
	})
	assert.NilError(t, err)
	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)

	requestDigest, err := requestCall.deriveRecipeDigest(c)
	assert.NilError(t, err)
	responseDigest, err := responseCall.deriveRecipeDigest(c)
	assert.NilError(t, err)
	for _, dig := range []digest.Digest{
		requestDigest,
		requestExtra.Digest,
		responseDigest,
		responseExtra.Digest,
	} {
		postings := c.egraphResultsByDigest[dig.String()]
		ok := postings != nil && postings.Contains(shared.id)
		assert.Assert(t, ok, "expected posting for digest %s", dig)
	}

	cacheTestReleaseSession(t, c, ctx)
}

func TestStructuralHitCanReuseResultFromSameOutputEqClass(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	shared := call.ExtraDigest{
		Digest: digest.FromString("shared-output-eq-class"),
		Label:  "shared",
	}

	t1Key := call.New().Append(Int(0).Type(), "output-eq-term-1")
	t1Call := cacheTestIntCall("output-eq-term-1")
	t2Key := call.New().Append(Int(0).Type(), "output-eq-term-2")
	t2Call := cacheTestIntCall("output-eq-term-2")
	assert.Assert(t, t1Key.Digest() != t2Key.Digest())

	t1OutCall := cacheTestIntCall("output-eq-out-1", shared)
	t2OutCall := cacheTestIntCall("output-eq-out-2", shared)

	t1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: t1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(t1OutCall, 11), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !t1Res.HitCache())

	t2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: t2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(t2OutCall, 22), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !t2Res.HitCache())

	initCalls := 0
	hitRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: t1Call}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(t1Call, 33), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, hitRes.HitCache())
	assert.Equal(t, 11, cacheTestUnwrapInt(t, hitRes))
	assert.Equal(t, cacheTestMustEncodeID(t, t1Res), cacheTestMustEncodeID(t, hitRes))

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestTeachCallEquivalentToResult(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	parentCall := cacheTestIntCall("teach-parent")
	parentRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: parentCall}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(42)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !parentRes.HitCache())

	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)

	childCall := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "teach-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentShared.id)},
	}

	assert.NilError(t, c.TeachCallEquivalentToResult(ctx, "test-session", childCall, parentRes))

	childInitCalls := 0
	childRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: childCall.clone()}, func(context.Context) (AnyResult, error) {
		childInitCalls++
		return cacheTestPlainResult(NewInt(99)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, childInitCalls)
	assert.Assert(t, childRes.HitCache())
	assert.Equal(t, 42, cacheTestUnwrapInt(t, childRes))
	assert.Equal(t, cacheTestMustEncodeID(t, parentRes), cacheTestMustEncodeID(t, childRes))

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestPendingResultCallRefRecipeID(t *testing.T) {
	t.Parallel()

	parentCall := cacheTestIntCall("pending-parent")
	childCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "pending-child",
		Receiver: &ResultCallRef{
			Call: parentCall.clone(),
		},
	}

	parentRecipeID, err := parentCall.recipeID(t.Context(), nil)
	assert.NilError(t, err)
	childRecipeID, err := childCall.recipeID(t.Context(), nil)
	assert.NilError(t, err)
	assert.Assert(t, childRecipeID.Receiver() != nil)
	assert.Equal(t, parentRecipeID.Digest(), childRecipeID.Receiver().Digest())
}

func TestAttachResultNormalizesPendingResultCallRef(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	parentCall := cacheTestIntCall("normalize-parent")
	parentRes, err := NewResultForCall(NewInt(1), parentCall)
	assert.NilError(t, err)
	attachedParent, err := c.AttachResult(ctx, "test-session", noopTypeResolver{}, parentRes)
	assert.NilError(t, err)

	childCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "normalize-child",
		Receiver: &ResultCallRef{
			Call: parentCall.clone(),
		},
	}
	childRes, err := NewResultForCall(NewInt(2), childCall)
	assert.NilError(t, err)
	attachedChild, err := c.AttachResult(ctx, "test-session", noopTypeResolver{}, childRes)
	assert.NilError(t, err)

	parentShared := attachedParent.cacheSharedResult()
	childShared := attachedChild.cacheSharedResult()
	assert.Assert(t, parentShared != nil && parentShared.id != 0)
	assert.Assert(t, childShared != nil && childShared.id != 0)
	assert.Assert(t, childShared.resultCall != nil)
	assert.Assert(t, childShared.resultCall.Receiver != nil)
	assert.Equal(t, uint64(parentShared.id), childShared.resultCall.Receiver.ResultID)
	assert.Assert(t, childShared.resultCall.Receiver.Call == nil)

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestObjectResultResultCallAndReceiver(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	srv := cacheTestServer(t)

	objType := (&cacheTestObject{}).Type()

	parentFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "parent",
		Type:  NewResultCallType(objType),
	}
	parentRes := cacheTestObjectResult(t, srv, parentFrame, 11, nil)
	attachedParentAny, err := cacheIface.AttachResult(ctx, "test-session", srv, parentRes)
	assert.NilError(t, err)
	attachedParent := attachedParentAny.(ObjectResult[*cacheTestObject])

	parentID, err := attachedParent.ID()
	assert.NilError(t, err)

	childFrame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "child",
		Type:  NewResultCallType(objType),
		Receiver: &ResultCallRef{
			ResultID: parentID.EngineResultID(),
		},
	}
	childRes := cacheTestObjectResult(t, srv, childFrame, 22, nil)
	attachedChildAny, err := cacheIface.AttachResult(ctx, "test-session", srv, childRes)
	assert.NilError(t, err)
	attachedChild := attachedChildAny.(ObjectResult[*cacheTestObject])

	childCall, err := attachedChild.ResultCall()
	assert.NilError(t, err)
	assert.Equal(t, "child", childCall.Field)
	assert.Equal(t, parentID.EngineResultID(), childCall.Receiver.ResultID)

	receiver, err := attachedChild.Receiver(ctx, srv)
	assert.NilError(t, err)
	assert.Assert(t, receiver != nil)

	receiverObj := receiver.(ObjectResult[*cacheTestObject])
	assert.Equal(t, 11, receiverObj.Self().Value)

	receiverCall, err := receiverObj.ResultCall()
	assert.NilError(t, err)
	assert.Equal(t, "parent", receiverCall.Field)
}

func TestCacheHitRewrapsObjectResultForCurrentServer(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)

	srvA := cacheTestObjectResolverServer(t, 1)
	srvB := cacheTestObjectResolverServer(t, 2)
	ctxA := srvToContext(ctx, srvA)
	ctxB := srvToContext(ctx, srvB)

	objAAny, err := srvA.Root().Select(ctxA, srvA, Selector{Field: "obj"})
	assert.NilError(t, err)
	objA := objAAny.(ObjectResult[*cacheTestObject])
	assert.Assert(t, !objA.HitCache())
	markerAAny, err := objA.Select(ctxA, srvA, Selector{Field: "marker"})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, markerAAny))

	objBAny, err := srvB.Root().Select(ctxB, srvB, Selector{Field: "obj"})
	assert.NilError(t, err)
	objB := objBAny.(ObjectResult[*cacheTestObject])
	assert.Assert(t, objB.HitCache())
	markerBAny, err := objB.Select(ctxB, srvB, Selector{Field: "marker"})
	assert.NilError(t, err)
	assert.Equal(t, 2, cacheTestUnwrapInt(t, markerBAny))

	cacheTestReleaseSession(t, cacheIface, ctxA)
}

func TestInputSpecsInputsFromResultCallArgs(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	specs := NewInputSpecs(
		InputSpec{Name: "msg", Type: String("")},
		InputSpec{Name: "count", Type: Int(0), Default: NewInt(7)},
	)
	args := []*ResultCallArg{
		{
			Name:  "msg",
			Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "hello"},
		},
	}

	inputs, err := specs.InputsFromResultCallArgs(ctx, args, "")
	assert.NilError(t, err)

	msg := inputs["msg"].(String)
	count := inputs["count"].(Int)
	assert.Equal(t, "hello", msg.String())
	assert.Equal(t, 7, count.Int())
}

func TestResultContentPreferredDigestUsesContentDigest(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	srv := cacheTestServer(t)

	contentDig := digest.FromString("service-content")
	objType := (&cacheTestObject{}).Type()
	frame := &ResultCall{
		Kind:         ResultCallKindField,
		Field:        "service",
		Type:         NewResultCallType(objType),
		ExtraDigests: []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: contentDig}},
	}
	res := cacheTestObjectResult(t, srv, frame, 33, nil)
	attachedAny, err := cacheIface.AttachResult(ctx, "test-session", srv, res)
	assert.NilError(t, err)
	attached := attachedAny.(ObjectResult[*cacheTestObject])

	got, err := attached.ContentPreferredDigest(ctx)
	assert.NilError(t, err)
	assert.Equal(t, contentDig.String(), got.String())

	recipeID, err := attached.RecipeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, contentDig.String(), recipeID.ContentDigest().String())
}

func TestLookupCacheForIDExtraDigestFallback(t *testing.T) {
	t.Parallel()

	t.Run("hit_on_exact_output_digest_match", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		ctx = ContextWithCache(ctx, cacheIface)
		c := cacheIface

		shared := call.ExtraDigest{
			Digest: digest.FromString("fallback-extra-shared"),
			Label:  "shared",
		}

		sourceKey := call.New().Append(Int(0).Type(), "_contextDirectory")
		sourceCall := cacheTestIntCall("_contextDirectory")
		sourceRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: sourceCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(&ResultCall{
				Kind:         ResultCallKindField,
				Type:         NewResultCallType(Int(0).Type()),
				Field:        "_contextDirectory",
				ExtraDigests: []call.ExtraDigest{shared},
			}, 71), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !sourceRes.HitCache())

		requestKey := sourceKey.With(
			call.WithArgs(call.NewArgument("variant", call.NewLiteralInt(1), false)),
			call.WithExtraDigest(shared),
		)
		requestCall := &ResultCall{
			Kind:         ResultCallKindField,
			Type:         NewResultCallType(Int(0).Type()),
			Field:        "_contextDirectory",
			ExtraDigests: []call.ExtraDigest{shared},
			Args: []*ResultCallArg{{
				Name:  "variant",
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: 1},
			}},
		}
		assert.Assert(t, sourceKey.Digest() != requestKey.Digest())

		requestInitCalls := 0
		requestRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			requestInitCalls++
			return cacheTestIntResult(requestCall, 999), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, requestInitCalls)
		assert.Assert(t, requestRes.HitCache())
		assert.Equal(t, 71, cacheTestUnwrapInt(t, requestRes))
		assert.Equal(t, cacheTestMustEncodeID(t, sourceRes), cacheTestMustEncodeID(t, requestRes))
		foundShared := false
		for _, extra := range cacheTestMustRecipeID(t, ctx, requestRes).ExtraDigests() {
			if extra.Digest == shared.Digest && extra.Label == shared.Label {
				foundShared = true
				break
			}
		}
		assert.Assert(t, foundShared)

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})

	t.Run("miss_without_exact_output_digest_match", func(t *testing.T) {
		ctx := cacheTestContext(t.Context())
		cacheIface, err := NewCache(ctx, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface

		sourceExtra := call.ExtraDigest{
			Digest: digest.FromString("fallback-extra-source"),
			Label:  "source",
		}
		requestExtra := call.ExtraDigest{
			Digest: digest.FromString("fallback-extra-request"),
			Label:  "request",
		}

		sourceCall := cacheTestIntCall("fallback-miss-source")
		sourceRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: sourceCall}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestIntCall("fallback-miss-source", sourceExtra), 81), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !sourceRes.HitCache())

		requestCall := cacheTestIntCall("fallback-miss-request", requestExtra)
		requestInitCalls := 0
		requestRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
			requestInitCalls++
			return cacheTestIntResult(requestCall, 82), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 1, requestInitCalls)
		assert.Assert(t, !requestRes.HitCache())
		assert.Equal(t, 82, cacheTestUnwrapInt(t, requestRes))

		cacheTestReleaseSession(t, c, ctx)
		assert.Equal(t, 0, c.Size())
	})
}

func TestHitTeachesReturnedRequestIDToCache(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	shared := call.ExtraDigest{
		Digest: digest.FromString("teach-hit-request-id-shared"),
		Label:  "eq-shared",
	}

	parentACall := cacheTestIntCall("teach-hit-parent-a")
	parentBCall := cacheTestIntCall("teach-hit-parent-b")
	parentAOutCall := cacheTestIntCall("teach-hit-parent-a", shared)
	parentBOutCall := cacheTestIntCall("teach-hit-parent-b", shared)

	parentARes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: parentACall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(parentAOutCall, 1), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !parentARes.HitCache())

	parentBRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: parentBCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(parentBOutCall, 2), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !parentBRes.HitCache())

	childARes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "teach-hit-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentARes.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(1001)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !childARes.HitCache())

	childBReq := &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "teach-hit-child",
		Receiver: &ResultCallRef{ResultID: uint64(parentBRes.cacheSharedResult().id)},
	}}
	childBInitCalls := 0
	childBRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, childBReq, func(context.Context) (AnyResult, error) {
		childBInitCalls++
		return cacheTestPlainResult(NewInt(1002)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, childBInitCalls)
	assert.Assert(t, childBRes.HitCache())
	assert.Equal(t, cacheTestMustEncodeID(t, childARes), cacheTestMustEncodeID(t, childBRes))

	childBDigest, err := childBReq.deriveRecipeDigest(c)
	assert.NilError(t, err)
	resolvedChildB, hit, err := c.lookupCacheForDigests(ctx, "test-session", noopTypeResolver{}, childBDigest, childBReq.ExtraDigests)
	assert.NilError(t, err)
	assert.Assert(t, hit)
	assert.Equal(t, childBRes.cacheSharedResult().id, resolvedChildB.cacheSharedResult().id)

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestExtraDigestLabelIsolation(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	sharedBytes := digest.FromString("label-isolation-shared-bytes")
	sharedA := call.ExtraDigest{Digest: sharedBytes, Label: "label-a"}
	sharedB := call.ExtraDigest{Digest: sharedBytes, Label: "label-b"}
	noiseA := call.ExtraDigest{Digest: digest.FromString("label-isolation-noise-a"), Label: "noise-a"}
	noiseB := call.ExtraDigest{Digest: digest.FromString("label-isolation-noise-b"), Label: "noise-b"}
	contentA := digest.FromString("label-isolation-content-a")
	contentB := digest.FromString("label-isolation-content-b")

	f1Key := call.New().Append(Int(0).Type(), "label-isolation-f-1")
	f2Key := call.New().Append(Int(0).Type(), "label-isolation-f-2")
	f1Call := cacheTestIntCall("label-isolation-f-1")
	f2Call := cacheTestIntCall("label-isolation-f-2")
	assert.Assert(t, f1Key.Digest() != f2Key.Digest())

	// Labels differ for the shared digest bytes, while content digests are also
	// present and intentionally different. This verifies label-only differences
	// are informational and do not block equivalence/hits.
	f1Out := f1Key.
		With(call.WithContentDigest(contentA)).
		With(call.WithExtraDigest(sharedA)).
		With(call.WithExtraDigest(noiseA))
	f2Out := f2Key.
		With(call.WithContentDigest(contentB)).
		With(call.WithExtraDigest(sharedB)).
		With(call.WithExtraDigest(noiseB))
	assert.Assert(t, contentA != contentB)
	assert.Equal(t, contentA.String(), f1Out.ContentDigest().String())
	assert.Equal(t, contentB.String(), f2Out.ContentDigest().String())
	f1HasLabelA := false
	f1HasLabelB := false
	for _, extra := range f1Out.ExtraDigests() {
		if extra.Digest != sharedBytes {
			continue
		}
		if extra.Label == sharedA.Label {
			f1HasLabelA = true
		}
		if extra.Label == sharedB.Label {
			f1HasLabelB = true
		}
	}
	f2HasLabelA := false
	f2HasLabelB := false
	for _, extra := range f2Out.ExtraDigests() {
		if extra.Digest != sharedBytes {
			continue
		}
		if extra.Label == sharedA.Label {
			f2HasLabelA = true
		}
		if extra.Label == sharedB.Label {
			f2HasLabelB = true
		}
	}
	assert.Assert(t, f1HasLabelA)
	assert.Assert(t, f2HasLabelB)
	assert.Assert(t, !f1HasLabelB)
	assert.Assert(t, !f2HasLabelA)

	f1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(&ResultCall{
			Kind:         ResultCallKindField,
			Type:         NewResultCallType(Int(0).Type()),
			Field:        "label-isolation-f-1",
			ExtraDigests: []call.ExtraDigest{sharedA, noiseA},
		}, 501).(Result[Int]).WithContentDigest(ctx, contentA)
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())

	f2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: f2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(&ResultCall{
			Kind:         ResultCallKindField,
			Type:         NewResultCallType(Int(0).Type()),
			Field:        "label-isolation-f-2",
			ExtraDigests: []call.ExtraDigest{sharedB, noiseB},
		}, 502).(Result[Int]).WithContentDigest(ctx, contentB)
	})
	assert.NilError(t, err)
	assert.Assert(t, !f2Res.HitCache())

	g1Key := f1Key.Append(Int(0).Type(), "label-isolation-g")
	g2Key := f2Key.Append(Int(0).Type(), "label-isolation-g")
	assert.Assert(t, g1Key.Digest() != g2Key.Digest())

	g1Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "label-isolation-g",
		Receiver: &ResultCallRef{ResultID: uint64(f1Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(601)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "label-isolation-g",
		Receiver: &ResultCallRef{ResultID: uint64(f2Res.cacheSharedResult().id)},
	}}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return cacheTestPlainResult(NewInt(602)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 601, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, cacheTestMustEncodeID(t, g1Res), cacheTestMustEncodeID(t, g2Res))

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.egraphDigestToClass))
}

func TestCacheHitReturnIDGetsContentDigestFromEqClassMetadata(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	contentDigest := digest.FromString("hit-return-id-content-digest")
	requestCall := cacheTestIntCall("hit-return-id-request")
	outputCall := cacheTestIntCall("hit-return-id-output")

	res1, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(outputCall, 77).(Result[Int]).WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, ctx, res1).ContentDigest().String())

	shared := res1.cacheSharedResult()
	assert.Assert(t, shared != nil)
	initCalls := 0
	res2, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(requestCall, 88), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, ctx, res2).ContentDigest().String())

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestCacheFreshReturnIDGetsContentDigestFromEqClassMetadata(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	contentDigest := digest.FromString("fresh-return-id-content-digest")
	sourceCall := cacheTestIntCall("fresh-return-id-source")
	sourceOutCall := cacheTestIntCall("fresh-return-id-output")

	sourceRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: sourceCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(sourceOutCall, 91).(Result[Int]).WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)
	shared := sourceRes.cacheSharedResult()
	assert.Assert(t, shared != nil)

	requestCall := cacheTestIntCall("fresh-return-id-request")
	wrappedRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return Result[Typed]{
			shared: shared,
		}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !wrappedRes.HitCache())
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, ctx, wrappedRes).ContentDigest().String())

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestCacheTeachContentDigestPreservesAttachment(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	reqCall := cacheTestIntCall("teach-content-digest")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 123), nil
	})
	assert.NilError(t, err)

	beforeID := cacheTestMustID(t, res)
	contentDigest := digest.FromString("teach-content-digest")
	assert.NilError(t, c.TeachContentDigest(ctx, res, contentDigest))

	afterID := cacheTestMustID(t, res)
	assert.Equal(t, beforeID.EngineResultID(), afterID.EngineResultID())
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, ctx, res).ContentDigest().String())

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)
	c.egraphMu.RLock()
	ok := c.egraphResultsByDigest[contentDigest.String()].Contains(shared.id)
	c.egraphMu.RUnlock()
	assert.Assert(t, ok)

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestCacheTeachContentDigestWithResultRefs(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cacheIface)
	c := cacheIface

	depCall := cacheTestIntCall("teach-content-digest-dep")
	depRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: depCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(depCall, 11), nil
	})
	assert.NilError(t, err)

	rootCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "teach-content-digest-root",
		Args: []*ResultCallArg{
			{
				Name: "dep",
				Value: &ResultCallLiteral{
					Kind: ResultCallLiteralKindResultRef,
					ResultRef: &ResultCallRef{
						ResultID: uint64(depRes.cacheSharedResult().id),
					},
				},
			},
		},
	}
	rootRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: rootCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(rootCall, 22), nil
	})
	assert.NilError(t, err)

	contentDigest := digest.FromString("teach-content-digest-with-result-refs")
	assert.NilError(t, c.TeachContentDigest(ctx, rootRes, contentDigest))
	assert.Equal(t, contentDigest.String(), cacheTestMustRecipeID(t, ctx, rootRes).ContentDigest().String())

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestDerefValueForNullables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    Typed
		expected int
	}{
		{
			name: "dynamic-nullable",
			value: DynamicNullable{
				Elem:  NewInt(0),
				Value: NewInt(21),
				Valid: true,
			},
			expected: 21,
		},
		{
			name: "nullable-generic",
			value: Nullable[Int]{
				Value: NewInt(42),
				Valid: true,
			},
			expected: 42,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call := &ResultCall{
				Kind:  ResultCallKindField,
				Type:  NewResultCallType(tc.value.Type()),
				Field: tc.name,
			}
			outer := cacheTestDetachedResult(call, tc.value)

			deref, ok := outer.DerefValue()
			assert.Assert(t, ok)
			assert.Equal(t, tc.expected, cacheTestUnwrapInt(t, deref))
		})
	}
}

func TestCacheDoNotCacheNormalizesNestedHitMetadata(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	innerCall := cacheTestIntCall("inner")
	innerRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: innerCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(innerCall, 9), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !innerRes.HitCache())

	outerCall := cacheTestIntCall("outer")
	outerRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: outerCall,
		DoNotCache: true,
	}, func(ctx context.Context) (AnyResult, error) {
		nested, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: innerCall}, func(context.Context) (AnyResult, error) {
			return nil, fmt.Errorf("unexpected nested initializer call")
		})
		if err != nil {
			return nil, err
		}
		assert.Assert(t, nested.HitCache())
		return nested, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !outerRes.HitCache())
	assert.Equal(t, 9, cacheTestUnwrapInt(t, outerRes))
	assert.Equal(t, 1, c.Size())
}

func TestCacheDoNotCachePreservesAttachedReturnedObject(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	srv := cacheTestServer(t)

	objectCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "attached-object",
	}
	innerAny, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: objectCall}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, objectCall, 17, nil), nil
	})
	assert.NilError(t, err)
	innerRes, ok := innerAny.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	innerID, err := innerRes.ID()
	assert.NilError(t, err)

	outerCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "donotcache-object",
	}
	outerAny, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{
		ResultCall: outerCall,
		DoNotCache: true,
	}, func(context.Context) (AnyResult, error) {
		return innerRes, nil
	})
	assert.NilError(t, err)

	outerRes, ok := outerAny.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Assert(t, !outerRes.HitCache())

	outerID, err := outerRes.ID()
	assert.NilError(t, err)
	assert.Equal(t, innerID.EngineResultID(), outerID.EngineResultID())
	assert.Equal(t, 1, c.Size())
}

func TestCacheSecondaryIndexesCleanedOnRelease(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	storageID := call.New().Append(Int(0).Type(), "storage-key")
	storageCall := cacheTestIntCall("storage-key")
	resultDigest := digest.FromString("result-digest")
	resultContent := digest.FromString("result-content")

	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: storageCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(&ResultCall{
			Kind:         ResultCallKindField,
			Type:         NewResultCallType(Int(0).Type()),
			Field:        "storage-key",
			ExtraDigests: []call.ExtraDigest{{Digest: resultDigest}},
		}, 44).(Result[Int]).WithContentDigest(ctx, resultContent)
	})
	assert.NilError(t, err)

	storageKey := storageID.Digest().String()
	resultOutputEq := resultContent.String()
	assert.Assert(t, storageKey != resultOutputEq)
	assert.Equal(t, 1, len(c.resultOutputEqClasses))
	assert.Assert(t, len(c.egraphTerms) > 0)
	assert.Assert(t, c.Size() > 0)

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, len(c.ongoingCalls))
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
}

func TestCacheReleaseRemovesDigestPostingsFromEntireOutputEqClass(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	ctxA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "release-eq-class-a",
		SessionID: "release-eq-class-a",
	})
	ctxB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "release-eq-class-b",
		SessionID: "release-eq-class-b",
	})
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	requestCall := cacheTestIntCall("release-eq-class-request")
	outputCall := cacheTestIntCall("release-eq-class-output")
	keeperCall := cacheTestIntCall("release-eq-class-keeper")
	foreignDigest := digest.FromString("release-eq-class-foreign")

	res, err := c.GetOrInitCall(ctxA, "release-eq-class-a", noopTypeResolver{}, &CallRequest{ResultCall: requestCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(outputCall, 44), nil
	})
	assert.NilError(t, err)
	_, err = c.GetOrInitCall(ctxB, "release-eq-class-b", noopTypeResolver{}, &CallRequest{ResultCall: keeperCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(keeperCall, 55), nil
	})
	assert.NilError(t, err)

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)

	c.egraphMu.Lock()
	outputEqClasses := c.outputEqClassesForResultLocked(shared.id)
	assert.Assert(t, len(outputEqClasses) > 0)

	var outputEqID eqClassID
	for eqID := range outputEqClasses {
		outputEqID = eqID
		break
	}
	assert.Assert(t, outputEqID != 0)

	foreignEqID := c.ensureEqClassForDigestLocked(ctxA, foreignDigest.String())
	outputEqID = c.mergeEqClassesLocked(ctxA, outputEqID, foreignEqID)
	assert.Assert(t, outputEqID != 0)
	assert.Assert(t, c.eqClassToDigests[outputEqID] != nil)
	_, ok := c.eqClassToDigests[outputEqID][foreignDigest.String()]
	assert.Assert(t, ok)

	foreignSet := c.egraphResultsByDigest[foreignDigest.String()]
	if foreignSet == nil {
		foreignSet = newSharedResultIDSet()
		c.egraphResultsByDigest[foreignDigest.String()] = foreignSet
	}
	foreignSet.Insert(shared.id)
	c.egraphMu.Unlock()

	assert.NilError(t, c.ReleaseSession(ctxA, "release-eq-class-a"))

	c.egraphMu.RLock()
	_, ok = c.egraphResultsByDigest[foreignDigest.String()]
	c.egraphMu.RUnlock()
	assert.Assert(t, !ok)
	assert.Equal(t, 1, c.Size())

	assert.NilError(t, c.ReleaseSession(ctxB, "release-eq-class-b"))
	assert.Equal(t, 0, c.Size())
}

func TestCacheArrayResultRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	keyCall := cacheTestIntCall("array-result")
	res1, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return cacheTestDetachedResult(&ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(NewIntArray[int]().Type()),
			Field: "array-result",
		}, NewIntArray(1, 2, 3)), nil
	})
	assert.NilError(t, err)
	enum1, ok := res1.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Equal(t, 3, enum1.Len())
	nth2, err := enum1.Nth(2)
	assert.NilError(t, err)
	v2, ok := nth2.(Int)
	assert.Assert(t, ok)
	assert.Equal(t, 2, int(v2))

	res2, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	enum2, ok := res2.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Equal(t, 3, enum2.Len())

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.Size())
}

func TestCacheArrayResultsRetainChildResultsAcrossProducerSessionRelease(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, field string, arrayType Typed, initParent func(*ResultCall, ObjectResult[*cacheTestObject], ObjectResult[*cacheTestObject]) AnyResult) {
		t.Helper()

		baseCtx := t.Context()
		ctxA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
			ClientID:  field + "-owner-client",
			SessionID: field + "-owner-session",
		})
		ctxB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
			ClientID:  field + "-consumer-client",
			SessionID: field + "-consumer-session",
		})

		cacheIface, err := NewCache(ctxA, "", nil, nil)
		assert.NilError(t, err)
		c := cacheIface
		ctxA = ContextWithCache(ctxA, c)
		ctxB = ContextWithCache(ctxB, c)
		srv := cacheTestServer(t)

		child1Call := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: field + "-child-1",
		}
		child2Call := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: field + "-child-2",
		}
		arrayCall := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(arrayType.Type()),
			Field: field,
		}

		child1Any, err := c.GetOrInitCall(ctxA, field+"-owner-session", srv, &CallRequest{ResultCall: child1Call}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResult(t, srv, child1Call, 1, nil), nil
		})
		assert.NilError(t, err)
		child1 := child1Any.(ObjectResult[*cacheTestObject])
		child1Shared := child1.cacheSharedResult()
		assert.Assert(t, child1Shared != nil)
		assert.Assert(t, child1Shared.id != 0)

		child2Any, err := c.GetOrInitCall(ctxA, field+"-owner-session", srv, &CallRequest{ResultCall: child2Call}, func(context.Context) (AnyResult, error) {
			return cacheTestObjectResult(t, srv, child2Call, 2, nil), nil
		})
		assert.NilError(t, err)
		child2 := child2Any.(ObjectResult[*cacheTestObject])

		parentAny, err := c.GetOrInitCall(ctxA, field+"-owner-session", srv, &CallRequest{ResultCall: arrayCall}, func(context.Context) (AnyResult, error) {
			return initParent(arrayCall, child1, child2), nil
		})
		assert.NilError(t, err)
		parentShared := parentAny.cacheSharedResult()
		assert.Assert(t, parentShared != nil)
		assert.Assert(t, parentShared.id != 0)

		hitAny, err := c.GetOrInitCall(ctxB, field+"-consumer-session", srv, &CallRequest{ResultCall: arrayCall}, func(context.Context) (AnyResult, error) {
			return nil, fmt.Errorf("unexpected initializer call")
		})
		assert.NilError(t, err)
		assert.Assert(t, hitAny.HitCache())

		enum, ok := hitAny.Unwrap().(Enumerable)
		assert.Assert(t, ok)
		firstAny, err := enum.NthValue(1, nil)
		assert.NilError(t, err)
		firstChild := firstAny.(ObjectResult[*cacheTestObject])
		_, err = firstChild.ResultCall()
		assert.NilError(t, err)

		assert.NilError(t, c.ReleaseSession(ctxA, field+"-owner-session"))

		c.egraphMu.RLock()
		parentStillLive := c.resultsByID[parentShared.id] != nil
		childStillLive := c.resultsByID[child1Shared.id] != nil
		c.egraphMu.RUnlock()
		assert.Assert(t, parentStillLive)
		assert.Assert(t, childStillLive)

		childCall, err := firstChild.ResultCall()
		assert.NilError(t, err)
		assert.Assert(t, childCall != nil)

		firstID, err := firstChild.ID()
		assert.NilError(t, err)
		reloadedAny, err := srv.Load(ctxB, firstID)
		assert.NilError(t, err)
		reloaded, ok := reloadedAny.(ObjectResult[*cacheTestObject])
		assert.Assert(t, ok)
		assert.Equal(t, 1, reloaded.Self().Value)

		assert.NilError(t, c.ReleaseSession(ctxB, field+"-consumer-session"))
		assert.Equal(t, 0, c.Size())
	}

	t.Run("ObjectResultArray", func(t *testing.T) {
		run(t, "object-array-result", ObjectResultArray[*cacheTestObject]{}, func(arrayCall *ResultCall, child1, child2 ObjectResult[*cacheTestObject]) AnyResult {
			return cacheTestDetachedResult(arrayCall, ObjectResultArray[*cacheTestObject]{child1, child2})
		})
	})

	t.Run("DynamicResultArrayOutput", func(t *testing.T) {
		run(t, "dynamic-result-array", DynamicResultArrayOutput{Elem: &cacheTestObject{}}, func(arrayCall *ResultCall, child1, child2 ObjectResult[*cacheTestObject]) AnyResult {
			return cacheTestDetachedResult(arrayCall, DynamicResultArrayOutput{
				Elem:   &cacheTestObject{},
				Values: []AnyResult{child1, child2},
			})
		})
	})
}

func TestCacheArrayResultStressDoesNotReturnHitWithoutCallFrame(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	srv := cacheTestServer(t)

	seedCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "stress-array-seed-client",
		SessionID: "stress-array-seed-session",
	})
	seedCtx = ContextWithCache(seedCtx, c)
	seedCtx = srvToContext(seedCtx, srv)

	child1Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-array-child-1",
	}
	child2Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-array-child-2",
	}
	arrayCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(ObjectResultArray[*cacheTestObject]{}.Type()),
		Field: "stress-array-parent",
	}

	child1Any, err := c.GetOrInitCall(seedCtx, "stress-array-seed-session", srv, &CallRequest{ResultCall: child1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child1Call, 1, nil), nil
	})
	assert.NilError(t, err)
	child1 := child1Any.(ObjectResult[*cacheTestObject])

	child2Any, err := c.GetOrInitCall(seedCtx, "stress-array-seed-session", srv, &CallRequest{ResultCall: child2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child2Call, 2, nil), nil
	})
	assert.NilError(t, err)
	child2 := child2Any.(ObjectResult[*cacheTestObject])

	newSessionCtx := func(worker, iter int) context.Context {
		ctx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
			ClientID:  fmt.Sprintf("stress-array-client-%d-%d", worker, iter),
			SessionID: fmt.Sprintf("stress-array-session-%d-%d", worker, iter),
		})
		ctx = ContextWithCache(ctx, c)
		ctx = srvToContext(ctx, srv)
		return ctx
	}

	buildArray := func(ctx context.Context, sessionID string) (AnyResult, error) {
		return c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: arrayCall}, func(context.Context) (AnyResult, error) {
			return cacheTestDetachedResult(arrayCall, ObjectResultArray[*cacheTestObject]{child1, child2}), nil
		})
	}

	const (
		workers           = 24
		attemptsPerWorker = 1000
	)
	isTargetFailure := func(err error) bool {
		if err == nil {
			return false
		}
		return strings.Contains(err.Error(), "without call frame") ||
			strings.Contains(err.Error(), "has no call frame")
	}

	const ownerSessionID = "stress-array-owner-session"
	errConsumerInit := errors.New("stress consumer should only hit cache")

	ownerCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "stress-array-owner-client",
		SessionID: ownerSessionID,
	})
	ownerCtx = ContextWithCache(ownerCtx, c)
	ownerCtx = srvToContext(ownerCtx, srv)

	_, err = buildArray(ownerCtx, ownerSessionID)
	assert.NilError(t, err)

	start := make(chan struct{})
	stopProducer := make(chan struct{})
	producerDone := make(chan struct{})
	producerErrCh := make(chan error, 1)
	var attempts atomic.Int64
	var hitCount atomic.Int64
	var failure atomic.Pointer[string]

	go func() {
		defer close(producerDone)
		<-start
		for iter := 0; ; iter++ {
			select {
			case <-stopProducer:
				return
			default:
			}

			if _, err := buildArray(ownerCtx, ownerSessionID); err != nil {
				producerErrCh <- err
				return
			}
			time.Sleep(50 * time.Microsecond)
			if err := c.ReleaseSession(ownerCtx, ownerSessionID); err != nil {
				producerErrCh <- err
				return
			}
		}
	}()

	eg, egCtx := errgroup.WithContext(baseCtx)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		eg.Go(func() error {
			<-start
			for iter := 0; iter < attemptsPerWorker; iter++ {
				if egCtx.Err() != nil {
					return egCtx.Err()
				}

				ctx := newSessionCtx(worker, iter)
				clientMD, err := engine.ClientMetadataFromContext(ctx)
				if err != nil {
					return err
				}

				res, err := c.GetOrInitCall(ctx, clientMD.SessionID, srv, &CallRequest{ResultCall: arrayCall}, func(context.Context) (AnyResult, error) {
					return nil, errConsumerInit
				})
				attempts.Add(1)
				if err != nil {
					if errors.Is(err, errConsumerInit) {
						continue
					}
					return err
				}
				if res.HitCache() {
					hitCount.Add(1)
				}

				if _, err := res.ResultCall(); err != nil {
					if isTargetFailure(err) {
						msg := fmt.Sprintf("result call failure on worker=%d iter=%d hit=%v: %v", worker, iter, res.HitCache(), err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}

				nth, err := res.NthValue(ctx, 1)
				if err != nil {
					if isTargetFailure(err) {
						msg := fmt.Sprintf("nth failure on worker=%d iter=%d hit=%v: %v", worker, iter, res.HitCache(), err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}
				if nth == nil {
					return fmt.Errorf("worker=%d iter=%d: nil nth result", worker, iter)
				}
				if _, err := nth.ResultCall(); err != nil {
					if isTargetFailure(err) {
						msg := fmt.Sprintf("nth result call failure on worker=%d iter=%d hit=%v: %v", worker, iter, res.HitCache(), err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}

				if err := c.ReleaseSession(ctx, clientMD.SessionID); err != nil {
					return err
				}
			}
			return nil
		})
	}
	close(start)

	err = eg.Wait()
	close(stopProducer)
	<-producerDone
	select {
	case producerErr := <-producerErrCh:
		err = errors.Join(err, producerErr)
	default:
	}
	assert.NilError(t, c.ReleaseSession(seedCtx, "stress-array-seed-session"))
	if msg := failure.Load(); msg != nil {
		t.Fatalf("reproduced array hit call-frame race after %d attempts and %d hits: %s", attempts.Load(), hitCount.Load(), *msg)
	}
	assert.NilError(t, err)
	t.Logf("completed %d attempts and %d cache hits without reproducing the array hit call-frame race", attempts.Load(), hitCount.Load())
}

func TestCacheArrayResultStressDoesNotRaceExplicitDependencyAttachment(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	srv := cacheTestServer(t)

	seedCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "stress-attach-seed-client",
		SessionID: "stress-attach-seed-session",
	})
	seedCtx = ContextWithCache(seedCtx, c)
	seedCtx = srvToContext(seedCtx, srv)

	child1Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-attach-child-1",
	}
	child2Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-attach-child-2",
	}
	arrayCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(ObjectResultArray[*cacheTestObject]{}.Type()),
		Field: "stress-attach-parent",
	}

	child1Any, err := c.GetOrInitCall(seedCtx, "stress-attach-seed-session", srv, &CallRequest{ResultCall: child1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child1Call, 1, nil), nil
	})
	assert.NilError(t, err)
	child1 := child1Any.(ObjectResult[*cacheTestObject])

	child2Any, err := c.GetOrInitCall(seedCtx, "stress-attach-seed-session", srv, &CallRequest{ResultCall: child2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child2Call, 2, nil), nil
	})
	assert.NilError(t, err)
	child2 := child2Any.(ObjectResult[*cacheTestObject])

	buildArray := func(ctx context.Context, sessionID string) (AnyResult, error) {
		return c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: arrayCall}, func(context.Context) (AnyResult, error) {
			return cacheTestDetachedResult(arrayCall, ObjectResultArray[*cacheTestObject]{child1, child2}), nil
		})
	}

	const (
		workers             = 24
		attemptsPerWorker   = 400
		targetFailureSubstr = "add explicit dependency: parent result"
	)

	newSessionCtx := func(worker, iter int) context.Context {
		ctx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
			ClientID:  fmt.Sprintf("stress-attach-client-%d-%d", worker, iter),
			SessionID: fmt.Sprintf("stress-attach-session-%d-%d", worker, iter),
		})
		ctx = ContextWithCache(ctx, c)
		ctx = srvToContext(ctx, srv)
		return ctx
	}

	start := make(chan struct{})
	var attempts atomic.Int64
	var hitCount atomic.Int64
	var failure atomic.Pointer[string]

	eg, egCtx := errgroup.WithContext(baseCtx)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		eg.Go(func() error {
			<-start
			for iter := 0; iter < attemptsPerWorker; iter++ {
				if egCtx.Err() != nil {
					return egCtx.Err()
				}

				ctx := newSessionCtx(worker, iter)
				clientMD, err := engine.ClientMetadataFromContext(ctx)
				if err != nil {
					return err
				}

				res, err := buildArray(ctx, clientMD.SessionID)
				attempts.Add(1)
				if err != nil {
					if strings.Contains(err.Error(), targetFailureSubstr) {
						msg := fmt.Sprintf("explicit dependency attachment failure on worker=%d iter=%d: %v", worker, iter, err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}
				if res.HitCache() {
					hitCount.Add(1)
				}

				if err := c.ReleaseSession(ctx, clientMD.SessionID); err != nil {
					return err
				}
			}
			return nil
		})
	}
	close(start)

	err = eg.Wait()
	assert.NilError(t, c.ReleaseSession(seedCtx, "stress-attach-seed-session"))
	if msg := failure.Load(); msg != nil {
		t.Fatalf("reproduced explicit dependency attachment race after %d attempts and %d cache hits: %s", attempts.Load(), hitCount.Load(), *msg)
	}
	assert.NilError(t, err)
	t.Logf("completed %d attempts and %d cache hits without reproducing the explicit dependency attachment race", attempts.Load(), hitCount.Load())
}

func TestCacheMixedSessionWaitersCancelDoNotLeak(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	srv := cacheTestServer(t)

	seedCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "stress-mixed-seed-client",
		SessionID: "stress-mixed-seed-session",
	})
	seedCtx = ContextWithCache(seedCtx, c)
	seedCtx = srvToContext(seedCtx, srv)

	child1Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-mixed-child-1",
	}
	child2Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-mixed-child-2",
	}

	child1Any, err := c.GetOrInitCall(seedCtx, "stress-mixed-seed-session", srv, &CallRequest{ResultCall: child1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child1Call, 1, nil), nil
	})
	assert.NilError(t, err)
	child1 := child1Any.(ObjectResult[*cacheTestObject])

	child2Any, err := c.GetOrInitCall(seedCtx, "stress-mixed-seed-session", srv, &CallRequest{ResultCall: child2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child2Call, 2, nil), nil
	})
	assert.NilError(t, err)
	child2 := child2Any.(ObjectResult[*cacheTestObject])

	child1Shared := child1.cacheSharedResult()
	child2Shared := child2.cacheSharedResult()
	assert.Assert(t, child1Shared != nil && child1Shared.id != 0)
	assert.Assert(t, child2Shared != nil && child2Shared.id != 0)

	const (
		attempts          = 100
		waitersPerAttempt = 18
		earlyCanceled     = 6
		racingCanceled    = 6
		concurrencyKey    = "stress-mixed-shared"
	)

	type waiterSpec struct {
		waitCtx    context.Context
		releaseCtx context.Context
		cancel     context.CancelFunc
		sessionID  string
	}

	type waiterOutcome struct {
		res AnyResult
		err error
	}

	for attempt := 0; attempt < attempts; attempt++ {
		parentCall := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(ObjectResultArray[*cacheTestObject]{}.Type()),
			Field: fmt.Sprintf("stress-mixed-parent-%d", attempt),
		}
		callConcKeys := callConcurrencyKeys{
			callKey:        cacheTestCallDigest(parentCall).String(),
			concurrencyKey: concurrencyKey,
		}

		waiterSpecs := make([]waiterSpec, waitersPerAttempt)
		waiterOutcomes := make([]waiterOutcome, waitersPerAttempt)
		initStarted := make(chan struct{})
		unblockInit := make(chan struct{})
		var initStartedOnce sync.Once
		var initCalls atomic.Int32
		var wg sync.WaitGroup

		for i := 0; i < waitersPerAttempt; i++ {
			sessionID := fmt.Sprintf("stress-mixed-session-%d-%d", attempt, i)
			sessionCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
				ClientID:  fmt.Sprintf("stress-mixed-client-%d-%d", attempt, i),
				SessionID: sessionID,
			})
			sessionCtx = ContextWithCache(sessionCtx, c)
			sessionCtx = srvToContext(sessionCtx, srv)
			waitCtx, cancel := context.WithCancel(sessionCtx)
			waiterSpecs[i] = waiterSpec{
				waitCtx:    waitCtx,
				releaseCtx: sessionCtx,
				cancel:     cancel,
				sessionID:  sessionID,
			}

			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				res, err := c.GetOrInitCall(waiterSpecs[i].waitCtx, waiterSpecs[i].sessionID, srv, &CallRequest{
					ResultCall:     parentCall,
					ConcurrencyKey: concurrencyKey,
				}, func(context.Context) (AnyResult, error) {
					initCalls.Add(1)
					initStartedOnce.Do(func() { close(initStarted) })
					<-unblockInit
					return cacheTestDetachedResult(parentCall, ObjectResultArray[*cacheTestObject]{child1, child2}), nil
				})
				waiterOutcomes[i] = waiterOutcome{
					res: res,
					err: err,
				}
			}(i)
		}

		select {
		case <-initStarted:
		case <-time.After(5 * time.Second):
			t.Fatalf("attempt %d: timed out waiting for init start", attempt)
		}

		waiterCountReached := false
		waiterPollDeadline := time.Now().Add(5 * time.Second)
		lastObservedWaiters := -1
		for time.Now().Before(waiterPollDeadline) {
			c.callsMu.Lock()
			oc := c.ongoingCalls[callConcKeys]
			if oc != nil {
				lastObservedWaiters = oc.waiters
			}
			c.callsMu.Unlock()

			if oc != nil && lastObservedWaiters == waitersPerAttempt {
				waiterCountReached = true
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		assert.Assert(t, waiterCountReached, "attempt %d: expected %d waiters, last observed %d", attempt, waitersPerAttempt, lastObservedWaiters)

		for i := 0; i < earlyCanceled; i++ {
			waiterSpecs[i].cancel()
		}
		time.Sleep(1 * time.Millisecond)
		for i := earlyCanceled; i < earlyCanceled+racingCanceled; i++ {
			go waiterSpecs[i].cancel()
		}
		close(unblockInit)

		wg.Wait()
		assert.Equal(t, int32(1), initCalls.Load(), "attempt %d: expected exactly one initializer", attempt)

		earlyCanceledCount := 0
		racingCanceledCount := 0
		successCount := 0
		for i, outcome := range waiterOutcomes {
			switch {
			case i < earlyCanceled:
				assert.Assert(t, is.ErrorIs(outcome.err, context.Canceled), "attempt %d waiter %d: expected context canceled, got %v", attempt, i, outcome.err)
				earlyCanceledCount++
			case i < earlyCanceled+racingCanceled:
				if outcome.err != nil {
					assert.Assert(t, is.ErrorIs(outcome.err, context.Canceled), "attempt %d waiter %d: expected nil or context canceled, got %v", attempt, i, outcome.err)
					racingCanceledCount++
					continue
				}
				assert.Assert(t, outcome.res != nil, "attempt %d waiter %d: expected result on success", attempt, i)
				_, err := outcome.res.ResultCall()
				assert.NilError(t, err, "attempt %d waiter %d: result call", attempt, i)
				nth, err := outcome.res.NthValue(waiterSpecs[i].releaseCtx, 1)
				assert.NilError(t, err, "attempt %d waiter %d: nth value", attempt, i)
				assert.Assert(t, nth != nil, "attempt %d waiter %d: nil nth result", attempt, i)
				_, err = nth.ResultCall()
				assert.NilError(t, err, "attempt %d waiter %d: nth result call", attempt, i)
				successCount++
			default:
				assert.NilError(t, outcome.err, "attempt %d waiter %d: expected success", attempt, i)
				assert.Assert(t, outcome.res != nil, "attempt %d waiter %d: expected result on success", attempt, i)
				_, err := outcome.res.ResultCall()
				assert.NilError(t, err, "attempt %d waiter %d: result call", attempt, i)
				nth, err := outcome.res.NthValue(waiterSpecs[i].releaseCtx, 1)
				assert.NilError(t, err, "attempt %d waiter %d: nth value", attempt, i)
				assert.Assert(t, nth != nil, "attempt %d waiter %d: nil nth result", attempt, i)
				_, err = nth.ResultCall()
				assert.NilError(t, err, "attempt %d waiter %d: nth result call", attempt, i)
				successCount++
			}
		}
		assert.Equal(t, earlyCanceled, earlyCanceledCount, "attempt %d: expected all early canceled waiters to return canceled", attempt)
		assert.Assert(t, successCount >= waitersPerAttempt-earlyCanceled-racingCanceled, "attempt %d: expected at least uncanceled waiters to succeed, got %d", attempt, successCount)

		for _, spec := range waiterSpecs {
			assert.NilError(t, c.ReleaseSession(spec.releaseCtx, spec.sessionID), "attempt %d release session %s", attempt, spec.sessionID)
			spec.cancel()
		}

		c.callsMu.Lock()
		_, ongoingStillPresent := c.ongoingCalls[callConcKeys]
		ongoingCallsCount := len(c.ongoingCalls)
		c.callsMu.Unlock()
		assert.Assert(t, !ongoingStillPresent, "attempt %d: ongoing call still present", attempt)
		assert.Equal(t, 0, ongoingCallsCount, "attempt %d: unexpected ongoing call count", attempt)

		c.sessionMu.Lock()
		remainingSessionCount := len(c.sessionResultIDsBySession)
		seedResults := c.sessionResultIDsBySession["stress-mixed-seed-session"]
		c.sessionMu.Unlock()
		assert.Equal(t, 1, remainingSessionCount, "attempt %d: expected only seed session to remain", attempt)
		assert.Assert(t, seedResults != nil)
		assert.Equal(t, 2, len(seedResults), "attempt %d: expected seed session to retain only child results", attempt)

		c.egraphMu.RLock()
		resultCount := len(c.resultsByID)
		child1StillPresent := c.resultsByID[child1Shared.id] != nil
		child2StillPresent := c.resultsByID[child2Shared.id] != nil
		c.egraphMu.RUnlock()
		assert.Equal(t, 2, resultCount, "attempt %d: expected only child results to remain", attempt)
		assert.Assert(t, child1StillPresent, "attempt %d: child1 missing after waiter releases", attempt)
		assert.Assert(t, child2StillPresent, "attempt %d: child2 missing after waiter releases", attempt)
	}

	assert.NilError(t, c.ReleaseSession(seedCtx, "stress-mixed-seed-session"))

	c.callsMu.Lock()
	remainingOngoingCalls := len(c.ongoingCalls)
	c.callsMu.Unlock()
	c.sessionMu.Lock()
	remainingSessionCount := len(c.sessionResultIDsBySession)
	c.sessionMu.Unlock()
	c.egraphMu.RLock()
	remainingResults := len(c.resultsByID)
	c.egraphMu.RUnlock()

	assert.Equal(t, 0, remainingOngoingCalls)
	assert.Equal(t, 0, remainingSessionCount)
	assert.Equal(t, 0, remainingResults)
	assert.Equal(t, 0, c.Size())
}

func TestCacheLoadResultByResultIDDoesNotReturnHitWithoutCallFrame(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	srv := cacheTestServer(t)

	seedCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "stress-load-by-id-seed-client",
		SessionID: "stress-load-by-id-seed-session",
	})
	seedCtx = ContextWithCache(seedCtx, c)
	seedCtx = srvToContext(seedCtx, srv)

	child1Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-load-by-id-child-1",
	}
	child2Call := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "stress-load-by-id-child-2",
	}
	arrayCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(ObjectResultArray[*cacheTestObject]{}.Type()),
		Field: "stress-load-by-id-parent",
	}

	child1Any, err := c.GetOrInitCall(seedCtx, "stress-load-by-id-seed-session", srv, &CallRequest{ResultCall: child1Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child1Call, 1, nil), nil
	})
	assert.NilError(t, err)
	child1 := child1Any.(ObjectResult[*cacheTestObject])

	child2Any, err := c.GetOrInitCall(seedCtx, "stress-load-by-id-seed-session", srv, &CallRequest{ResultCall: child2Call}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, child2Call, 2, nil), nil
	})
	assert.NilError(t, err)
	child2 := child2Any.(ObjectResult[*cacheTestObject])

	newSessionCtx := func(worker, iter int) context.Context {
		ctx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
			ClientID:  fmt.Sprintf("stress-load-by-id-client-%d-%d", worker, iter),
			SessionID: fmt.Sprintf("stress-load-by-id-session-%d-%d", worker, iter),
		})
		ctx = ContextWithCache(ctx, c)
		ctx = srvToContext(ctx, srv)
		return ctx
	}

	buildArray := func(ctx context.Context, sessionID string) (AnyResult, error) {
		return c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: arrayCall}, func(context.Context) (AnyResult, error) {
			return cacheTestDetachedResult(arrayCall, ObjectResultArray[*cacheTestObject]{child1, child2}), nil
		})
	}

	const (
		workers           = 24
		attemptsPerWorker = 1000
	)
	isTargetFailure := func(err error) bool {
		if err == nil {
			return false
		}
		return strings.Contains(err.Error(), "without call frame") ||
			strings.Contains(err.Error(), "has no call frame")
	}

	const ownerSessionID = "stress-load-by-id-owner-session"
	ownerCtx := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "stress-load-by-id-owner-client",
		SessionID: ownerSessionID,
	})
	ownerCtx = ContextWithCache(ownerCtx, c)
	ownerCtx = srvToContext(ownerCtx, srv)

	ownerRes, err := buildArray(ownerCtx, ownerSessionID)
	assert.NilError(t, err)
	ownerShared := ownerRes.cacheSharedResult()
	assert.Assert(t, ownerShared != nil && ownerShared.id != 0)

	var currentResultID atomic.Uint64
	currentResultID.Store(uint64(ownerShared.id))

	start := make(chan struct{})
	stopOwner := make(chan struct{})
	ownerDone := make(chan struct{})
	ownerErrCh := make(chan error, 1)
	var attempts atomic.Int64
	var loadCount atomic.Int64
	var failure atomic.Pointer[string]

	go func() {
		defer close(ownerDone)
		<-start
		for {
			select {
			case <-stopOwner:
				return
			default:
			}

			res, err := buildArray(ownerCtx, ownerSessionID)
			if err != nil {
				ownerErrCh <- err
				return
			}
			shared := res.cacheSharedResult()
			if shared == nil || shared.id == 0 {
				ownerErrCh <- fmt.Errorf("owner result missing shared result id")
				return
			}
			currentResultID.Store(uint64(shared.id))

			time.Sleep(50 * time.Microsecond)

			if err := c.ReleaseSession(ownerCtx, ownerSessionID); err != nil {
				ownerErrCh <- err
				return
			}
		}
	}()

	eg, egCtx := errgroup.WithContext(baseCtx)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		eg.Go(func() error {
			<-start
			for iter := 0; iter < attemptsPerWorker; iter++ {
				if egCtx.Err() != nil {
					return egCtx.Err()
				}

				resultID := currentResultID.Load()
				if resultID == 0 {
					continue
				}

				ctx := newSessionCtx(worker, iter)
				clientMD, err := engine.ClientMetadataFromContext(ctx)
				if err != nil {
					return err
				}

				res, err := c.LoadResultByResultID(ctx, clientMD.SessionID, srv, resultID)
				attempts.Add(1)
				if err != nil {
					if strings.Contains(err.Error(), "missing shared result") {
						continue
					}
					return err
				}
				loadCount.Add(1)

				if _, err := res.ResultCall(); err != nil {
					if isTargetFailure(err) {
						msg := fmt.Sprintf("result call failure on worker=%d iter=%d resultID=%d: %v", worker, iter, resultID, err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}

				nth, err := res.NthValue(ctx, 1)
				if err != nil {
					if isTargetFailure(err) {
						msg := fmt.Sprintf("nth failure on worker=%d iter=%d resultID=%d: %v", worker, iter, resultID, err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}
				if nth == nil {
					return fmt.Errorf("worker=%d iter=%d: nil nth result", worker, iter)
				}
				if _, err := nth.ResultCall(); err != nil {
					if isTargetFailure(err) {
						msg := fmt.Sprintf("nth result call failure on worker=%d iter=%d resultID=%d: %v", worker, iter, resultID, err)
						failure.Store(&msg)
						return fmt.Errorf("%s", msg)
					}
					return err
				}

				if err := c.ReleaseSession(ctx, clientMD.SessionID); err != nil {
					return err
				}
			}
			return nil
		})
	}
	close(start)

	err = eg.Wait()
	close(stopOwner)
	<-ownerDone
	select {
	case ownerErr := <-ownerErrCh:
		err = errors.Join(err, ownerErr)
	default:
	}
	assert.NilError(t, c.ReleaseSession(ownerCtx, ownerSessionID))
	assert.NilError(t, c.ReleaseSession(seedCtx, "stress-load-by-id-seed-session"))
	if msg := failure.Load(); msg != nil {
		t.Fatalf("reproduced LoadResultByResultID call-frame race after %d attempts and %d loads: %s", attempts.Load(), loadCount.Load(), *msg)
	}
	assert.NilError(t, err)
	t.Logf("completed %d attempts and %d successful loads without reproducing the LoadResultByResultID call-frame race", attempts.Load(), loadCount.Load())
}

func TestCacheObjectResultRoundTripAndRelease(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface
	srv := cacheTestServer(t)

	keyCall := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "object-result",
	}
	var releaseCalls atomic.Int32

	res1, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, keyCall, 42, func(context.Context) error {
			releaseCalls.Add(1)
			return nil
		}), nil
	})
	assert.NilError(t, err)
	obj1, ok := res1.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj1.Self().Value)
	assert.Assert(t, !obj1.HitCache())

	res2, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: keyCall}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	obj2, ok := res2.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj2.Self().Value)
	assert.Assert(t, obj2.HitCache())

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, int32(1), releaseCalls.Load())
	assert.Equal(t, 0, c.Size())
}

func TestCacheTTLWithDBUsesStorageAndCallIndexes(t *testing.T) {
	t.Parallel()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "cache-test-client",
		SessionID: "cache-test-session",
	})
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	keyCall := cacheTestIntCall("ttl-key")
	initCalls := 0

	_, err = c.GetOrInitCall(ctx, "cache-test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: keyCall,
		TTL:        60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyCall, 5), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)

	res2, err := c.GetOrInitCall(ctx, "cache-test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: keyCall,
		TTL:        60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyCall, 6), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	cacheTestReleaseSession(t, c, ctx)
	// Persist-safe only affects DB metadata persistence; in-memory cache entries are
	// released when refs drain.
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
	assert.Equal(t, 0, c.Size())
}

func TestCachePersistableRetainedAcrossSessionClose(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	base := cacheIface

	key := cacheTestIntCall("persistable-across-session-close")
	ctxSessionA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-client-a",
		SessionID: "persistable-session-a",
	})
	ctxSessionB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "persistable-client-b",
		SessionID: "persistable-session-b",
	})

	initCallsA := 0
	resA, err := base.GetOrInitCall(ctxSessionA, "persistable-session-a", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		initCallsA++
		return cacheTestIntResult(key, 41), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCallsA)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, 1, base.EntryStats().RetainedCalls)

	assert.NilError(t, base.ReleaseSession(ctxSessionA, "persistable-session-a"))
	assert.Equal(t, 1, base.EntryStats().RetainedCalls)
	assert.Equal(t, 1, base.Size())

	initCallsB := 0
	resB, err := base.GetOrInitCall(ctxSessionB, "persistable-session-b", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		initCallsB++
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCallsB)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 41, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, base.ReleaseSession(ctxSessionB, "persistable-session-b"))
	assert.Equal(t, 1, base.EntryStats().RetainedCalls)
	assert.Equal(t, 1, base.Size())
}

func TestCacheNonPersistableDropsWhenRefsDrain(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := cacheTestIntCall("non-persistable-drops")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: false,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 7), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)
	assert.Equal(t, 0, len(c.resultOutputEqClasses))
	assert.Equal(t, 0, c.Size())
}

func TestCachePersistableHitUpgradesExistingResultToRetained(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := cacheTestIntCall("persistable-hit-upgrade")
	initCalls := 0

	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: false,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(key, 17), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)

	resB, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 17, cacheTestUnwrapInt(t, resB))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)

	cacheTestReleaseSession(t, c, ctx)
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Equal(t, 1, len(c.resultOutputEqClasses))

	initCallsAfter := 0
	resC, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		initCallsAfter++
		return cacheTestIntResult(key, 123), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCallsAfter)
	assert.Assert(t, resC.HitCache())
	assert.Equal(t, 17, cacheTestUnwrapInt(t, resC))
	cacheTestReleaseSession(t, c, ctx)
}

func TestCacheMakeResultUnpruneableRetainsAcrossSessionClose(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	ctxSessionA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "unpruneable-client-a",
		SessionID: "unpruneable-session-a",
	})
	ctxSessionB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "unpruneable-client-b",
		SessionID: "unpruneable-session-b",
	})
	key := cacheTestIntCall("make-result-unpruneable")

	initCallsA := 0
	resA, err := c.GetOrInitCall(ctxSessionA, "unpruneable-session-a", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		initCallsA++
		return cacheTestIntResult(key, 55), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCallsA)
	assert.Equal(t, 0, c.EntryStats().RetainedCalls)

	assert.NilError(t, c.MakeResultUnpruneable(ctxSessionA, resA))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)

	assert.NilError(t, c.ReleaseSession(ctxSessionA, "unpruneable-session-a"))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Equal(t, 1, c.Size())

	initCallsB := 0
	resB, err := c.GetOrInitCall(ctxSessionB, "unpruneable-session-b", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		initCallsB++
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, initCallsB)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 55, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, c.ReleaseSession(ctxSessionB, "unpruneable-session-b"))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
}

func TestCacheMakeResultUnpruneableSkipsPrune(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := cacheTestIntCall("make-result-unpruneable-skip-prune")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 77), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.MakeResultUnpruneable(ctx, res))
	cacheTestReleaseSession(t, c, ctx)

	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(report.Entries))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Equal(t, 1, c.Size())

	c.egraphMu.RLock()
	edge := c.persistedEdgesByResult[res.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, edge.unpruneable)
	assert.Equal(t, int64(0), edge.expiresAtUnix)
}

func TestCacheMakeResultUnpruneableClearsPersistedExpiry(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := cacheTestIntCall("make-result-unpruneable-clear-expiry")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
		TTL:           60,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 88), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.MakeResultUnpruneable(ctx, res))

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)

	c.egraphMu.RLock()
	edge := c.persistedEdgesByResult[shared.id]
	resExpiry := shared.expiresAtUnix
	c.egraphMu.RUnlock()
	assert.Assert(t, edge.unpruneable)
	assert.Equal(t, int64(0), edge.expiresAtUnix)
	assert.Equal(t, int64(0), resExpiry)
}

func TestCacheUsageEntriesAllReportsSessionAndRetainedState(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	ctxSession := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "usage-retained-client",
		SessionID: "usage-retained-session",
	})
	key := cacheTestIntCall("usage-retained-persisted")

	res, err := c.GetOrInitCall(ctxSession, "usage-retained-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 123), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())

	entries := c.UsageEntriesAll(baseCtx)
	assert.Equal(t, 1, len(entries))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Assert(t, entries[0].ActivelyUsed)
	assert.Assert(t, entries[0].CreatedTimeUnixNano > 0)
	assert.Assert(t, entries[0].MostRecentUseTimeUnixNano > 0)

	assert.NilError(t, c.ReleaseSession(ctxSession, "usage-retained-session"))

	entries = c.UsageEntriesAll(baseCtx)
	assert.Equal(t, 1, len(entries))
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
	assert.Assert(t, !entries[0].ActivelyUsed)
	assert.Assert(t, entries[0].MostRecentUseTimeUnixNano >= entries[0].CreatedTimeUnixNano)
}

func TestCacheUsageEntriesAllDedupesByUsageIdentity(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	sizeCalls := &atomic.Int32{}
	const dedupeIdentity = "snapshot://shared-identity"

	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    cacheTestIntCall("usage-dedupe-a"),
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(cacheTestIntCall("usage-dedupe-a"), 1, 512, dedupeIdentity, sizeCalls), nil
	})
	assert.NilError(t, err)
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    cacheTestIntCall("usage-dedupe-b"),
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(cacheTestIntCall("usage-dedupe-b"), 2, 512, dedupeIdentity, sizeCalls), nil
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, c, ctx)

	entries := c.UsageEntriesAll(ctx)
	assert.Equal(t, 2, len(entries))
	assert.Equal(t, int32(1), sizeCalls.Load())

	var totalBytes int64
	var nonZeroEntries int
	for _, ent := range entries {
		totalBytes += ent.SizeBytes
		if ent.SizeBytes > 0 {
			nonZeroEntries++
		}
	}
	assert.Equal(t, int64(512), totalBytes)
	assert.Equal(t, 1, nonZeroEntries)
}

func TestCacheUsageEntriesAllSumsOwnedUsageIdentities(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	sizeCalls := &atomic.Int32{}

	keyA := cacheTestIntCall("usage-multi-a")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    keyA,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(cacheTestSizedInt{
			Int:             NewInt(1),
			sizeByIdentity:  map[string]int64{"snapshot://a": 10, "snapshot://b": 20},
			usageIdentities: []string{"snapshot://a", "snapshot://b"},
			sizeCalls:       sizeCalls,
		}, keyA)
	})
	assert.NilError(t, err)

	keyB := cacheTestIntCall("usage-multi-b")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    keyB,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(cacheTestSizedInt{
			Int:             NewInt(2),
			sizeByIdentity:  map[string]int64{"snapshot://b": 20, "snapshot://c": 40},
			usageIdentities: []string{"snapshot://b", "snapshot://c"},
			sizeCalls:       sizeCalls,
		}, keyB)
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, c, ctx)

	entries := c.UsageEntriesAll(ctx)
	assert.Equal(t, 2, len(entries))
	assert.Equal(t, int32(3), sizeCalls.Load())

	sizes := make([]int64, 0, len(entries))
	var totalBytes int64
	for _, ent := range entries {
		totalBytes += ent.SizeBytes
		sizes = append(sizes, ent.SizeBytes)
	}
	slices.Sort(sizes)
	assert.DeepEqual(t, sizes, []int64{30, 40})
	assert.Equal(t, int64(70), totalBytes)
}

func TestCacheUsageEntriesAllMutableUsageRemeasuresAfterSizeChange(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	sizeCalls := &atomic.Int32{}
	sizeSource := &atomic.Int64{}
	sizeSource.Store(100)
	key := cacheTestIntCall("usage-mutable-refresh")

	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestMutableSizedIntResult(key, 1, sizeSource, "snapshot://mutable-refresh", sizeCalls), nil
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, c, ctx)

	entries1 := c.UsageEntriesAll(ctx)
	assert.Equal(t, 1, len(entries1))
	assert.Equal(t, int64(100), entries1[0].SizeBytes)
	assert.Equal(t, int32(1), sizeCalls.Load())

	sizeSource.Store(200)
	entries2 := c.UsageEntriesAll(ctx)
	assert.Equal(t, 1, len(entries2))
	assert.Equal(t, int64(200), entries2[0].SizeBytes)
	assert.Equal(t, int32(2), sizeCalls.Load())
}

func TestCachePruneKeepDuration(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	oldKey := cacheTestIntCall("prune-keep-duration-old")
	oldRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    oldKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(oldKey, 1, 50, "snapshot://prune-keep-duration-old", nil), nil
	})
	assert.NilError(t, err)

	newKey := cacheTestIntCall("prune-keep-duration-new")
	newRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    newKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(newKey, 2, 50, "snapshot://prune-keep-duration-new", nil), nil
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, c, ctx)

	oldShared := oldRes.cacheSharedResult()
	newShared := newRes.cacheSharedResult()
	assert.Assert(t, oldShared != nil)
	assert.Assert(t, newShared != nil)

	now := time.Now()
	c.egraphMu.Lock()
	oldShared.lastUsedAtUnixNano = now.Add(-2 * time.Hour).UnixNano()
	oldShared.createdAtUnixNano = oldShared.lastUsedAtUnixNano
	newShared.lastUsedAtUnixNano = now.UnixNano()
	newShared.createdAtUnixNano = newShared.lastUsedAtUnixNano
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{
		All:          true,
		KeepDuration: time.Hour,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, cacheTestSharedResultEntryID(oldRes), report.Entries[0].ID)

	c.egraphMu.RLock()
	_, oldStillPresent := c.resultsByID[oldShared.id]
	_, newStillPresent := c.resultsByID[newShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !oldStillPresent)
	assert.Assert(t, newStillPresent)
}

func TestCachePruneThresholdTargetSpace(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	keys := []*ResultCall{
		cacheTestIntCall("prune-threshold-1"),
		cacheTestIntCall("prune-threshold-2"),
		cacheTestIntCall("prune-threshold-3"),
	}
	results := make([]AnyResult, 0, len(keys))
	for i, key := range keys {
		keyCopy := key
		valueCopy := i + 1
		identityCopy := fmt.Sprintf("snapshot://prune-threshold-%d", i+1)
		res, getErr := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:    keyCopy,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestSizedIntResult(keyCopy, valueCopy, 100, identityCopy, nil), nil
		})
		assert.NilError(t, getErr)
		results = append(results, res)
	}

	cacheTestReleaseSession(t, c, ctx)

	now := time.Now()
	c.egraphMu.Lock()
	for i, res := range results {
		shared := res.cacheSharedResult()
		assert.Assert(t, shared != nil)
		ts := now.Add(time.Duration(-3+i) * time.Hour).UnixNano()
		shared.lastUsedAtUnixNano = ts
		shared.createdAtUnixNano = ts
	}
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{
		All:          true,
		MaxUsedSpace: 250,
		TargetSpace:  100,
	}})
	assert.NilError(t, err)
	assert.Equal(t, 2, len(report.Entries))
	assert.Equal(t, int64(200), report.ReclaimedBytes)
	assert.Equal(t, cacheTestSharedResultEntryID(results[0]), report.Entries[0].ID)
	assert.Equal(t, cacheTestSharedResultEntryID(results[1]), report.Entries[1].ID)
}

func TestCachePruneSessionOwnedEntriesAreNeverPruned(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	ctxInUse := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "prune-in-use-client",
		SessionID: "prune-in-use-session",
	})
	inUseKey := cacheTestIntCall("prune-in-use")
	inUseRes, err := c.GetOrInitCall(ctxInUse, "prune-in-use-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    inUseKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(inUseKey, 1, 60, "snapshot://prune-in-use", nil), nil
	})
	assert.NilError(t, err)

	ctxPrunable := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "prune-prunable-client",
		SessionID: "prune-prunable-session",
	})
	prunableKey := cacheTestIntCall("prune-prunable")
	prunableRes, err := c.GetOrInitCall(ctxPrunable, "prune-prunable-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    prunableKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(prunableKey, 2, 60, "snapshot://prune-prunable", nil), nil
	})
	assert.NilError(t, err)

	assert.NilError(t, c.ReleaseSession(ctxPrunable, "prune-prunable-session"))

	report, err := c.Prune(baseCtx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, cacheTestSharedResultEntryID(prunableRes), report.Entries[0].ID)

	c.egraphMu.RLock()
	_, inUsePresent := c.resultsByID[inUseRes.cacheSharedResult().id]
	_, prunablePresent := c.resultsByID[prunableRes.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, inUsePresent)
	assert.Assert(t, !prunablePresent)
}

func TestCacheDoNotCacheRejectsOnReleaser(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := cacheTestIntCall("do-not-cache-onrelease")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
		DoNotCache: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResultWithOnRelease(key, 1, func(context.Context) error {
			return nil
		}), nil
	})
	assert.ErrorContains(t, err, "cannot implement OnReleaser")
}

func TestCachePruneOrderedSimulationKeepsZeroDeltaPrerequisites(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	baseCall := cacheTestIntCall("prune-ordered-base")
	baseRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    baseCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(baseCall, 1, 100, "snapshot://prune-ordered-base", nil), nil
	})
	assert.NilError(t, err)

	rootACall := cacheTestIntCall("prune-ordered-root-a")
	rootARes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    rootACall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(rootACall, 2, 10, "snapshot://prune-ordered-root-a", nil), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.AddExplicitDependency(ctx, rootARes, baseRes, "test_prune_ordered_root_a"))

	rootBCall := cacheTestIntCall("prune-ordered-root-b")
	rootBRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    rootBCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(rootBCall, 3, 10, "snapshot://prune-ordered-root-b", nil), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.AddExplicitDependency(ctx, rootBRes, baseRes, "test_prune_ordered_root_b"))

	cacheTestReleaseSession(t, c, ctx)

	now := time.Now().Add(-2 * time.Hour).UnixNano()
	c.egraphMu.Lock()
	baseRes.cacheSharedResult().lastUsedAtUnixNano = now
	baseRes.cacheSharedResult().createdAtUnixNano = now
	rootARes.cacheSharedResult().lastUsedAtUnixNano = now
	rootARes.cacheSharedResult().createdAtUnixNano = now
	rootBRes.cacheSharedResult().lastUsedAtUnixNano = now
	rootBRes.cacheSharedResult().createdAtUnixNano = now
	c.egraphMu.Unlock()

	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 3, len(report.Entries))
	assert.Equal(t, cacheTestSharedResultEntryID(baseRes), report.Entries[0].ID)
	assert.Equal(t, int64(0), report.Entries[0].SizeBytes)
	assert.Equal(t, cacheTestSharedResultEntryID(rootARes), report.Entries[1].ID)
	assert.Equal(t, int64(10), report.Entries[1].SizeBytes)
	assert.Equal(t, cacheTestSharedResultEntryID(rootBRes), report.Entries[2].ID)
	assert.Equal(t, int64(110), report.Entries[2].SizeBytes)
	assert.Equal(t, int64(120), report.ReclaimedBytes)
}

func TestCompactEqClassesSkipsWhenBelowThreshold(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	c.egraphMu.Lock()
	c.initEgraphLocked()
	a := c.ensureEqClassForDigestLocked(ctx, "compact-threshold-a")
	b := c.ensureEqClassForDigestLocked(ctx, "compact-threshold-b")
	c1 := c.ensureEqClassForDigestLocked(ctx, "compact-threshold-c")
	_ = c.ensureEqClassForDigestLocked(ctx, "compact-threshold-dead-1")
	_ = c.ensureEqClassForDigestLocked(ctx, "compact-threshold-dead-2")
	c.resultsByID = map[sharedResultID]*sharedResult{
		1: {id: 1, self: Int(1), hasValue: true, resultCall: cacheTestIntCall("compact-threshold-a")},
		2: {id: 2, self: Int(2), hasValue: true, resultCall: cacheTestIntCall("compact-threshold-b")},
		3: {id: 3, self: Int(3), hasValue: true, resultCall: cacheTestIntCall("compact-threshold-c")},
	}
	c.resultOutputEqClasses[1] = map[eqClassID]struct{}{a: {}}
	c.resultOutputEqClasses[2] = map[eqClassID]struct{}{b: {}}
	c.resultOutputEqClasses[3] = map[eqClassID]struct{}{c1: {}}
	compacted, oldSlots, newSlots := c.compactEqClassesLocked()
	c.egraphMu.Unlock()

	assert.Assert(t, !compacted)
	assert.Equal(t, 5, oldSlots)
	assert.Equal(t, 3, newSlots)
	assert.Equal(t, 6, len(c.egraphParents))
}

func TestCachePruneCompactsEqClassesAndPreservesLookup(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	ctxActive := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "prune-compact-active",
		SessionID: "prune-compact-active",
	})
	ctxPrunable := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "prune-compact-prunable",
		SessionID: "prune-compact-prunable",
	})
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	activeKey := cacheTestIntCall("prune-compact-active")
	activeRes, err := c.GetOrInitCall(ctxActive, "prune-compact-active", noopTypeResolver{}, &CallRequest{
		ResultCall: activeKey,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(activeKey, 11), nil
	})
	assert.NilError(t, err)
	activeShared := activeRes.cacheSharedResult()
	assert.Assert(t, activeShared != nil)

	prunableKey := cacheTestIntCall("prune-compact-prunable")
	prunableRes, err := c.GetOrInitCall(ctxPrunable, "prune-compact-prunable", noopTypeResolver{}, &CallRequest{
		ResultCall:    prunableKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(prunableKey, 22, 10, "snapshot://prune-compact-prunable", nil), nil
	})
	assert.NilError(t, err)
	prunableShared := prunableRes.cacheSharedResult()
	assert.Assert(t, prunableShared != nil)

	assert.NilError(t, c.ReleaseSession(ctxPrunable, "prune-compact-prunable"))
	c.egraphMu.Lock()
	originalSlots := len(c.egraphParents)
	_ = c.ensureEqClassForDigestLocked(baseCtx, "prune-compact-dead-1")
	_ = c.ensureEqClassForDigestLocked(baseCtx, "prune-compact-dead-2")
	_ = c.ensureEqClassForDigestLocked(baseCtx, "prune-compact-dead-3")
	_ = c.ensureEqClassForDigestLocked(baseCtx, "prune-compact-dead-4")
	bloatedSlots := len(c.egraphParents)
	c.egraphMu.Unlock()
	assert.Assert(t, bloatedSlots > originalSlots)

	report, err := c.Prune(baseCtx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, cacheTestSharedResultEntryID(prunableRes), report.Entries[0].ID)

	c.egraphMu.RLock()
	assert.Assert(t, len(c.egraphParents) < bloatedSlots)
	assert.Assert(t, len(c.egraphParents) >= 2)
	_, activePresent := c.resultsByID[activeShared.id]
	_, prunablePresent := c.resultsByID[prunableShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, activePresent)
	assert.Assert(t, !prunablePresent)

	hitCount := 0
	hitRes, err := c.GetOrInitCall(ctxActive, "prune-compact-active", noopTypeResolver{}, &CallRequest{ResultCall: activeKey}, func(context.Context) (AnyResult, error) {
		hitCount++
		return cacheTestIntResult(activeKey, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, hitCount)
	assert.Assert(t, hitRes.HitCache())
	assert.Equal(t, 11, cacheTestUnwrapInt(t, hitRes))

	cacheTestReleaseSession(t, c, ctxActive)
}

func TestCachePruneProtectsExactDependencyOfActiveResult(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	ctxActive := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "prune-exact-active",
		SessionID: "prune-exact-active",
	})
	ctxDep := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "prune-exact-dep",
		SessionID: "prune-exact-dep",
	})
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	activeCall := cacheTestIntCall("prune-exact-active-root")
	depCall := cacheTestIntCall("prune-exact-active-dep")

	activeRes, err := c.GetOrInitCall(ctxActive, "prune-exact-active", noopTypeResolver{}, &CallRequest{
		ResultCall: activeCall,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(activeCall, 1, 10, "snapshot://prune-exact-active-root", nil), nil
	})
	assert.NilError(t, err)
	depRes, err := c.GetOrInitCall(ctxDep, "prune-exact-dep", noopTypeResolver{}, &CallRequest{
		ResultCall:    depCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestSizedIntResult(depCall, 2, 20, "snapshot://prune-exact-active-dep", nil), nil
	})
	assert.NilError(t, err)
	depShared := depRes.cacheSharedResult()
	assert.Assert(t, depShared != nil)

	assert.NilError(t, c.AddExplicitDependency(ctxActive, activeRes, depRes, "test_prune_exact_dependency"))
	assert.NilError(t, c.ReleaseSession(ctxDep, "prune-exact-dep"))

	report, err := c.Prune(baseCtx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(report.Entries))

	c.egraphMu.RLock()
	_, activePresent := c.resultsByID[activeRes.cacheSharedResult().id]
	_, depPresent := c.resultsByID[depShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, activePresent)
	assert.Assert(t, depPresent)

	assert.NilError(t, c.ReleaseSession(ctxActive, "prune-exact-active"))

	report, err = c.Prune(baseCtx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, cacheTestSharedResultEntryID(depRes), report.Entries[0].ID)
}

func TestCachePruneDoesNotProtectTermProvenanceOnlyResultFromActiveResult(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	root := &sharedResult{
		id:       1,
		self:     cacheTestSizedInt{Int: Int(1), sizeByIdentity: map[string]int64{"snapshot://prune-structural-root": 10}, usageIdentities: []string{"snapshot://prune-structural-root"}},
		hasValue: true,
		resultCall: &ResultCall{
			Kind:        ResultCallKindSynthetic,
			SyntheticOp: "root",
			Type:        NewResultCallType(Int(0).Type()),
		},
		incomingOwnershipCount: 1,
	}
	provenanceOnly := &sharedResult{
		id:       2,
		self:     cacheTestSizedInt{Int: Int(2), sizeByIdentity: map[string]int64{"snapshot://prune-structural-provenance-only": 20}, usageIdentities: []string{"snapshot://prune-structural-provenance-only"}},
		hasValue: true,
		resultCall: &ResultCall{
			Kind:        ResultCallKindSynthetic,
			SyntheticOp: "provenanceOnly",
			Type:        NewResultCallType(Int(0).Type()),
		},
		incomingOwnershipCount: 1,
	}

	c.egraphMu.Lock()
	c.initEgraphLocked()
	c.resultsByID = map[sharedResultID]*sharedResult{
		root.id:           root,
		provenanceOnly.id: provenanceOnly,
	}

	rootEq := c.ensureEqClassForDigestLocked(baseCtx, "prune-structural-root")
	provenanceEq := c.ensureEqClassForDigestLocked(baseCtx, "prune-structural-provenance-only")
	c.resultOutputEqClasses[root.id] = map[eqClassID]struct{}{rootEq: {}}
	c.resultOutputEqClasses[provenanceOnly.id] = map[eqClassID]struct{}{provenanceEq: {}}
	c.persistedEdgesByResult = map[sharedResultID]persistedEdge{
		provenanceOnly.id: {
			resultID:          provenanceOnly.id,
			createdAtUnixNano: time.Now().UnixNano(),
		},
	}

	termID := egraphTermID(1)
	c.egraphTerms[termID] = newEgraphTerm(termID, digest.FromString("prune-structural-root-term"), []eqClassID{provenanceEq}, rootEq)
	c.outputEqClassToTerms[rootEq] = map[egraphTermID]struct{}{termID: {}}
	c.termInputProvenance[termID] = []egraphInputProvenanceKind{egraphInputProvenanceKindResult}
	c.egraphMu.Unlock()
	c.sessionMu.Lock()
	c.sessionResultIDsBySession = map[string]map[sharedResultID]struct{}{
		"prune-structural-active": {root.id: {}},
	}
	c.sessionMu.Unlock()

	report, err := c.Prune(baseCtx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(report.Entries))
	assert.Equal(t, "dagql.result.2", report.Entries[0].ID)

	c.egraphMu.RLock()
	_, rootPresent := c.resultsByID[root.id]
	_, provenancePresent := c.resultsByID[provenanceOnly.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, rootPresent)
	assert.Assert(t, !provenancePresent)
}

func TestCacheAttachDependencyResults(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	ctxParent := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "attach-owned-parent",
		SessionID: "attach-owned-parent",
	})
	ctxLookup := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "attach-owned-lookup",
		SessionID: "attach-owned-lookup",
	})
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	parentCall := cacheTestIntCall("parent-with-additional-output")
	childCall := cacheTestIntCall("child-additional-output")
	parentRes, err := c.GetOrInitCall(ctxParent, "attach-owned-parent", noopTypeResolver{}, &CallRequest{
		ResultCall:    parentCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		child := cacheTestSizedIntResult(
			childCall,
			2,
			128,
			"snapshot://cache-additional-output",
			nil,
		)
		return cacheTestDetachedResult(parentCall, &cacheTestOwnedDepsInt{
			Int:          NewInt(1),
			ownedResults: []AnyResult{child},
		}), nil
	})
	assert.NilError(t, err)
	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)

	childInitCalls := 0
	childRes, err := c.GetOrInitCall(ctxParent, "attach-owned-parent", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
		childInitCalls++
		return cacheTestIntResult(childCall, 99), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, childInitCalls)
	assert.Assert(t, childRes.HitCache())
	childVal, ok := UnwrapAs[cacheTestSizedInt](childRes)
	assert.Assert(t, ok)
	assert.Equal(t, int64(2), int64(childVal.Int))

	childShared := childRes.cacheSharedResult()
	assert.Assert(t, childShared != nil)

	c.egraphMu.RLock()
	cachedParent := c.resultsByID[parentShared.id]
	cachedChild := c.resultsByID[childShared.id]
	parentDependsOnChild := false
	childIncomingOwnershipCount := int64(0)
	if cachedParent != nil {
		_, parentDependsOnChild = cachedParent.deps[childShared.id]
	}
	if cachedChild != nil {
		childIncomingOwnershipCount = cachedChild.incomingOwnershipCount
	}
	c.egraphMu.RUnlock()

	assert.Assert(t, cachedParent != nil)
	assert.Assert(t, cachedChild != nil)
	assert.Assert(t, parentDependsOnChild)
	assert.Equal(t, int64(2), childIncomingOwnershipCount)

	assert.NilError(t, c.ReleaseSession(ctxParent, "attach-owned-parent"))

	c.egraphMu.RLock()
	_, parentStillPresent := c.resultsByID[parentShared.id]
	_, childStillPresent := c.resultsByID[childShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, parentStillPresent)
	assert.Assert(t, childStillPresent)

	childRes, err = c.GetOrInitCall(ctxLookup, "attach-owned-lookup", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
		childInitCalls++
		return cacheTestIntResult(childCall, 99), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, childRes.HitCache())
}

func TestCacheAttachDependencyResultsAlreadyAttachedChild(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	ctxChild := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "attached-child-owner",
		SessionID: "attached-child-owner",
	})
	ctxParent := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "attached-child-parent",
		SessionID: "attached-child-parent",
	})
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	childCall := cacheTestIntCall("child-already-attached")
	attachedChild, err := c.GetOrInitCall(ctxChild, "attached-child-owner", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, ValueFunc(cacheTestIntResult(childCall, 2)))
	assert.NilError(t, err)
	childShared := attachedChild.cacheSharedResult()
	assert.Assert(t, childShared != nil)

	parentCall := cacheTestIntCall("parent-already-attached-child")
	parentRes, err := c.GetOrInitCall(ctxParent, "attached-child-parent", noopTypeResolver{}, &CallRequest{ResultCall: parentCall}, func(context.Context) (AnyResult, error) {
		return cacheTestDetachedResult(parentCall, &cacheTestOwnedDepsInt{
			Int:          NewInt(1),
			ownedResults: []AnyResult{attachedChild},
		}), nil
	})
	assert.NilError(t, err)
	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)

	assert.NilError(t, c.ReleaseSession(ctxChild, "attached-child-owner"))

	c.egraphMu.RLock()
	heldChild := c.resultsByID[childShared.id]
	heldParent := c.resultsByID[parentShared.id]
	parentDependsOnChild := false
	if heldParent != nil {
		_, parentDependsOnChild = heldParent.deps[childShared.id]
	}
	c.egraphMu.RUnlock()
	assert.Assert(t, heldChild != nil)
	assert.Assert(t, heldParent != nil)
	assert.Assert(t, parentDependsOnChild)

	assert.NilError(t, c.ReleaseSession(ctxParent, "attached-child-parent"))

	c.egraphMu.RLock()
	removedChild := c.resultsByID[childShared.id]
	removedParent := c.resultsByID[parentShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, removedChild == nil)
	assert.Assert(t, removedParent == nil)
}

func TestCacheAddExplicitDependency(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	ctxParent := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "explicit-dep-parent",
		SessionID: "explicit-dep-parent",
	})
	ctxChild := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "explicit-dep-child",
		SessionID: "explicit-dep-child",
	})
	cacheIface, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	parentCall := cacheTestIntCall("parent-explicit-dependency")
	childCall := cacheTestIntCall("child-explicit-dependency")

	parentRes, err := c.GetOrInitCall(ctxParent, "explicit-dep-parent", noopTypeResolver{}, &CallRequest{ResultCall: parentCall}, ValueFunc(cacheTestIntResult(parentCall, 1)))
	assert.NilError(t, err)
	childRes, err := c.GetOrInitCall(ctxChild, "explicit-dep-child", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, ValueFunc(cacheTestIntResult(childCall, 2)))
	assert.NilError(t, err)

	parentShared := parentRes.cacheSharedResult()
	childShared := childRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)
	assert.Assert(t, childShared != nil)

	assert.NilError(t, c.AddExplicitDependency(ctxParent, parentRes, childRes, "test_explicit_dependency"))

	c.egraphMu.RLock()
	cachedParent := c.resultsByID[parentShared.id]
	parentDependsOnChild := false
	childIncomingOwnershipCount := int64(0)
	if cachedParent != nil {
		_, parentDependsOnChild = cachedParent.deps[childShared.id]
	}
	if cachedChild := c.resultsByID[childShared.id]; cachedChild != nil {
		childIncomingOwnershipCount = cachedChild.incomingOwnershipCount
	}
	c.egraphMu.RUnlock()

	assert.Assert(t, cachedParent != nil)
	assert.Assert(t, parentDependsOnChild)
	assert.Equal(t, int64(2), childIncomingOwnershipCount)

	assert.NilError(t, c.ReleaseSession(ctxChild, "explicit-dep-child"))

	c.egraphMu.RLock()
	_, childStillPresent := c.resultsByID[childShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, childStillPresent)

	assert.NilError(t, c.ReleaseSession(ctxParent, "explicit-dep-parent"))

	c.egraphMu.RLock()
	_, childStillPresent = c.resultsByID[childShared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !childStillPresent)
}

func TestCacheResultCallDerivedFromRequestID(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	childCall := cacheTestIntCall("frameChild")
	childRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(childCall, 7), nil
	})
	assert.NilError(t, err)
	childShared := childRes.cacheSharedResult()
	assert.Assert(t, childShared != nil)
	assert.Assert(t, childShared.id != 0)

	parentRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(Int(0).Type()),
		Field: "frameParent",
		Args: []*ResultCallArg{
			{Name: "child", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(childShared.id)}}},
			{Name: "payload", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindDigestedString, DigestedStringValue: "same", DigestedStringDigest: digest.FromString("frame-payload")}},
		},
	}}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(8)), nil
	})
	assert.NilError(t, err)
	parentShared := parentRes.cacheSharedResult()
	assert.Assert(t, parentShared != nil)
	assert.Assert(t, parentShared.resultCall != nil)

	frame := parentShared.resultCall
	assert.Equal(t, ResultCallKindField, frame.Kind)
	assert.Equal(t, "frameParent", frame.Field)
	assert.Assert(t, frame.Type != nil)
	assert.Equal(t, "Int", frame.Type.NamedType)
	assert.Equal(t, 2, len(frame.Args))
	assert.Equal(t, "child", frame.Args[0].Name)
	assert.Assert(t, frame.Args[0].Value != nil)
	assert.Equal(t, ResultCallLiteralKindResultRef, frame.Args[0].Value.Kind)
	assert.Assert(t, frame.Args[0].Value.ResultRef != nil)
	assert.Equal(t, uint64(childShared.id), frame.Args[0].Value.ResultRef.ResultID)
	assert.Equal(t, "payload", frame.Args[1].Name)
	assert.Assert(t, frame.Args[1].Value != nil)
	assert.Equal(t, ResultCallLiteralKindDigestedString, frame.Args[1].Value.Kind)
	assert.Equal(t, "same", frame.Args[1].Value.DigestedStringValue)
	assert.Equal(t, digest.FromString("frame-payload"), frame.Args[1].Value.DigestedStringDigest)
}

func TestCacheResultCallFirstWriterWins(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	id := cacheTestIntCall("frame-first-writer")
	firstFrame := &ResultCall{
		Kind:        ResultCallKindSynthetic,
		SyntheticOp: "first",
		Type:        NewResultCallType(Int(0).Type()),
	}
	secondFrame := &ResultCall{
		Kind:        ResultCallKindSynthetic,
		SyntheticOp: "second",
		Type:        NewResultCallType(Int(0).Type()),
	}

	first, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: id}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(firstFrame, 1), nil
	})
	assert.NilError(t, err)
	firstShared := first.cacheSharedResult()
	assert.Assert(t, firstShared != nil)
	assert.Assert(t, firstShared.resultCall != nil)
	assert.Equal(t, "first", firstShared.resultCall.SyntheticOp)

	second, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: id}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(secondFrame, 2), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, second.HitCache())

	secondShared := second.cacheSharedResult()
	assert.Assert(t, secondShared != nil)
	assert.Assert(t, secondShared.resultCall != nil)
	assert.Equal(t, "first", secondShared.resultCall.SyntheticOp)
}

func TestCacheArbitraryRoundTripAndRelease(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := "arbitrary-round-trip"
	var releaseCalls atomic.Int32
	initCalls := 0

	res1, err := c.GetOrInitArbitrary(ctx, "test-session", key, func(context.Context) (any, error) {
		initCalls++
		return cacheTestOpaqueValue{
			value: "hello",
			onRelease: func(context.Context) error {
				releaseCalls.Add(1)
				return nil
			},
		}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())
	v1, ok := res1.Value().(cacheTestOpaqueValue)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", v1.value)

	res2, err := c.GetOrInitArbitrary(ctx, "test-session", key, func(context.Context) (any, error) {
		initCalls++
		return cacheTestOpaqueValue{value: "ignored"}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	v2, ok := res2.Value().(cacheTestOpaqueValue)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", v2.value)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())

	assert.Assert(t, res1.Value() != nil)
	assert.Assert(t, res2.Value() != nil)
	assert.NilError(t, c.ReleaseSession(ctx, "test-session"))
	assert.Equal(t, int32(1), releaseCalls.Load())
	assert.Equal(t, 0, c.Size())
}

func TestCacheArbitraryConcurrent(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := "arbitrary-concurrent"
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := c.GetOrInitArbitrary(ctx, "test-session", key, func(context.Context) (any, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return "value", nil
		})
		assert.NilError(t, err)
		assert.Equal(t, "value", res.Value())
	}()

	select {
	case <-firstCallEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first caller to enter init callback")
	}

	for i := 1; i < totalCallers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := c.GetOrInitArbitrary(ctx, "test-session", key, func(context.Context) (any, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return "value", nil
			})
			assert.NilError(t, err)
			assert.Equal(t, "value", res.Value())
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.callsMu.Lock()
		oc := c.ongoingArbitraryCalls[key]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.callsMu.Unlock()

		if oc != nil && lastObservedWaiters == totalCallers {
			waiterCountReached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, waiterCountReached, "expected %d waiters, last observed %d", totalCallers, lastObservedWaiters)

	close(unblockFirstCall)

	ongoingCleared := false
	clearPollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(clearPollDeadline) {
		c.callsMu.Lock()
		_, exists := c.ongoingArbitraryCalls[key]
		c.callsMu.Unlock()
		if !exists {
			ongoingCleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, ongoingCleared, "ongoing arbitrary call was not cleared")

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
	assert.Assert(t, initialized[0])
	assert.Equal(t, 1, c.Size())
	assert.NilError(t, c.ReleaseSession(ctx, "test-session"))
	assert.Equal(t, 0, c.Size())
}

func TestCacheArbitraryRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())

	cacheIface, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	key := "arbitrary-recursive"
	_, err = c.GetOrInitArbitrary(ctx, "test-session", key, func(ctx context.Context) (any, error) {
		_, err := c.GetOrInitArbitrary(ctx, "test-session", key, func(context.Context) (any, error) {
			return "should-not-run", nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))
}
