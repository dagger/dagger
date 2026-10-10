package core

// Cache-evidence stamping tests for the seam core owns: carrier allocation
// gating (initCacheEvidence), the carrier→attribute mapping
// (recordCacheEvidence), and the AroundFunc lifecycle end-to-end against an
// in-memory SDK tracer — mirroring otelprof_services_test.go's discipline.
// Carrier POPULATION is dagql's seam and is tested there; these tests never
// re-test it.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func evidenceTestFrame(field string) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:  dagql.ResultCallKindField,
		Type:  dagql.NewResultCallType(dagql.Int(0).Type()),
		Field: field,
	}
}

func evidenceTestRecordingSpan(t *testing.T) (*tracetest.SpanRecorder, trace.Span) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	_, span := tp.Tracer("cache-evidence-test").Start(context.Background(), "test-span")
	return sr, span
}

func evidenceTestEndedAttrs(t *testing.T, sr *tracetest.SpanRecorder, span trace.Span) []attribute.KeyValue {
	t.Helper()
	span.End()
	ended := sr.Ended()
	assert.Assert(t, len(ended) > 0)
	return ended[len(ended)-1].Attributes()
}

// evidenceTestCacheAttrs filters to the dagger.io/cache.* attributes, asserting
// the contract's wire-type rule as it goes: every contract attribute is an
// OTel STRING value except the structural-input list, which must be a native
// STRINGSLICE (collected via evidenceTestStructuralInputs) — actual value
// types, not merely Emit() text that happens to match.
func evidenceTestCacheAttrs(t *testing.T, kvs []attribute.KeyValue) map[string]string {
	t.Helper()
	cacheAttrs := map[string]string{}
	for _, kv := range kvs {
		k := string(kv.Key)
		if !strings.HasPrefix(k, "dagger.io/cache.") {
			continue
		}
		switch k {
		case telemetryattrs.CacheStructuralInputsAttr, telemetryattrs.CacheDepsAttr, telemetryattrs.CachePartsAttr:
			// The contract's native string slices (checked separately via
			// evidenceTestStructuralInputs and evidenceTestStringSlice).
			assert.Equal(t, attribute.STRINGSLICE, kv.Value.Type(), "contract attribute %s must be STRINGSLICE-typed", k)
			continue
		}
		assert.Equal(t, attribute.STRING, kv.Value.Type(), "contract attribute %s must be STRING-typed", k)
		cacheAttrs[k] = kv.Value.AsString()
	}
	return cacheAttrs
}

// evidenceTestStructuralInputs extracts the native structural-input slice:
// (values, present). Present-and-empty and absent are distinct recorded facts.
func evidenceTestStructuralInputs(t *testing.T, kvs []attribute.KeyValue) ([]string, bool) {
	t.Helper()
	for _, kv := range kvs {
		if string(kv.Key) != telemetryattrs.CacheStructuralInputsAttr {
			continue
		}
		assert.Equal(t, attribute.STRINGSLICE, kv.Value.Type())
		return kv.Value.AsStringSlice(), true
	}
	return nil, false
}

// evidenceTestStringSlice extracts a native string-slice attribute: (values,
// present).
func evidenceTestStringSlice(kvs []attribute.KeyValue, key string) ([]string, bool) {
	for _, kv := range kvs {
		if string(kv.Key) == key {
			return kv.Value.AsStringSlice(), true
		}
	}
	return nil, false
}

func TestInitCacheEvidenceGates(t *testing.T) {
	t.Parallel()

	// Recording span + ordinary call: armed.
	_, span := evidenceTestRecordingSpan(t)
	req := &dagql.CallRequest{ResultCall: evidenceTestFrame("ordinary")}
	initCacheEvidence(span, req)
	assert.Assert(t, req.CacheEvidence != nil)
	assert.Equal(t, -1, req.CacheEvidence.MissUnknownInputIndex)

	// ProfileSkip-classified call: never armed.
	skipReq := &dagql.CallRequest{ResultCall: evidenceTestFrame("skipped")}
	skipReq.ResultCall.ProfileSkip = true
	initCacheEvidence(span, skipReq)
	assert.Assert(t, skipReq.CacheEvidence == nil)

	// Non-recording span: never armed.
	_, noopSpan := tracenoop.NewTracerProvider().Tracer("t").Start(context.Background(), "noop")
	noopReq := &dagql.CallRequest{ResultCall: evidenceTestFrame("nonrecording")}
	initCacheEvidence(noopSpan, noopReq)
	assert.Assert(t, noopReq.CacheEvidence == nil)

	// Degenerate inputs: no panic, no arming.
	initCacheEvidence(nil, req)
	initCacheEvidence(span, nil)
	initCacheEvidence(span, &dagql.CallRequest{})
}

