package dagql

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/wcprof"
)

// A profiled call that waits for the cache's e-graph lock shows the wait in
// the profile, as a lock wait of the call's op on dagql.egraph.
func TestCallWaitsForEgraphLockWcprof(t *testing.T) {
	ctx, c := lockWaitTestCache(t)
	call := cacheTestIntCall("lockWaitCall")

	c.egraphMu.Lock()
	done := lockWaitTestCall(ctx, c, call, nil)
	waitBlockedIn(t, "wcprof.ReadLock(")
	c.egraphMu.Unlock()
	require.NoError(t, <-done)

	header, events := lockWaitTestDump(t)
	requireWaitUnder(t, header, events, callOps(header, events, call), egraphLockIdent)
}

// A cache hit that waits for the cache's session lock shows the wait as a
// lock wait of the hit's op on dagql.sessions.
func TestCacheHitWaitsForSessionLockWcprof(t *testing.T) {
	ctx, c := lockWaitTestCache(t)
	call := cacheTestIntCall("lockWaitHit")
	require.NoError(t, <-lockWaitTestCall(ctx, c, call, nil))
	header, events := lockWaitTestDump(t)
	before := callOps(header, events, call)

	c.sessionMu.Lock()
	done := lockWaitTestCall(ctx, c, call, nil)
	waitBlockedIn(t, "wcprof.Lock(")
	c.sessionMu.Unlock()
	require.NoError(t, <-done)

	header, events = lockWaitTestDump(t)
	hits := callOps(header, events, call)
	for id := range before {
		delete(hits, id)
	}
	require.Len(t, hits, 1, "no op for the hit")
	requireWaitUnder(t, header, events, hits, sessionsLockIdent)
}

// What a call's result publication waits for is the publication's, within
// its op, not the execution's that ended before it.
func TestPublicationWaitsForEgraphLockWcprof(t *testing.T) {
	ctx, c := lockWaitTestCache(t)
	call := cacheTestIntCall("lockWaitPublish")

	var unlocked sync.WaitGroup
	unlocked.Add(1)
	done := lockWaitTestCall(ctx, c, call, func() {
		// held from inside the execution, so only its publication waits
		c.egraphMu.Lock()
		go func() {
			defer unlocked.Done()
			waitBlockedIn(t, "initCompletedResult(")
			c.egraphMu.Unlock()
		}()
	})
	require.NoError(t, <-done)
	unlocked.Wait()

	header, events := lockWaitTestDump(t)
	pubs := map[uint64]wcprof.DumpEvent{}
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.ClassID] == publishResultSpanName {
			pubs[ev.OpID] = ev
		}
	}
	var waited bool
	for _, ev := range events {
		if ev.Type != "wait" || header.Strings[ev.IdentID] != egraphLockIdent {
			continue
		}
		if pub, ok := pubs[ev.ParentID]; ok {
			require.LessOrEqual(t, pub.StartNS, ev.StartNS)
			require.LessOrEqual(t, ev.EndNS, pub.EndNS)
			waited = true
		}
	}
	require.True(t, waited, "no lock wait on %s under %s", egraphLockIdent, publishResultSpanName)
}

func lockWaitTestCache(t *testing.T) (context.Context, *Cache) {
	t.Helper()
	ctx := cacheTestContext(context.Background())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	wcprof.EnsureRecorder()
	return wcprof.ContextWithProfiling(ContextWithCache(ctx, c)), c
}

// lockWaitTestCall runs call in the background, running during (if not nil)
// inside its execution.
func lockWaitTestCall(ctx context.Context, c *Cache, call *ResultCall, during func()) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{},
			&CallRequest{ResultCall: call, ConcurrencyKey: "test-session"},
			func(context.Context) (AnyResult, error) {
				if during != nil {
					during()
				}
				return cacheTestDetachedResult(call, NewInt(1)), nil
			})
		done <- err
	}()
	return done
}

func lockWaitTestDump(t *testing.T) (*wcprof.DumpHeader, []wcprof.DumpEvent) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	return header, events
}

func callOps(header *wcprof.DumpHeader, events []wcprof.DumpEvent, call *ResultCall) map[uint64]bool {
	ops := map[uint64]bool{}
	for _, ev := range events {
		if ev.Type == "op" && ev.OpKind == wcprof.OpKindCall.String() && header.Strings[ev.ClassID] == profCallClass(call) {
			ops[ev.OpID] = true
		}
	}
	return ops
}

