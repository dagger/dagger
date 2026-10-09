package core

// The producer half of the call-payload transport. What must not regress
// silently: frames absent from spans have to ride logs, including frames buried
// inside an ID-literal argument — those are the ones LiteralID.pb flattens to a
// bare digest, so they can never ride a span attribute — and a digest must cross
// the wire at most once per delivery target, or an LLM loop re-sending the same
// chain drowns the log stream.

import (
	"context"
	"strings"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// recordedPayload is one call-payload record as it crossed the logger API.
type recordedPayload struct {
	scope           string
	bodyKind        otellog.Kind
	body            []byte
	contentType     string
	contentTypeKind otellog.Kind
	digestAttr      string
	digestAttrKind  otellog.Kind
	call            *callpbv1.Call
	digest          string
	err             error
}

// payloadRecorder captures call payload records and indexes decoded calls by
// the canonical digest a consumer must derive from the raw body.
type payloadRecorder struct {
	mu      sync.Mutex
	calls   map[string]*callpbv1.Call
	order   []string
	records []recordedPayload
}

func (r *payloadRecorder) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	got := recordedPayload{
		scope:    rec.InstrumentationScope().Name,
		bodyKind: rec.Body().Kind(),
		body:     append([]byte(nil), rec.Body().AsBytes()...),
	}
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetry.ContentTypeAttr:
			got.contentTypeKind = kv.Value.Kind()
			got.contentType = kv.Value.AsString()
		case telemetryattrs.CallPayloadDigestAttr:
			got.digestAttrKind = kv.Value.Kind()
			got.digestAttr = kv.Value.AsString()
		}
		return true
	})

	decoded := new(callpbv1.Call)
	got.err = proto.Unmarshal(got.body, decoded)
	if got.err == nil {
		got.call = decoded
		got.digest = decoded.GetDigest()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]*callpbv1.Call{}
	}
	if got.digest != "" {
		r.calls[got.digest] = got.call
		r.order = append(r.order, got.digest)
	}
	r.records = append(r.records, got)
	return nil
}

func (r *payloadRecorder) Shutdown(context.Context) error                         { return nil }
func (r *payloadRecorder) ForceFlush(context.Context) error                       { return nil }
func (r *payloadRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

func (r *payloadRecorder) get(digest string) *callpbv1.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[digest]
}

func (r *payloadRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *payloadRecorder) emissionCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func (r *payloadRecorder) firstDigest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) == 0 {
		return ""
	}
	return r.order[0]
}

func (r *payloadRecorder) snapshot() []recordedPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedPayload(nil), r.records...)
}

// testSeenKeys mirrors the production store's contract: ClaimCallPayload is a
// claiming LoadOrStore, so a second walk over an in-flight digest must see it
// as taken even before anything was persisted. A non-claiming fake would let
// the once-per-frame assertions below pass while production amplified every
// concurrent walk.
type testSeenKeys struct {
	keys sync.Map
}

func (s *testSeenKeys) ClaimCallPayload(key string) bool {
	_, seen := s.keys.LoadOrStore(key, struct{}{})
	return !seen
}

type alreadySeenTelemetryStore struct{}

func (alreadySeenTelemetryStore) LoadOrStoreTelemetrySeenKey(string) bool { return true }
func (alreadySeenTelemetryStore) StoreTelemetrySeenKey(string)            {}

type testSpanSeenKeys struct {
	keys sync.Map
}

func (s *testSpanSeenKeys) LoadOrStoreTelemetrySeenKey(key string) bool {
	_, seen := s.keys.LoadOrStore(key, struct{}{})
	return seen
}

func (s *testSpanSeenKeys) StoreTelemetrySeenKey(key string) {
	s.keys.Store(key, struct{}{})
}

type payloadRoutingTestServer struct {
	*mockServer
	payloadStore dagql.CallPayloadSeenKeyStore
	spanStore    dagql.TelemetrySeenKeyStore
}

func (s *payloadRoutingTestServer) TelemetrySeenKeyStore(context.Context) (dagql.TelemetrySeenKeyStore, error) {
	if s.spanStore != nil {
		return s.spanStore, nil
	}
	return alreadySeenTelemetryStore{}, nil
}

func (s *payloadRoutingTestServer) CallPayloadSeenKeyStore(context.Context) (dagql.CallPayloadSeenKeyStore, error) {
	return s.payloadStore, nil
}