func TestRecordCacheEvidenceMappingHit(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	selfDig := digest.FromString("evidence-map-self")
	pairDig := digest.FromString("evidence-map-pair")
	inputA := digest.FromString("evidence-map-input-a")
	inputB := digest.FromString("evidence-map-input-b")
	contentDig := digest.FromString("evidence-map-content")

	resFrame := evidenceTestFrame("producer")
	resFrame.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: contentDig}}
	res, err := dagql.NewResultForCall(dagql.NewInt(1), resFrame)
	assert.NilError(t, err)

	recordCacheEvidence(context.Background(), span, &dagql.CacheDecision{
		Outcome:               dagql.CacheOutcomeHit,
		HitRoute:              dagql.CacheHitRouteStructural,
		MissUnknownInputIndex: -1,
		SelfDigest:            selfDig,
		StructuralInputs:      []digest.Digest{inputA, inputB},
		PairingDigest:         pairDig,
	}, res)

	ended := evidenceTestEndedAttrs(t, sr, span)
	got := evidenceTestCacheAttrs(t, ended)
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr:            telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:             "hit",
		telemetryattrs.CacheHitRouteAttr:            "structural",
		telemetryattrs.CacheSelfDigestAttr:          selfDig.String(),
		telemetryattrs.CachePairingDigestAttr:       pairDig.String(),
		telemetryattrs.CacheOutputContentDigestAttr: contentDig.String(),
		telemetryattrs.CacheTypeAttr:                "Int",
	})
	inputs, present := evidenceTestStructuralInputs(t, ended)
	assert.Assert(t, present)
	// Exact values in exact order — the miss unknown-input index points here.
	assert.DeepEqual(t, inputs, []string{inputA.String(), inputB.String()})
}

func TestRecordCacheEvidenceMappingExecutedMissFacts(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	selfDig := digest.FromString("evidence-miss-self")
	recordCacheEvidence(context.Background(), span, &dagql.CacheDecision{
		Outcome:                    dagql.CacheOutcomeExecuted,
		MissIncompatibleCandidates: true,
		MissSawExpired:             true,
		MissUnknownInputIndex:      2,
		SelfDigest:                 selfDig,
		StructuralInputs:           nil,
		PairingDigest:              selfDig,
	}, nil)

	ended := evidenceTestEndedAttrs(t, sr, span)
	got := evidenceTestCacheAttrs(t, ended)
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr:                   telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:                    "executed",
		telemetryattrs.CacheMissIncompatibleCandidatesAttr: "true",
		telemetryattrs.CacheMissSawExpiredAttr:             "true",
		telemetryattrs.CacheMissUnknownInputAttr:           "2",
		telemetryattrs.CacheSelfDigestAttr:                 selfDig.String(),
		telemetryattrs.CachePairingDigestAttr:              selfDig.String(),
	})
	inputs, present := evidenceTestStructuralInputs(t, ended)
	assert.Assert(t, present)
	assert.Equal(t, 0, len(inputs), "a nil carrier list records a present-and-empty slice")
}

func TestRecordCacheEvidenceMappingJoinedStampsNoMissFacts(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	selfDig := digest.FromString("evidence-joined-self")
	recordCacheEvidence(context.Background(), span, &dagql.CacheDecision{
		Outcome: dagql.CacheOutcomeJoined,
		// Populated by the joiner's pre-join lookup probe; must not stamp.
		MissIncompatibleCandidates: true,
		MissSawExpired:             true,
		MissUnknownInputIndex:      0,
		SelfDigest:                 selfDig,
		PairingDigest:              selfDig,
	}, nil)

	ended := evidenceTestEndedAttrs(t, sr, span)
	got := evidenceTestCacheAttrs(t, ended)
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr:      telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:       "joined",
		telemetryattrs.CacheSelfDigestAttr:    selfDig.String(),
		telemetryattrs.CachePairingDigestAttr: selfDig.String(),
	})
	inputs, present := evidenceTestStructuralInputs(t, ended)
	assert.Assert(t, present)
	assert.Equal(t, 0, len(inputs))
}

func TestRecordCacheEvidenceMappingUncached(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	recordCacheEvidence(context.Background(), span, &dagql.CacheDecision{
		Outcome:               dagql.CacheOutcomeUncached,
		MissUnknownInputIndex: -1,
	}, nil)

	ended := evidenceTestEndedAttrs(t, sr, span)
	got := evidenceTestCacheAttrs(t, ended)
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr: telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:  "uncached",
	})
	// No structural identity stamped: the slice is ABSENT, not empty.
	_, present := evidenceTestStructuralInputs(t, ended)
	assert.Assert(t, !present)
}

