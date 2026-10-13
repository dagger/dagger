package dagql

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// HasWorkloadEvaluation distinguishes a retained computation from a value
// whose only deferred action is opening an existing storage handle.
type HasWorkloadEvaluation interface {
	HasWorkloadEvaluation() bool
}

// WorkloadEvaluationGroups maps completed parts without evaluation or I/O.
// It must not change the value or its cache state.
type WorkloadEvaluationGroups interface {
	WorkloadEvaluationGroups(context.Context, *ResultCall, []PartKey) ([]LazyGroupKey, error)
	WorkloadEvaluationGroup(context.Context, *ResultCall, LazyGroupKey) (LazyGroupKey, error)
}

type WorkloadEvaluationParent interface {
	WorkloadEvaluationParent(context.Context, *ResultCall, LazyGroupKey) (*PartDelegation, error)
}

func recordCompletedWorkloadEvaluation(ctx context.Context, res AnyResult, row *sharedResult, parts []PartKey) {
	if !enginetel.HasWorkloadSpans(ctx) || !workloadHasEvaluation(res) {
		return
	}
	groups := []LazyGroupKey{LazyGroupWhole}
	if reporting, ok := UnwrapAs[WorkloadEvaluationGroups](res); ok {
		var err error
		groups, err = reporting.WorkloadEvaluationGroups(ctx, row.loadResultCall(), parts)
		if err != nil {
			return
		}
	} else if _, refined := UnwrapAs[HasLazyEvaluationParts](res); refined {
		return
	}
	for _, group := range groups {
		recordWorkloadEvaluationHit(ctx, row, LazyGroupAddress{Group: group})
	}
}

func workloadEvaluationAddress(ctx context.Context, res AnyResult, row *sharedResult, group LazyGroupKey) (LazyGroupAddress, bool) {
	address := LazyGroupAddress{Group: group}
	if !enginetel.HasWorkloadSpans(ctx) || !workloadHasEvaluation(res) {
		return address, false
	}
	if reporting, ok := UnwrapAs[WorkloadEvaluationGroups](res); ok {
		var err error
		address.Group, err = reporting.WorkloadEvaluationGroup(ctx, row.loadResultCall(), group)
		if err != nil || address.Group == "" {
			return address, false
		}
	}
	return address, true
}

func workloadHasEvaluation(res AnyResult) bool {
	if reporting, ok := UnwrapAs[HasWorkloadEvaluation](res); ok {
		return reporting.HasWorkloadEvaluation()
	}
	_, ok := UnwrapAs[HasLazyEvaluation](res)
	return ok
}

// beginWorkloadEvaluation does no cache lookup and is safe while lazyMu is
// held. Digests are read after the attempt releases its cache locks.
func beginWorkloadEvaluation(ctx context.Context, row *sharedResult, address LazyGroupAddress, outcome string) trace.Span {
	if len(address.OutputPath) == 0 {
		address.OutputPath = nil
	}
	return enginetel.StartWorkloadSpan(ctx, "evaluate",
		trace.WithAttributes(
			attribute.String(telemetryattrs.WorkloadContractAttr, telemetryattrs.WorkloadContractV1),
			attribute.String(telemetryattrs.WorkloadScopeAttr, "evaluation:"+digest.FromString(lazyGroupAddressKey(address)).Encoded()),
			attribute.String(telemetryattrs.CacheResultIDAttr, strconv.FormatUint(uint64(row.id), 10)),
			attribute.String(telemetryattrs.CacheOutcomeAttr, outcome),
		),
	)
}