// payloadRecorderCtx returns a context whose logger provider records every
// call payload emitted through it, plus the nil dagql cache the recipe walk
// needs (every ref in these fixtures is inline).
func payloadRecorderCtx(t *testing.T) (*payloadRecorder, context.Context) {
	t.Helper()
	rec := &payloadRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(rec))
	ctx := telemetry.WithLoggerProvider(context.Background(), provider)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, root := tp.Tracer("call-payload-test").Start(ctx, "root")
	t.Cleanup(func() {
		root.End()
		require.NoError(t, tp.Shutdown(context.Background()))
	})
	return rec, dagql.ContextWithCache(ctx, nil)
}

// llm.withSkills(directory: dir).agent(): the shape of the live failure. Only
// the agent frame is ever spanned; the directory frame reaches the client
// through the ID-literal argument or not at all.
func skillsChain() (agent, withSkills, dir *dagql.ResultCall) {
	llm := testResultCall("llm", &Void{}, nil)
	dir = testResultCall("directory", &Void{}, testResultCall("host", &Void{}, nil))
	withSkills = testResultCall("withSkills", &Void{}, llm)
	withSkills.Args = []*dagql.ResultCallArg{{
		Name: "directory",
		Value: &dagql.ResultCallLiteral{
			Kind:      dagql.ResultCallLiteralKindResultRef,
			ResultRef: &dagql.ResultCallRef{Call: dir},
		},
	}}
	agent = testResultCall("agent", &Void{}, withSkills)
	return agent, withSkills, dir
}

func TestAroundFuncRoutesPayloadsBeforeSpanDeduplication(t *testing.T) {
	agent, _, _ := skillsChain()
	req := &dagql.CallRequest{ResultCall: agent}

	runRoute := func(store dagql.CallPayloadSeenKeyStore) (*payloadRecorder, context.Context) {
		recorder, ctx := payloadRecorderCtx(t)
		ctx = ContextWithQuery(ctx, &Query{Server: &payloadRoutingTestServer{
			mockServer:   &mockServer{},
			payloadStore: store,
		}})
		AroundFunc(ctx, req)
		return recorder, ctx
	}

	routeA := &testSeenKeys{}
	recorderA, ctxA := runRoute(routeA)
	require.Equal(t, 5, recorderA.emissionCount(),
		"a session-deduped span must still receive its route's complete call closure")

	firstRouteA := recorderA.emissionCount()
	AroundFunc(ctxA, req)
	require.Equal(t, firstRouteA, recorderA.emissionCount(),
		"revisiting the same delivery domain must remain idempotent")

	recorderB, _ := runRoute(&testSeenKeys{})
	require.Equal(t, firstRouteA, recorderB.emissionCount(),
		"a sibling delivery domain must receive the closure despite session-wide span dedupe")
}

func TestAroundFuncLogsOnlyPayloadsMissingFromSpans(t *testing.T) {
	agent, _, _ := skillsChain()
	recorder, ctx := payloadRecorderCtx(t)
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(spans))
	ctx, root := tp.Tracer("call-span-test").Start(ctx, "root")
	t.Cleanup(func() {
		root.End()
		require.NoError(t, tp.Shutdown(context.Background()))
	})
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)
	payloads := &testSeenKeys{}
	ctx = ContextWithQuery(ctx, &Query{Server: &payloadRoutingTestServer{
		mockServer:   &mockServer{},
		payloadStore: payloads,
		spanStore:    &testSpanSeenKeys{},
	}})
	req := &dagql.CallRequest{ResultCall: agent}
	_, done := AroundFunc(ctx, req)
	var callErr error
	done(nil, false, &callErr)

	require.Equal(t, 4, recorder.emissionCount(),
		"the root payload rides its span; only the four unspanned frames need logs")
	require.Nil(t, recorder.get(rootDigest.String()),
		"a payload carried by a recording span must not also be logged")
	require.False(t, payloads.ClaimCallPayload(rootDigest.String()),
		"the spanned root stays claimed for the span exporter to settle")
	requireSpanCarriesCall(t, spans.Started(), rootDigest.String(), "agent")

	// A new spanned descendant carries its own frame without re-emitting the
	// receiver closure. Re-selecting it (span deduped, log fallback) must
	// remain idempotent as well.
	next := testResultCall("withResponse", &Void{}, agent)
	nextDigest, err := next.RecipeDigest(ctx)
	require.NoError(t, err)
	for range 2 {
		_, done := AroundFunc(ctx, &dagql.CallRequest{ResultCall: next})
		done(nil, false, &callErr)
	}
	require.Equal(t, 4, recorder.emissionCount())
	require.Nil(t, recorder.get(nextDigest.String()))
	requireSpanCarriesCall(t, spans.Started(), nextDigest.String(), "withResponse")
}

