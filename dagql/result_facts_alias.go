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
	return req != nil && (len(req.ResultFacts) != 0 || req.ResultFactsIncomplete || req.ResultFactsPrepared)
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
	if !requestHasResultFacts(req) || result == nil || c.resultMatchesRequest(result.cacheSharedResult(), req) {
		return result, nil
	}
	planned := req.Clone()
	// A different operation's equal bytes do not establish read coverage.
	// Structural reuse of the same operation keeps its recorded body reads.
	ownSelf, _, ownErr := result.cacheSharedResult().loadResultCall().selfDigestAndInputRefs(c)
	reqSelf, _, reqErr := req.selfDigestAndInputRefs(c)
	planned.omitValueFacts = req.ResultFactsPrepared || ownErr != nil || reqErr != nil || ownSelf != reqSelf
	if planned.omitValueFacts && !req.ResultFactsPrepared {
		planned.ResultFactsIncomplete = true
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
