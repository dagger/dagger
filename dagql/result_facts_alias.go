package dagql

import (
	"context"
	"errors"
	"fmt"
)

// A call result owns its frame and facts, and borrows a value from a retained
// result. Output equivalence never combines their facts. The explicit value
// reference is persisted and relocated like every other dependency reference.
func newCallResultAlias(frame *ResultCall, value AnyResult, inherit bool) *sharedResult {
	source := value.cacheSharedResult()
	state := source.loadPayloadState()
	row := &sharedResult{
		valueSource: source, self: state.self, hasValue: state.hasValue,
		isObject: state.isObject, objClass: state.objClass,
		persistDeclined: source.persistDeclined,
	}
	row.storeResultCall(frame.clone())
	if inherit {
		row.resultFacts = cloneResultFacts(state.resultFacts)
		row.resultFactsIncomplete = state.resultFactsIncomplete
		row.resultFactBytes = resultFactPayloadBytes(row.resultFacts)
	}
	row.payloadBytes = row.resultFactBytes
	return row
}

func (row *sharedResult) hasResultFacts() bool {
	state := row.loadPayloadState()
	return len(state.resultFacts) != 0 || state.resultFactsIncomplete
}

func requestHasResultFacts(req *CallRequest) bool {
	return req != nil && (len(req.ResultFacts) != 0 || req.ResultFactsIncomplete || req.ResultFactsPrepared || req.CaptureResultFacts != nil || req.RebindResultFacts != nil)
}

func (c *Cache) resultMatchesRequest(row *sharedResult, req *CallRequest) bool {
	frame := row.loadResultCall()
	if frame == nil {
		return false
	}
	own, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return false
	}
	requested, err := req.deriveRecipeDigest(c)
	return err == nil && own == requested
}

// A cache can reuse an equivalent payload, but the numeric ID returned to an
// SDK must name this call's provenance. This also handles a cache whose recipe
// had previously been taught onto a different producer's output.
func (c *Cache) callResultForRequest(ctx context.Context, session string, resolver TypeResolver, req *CallRequest, result AnyResult) (AnyResult, error) {
	if !requestHasResultFacts(req) || result == nil {
		return result, nil
	}
	matchingRecipe := c.resultMatchesRequest(result.cacheSharedResult(), req)
	reused := result.HitCache() || req.successfulJoinedResult
	if matchingRecipe && (!reused || req.RebindResultFacts == nil && !req.ResultFactsPrepared) {
		return result, nil
	}
	planned := req.Clone()
	state := result.cacheSharedResult().loadPayloadState()
	if req.RebindResultFacts != nil && reused {
		facts, complete, err := req.RebindResultFacts(ctx, result.cacheSharedResult().loadResultCall().clone(), result, cloneResultFacts(state.resultFacts), !state.resultFactsIncomplete)
		if err != nil {
			return nil, fmt.Errorf("rebind result facts: %w", err)
		}
		if err := validateResultFacts(PersistedResultEnvelope{FactsCoverageVersion: resultFactsCoverageVersion, ResultFacts: facts}, true); err != nil {
			return nil, fmt.Errorf("rebind result facts: %w", err)
		}
		// Replacement facts describe this producer, not the borrowed value.
		// Unknown old coverage cannot become complete through a transform;
		// only a fully prepared current request can replace that coverage.
		planned.ResultFacts = unionResultFacts(cloneResultFacts(facts))
		planned.ResultFactsIncomplete = req.ResultFactsIncomplete || !complete || state.resultFactsIncomplete && !req.ResultFactsPrepared
		planned.ResultFactsPrepared = true
		planned.omitValueFacts = true
		// The caller's final application must use replacements even when
		// they are unchanged and the existing result can be returned.
		req.ResultFacts = cloneResultFacts(planned.ResultFacts)
		req.ResultFactsIncomplete = planned.ResultFactsIncomplete
		req.ResultFactsPrepared = planned.ResultFactsPrepared
		if matchingRecipe && resultFactsEqual(unionResultFacts(state.resultFacts), planned.ResultFacts) && state.resultFactsIncomplete == planned.ResultFactsIncomplete {
			return result, nil
		}
		planned.forceResultFactsAlias = true
	} else if req.ResultFactsPrepared && reused {
		// A clone carries the finalized replacement set without callbacks.
		// Its coverage flag is independent of whether that set is complete.
		planned.ResultFacts = unionResultFacts(cloneResultFacts(req.ResultFacts))
		if matchingRecipe && resultFactsEqual(unionResultFacts(state.resultFacts), planned.ResultFacts) && state.resultFactsIncomplete == planned.ResultFactsIncomplete {
			return result, nil
		}
		planned.omitValueFacts = true
		planned.forceResultFactsAlias = true
	} else {
		// A cache hit from another producer does not establish this call's
		// facts, even for the same function with equal input values. Without
		// a rebinder, only fully prepared current facts establish coverage.
		// A cold resolver's returned value is a real dependency; its facts
		// were already handled by initCompletedResult before this point.
		planned.omitValueFacts = true
		if !req.ResultFactsPrepared {
			planned.ResultFactsIncomplete = true
		}
	}
	oc := &ongoingCall{val: result, ttlSeconds: req.TTL}
	if err := c.initCompletedResult(ctx, resolver, oc, planned, session); err != nil {
		return nil, errors.Join(err, c.releaseOngoingCallHandoff(ctx, oc))
	}
	if err := c.trackSessionResult(ctx, session, Result[Typed]{shared: oc.res}, result.HitCache()); err != nil {
		return nil, errors.Join(err, c.releaseOngoingCallHandoff(ctx, oc))
	}
	if req.IsPersistable && !oc.res.persistDeclined {
		c.egraphMu.Lock()
		c.upsertPersistedEdgeLocked(ctx, oc.res, oc.persistedEdgeExpiresAtUnix, false)
		c.egraphMu.Unlock()
	}
	if err := c.releaseOngoingCallHandoff(ctx, oc); err != nil {
		return nil, err
	}
	if req.RebindResultFacts != nil {
		// The caller's final fact application must use the replacements too.
		req.ResultFacts = cloneResultFacts(planned.ResultFacts)
		req.ResultFactsIncomplete = planned.ResultFactsIncomplete
		req.ResultFactsPrepared = planned.ResultFactsPrepared
	}
	return wrapSharedResultWithResolver(ctx, oc.res, result.HitCache(), resolver)
}

func (c *Cache) aliasValueDependency(row *sharedResult) (*sharedResult, error) {
	state := row.loadPayloadState()
	if state.valueSource == nil {
		return nil, nil
	}
	if state.valueSource.id == 0 || state.valueSource == row {
		return nil, fmt.Errorf("call result has no attached value")
	}
	return state.valueSource, nil
}
