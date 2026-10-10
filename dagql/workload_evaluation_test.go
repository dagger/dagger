package dagql

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The fixture's completed mapping is data-only, unlike its operational
// resolver, which can settle metadata.
func (obj *cacheTestPartsObject) WorkloadEvaluationGroups(ctx context.Context, _ *ResultCall, parts []PartKey) ([]LazyGroupKey, error) {
	return partsTestDirectResolve(ctx, nil, parts)
}

func (*cacheTestPartsObject) WorkloadEvaluationGroup(_ context.Context, _ *ResultCall, group LazyGroupKey) (LazyGroupKey, error) {
	return group, nil
}

func TestWorkloadPreparationUsesCompletedContent(t *testing.T) {
	ctx, _ := newPartsTestCache(t, nil)
	recorded := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorded))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx = enginetel.WithWorkloadTracer(ctx, provider.Tracer(enginetel.WorkloadScope))
	result := cacheTestDetachedResult(cacheTestIntCall("detached"), NewInt(42))
	_, finish := BeginWorkloadPreparation(ctx)
	require.Empty(t, recorded.Ended())
	completed, err := result.WithContentDigestAny(ctx, digest.Digest("xxh3:completed"))
	require.NoError(t, err)
	finish(completed, nil)
	require.Len(t, recorded.Ended(), 1)
	span := recorded.Ended()[0]
	content, ok := attrString(span, telemetryattrs.DagContentPreferredDigestAttr)
	require.True(t, ok)
	require.Equal(t, "xxh3:completed", content)
	outcome, _ := attrString(span, telemetryattrs.CacheOutcomeAttr)
	require.Equal(t, "executed", outcome)
	var referenceOnly bool
	for _, attr := range span.Attributes() {
		if string(attr.Key) == telemetryattrs.WorkloadReferenceAttr {
			referenceOnly = attr.Value.AsBool()
		}
	}
	require.True(t, referenceOnly)
}

func TestWorkloadEvaluationRecordsDemandAndReuse(t *testing.T) {
	ctx, cache := newPartsTestCache(t, nil)
	recorded := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorded))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx = enginetel.WithWorkloadTracer(ctx, provider.Tracer(enginetel.WorkloadScope))
	var metadataRuns, outputRuns atomic.Int32
	value := &cacheTestPartsObject{Value: 42, resolveFn: partsTestDirectResolve}
	value.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error { metadataRuns.Add(1); return nil },
		partsTestGroupOut:  func(context.Context) error { outputRuns.Add(1); return nil },
	}
	result := newPartsTestResult(t, cache, ctx, value)
	evaluations := func() []sdktrace.ReadOnlySpan {
		var spans []sdktrace.ReadOnlySpan
		for _, span := range recorded.Ended() {
			scope, _ := attrString(span, telemetryattrs.WorkloadScopeAttr)
			if strings.HasPrefix(scope, "evaluation:") {
				spans = append(spans, span)
			}
		}
		return spans
	}
	require.Empty(t, evaluations(), "constructing a result is not an evaluation demand")

	require.NoError(t, cache.EvaluateParts(ctx, result, partsTestPartMeta))
	require.Equal(t, int32(1), metadataRuns.Load())
	require.Zero(t, outputRuns.Load(), "metadata must not run execution output")
	require.Len(t, evaluations(), 1)

	require.NoError(t, cache.EvaluateParts(ctx, result, partsTestPartXMeta))
	require.Equal(t, int32(1), outputRuns.Load())
	require.Len(t, evaluations(), 2)
	first := evaluations()[1]
	scope, ok := attrString(first, telemetryattrs.WorkloadScopeAttr)
	require.True(t, ok)
	outcome, ok := attrString(first, telemetryattrs.CacheOutcomeAttr)
	require.True(t, ok)
	require.Equal(t, "executed", outcome)

	require.NoError(t, cache.EvaluateParts(ctx, result, partsTestPartFS))
	require.Equal(t, int32(1), outputRuns.Load(), "another output part reuses the same evaluation")
	last := evaluations()[2]
	hitScope, _ := attrString(last, telemetryattrs.WorkloadScopeAttr)
	hitOutcome, _ := attrString(last, telemetryattrs.CacheOutcomeAttr)
	require.Equal(t, scope, hitScope, "both parts belong to one billing scope")
	require.Equal(t, "hit", hitOutcome)
	runDigest, _ := attrString(first, telemetryattrs.DagContentPreferredDigestAttr)
	hitDigest, _ := attrString(last, telemetryattrs.DagContentPreferredDigestAttr)
	require.NotEmpty(t, runDigest)
	require.Equal(t, runDigest, hitDigest)

	require.NoError(t, cache.Evaluate(ctx, result))
	before := len(evaluations())
	resolveCalls := value.resolveCalls.Load()
	require.NoError(t, cache.EvaluateParts(ctx, result, partsTestPartXMeta))
	require.Len(t, evaluations(), before+1, "whole-result readiness must still report reuse")
	require.Equal(t, resolveCalls, value.resolveCalls.Load(), "observation must not rerun resolution")
	require.Equal(t, int32(1), outputRuns.Load())
}