// A span that does not record is exported nowhere, so its root must fall back
// to the payload log lane with the rest of its closure.
func TestAroundFuncLogsRootOfNonRecordingSpan(t *testing.T) {
	agent, _, _ := skillsChain()
	recorder, ctx := payloadRecorderCtx(t)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	ctx, root := tp.Tracer("unsampled-test").Start(ctx, "root")
	t.Cleanup(func() {
		root.End()
		require.NoError(t, tp.Shutdown(context.Background()))
	})
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)
	ctx = ContextWithQuery(ctx, &Query{Server: &payloadRoutingTestServer{
		mockServer:   &mockServer{},
		payloadStore: &testSeenKeys{},
		spanStore:    &testSpanSeenKeys{},
	}})
	_, done := AroundFunc(ctx, &dagql.CallRequest{ResultCall: agent})
	var callErr error
	done(nil, false, &callErr)
	require.Equal(t, 5, recorder.emissionCount())
	require.NotNil(t, recorder.get(rootDigest.String()))
}

// requireSpanCarriesCall asserts that one span was started for digest and
// that its start snapshot already carried the call's frame, as clients need
// to render the call before it ends.
func requireSpanCarriesCall(t *testing.T, started []sdktrace.ReadWriteSpan, digest, field string) {
	t.Helper()
	var found []*callpbv1.Call
	for _, span := range started {
		var spanDigest, encoded string
		for _, attr := range span.Attributes() {
			switch string(attr.Key) {
			case telemetry.DagDigestAttr:
				spanDigest = attr.Value.AsString()
			case telemetry.DagCallAttr:
				encoded = attr.Value.AsString()
			}
		}
		if spanDigest != digest {
			continue
		}
		require.NotEmpty(t, encoded, "call span %s must carry its frame", digest)
		var call callpbv1.Call
		require.NoError(t, call.Decode(encoded))
		found = append(found, &call)
	}
	require.Len(t, found, 1, "exactly one span for %s", digest)
	require.Equal(t, field, found[0].Field)
	require.Equal(t, digest, found[0].Digest)
}

// blobCall is a Query.blob-shaped call whose frame inlines size bytes of
// literal argument, the shape that once put a 129 MB attribute on one span.
func blobCall(size int) *dagql.ResultCall {
	blob := testResultCall("blob", &Void{}, nil)
	blob.Args = []*dagql.ResultCallArg{{
		Name:  "contents",
		Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: strings.Repeat("a", size)},
	}}
	return blob
}

// blobCallNear returns the blob call whose encoded span attribute is the
// largest that is still at most limit bytes.
func blobCallNear(ctx context.Context, t *testing.T, limit int) (*dagql.ResultCall, int) {
	t.Helper()
	encodedLen := func(size int) int {
		callPB, err := blobCall(size).CallPB(ctx)
		require.NoError(t, err)
		encoded, err := callPB.Encode()
		require.NoError(t, err)
		return len(encoded)
	}
	// base64 makes the encoded length linear in the literal's size, in
	// steps of 4 per 3 bytes, once the length varints stop growing.
	size := limit * 3 / 4
	for range 4 {
		size += (limit - encodedLen(size)) * 3 / 4
	}
	for encodedLen(size) > limit {
		size--
	}
	for encodedLen(size+1) <= limit {
		size++
	}
	return blobCall(size), size
}

// A span is exported whole and must fit in one OTLP frame, so a frame too
// large for its span attribute rides the payload log lane instead.
func TestAroundFuncLogsFramesTooLargeForSpan(t *testing.T) {
	recorder, ctx := payloadRecorderCtx(t)
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(spans))
	ctx, root := tp.Tracer("large-call-span-test").Start(ctx, "root")
	t.Cleanup(func() {
		root.End()
		require.NoError(t, tp.Shutdown(context.Background()))
	})
	payloads := &testSeenKeys{}
	ctx = ContextWithQuery(ctx, &Query{Server: &payloadRoutingTestServer{
		mockServer:   &mockServer{},
		payloadStore: payloads,
		spanStore:    &testSpanSeenKeys{},
	}})
	run := func(call *dagql.ResultCall) string {
		dgst, err := call.RecipeDigest(ctx)
		require.NoError(t, err)
		_, done := AroundFunc(ctx, &dagql.CallRequest{ResultCall: call})
		var callErr error
		done(nil, false, &callErr)
		return dgst.String()
	}

	under, underSize := blobCallNear(ctx, t, maxSpanCallPayloadBytes)
	underDigest := run(under)
	requireSpanCarriesCall(t, spans.Started(), underDigest, "blob")
	require.Nil(t, recorder.get(underDigest),
		"a frame within the cap rides its span and must not also be logged")

	overDigest := run(blobCall(underSize + 1))
	var overSpans int
	for _, span := range spans.Started() {
		var spanDigest string
		var hasCall bool
		for _, attr := range span.Attributes() {
			switch string(attr.Key) {
			case telemetry.DagDigestAttr:
				spanDigest = attr.Value.AsString()
			case telemetry.DagCallAttr:
				hasCall = true
			}
		}
		if spanDigest == overDigest {
			overSpans++
			require.False(t, hasCall, "a frame over the cap must not ride its span")
		}
	}
	require.Equal(t, 1, overSpans, "the large call still gets its span")

	var logged []recordedPayload
	for _, record := range recorder.snapshot() {
		if record.digestAttr == overDigest {
			logged = append(logged, record)
		}
	}
	require.Len(t, logged, 1, "a frame over the cap must be logged exactly once")
	require.NoError(t, logged[0].err)
	require.Equal(t, otellog.KindBytes, logged[0].bodyKind)
	require.Equal(t, telemetryattrs.CallPayloadContentType, logged[0].contentType)
	require.Equal(t, overDigest, logged[0].digest)
	require.Equal(t, "blob", logged[0].call.Field)
	require.False(t, payloads.ClaimCallPayload(overDigest),
		"the logged root stays claimed for the log exporter to settle")
}