func endWorkloadEvaluation(ctx context.Context, span trace.Span, row *sharedResult, address LazyGroupAddress, err error) {
	if span == nil {
		return
	}
	defer span.End()
	if err != nil {
		span.SetStatus(codes.Error, "")
	}
	frame := row.loadResultCall()
	// Restored metadata and snapshot-opening groups use the retained
	// operation's scope, not the storage callback's scope.
	if len(address.OutputPath) == 0 {
		if native, ok := UnwrapAs[WorkloadEvaluationGroups](row.loadPayloadState().self); ok {
			if group, groupErr := native.WorkloadEvaluationGroup(ctx, frame, address.Group); groupErr == nil && group != "" {
				address.Group = group
				address.OutputPath = nil
				span.SetAttributes(attribute.String(telemetryattrs.WorkloadScopeAttr, "evaluation:"+digest.FromString(lazyGroupAddressKey(address)).Encoded()))
			}
		}
	}
	for i := 1; i < len(address.OutputPath); i += 2 {
		if frame != nil {
			frame = persistedListItemCall(frame, address.OutputPath[i].Index+1)
		}
	}
	if frame == nil {
		return
	}
	if recipe, err := frame.RecipeDigest(ctx); err == nil {
		span.SetAttributes(attribute.String(telemetry.DagDigestAttr, recipe.String()))
	}
	RecordContentPreferredDigest(ctx, span, frame, nil)
	if err == nil {
		if owner, group, release := workloadEvaluationProducer(ctx, row, address); owner != nil {
			defer release()
			span.SetAttributes(attribute.String(telemetryattrs.WorkloadScopeAttr, "delegation"))
			useCtx := enginetel.WithWorkloadParent(ctx, span.SpanContext())
			use := beginWorkloadEvaluation(useCtx, owner, group, "hit")
			endWorkloadEvaluation(useCtx, use, owner, group, nil)
		}
	}
}

// workloadEvaluationProducer walks captured part metadata only. It does not
// decode values, open snapshots, or request evaluation. Exact dependency holds
// keep each parent alive until the producer's observation has ended.
func workloadEvaluationProducer(ctx context.Context, row *sharedResult, address LazyGroupAddress) (*sharedResult, LazyGroupAddress, func()) {
	frame := row.loadResultCall()
	for i := 1; i < len(address.OutputPath) && frame != nil; i += 2 {
		frame = persistedListItemCall(frame, address.OutputPath[i].Index+1)
	}
	if frame == nil || frame.Type == nil || frame.Type.NamedType != "Container" || address.Group == "metadata" || address.Group == LazyGroupWhole {
		return nil, LazyGroupAddress{}, nil
	}
	cache, err := EngineCache(ctx)
	if err != nil {
		return nil, LazyGroupAddress{}, nil
	}
	var held []*sharedResult
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = cache.releasePartRow(context.WithoutCancel(ctx), held[i])
		}
	}
	current, group := row, address
	seen := map[partDelegationStep]bool{}
	for range 64 {
		step := partDelegationStep{current.id, lazyGroupAddressKey(group)}
		if seen[step] {
			break
		}
		seen[step] = true
		delegation, err := cache.workloadEvaluationDelegation(ctx, current, group)
		if err != nil {
			break
		}
		if delegation == nil {
			if current != row {
				return current, group, release
			}
			break
		}
		cache.egraphMu.Lock()
		parent := cache.resultsByID[sharedResultID(delegation.ParentResultID)]
		_, direct := current.deps[sharedResultID(delegation.ParentResultID)]
		if parent == nil || parent == current || !direct {
			cache.egraphMu.Unlock()
			break
		}
		cache.incrementIncomingOwnershipLocked(ctx, parent)
		cache.egraphMu.Unlock()
		held = append(held, parent)
		var ok bool
		group, ok = cache.workloadDelegatedGroup(ctx, parent, delegation.Address)
		if !ok {
			break
		}
		current = parent
	}
	release()
	return nil, LazyGroupAddress{}, nil
}