func requireWaitUnder(t *testing.T, header *wcprof.DumpHeader, events []wcprof.DumpEvent, parents map[uint64]bool, ident string) {
	t.Helper()
	require.NotEmpty(t, parents, "no op to wait under")
	for _, ev := range events {
		if ev.Type == "wait" && parents[ev.ParentID] && header.Strings[ev.IdentID] == ident {
			require.Equal(t, wcprof.WaitReasonLock.String(), ev.Reason)
			require.LessOrEqual(t, ev.StartNS, ev.EndNS)
			return
		}
	}
	t.Fatalf("no lock wait on %s under the call's op", ident)
}

// waitBlockedIn waits until some goroutine is blocked on a lock inside fn.
func waitBlockedIn(t *testing.T, fn string) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, fn) && (strings.Contains(stack, "[sync.Mutex.Lock") || strings.Contains(stack, "[sync.RWMutex.")) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Errorf("nothing blocked on a lock in %s", fn)
			return
		}
		runtime.Gosched()
	}
}

// A lazy evaluation's gathering of its install spans is a step of its own
// under the evaluation's op, and its walk under the graph lock is a step of
// its own under that, dagql.installSpans.walk, with the walk's wait for the
// lock under it: a profile tells them apart from the evaluation's other
// set-up, and a writer waiting behind readers lines up with the walks that
// read when it began.
func TestLazyInstallSpansWaitForLocksWcprof(t *testing.T) {
	wcprof.EnsureRecorder()
	const sessionID = "lazy-install-spans-session"
	const resultID = sharedResultID(7)
	_, rootCtx, root := newLazyRecordingRoot("POST /query")
	defer root.End()
	// A module call's return on the result, so the gathering walks above it
	// under the graph lock.
	const moduleReturnID = sharedResultID(8)
	result := &sharedResult{id: resultID}
	moduleReturn := &sharedResult{id: moduleReturnID}
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{resultID: result, moduleReturnID: moduleReturn}}
	c.egraphMu.Lock()
	require.NoError(t, c.addExplicitDependencyLocked(t.Context(), moduleReturn, result, "test"))
	c.egraphMu.Unlock()
	_, producer := Tracer(rootCtx).Start(rootCtx, "Container.withExec")
	producer.End()
	_, moduleCall := Tracer(rootCtx).Start(rootCtx, "Go.binary")
	moduleCall.End()
	c.sessionMu.Lock()
	c.sessionLazySpansBySession = map[string]map[sharedResultID]trace.SpanContext{
		sessionID: {resultID: producer.SpanContext()},
	}
	c.sessionMu.Unlock()
	c.recordSessionResultInstallSpanLocked(t.Context(), sessionID, resultID, producer.SpanContext(), false)
	c.recordSessionResultInstallSpanLocked(t.Context(), sessionID, moduleReturnID, moduleCall.SpanContext(), true)

	evalCtx := wcprof.ContextWithProfiling(engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: sessionID, ClientID: "client-1"}))
	evalCtx, lazyOp := wcprof.BeginOp(evalCtx, wcprof.OpKindLazy, "Container.withExec", wcprof.OpOpts{})

	c.egraphMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, LazyGroupWhole, &ResultCall{Field: "withExec"}, "")
		var err error
		endOTelLazyOp(lazySpan, nil, isResume, resultID, false, false, "", lazySpanCacheState{}, &err)
	}()
	waitBlockedIn(t, "wcprof.ReadLock(")
	c.egraphMu.Unlock()
	<-done
	lazyOp.End(wcprof.OutcomeOK)

	header, events := lockWaitTestDump(t)
	steps := map[uint64]bool{}
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.ClassID] == "lazy.installSpans" && ev.ParentID == lazyOp.ID() {
			require.Equal(t, wcprof.OpKindInternal.String(), ev.OpKind)
			steps[ev.OpID] = true
		}
	}
	require.Len(t, steps, 1, "no lazy.installSpans step under the lazy op")
	walks := map[uint64]bool{}
	for _, ev := range events {
		if ev.Type == "op" && header.Strings[ev.ClassID] == "dagql.installSpans.walk" && steps[ev.ParentID] {
			walks[ev.OpID] = true
		}
	}
	require.Len(t, walks, 1, "no dagql.installSpans.walk step under the gathering")
	requireWaitUnder(t, header, events, walks, egraphLockIdent)
}