func TestRecordCallPayloadsEmitsTransitiveClosure(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, withSkills, dir := skillsChain()

	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	recordCallPayloads(ctx, &testSeenKeys{}, rootDigest.String(), agent)
	require.Equal(t, rootDigest.String(), rec.firstDigest(), "the requested root must lead its closure")

	records := rec.snapshot()
	require.Len(t, records, 5, "the root and every transitive frame must each be emitted once")
	fields := make([]string, 0, len(records))
	for _, record := range records {
		require.NoError(t, record.err)
		require.Equal(t, InstrumentationLibrary, record.scope)
		require.Equal(t, otellog.KindBytes, record.bodyKind)
		require.Equal(t, otellog.KindString, record.contentTypeKind)
		require.Equal(t, telemetryattrs.CallPayloadContentType, record.contentType)
		require.NotNil(t, record.call)
		require.NotEmpty(t, record.call.Digest, "the payload must carry its own digest")
		require.Equal(t, otellog.KindString, record.digestAttrKind)
		require.Equal(t, record.call.Digest, record.digestAttr,
			"the digest attribute must name the payload it rides on")

		deterministic, err := (proto.MarshalOptions{Deterministic: true}).Marshal(record.call)
		require.NoError(t, err)
		require.Equal(t, deterministic, record.body)
		fields = append(fields, record.call.Field)
	}
	require.ElementsMatch(t, []string{"llm", "withSkills", "agent", "host", "directory"}, fields)

	// The frame behind the ID-literal argument is the whole point: a span
	// payload flattens it to a bare digest, so this channel is the only way
	// it can ever reach a client.
	dirDigest, err := dir.RecipeDigest(ctx)
	require.NoError(t, err)
	dirCall := rec.get(dirDigest.String())
	require.NotNil(t, dirCall, "the ID-literal argument's frame was not published")
	require.Equal(t, "directory", dirCall.Field)
	require.Equal(t, dirDigest.String(), dirCall.Digest)

	// ... and so is everything on the receiver spine below it, since any of
	// those may equally have gone unspanned.
	withSkillsDigest, err := withSkills.RecipeDigest(ctx)
	require.NoError(t, err)
	require.NotNil(t, rec.get(withSkillsDigest.String()))

	// Without a recording span of its own, the root takes the same log
	// transport as every transitive frame.
	rootCall := rec.get(rootDigest.String())
	require.NotNil(t, rootCall, "the root frame was not published")
	require.Equal(t, "agent", rootCall.Field)
}

func TestRecordCallPayloadsSkipsClaimedFrames(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, withSkills, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)
	claimedDigest, err := withSkills.RecipeDigest(ctx)
	require.NoError(t, err)

	seen := &testSeenKeys{}
	require.True(t, seen.ClaimCallPayload(claimedDigest.String()))
	recordCallPayloads(ctx, seen, rootDigest.String(), agent)

	require.Equal(t, 4, rec.emissionCount())
	require.Nil(t, rec.get(claimedDigest.String()),
		"a transitive frame already claimed for payload delivery must not be logged again")
	require.NotNil(t, rec.get(rootDigest.String()),
		"an unclaimed root must reach the payload log lane")
}