func (c *Cache) workloadEvaluationDelegation(ctx context.Context, row *sharedResult, group LazyGroupAddress) (*PartDelegation, error) {
	if native, ok := UnwrapAs[WorkloadEvaluationParent](row.loadPayloadState().self); ok && len(group.OutputPath) == 0 {
		return native.WorkloadEvaluationParent(ctx, row.loadResultCall(), group.Group)
	}
	part := PersistedPartAddress{OutputPath: group.OutputPath, Part: PartKey(group.Group)}
	record, _, _, err := c.probePart(ctx, row, part)
	if err != nil {
		return nil, err
	}
	var delegation *PartDelegation
	err = walkTransferPayloads(&record.Envelope, record.Call, record.SnapshotLinks, func(f PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if !slices.Equal(v.Path, group.OutputPath) {
			return v.Payload, nil
		}
		if router, ok := f.Transfer.(WorkloadPartRouter); ok {
			var err error
			delegation, err = router.WorkloadPartDelegation(v, group.Group)
			return v.Payload, err
		}
		return v.Payload, nil
	})
	return delegation, err
}

func (c *Cache) workloadDelegatedGroup(ctx context.Context, parent *sharedResult, address PersistedPartAddress) (LazyGroupAddress, bool) {
	if native, ok := UnwrapAs[WorkloadEvaluationGroups](parent.loadPayloadState().self); ok && len(address.OutputPath) == 0 {
		groups, err := native.WorkloadEvaluationGroups(ctx, parent.loadResultCall(), []PartKey{address.Part})
		if err != nil || len(groups) != 1 {
			return LazyGroupAddress{}, false
		}
		return LazyGroupAddress{Group: groups[0]}, true
	}
	record, _, _, err := c.probePart(ctx, parent, address)
	if err != nil {
		return LazyGroupAddress{}, false
	}
	route, err := routePartRecord(record, address)
	if err != nil || route.NeedsMetadata || (!route.HasLazyOperation && route.Delegation == nil) {
		return LazyGroupAddress{}, false
	}
	if route.Delegation != nil {
		return LazyGroupAddress{OutputPath: address.OutputPath, Group: LazyGroupKey(address.Part)}, true
	}
	return route.Group, true
}

// BeginWorkloadPreparation observes a detached whole-value evaluation used to
// prepare content identity. It supplies reference evidence, not a consumer use.
// Finish with the returned value so its final content identity is available.
func BeginWorkloadPreparation(ctx context.Context) (context.Context, func(AnyResult, error)) {
	if !enginetel.HasWorkloadSpans(ctx) {
		return ctx, func(AnyResult, error) {}
	}
	referenceCtx := enginetel.WithWorkloadReference(ctx)
	span := enginetel.StartWorkloadSpan(enginetel.WithWorkloadRoot(referenceCtx), "evaluate",
		trace.WithAttributes(
			attribute.String(telemetryattrs.WorkloadContractAttr, telemetryattrs.WorkloadContractV1),
			attribute.String(telemetryattrs.WorkloadScopeAttr, "evaluation:"+digest.FromString(lazyGroupAddressKey(LazyGroupAddress{Group: LazyGroupWhole})).Encoded()),
			attribute.String(telemetryattrs.CacheOutcomeAttr, "executed"),
		))
	if span == nil {
		return ctx, func(AnyResult, error) {}
	}
	return enginetel.WithWorkloadParent(referenceCtx, span.SpanContext()), func(res AnyResult, err error) {
		defer span.End()
		if err != nil {
			span.SetStatus(codes.Error, "")
		}
		if res == nil {
			return
		}
		frame, frameErr := res.ResultCall()
		if frameErr != nil || frame == nil {
			return
		}
		if recipe, recipeErr := frame.RecipeDigest(ctx); recipeErr == nil {
			span.SetAttributes(attribute.String(telemetry.DagDigestAttr, recipe.String()))
		}
		RecordContentPreferredDigest(ctx, span, frame, res)
	}
}

func recordWorkloadEvaluationHit(ctx context.Context, row *sharedResult, address LazyGroupAddress) {
	span := beginWorkloadEvaluation(ctx, row, address, "hit")
	endWorkloadEvaluation(ctx, span, row, address, nil)
}