func TestRecordCacheEvidenceMappingUndecidedStampsNothing(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	// Never-populated carrier (invocation errored before any decision) and nil
	// carrier both stamp nothing — the contract marker never rides a fact-free
	// span.
	recordCacheEvidence(context.Background(), span, dagql.NewCacheDecision(), nil)
	recordCacheEvidence(context.Background(), span, nil, nil)

	got := evidenceTestCacheAttrs(t, evidenceTestEndedAttrs(t, sr, span))
	assert.Equal(t, 0, len(got))
}

func TestAroundFuncCacheEvidenceLifecycle(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	ctx, root := tp.Tracer("cache-evidence-test").Start(context.Background(), "root")
	defer root.End()

	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	// Ordinary call: AroundFunc starts a span and arms the carrier; the done
	// callback maps whatever dagql recorded onto that span.
	req := &dagql.CallRequest{
		ResultCall:       evidenceTestFrame("evidenceLifecycle"),
		ReceiverTypeName: "Container",
	}
	_, done := AroundFunc(ctx, req)
	assert.Assert(t, req.CacheEvidence != nil)

	// Simulate dagql's population for a plain executed call.
	req.CacheEvidence.Outcome = dagql.CacheOutcomeExecuted
	req.CacheEvidence.SelfDigest = digest.FromString("lifecycle-self")
	req.CacheEvidence.PairingDigest = digest.FromString("lifecycle-self")

	var rerr error
	done(nil, false, &rerr)

	ended := sr.Ended()
	assert.Equal(t, 1, len(ended))
	var sawDigest, sawCall bool
	for _, attr := range ended[0].Attributes() {
		switch string(attr.Key) {
		case telemetry.DagDigestAttr:
			sawDigest = true
		case telemetry.DagCallAttr:
			sawCall = true
		}
	}
	assert.Assert(t, sawDigest, "call span must retain its digest")
	// A recording span is the carrier of its own call frame: clients render
	// the call from it, and the engine delivers it on a protected span lane.
	assert.Assert(t, sawCall, "call span must carry its call payload")
	got := evidenceTestCacheAttrs(t, ended[0].Attributes())
	assert.Equal(t, got[telemetryattrs.CacheContractAttr], telemetryattrs.CacheContractV1)
	assert.Equal(t, got[telemetryattrs.CacheOutcomeAttr], "executed")
	assert.Equal(t, got[telemetryattrs.CacheSelfDigestAttr], digest.FromString("lifecycle-self").String())

	// Introspection call: suppressed before any span exists — never armed.
	introReq := &dagql.CallRequest{
		ResultCall:       evidenceTestFrame("__schema"),
		ReceiverTypeName: "Query",
	}
	_, _ = AroundFunc(ctx, introReq)
	assert.Assert(t, introReq.CacheEvidence == nil)

	// ProfileSkip-classified call (reflection receiver): its span exists but
	// the carrier is never armed, so nothing can stamp.
	skipReq := &dagql.CallRequest{
		ResultCall:       evidenceTestFrame("load"),
		ReceiverTypeName: "TypeDef",
	}
	_, skipDone := AroundFunc(ctx, skipReq)
	assert.Assert(t, skipReq.ResultCall.ProfileSkip)
	assert.Assert(t, skipReq.CacheEvidence == nil)
	skipDone(nil, false, &rerr)
	for _, ended := range sr.Ended() {
		for _, kv := range ended.Attributes() {
			if string(kv.Key) == telemetryattrs.CacheContractAttr && ended.Name() == "TypeDef.load" {
				t.Fatalf("profile-skipped span must not carry the cache contract")
			}
		}
	}
}

type evidenceTestTypeResolver struct{}

func (evidenceTestTypeResolver) ObjectType(string) (dagql.ObjectType, bool) { return nil, false }
func (evidenceTestTypeResolver) ScalarType(string) (dagql.ScalarType, bool) { return nil, false }

// A cache-backed result names its engine-local result number, the number the
// cache's facts use, so the span joins them. A detached result stamps none
// (TestRecordCacheEvidenceMappingHit's exact attribute set).
func TestRecordCacheEvidenceResultID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, cache.CloseDiscardingPersistence()) })
	frame := evidenceTestFrame("resultNumber")
	res, err := cache.GetOrInitCall(ctx, "evidence-session", evidenceTestTypeResolver{}, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(dagql.NewInt(1), frame)
	})
	assert.NilError(t, err)
	number, ok := dagql.CacheResultNumber(res)
	assert.Assert(t, ok)

	sr, span := evidenceTestRecordingSpan(t)
	recordCacheEvidence(context.Background(), span, &dagql.CacheDecision{Outcome: dagql.CacheOutcomeExecuted, MissUnknownInputIndex: -1}, res)
	got := evidenceTestCacheAttrs(t, evidenceTestEndedAttrs(t, sr, span))
	assert.Equal(t, got[telemetryattrs.CacheResultIDAttr], strconv.FormatUint(number, 10))
}