func TestRecordCallPayloadsDedupesPerDeliveryDomain(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, _, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	seen := &testSeenKeys{}
	recordCallPayloads(ctx, seen, rootDigest.String(), agent)
	first := rec.emissionCount()
	require.Equal(t, 5, first, "the initial root and its complete closure must be emitted")

	// A second selection of the same call publishes nothing in this synchronous
	// delivery fixture: the first walk covered the whole transitive closure.
	recordCallPayloads(ctx, seen, rootDigest.String(), agent)
	require.Equal(t, first, rec.emissionCount())

	// A LONGER chain over the same frames publishes only what is NEW: its own
	// root frame and the newly referenced prompt; the receiver spine below it
	// was already spent.
	prompt := testResultCall("file", &Void{}, nil)
	longer := testResultCall("withPrompt", &Void{}, agent)
	longer.Args = []*dagql.ResultCallArg{{
		Name: "file",
		Value: &dagql.ResultCallLiteral{
			Kind:      dagql.ResultCallLiteralKindResultRef,
			ResultRef: &dagql.ResultCallRef{Call: prompt},
		},
	}}
	longerDigest, err := longer.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, seen, longerDigest.String(), longer)
	require.Equal(t, first+2, rec.emissionCount(), "only the new root and referenced frame should be published")
	require.NotNil(t, rec.get(longerDigest.String()), "the new root frame was not published")
	promptDigest, err := prompt.RecipeDigest(ctx)
	require.NoError(t, err)
	require.NotNil(t, rec.get(promptDigest.String()), "the new frame was not published")
}

// Without a seen-key store there is no session to dedupe against, so the
// channel stays quiet rather than republishing a chain per call.
func TestRecordCallPayloadsRequiresSeenKeyStore(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, _, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	recordCallPayloads(ctx, nil, rootDigest.String(), agent)
	require.Equal(t, 0, rec.len())
}

// Concurrent walks over a shared chain — parallel selections, an LLM loop
// re-sending the same chain — must claim each frame at the producer, before
// any recipe work, rather than rely on the exporter to dedupe after the fact.
// Otherwise every walk that starts before the first one's records are
// persisted rebuilds, encodes and emits the whole closure again.
func TestRecordCallPayloadsClaimsBeforeConcurrentWalks(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, _, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	seen := &testSeenKeys{}
	const walks = 50
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range walks {
		wg.Go(func() {
			<-start
			recordCallPayloads(ctx, seen, rootDigest.String(), agent)
		})
	}
	close(start)
	wg.Wait()
	require.Equal(t, 5, rec.emissionCount(),
		"concurrent walks over the same chain must emit each frame once")

	// Nothing has been persisted yet, but the claims are already spent: a
	// sequential walk in that window must not re-emit either.
	recordCallPayloads(ctx, seen, rootDigest.String(), agent)
	require.Equal(t, 5, rec.emissionCount())
}

// testClosureKeys is testSeenKeys plus the closure coverage a session store
// keeps, and counts claim attempts so tests can see how much of a chain a
// walk visits.
type testClosureKeys struct {
	mu       sync.Mutex
	claimed  map[string]bool
	covered  map[string]bool
	lost     map[string]bool
	spent    map[string]bool
	repaired map[string]bool
	epoch    uint64
	attempts int
	repairs  int
	// afterSkip, if set, runs once, right after the first walk is told a
	// closure is covered: the moment an exporter's failed write could release
	// a claim inside the closure that walk has just decided to skip.
	afterSkip func()
	// beforeRepair, if set, runs once when a walk asks whether to repair: the
	// moment the root that walk failed to claim could be released.
	beforeRepair func()
}

func newTestClosureKeys() *testClosureKeys {
	return &testClosureKeys{
		claimed:  map[string]bool{},
		covered:  map[string]bool{},
		lost:     map[string]bool{},
		spent:    map[string]bool{},
		repaired: map[string]bool{},
	}
}

func (s *testClosureKeys) ClaimCallPayload(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.claimed[key] {
		return false
	}
	s.claimed[key] = true
	delete(s.lost, key)
	return true
}

func (s *testClosureKeys) ClaimCallPayloadForRepair(key string) (claimed, refused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.claimed[key] {
		return false, false
	}
	if !s.lost[key] {
		return false, true
	}
	s.claimed[key] = true
	delete(s.lost, key)
	s.spent[key] = true
	return true, false
}

func (s *testClosureKeys) StartCallPayloadRepair(root string) bool {
	s.mu.Lock()
	before := s.beforeRepair
	s.beforeRepair = nil
	s.mu.Unlock()
	if before != nil {
		before()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lost) == 0 || s.covered[root] || s.repaired[root] {
		return false
	}
	s.repaired[root] = true
	s.repairs++
	return true
}

