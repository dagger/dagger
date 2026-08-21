package core

// The producer half of the call-payload transport. What must not regress
// silently: frames absent from spans have to ride logs, including frames buried
// inside an ID-literal argument — those are the ones LiteralID.pb flattens to a
// bare digest, so they can never ride a span attribute — and a digest must cross
// the wire at most once per delivery target, or an LLM loop re-sending the same
// chain drowns the log stream.

import (
	"context"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
	hasDigestAttr   bool
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
		case "dagger.io/dag.call.payload.digest":
			got.hasDigestAttr = true
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

type testSeenKeys struct {
	keys sync.Map
}

func (s *testSeenKeys) CallPayloadNeedsEmission(key string) bool {
	_, seen := s.keys.LoadOrStore(key, struct{}{})
	return !seen
}

func (s *testSeenKeys) CallPayloadDelivered(key string) {
	s.keys.Store(key, struct{}{})
}

type alreadySeenTelemetryStore struct{}

func (alreadySeenTelemetryStore) LoadOrStoreTelemetrySeenKey(string) bool { return true }
func (alreadySeenTelemetryStore) StoreTelemetrySeenKey(string)            {}

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
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)
	ctx = ContextWithQuery(ctx, &Query{Server: &payloadRoutingTestServer{
		mockServer:   &mockServer{},
		payloadStore: &testSeenKeys{},
		spanStore:    &testSeenKeys{},
	}})
	req := &dagql.CallRequest{ResultCall: agent}
	_, done := AroundFunc(ctx, req)
	var callErr error
	done(nil, false, &callErr)

	require.Equal(t, 4, recorder.emissionCount(),
		"the root payload rides its span; only the four unspanned frames need logs")
	require.Nil(t, recorder.get(rootDigest.String()),
		"a payload carried by a recording span must not also be logged")
}

func TestRecordCallPayloadsEmitsTransitiveClosure(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, withSkills, dir := skillsChain()

	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	recordCallPayloads(ctx, &testSeenKeys{}, rootDigest.String(), agent, false)
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
		require.False(t, record.hasDigestAttr)
		require.NotNil(t, record.call)
		require.NotEmpty(t, record.call.Digest, "the payload must carry its own digest")

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

	// The root uses the same log transport as every transitive frame; newly
	// emitted spans carry only its digest.
	rootCall := rec.get(rootDigest.String())
	require.NotNil(t, rootCall, "the root frame was not published")
	require.Equal(t, "agent", rootCall.Field)
}

func TestRecordCallPayloadsSkipsFramesDeliveredBySpans(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, withSkills, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)
	spannedDigest, err := withSkills.RecipeDigest(ctx)
	require.NoError(t, err)

	seen := &testSeenKeys{}
	seen.CallPayloadDelivered(spannedDigest.String())
	recordCallPayloads(ctx, seen, rootDigest.String(), agent, false)

	require.Equal(t, 4, rec.emissionCount())
	require.Nil(t, rec.get(spannedDigest.String()),
		"a transitive frame carried by a span must not also be logged")
	require.NotNil(t, rec.get(rootDigest.String()),
		"an unspanned root must retain the log fallback")
}

func TestRecordCallPayloadsDedupesPerDeliveryDomain(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	agent, _, _ := skillsChain()
	rootDigest, err := agent.RecipeDigest(ctx)
	require.NoError(t, err)

	seen := &testSeenKeys{}
	recordCallPayloads(ctx, seen, rootDigest.String(), agent, false)
	first := rec.emissionCount()
	require.Equal(t, 5, first, "the initial root and its complete closure must be emitted")

	// A second selection of the same call publishes nothing in this synchronous
	// delivery fixture: the first walk covered the whole transitive closure.
	recordCallPayloads(ctx, seen, rootDigest.String(), agent, false)
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
	recordCallPayloads(ctx, seen, longerDigest.String(), longer, false)
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

	recordCallPayloads(ctx, nil, rootDigest.String(), agent, false)
	require.Equal(t, 0, rec.len())
}
