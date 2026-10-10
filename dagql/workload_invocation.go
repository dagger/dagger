package dagql

import (
	"context"
	"strconv"
	"strings"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// GetOrInitCall records actual invocation bodies independently of their
// waiters. Presentation filtering must not remove accounting evidence.
func (c *Cache) GetOrInitCall(
	ctx context.Context,
	sessionID string,
	resolver TypeResolver,
	req *CallRequest,
	fn func(context.Context) (AnyResult, error),
) (AnyResult, error) {
	if !enginetel.HasWorkloadSpans(ctx) || req == nil || req.ResultCall == nil {
		return c.getOrInitSessionCall(ctx, sessionID, resolver, req, fn)
	}
	// Reflection and introspection describe the schema, not execution work.
	// Keep the parent context so any nested execution-capable call remains
	// accountable. Hidden calls of other classes must still be recorded.
	if req.ProfileSkip || strings.HasPrefix(req.ReceiverTypeName, "__") ||
		req.Field == "__schema" || req.Field == "__type" || req.Field == "__typename" {
		return c.getOrInitSessionCall(ctx, sessionID, resolver, req, fn)
	}
	if req.CacheEvidence == nil {
		req.CacheEvidence = NewCacheDecision()
	}
	res, err := c.getOrInitSessionCall(ctx, sessionID, resolver, req, func(ctx context.Context) (res AnyResult, rerr error) {
		outcome := CacheOutcomeExecuted
		if req.DoNotCache {
			outcome = CacheOutcomeUncached
		}
		span := beginWorkloadInvocation(ctx, req, outcome)
		if span == nil {
			return fn(ctx)
		}
		ctx = enginetel.WithWorkloadParent(ctx, span.SpanContext())
		defer func() { endWorkloadInvocation(context.WithoutCancel(ctx), span, req.ResultCall, res, rerr) }()
		return fn(ctx)
	})
	outcome := req.CacheEvidence.Outcome
	if outcome == CacheOutcomeHit || outcome == CacheOutcomeJoined {
		span := beginWorkloadInvocation(ctx, req, outcome)
		endWorkloadInvocation(context.WithoutCancel(ctx), span, req.ResultCall, res, err)
	}
	return res, err
}

func beginWorkloadInvocation(ctx context.Context, req *CallRequest, outcome CacheOutcome) trace.Span {
	receiver := req.ReceiverTypeName
	if receiver == "" {
		receiver = "Query"
	}
	return enginetel.StartWorkloadSpan(ctx, receiver+"."+req.Field, trace.WithAttributes(
		attribute.String(telemetryattrs.WorkloadContractAttr, telemetryattrs.WorkloadContractV1),
		attribute.String(telemetryattrs.WorkloadScopeAttr, telemetryattrs.WorkloadCallScope),
		attribute.String(telemetryattrs.CacheOutcomeAttr, string(outcome)),
	))
}

func endWorkloadInvocation(ctx context.Context, span trace.Span, frame *ResultCall, res AnyResult, err error) {
	if span == nil {
		return
	}
	defer span.End()
	if err != nil {
		span.SetStatus(codes.Error, "")
	}
	if recipe, err := frame.RecipeDigest(ctx); err == nil {
		span.SetAttributes(attribute.String(telemetry.DagDigestAttr, recipe.String()))
	}
	RecordContentPreferredDigest(ctx, span, frame, res)
	if id, ok := CacheResultNumber(res); ok {
		span.SetAttributes(attribute.String(telemetryattrs.CacheResultIDAttr, strconv.FormatUint(id, 10)))
	}
}