func (s *testClosureKeys) CallPayloadReleaseEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

func (s *testClosureKeys) CallPayloadClosureCovered(key string) bool {
	s.mu.Lock()
	covered := s.covered[key]
	after := s.afterSkip
	if covered {
		s.afterSkip = nil
	}
	s.mu.Unlock()
	if covered && after != nil {
		after()
	}
	return covered
}

func (s *testClosureKeys) CoverCallPayloadClosures(keys []string, epoch uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.epoch {
		return false
	}
	for _, key := range keys {
		s.covered[key] = true
	}
	return true
}

// release mirrors a failed write: the claim is released and coverage reset.
func (s *testClosureKeys) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.claimed, key)
	s.covered = map[string]bool{}
	s.epoch++
}

// lose mirrors a write the exporter gave up on: a release that stays
// pending until something claims the frame again, unless a repair walk
// already claimed it once.
func (s *testClosureKeys) lose(key string) {
	s.release(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spent[key] {
		return
	}
	s.lost[key] = true
	s.repaired = map[string]bool{}
}

func (s *testClosureKeys) repairsStarted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repairs
}

func (s *testClosureKeys) takeAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.attempts
	s.attempts = 0
	return n
}

// chainCall builds a receiver chain of n steps, like a module re-driving a
// long pipeline one call at a time.
func chainCall(n int) []*dagql.ResultCall {
	frames := make([]*dagql.ResultCall, 0, n)
	var prev *dagql.ResultCall
	for i := range n {
		frame := testResultCall("step", &Void{}, prev)
		frame.Args = []*dagql.ResultCallArg{{
			Name:  "n",
			Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindInt, IntValue: int64(i)},
		}}
		frames = append(frames, frame)
		prev = frame
	}
	return frames
}

// Extending a chain whose closure was already walked must cost only the new
// frames. Before closure coverage, every new call re-walked (and re-claimed)
// its whole closure, which made re-driving an n-step cached chain O(n^2).
func TestRecordCallPayloadsStopsAtCoveredClosure(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	const depth = 200
	frames := chainCall(depth + 1)

	keys := newTestClosureKeys()
	prefix := frames[depth-1]
	prefixDigest, err := prefix.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, keys, prefixDigest.String(), prefix)
	require.Equal(t, depth, rec.emissionCount(), "the first walk emits the whole chain")
	keys.takeAttempts()

	top := frames[depth]
	topDigest, err := top.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, keys, topDigest.String(), top)
	require.Equal(t, depth+1, rec.emissionCount(), "only the new frame is emitted")
	require.NotNil(t, rec.get(topDigest.String()))
	require.LessOrEqual(t, keys.takeAttempts(), 2,
		"extending a covered chain by one call must not re-claim the frames below it")

	recordCallPayloads(ctx, keys, topDigest.String(), top)
	require.Equal(t, depth+1, rec.emissionCount())
	require.Equal(t, 1, keys.takeAttempts(), "with nothing lost, a replay stops at its root's claim")
	require.Zero(t, keys.repairsStarted())
}

// Coverage is only a shortcut: a frame claimed but not yet walked (here, the
// middle of the chain) must not hide the frames below it, and a released claim
// must be re-emitted by a later walk even from inside a covered closure.
func TestRecordCallPayloadsClosureCoverageKeepsPayloads(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(5)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	require.True(t, keys.ClaimCallPayload(digests[2]))
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	require.Equal(t, 4, rec.emissionCount(),
		"a claimed but unwalked frame must not prune the frames below it")
	require.Nil(t, rec.get(digests[2]))
	require.NotNil(t, rec.get(digests[0]))

	keys.release(digests[1])
	top := testResultCall("top", &Void{}, frames[4])
	topDigest, err := top.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, keys, topDigest.String(), top)
	require.Equal(t, 6, rec.emissionCount(),
		"a released frame inside a covered closure must be emitted again")
	var released int
	for _, record := range rec.snapshot() {
		if record.digest == digests[1] {
			released++
		}
	}
	require.Equal(t, 2, released)
}

// A claim released after a walk decided to skip the covered closure holding
// it, but before the walk finished, must not be left unclaimed: the walk's
// root is claimed by then, so no replay of that call would reach the frame.
// The walk notices the release when it records its coverage and repeats a
// full pass over its closure.
func TestRecordCallPayloadsRewalksAfterReleaseDuringPrunedWalk(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(5)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	recordCallPayloads(ctx, keys, digests[3], frames[3])
	require.Equal(t, 4, rec.emissionCount())

	keys.afterSkip = func() { keys.release(digests[1]) }
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	require.Nil(t, keys.afterSkip, "the walk must have skipped the covered closure")
	require.Equal(t, 6, rec.emissionCount(),
		"the frame released during the walk must be emitted again")
	require.False(t, keys.ClaimCallPayload(digests[1]),
		"the released frame must be claimed again")
	var released int
	for _, record := range rec.snapshot() {
		if record.digest == digests[1] {
			released++
		}
	}
	require.Equal(t, 2, released)
}

