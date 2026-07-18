package core

// Cache-evidence stamping tests for the seam core owns: carrier allocation
// gating (initCacheEvidence), the carrier→attribute mapping
// (recordCacheEvidence), and the AroundFunc lifecycle end-to-end against an
// in-memory SDK tracer — mirroring otelprof_services_test.go's discipline.
// Carrier POPULATION is dagql's seam and is tested there; these tests never
// re-test it.

import (
	"context"
	"strings"
	"testing"

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
// the contract's wire-type rule as it goes: every contract attribute must be an
// OTel STRING value (the scalar-string shape the Cloud ingest round-trips
// bit-exact), not merely Emit() text that happens to match.
func evidenceTestCacheAttrs(t *testing.T, kvs []attribute.KeyValue) map[string]string {
	t.Helper()
	cacheAttrs := map[string]string{}
	for _, kv := range kvs {
		k := string(kv.Key)
		if !strings.HasPrefix(k, "dagger.io/cache.") {
			continue
		}
		assert.Equal(t, attribute.STRING, kv.Value.Type(), "contract attribute %s must be STRING-typed", k)
		cacheAttrs[k] = kv.Value.AsString()
	}
	return cacheAttrs
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

	recordCacheEvidence(span, &dagql.CacheDecision{
		Outcome:               dagql.CacheOutcomeHit,
		HitRoute:              dagql.CacheHitRouteStructural,
		MissUnknownInputIndex: -1,
		SelfDigest:            selfDig,
		StructuralInputs:      []digest.Digest{inputA, inputB},
		PairingDigest:         pairDig,
	}, res)

	got := evidenceTestCacheAttrs(t, evidenceTestEndedAttrs(t, sr, span))
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr:            telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:             "hit",
		telemetryattrs.CacheHitRouteAttr:            "structural",
		telemetryattrs.CacheSelfDigestAttr:          selfDig.String(),
		telemetryattrs.CacheStructuralInputsAttr:    `["` + inputA.String() + `","` + inputB.String() + `"]`,
		telemetryattrs.CachePairingDigestAttr:       pairDig.String(),
		telemetryattrs.CacheOutputContentDigestAttr: contentDig.String(),
	})
}

func TestRecordCacheEvidenceMappingExecutedMissFacts(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	selfDig := digest.FromString("evidence-miss-self")
	recordCacheEvidence(span, &dagql.CacheDecision{
		Outcome:                    dagql.CacheOutcomeExecuted,
		MissIncompatibleCandidates: true,
		MissSawExpired:             true,
		MissUnknownInputIndex:      2,
		SelfDigest:                 selfDig,
		StructuralInputs:           nil,
		PairingDigest:              selfDig,
	}, nil)

	got := evidenceTestCacheAttrs(t, evidenceTestEndedAttrs(t, sr, span))
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr:                   telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:                    "executed",
		telemetryattrs.CacheMissIncompatibleCandidatesAttr: "true",
		telemetryattrs.CacheMissSawExpiredAttr:             "true",
		telemetryattrs.CacheMissUnknownInputAttr:           "2",
		telemetryattrs.CacheSelfDigestAttr:                 selfDig.String(),
		telemetryattrs.CacheStructuralInputsAttr:           "[]",
		telemetryattrs.CachePairingDigestAttr:              selfDig.String(),
	})
}

func TestRecordCacheEvidenceMappingJoinedStampsNoMissFacts(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	selfDig := digest.FromString("evidence-joined-self")
	recordCacheEvidence(span, &dagql.CacheDecision{
		Outcome: dagql.CacheOutcomeJoined,
		// Populated by the joiner's pre-join lookup probe; must not stamp.
		MissIncompatibleCandidates: true,
		MissSawExpired:             true,
		MissUnknownInputIndex:      0,
		SelfDigest:                 selfDig,
		PairingDigest:              selfDig,
	}, nil)

	got := evidenceTestCacheAttrs(t, evidenceTestEndedAttrs(t, sr, span))
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr:         telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:          "joined",
		telemetryattrs.CacheSelfDigestAttr:       selfDig.String(),
		telemetryattrs.CacheStructuralInputsAttr: "[]",
		telemetryattrs.CachePairingDigestAttr:    selfDig.String(),
	})
}

func TestRecordCacheEvidenceMappingUncached(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	recordCacheEvidence(span, &dagql.CacheDecision{
		Outcome:               dagql.CacheOutcomeUncached,
		MissUnknownInputIndex: -1,
	}, nil)

	got := evidenceTestCacheAttrs(t, evidenceTestEndedAttrs(t, sr, span))
	assert.DeepEqual(t, got, map[string]string{
		telemetryattrs.CacheContractAttr: telemetryattrs.CacheContractV1,
		telemetryattrs.CacheOutcomeAttr:  "uncached",
	})
}

func TestRecordCacheEvidenceMappingUndecidedStampsNothing(t *testing.T) {
	t.Parallel()
	sr, span := evidenceTestRecordingSpan(t)

	// Never-populated carrier (invocation errored before any decision) and nil
	// carrier both stamp nothing — the contract marker never rides a fact-free
	// span.
	recordCacheEvidence(span, dagql.NewCacheDecision(), nil)
	recordCacheEvidence(span, nil, nil)

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