// The span of a persistable call reports its result's state in the cache: its
// dependencies, the retention its publication adds with the edge's expiry, its
// own expiry and its type. A result with no filesystem part reports no parts.
func TestRecordCacheEvidenceCacheState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, cache.CloseDiscardingPersistence()) })
	frame := evidenceTestFrame("cacheState")
	req := &dagql.CallRequest{ResultCall: frame, CacheEvidence: dagql.NewCacheDecision(), IsPersistable: true, TTL: 600}
	res, err := cache.GetOrInitCall(ctx, "evidence-session", evidenceTestTypeResolver{}, req, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(dagql.NewInt(1), frame)
	})
	assert.NilError(t, err)
	state, ok := req.CacheEvidence.ResultState(ctx, res)
	assert.Assert(t, ok)
	assert.Assert(t, state.Retained && state.RetentionExpiresAtUnix != 0 && state.ExpiresAtUnix != 0)

	sr, span := evidenceTestRecordingSpan(t)
	recordCacheEvidence(ctx, span, req.CacheEvidence, res)
	kvs := evidenceTestEndedAttrs(t, sr, span)
	got := evidenceTestCacheAttrs(t, kvs)
	assert.Equal(t, "true", got[telemetryattrs.CacheRetainedAttr])
	assert.Equal(t, strconv.FormatInt(state.RetentionExpiresAtUnix, 10), got[telemetryattrs.CacheRetentionExpiresAttr])
	assert.Equal(t, strconv.FormatInt(state.ExpiresAtUnix, 10), got[telemetryattrs.CacheExpiresAttr])
	assert.Equal(t, "Int", got[telemetryattrs.CacheTypeAttr])
	deps, ok := evidenceTestStringSlice(kvs, telemetryattrs.CacheDepsAttr)
	assert.Assert(t, ok, "an empty dependency list is a recorded fact")
	assert.Equal(t, 0, len(deps))
	_, hasParts := evidenceTestStringSlice(kvs, telemetryattrs.CachePartsAttr)
	assert.Assert(t, !hasParts)

	// The state maps attribute for attribute.
	mapped := cacheStateAttrs(dagql.CacheResultState{Deps: []uint64{3, 12}, Parts: []string{`{"part":"fs"}`}})
	assert.DeepEqual(t, []string{"3", "12"}, mapped[0].Value.AsStringSlice())
	assert.Equal(t, attribute.Key(telemetryattrs.CacheDepsAttr), mapped[0].Key)
	assert.Equal(t, 2, len(mapped), "no retention, expiry or empty parts are stamped")
	assert.DeepEqual(t, []string{`{"part":"fs"}`}, mapped[1].Value.AsStringSlice())
}

// evidenceTestChain builds a result whose frame has a receiver chain of n
// inline calls with an argument each, like a container built up step by step.
func evidenceTestChain(t testing.TB, n int, content digest.Digest) dagql.AnyResult {
	t.Helper()
	var receiver *dagql.ResultCallRef
	for i := range n {
		step := evidenceTestFrame("step" + strconv.Itoa(i))
		step.Receiver = receiver
		step.Args = []*dagql.ResultCallArg{{Name: "value", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: strings.Repeat("v", 64)}}}
		receiver = &dagql.ResultCallRef{Call: step}
	}
	frame := evidenceTestFrame("final")
	frame.Receiver = receiver
	if content != "" {
		frame.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: content}}
	}
	res, err := dagql.NewResultForCall(dagql.NewInt(1), frame)
	assert.NilError(t, err)
	return res
}

// ResultFrameFacts reads the same facts as cloning the frame with ResultCall,
// which recordCacheEvidence used to do for every recorded span.
func TestResultFrameFacts(t *testing.T) {
	t.Parallel()
	contentDig := digest.FromString("frame-facts-content")
	for _, content := range []digest.Digest{"", contentDig} {
		res := evidenceTestChain(t, 8, content)
		frame, err := res.ResultCall()
		assert.NilError(t, err)
		gotContent, gotType, ok := dagql.ResultFrameFacts(res)
		assert.Assert(t, ok)
		assert.Equal(t, frame.ContentDigest(), gotContent)
		assert.Equal(t, frame.Type.NamedType, gotType)
		assert.Equal(t, content, gotContent)
		assert.Equal(t, "Int", gotType)
	}

	_, _, ok := dagql.ResultFrameFacts(nil)
	assert.Assert(t, !ok)
}

func BenchmarkRecordCacheEvidence(b *testing.B) {
	res := evidenceTestChain(b, 32, digest.FromString("bench-content"))
	ev := &dagql.CacheDecision{Outcome: dagql.CacheOutcomeHit, HitRoute: dagql.CacheHitRouteStructural, MissUnknownInputIndex: -1}
	ctx := context.Background()
	span := tracenoop.Span{}
	b.ReportAllocs()
	for b.Loop() {
		recordCacheEvidence(ctx, span, ev, res)
	}
}