// A frame another producer held while a walk passed it, and whose write the
// exporter then gave up on, must be emitted again by a later walk even when
// that walk starts at a root that is already claimed: otherwise every replay
// of the root stops at its claim and the frame stays missing for the client.
func TestRecordCallPayloadsReplayRepairsLostFrame(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(5)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	require.True(t, keys.ClaimCallPayload(digests[1]), "another producer holds the frame")
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	require.Equal(t, 4, rec.emissionCount())
	require.Nil(t, rec.get(digests[1]))

	keys.lose(digests[1])
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	require.NotNil(t, rec.get(digests[1]), "a replay must re-emit the lost frame")
	require.Equal(t, 5, rec.emissionCount(), "nothing else is emitted twice")
	require.False(t, keys.ClaimCallPayload(digests[1]), "the lost frame is claimed again")
}

// A call that rides its own span repairs the same way when its root was
// already claimed.
func TestRecordCallPayloadsForSpanRepairsLostFrame(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(3)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	require.True(t, keys.ClaimCallPayload(digests[2]))
	recordCallPayloadsForSpan(ctx, keys, digests[2], frames[2], true, true)
	require.Equal(t, 2, rec.emissionCount())

	keys.lose(digests[0])
	recordCallPayloadsForSpan(ctx, keys, digests[2], frames[2], false, true)
	require.Equal(t, 3, rec.emissionCount(), "the replay must re-emit only the lost frame")
	require.Equal(t, digests[0], rec.snapshot()[2].digest)
}

// While a loss stays pending (here, one no replay reaches), each root walks
// at most once, and a walk prunes at the closures earlier repair walks
// covered: re-driving a deep chain costs a claim per replay, not a walk.
func TestRecordCallPayloadsRepairWalksOncePerLoss(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	const depth = 200
	frames := chainCall(depth)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	recordCallPayloads(ctx, keys, digests[depth-1], frames[depth-1])
	require.Equal(t, depth, rec.emissionCount())
	keys.takeAttempts()

	keys.lose("xxh3:unreachable")
	recordCallPayloads(ctx, keys, digests[depth-1], frames[depth-1])
	require.Equal(t, 1, keys.repairsStarted())
	require.Equal(t, depth, keys.takeAttempts(),
		"the loss cleared coverage, so the first repair walk claims over the whole chain once")
	require.Equal(t, depth, rec.emissionCount(), "nothing in the chain was lost")

	for range 3 {
		recordCallPayloads(ctx, keys, digests[depth-1], frames[depth-1])
	}
	require.Equal(t, 1, keys.repairsStarted(), "a root walks once per loss")
	require.Equal(t, 3, keys.takeAttempts(), "later replays claim only their root")

	recordCallPayloads(ctx, keys, digests[depth/2], frames[depth/2])
	require.Equal(t, 1, keys.repairsStarted(), "a root the repair walk covered needs no walk")
	require.Equal(t, 1, keys.takeAttempts())

	top := testResultCall("top", &Void{}, frames[depth-1])
	topDigest, err := top.RecipeDigest(ctx)
	require.NoError(t, err)
	require.True(t, keys.ClaimCallPayload(topDigest.String()), "another producer holds the new root")
	keys.takeAttempts()
	recordCallPayloads(ctx, keys, topDigest.String(), top)
	require.Equal(t, 2, keys.repairsStarted())
	require.LessOrEqual(t, keys.takeAttempts(), 2,
		"a new root's repair walk stops at the closure the earlier one covered")

	keys.lose("xxh3:unreachable-too")
	recordCallPayloads(ctx, keys, digests[depth-1], frames[depth-1])
	require.Equal(t, 3, keys.repairsStarted(), "a new loss lets the root walk again")
}

// A payload gets at most one repair copy per target. Once a repair walk
// claimed it and that copy was lost too, repair walks that other losses keep
// running must not emit it again; such a walk records no coverage, so an
// ordinary walk still reaches the frame as it would without repair.
func TestRecordCallPayloadsRepairCopiesEachPayloadOnce(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(5)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}
	lost := digests[1]
	copies := func() int {
		var n int
		for _, record := range rec.snapshot() {
			if record.digest == lost {
				n++
			}
		}
		return n
	}

	keys := newTestClosureKeys()
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	sibling := testResultCall("sibling", &Void{}, frames[2])
	siblingDigest, err := sibling.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, keys, siblingDigest.String(), sibling)
	require.Equal(t, 1, copies())

	keys.lose(lost)
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	require.Equal(t, 2, copies(), "the first replay repairs the lost frame")

	keys.lose(lost)
	keys.lose("xxh3:unreachable")
	recordCallPayloads(ctx, keys, siblingDigest.String(), sibling)
	require.Equal(t, 2, copies(), "a repair copy that was lost too is not repaired again")
	require.False(t, keys.CallPayloadClosureCovered(digests[2]),
		"a walk that left a frame unclaimed must not record coverage")

	top := testResultCall("top", &Void{}, frames[4])
	topDigest, err := top.RecipeDigest(ctx)
	require.NoError(t, err)
	recordCallPayloads(ctx, keys, topDigest.String(), top)
	require.Equal(t, 3, copies(), "an ordinary walk still reaches the frame")
}

// A frame released by a failure that is not final is still queued for its
// export's retry. A repair walk that other losses run must not emit another
// copy of it, nor record coverage over it.
func TestRecordCallPayloadsRepairLeavesRetriedFramesAlone(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(4)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	recordCallPayloads(ctx, keys, digests[3], frames[3])
	require.Equal(t, 4, rec.emissionCount())

	keys.lose("xxh3:unreachable")
	keys.release(digests[1])
	recordCallPayloads(ctx, keys, digests[3], frames[3])
	require.Equal(t, 1, keys.repairsStarted())
	require.Equal(t, 4, rec.emissionCount(), "the retried frame is its export's to deliver")
	require.False(t, keys.CallPayloadClosureCovered(digests[2]),
		"a walk that left a frame unclaimed must not record coverage")
}

// A repair walk starts at a root someone else claimed. If that claim is
// released before the walk reads the release epoch, the walk must not record
// the root as covered: it never logged or claimed it, and later walks would
// prune there and never emit it.
func TestRecordCallPayloadsRepairWalkDoesNotCoverUnownedRoot(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(4)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	recordCallPayloads(ctx, keys, digests[2], frames[2])
	require.Equal(t, 3, rec.emissionCount())

	// Some other frame is lost, and the root's own claim is released right
	// after this walk failed to claim it.
	keys.lose(digests[0])
	keys.beforeRepair = func() { keys.release(digests[2]) }
	recordCallPayloads(ctx, keys, digests[2], frames[2])
	require.Equal(t, 1, keys.repairsStarted())
	require.False(t, keys.CallPayloadClosureCovered(digests[2]),
		"a repair walk must not cover a root it does not own")

	recordCallPayloads(ctx, keys, digests[3], frames[3])
	var root int
	for _, record := range rec.snapshot() {
		if record.digest == digests[2] {
			root++
		}
	}
	require.Equal(t, 2, root, "a later walk must reach and re-emit the released root")
}

// A frame the cache stops holding, deep inside a closure an earlier walk
// already emitted, must not cost a later call its payload logs. Rebuilding
// the later call's whole recipe ID fails on such a frame, so a walk that
// needed the full ID logged nothing for it, and the claimed root was never
// retried: only a root that rides its own span still reached the client, and
// no other new frame of that call, or of any later call built on it, did. A
// walk that stops at the covered closure never reaches the gap.
func TestRecordCallPayloadsCoveredClosureHidesUnresolvableFrame(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	frames := chainCall(5)
	digests := make([]string, len(frames))
	for i, frame := range frames {
		dgst, err := frame.RecipeDigest(ctx)
		require.NoError(t, err)
		digests[i] = dgst.String()
	}

	keys := newTestClosureKeys()
	recordCallPayloads(ctx, keys, digests[4], frames[4])
	require.Equal(t, 5, rec.emissionCount())

	// The bottom frame is gone: frames[1] now reaches it only through a
	// shared-result reference the cache cannot resolve. Digests are already
	// derived, as they are for any frame an earlier walk visited.
	frames[1].Receiver = &dagql.ResultCallRef{ResultID: 42}
	top := testResultCall("top", &Void{}, frames[4])
	topDigest, err := top.RecipeDigest(ctx)
	require.NoError(t, err)
	_, err = top.RecipeID(ctx)
	require.Error(t, err, "the full recipe ID can no longer be rebuilt")

	recordCallPayloads(ctx, keys, topDigest.String(), top)
	require.Equal(t, 6, rec.emissionCount(), "the new call's payload is still emitted")
	require.NotNil(t, rec.get(topDigest.String()))
}
