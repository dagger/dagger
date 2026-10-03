package dagql

import (
	"context"
	"errors"
)

// Pruning a blob-backed cache, the Cloud's, goes through holdings.
//
// Each holding is one ownership unit of its entry, so outside a prune a value
// stays as long as any engine holds its recipe. But every engine that
// connects receives every stored root through placement, and holds it
// retained. If holdings kept values in a prune, dropping a stored root would
// free nothing while any engine runs, and the planner, finding nothing freed,
// would go on to drop every root.
//
// So inside a prune, and only there, the cascade treats holdings as if they
// owned nothing: every value that only holdings still keep goes, with its
// record, its stored parts and its hold on its own dependencies. Its entry
// stays, with no value, while a holding owns it: an entry known only through
// holdings. The planner's simulation counts owners the same way. Outside a
// prune, ownership is unchanged: a merge can store a record that only its
// sender's holding keeps until a part that names it is stored.

// engineHoldingsLocked counts the holdings of engine caches on res: one
// ownership unit each. An engine's Cloud holding owns nothing. Requires
// egraphMu.
func (res *sharedResult) engineHoldingsLocked() int64 {
	var n int64
	for key := range res.holders {
		if key.Cache != cloudCacheID {
			n++
		}
	}
	return n
}

// hasOwnValueLocked reports whether res has a value of its own: decoded, or a
// stored record. Requires egraphMu.
func (res *sharedResult) hasOwnValueLocked() bool {
	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	return res.hasValue || res.persistedEnvelope != nil
}

// dropPrunedValuesLocked carries a prune's cascade through holdings on a
// blob-backed cache, from the entries whose ownership the prune has just
// lowered. A value that nothing but holdings keeps is dropped
// (dropValueLocked), and the dependencies it releases are examined in turn.
// It returns the entries left with no owner at all, for
// collectUnownedResultsLocked, and how many values it dropped. Requires
// egraphMu for writing.
func (c *Cache) dropPrunedValuesLocked(ctx context.Context, queue []*sharedResult) ([]*sharedResult, int, error) {
	var (
		collect []*sharedResult
		dropped int
		rerr    error
	)
	for len(queue) > 0 {
		res := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if c.resultsByID[res.id] != res || res.incomingOwnershipCount > res.engineHoldingsLocked() {
			continue
		}
		if res.hasOwnValueLocked() {
			deps, err := c.dropValueLocked(ctx, res)
			queue = append(queue, deps...)
			rerr = errors.Join(rerr, err)
			dropped++
		}
		if res.incomingOwnershipCount == 0 {
			collect = append(collect, res)
		}
	}
	return collect, dropped, rerr
}

// dropValueLocked takes res's value away and leaves it as an entry known only
// through holdings: no record, no stored parts and no dependencies, whose
// ownership it releases. It keeps its number, its identity in the e-graph and
// its holdings. It returns the released dependencies. Requires egraphMu for
// writing.
func (c *Cache) dropValueLocked(ctx context.Context, res *sharedResult) ([]*sharedResult, error) {
	var (
		deps []*sharedResult
		rerr error
	)
	for depID := range res.deps {
		c.forgetDependencyEdgeLocked(res.id, depID)
		dep := c.resultsByID[depID]
		if dep == nil {
			continue
		}
		_, err := c.decrementIncomingOwnershipLocked(ctx, dep, nil)
		rerr = errors.Join(rerr, err)
		deps = append(deps, dep)
	}
	res.deps = nil
	res.storedParts = nil
	res.dependencyOwnershipRevision++
	res.transferRevision++
	res.imported = false
	res.expiresAtUnix = 0
	res.sessionResourceHandle = ""
	if _, err := c.recomputeRequiredSessionResourcesLocked(res); err != nil {
		rerr = errors.Join(rerr, err)
	}
	res.storeResultCall(nil)
	res.payloadMu.Lock()
	res.self = nil
	res.isObject = false
	res.objClass = objectClassRef{}
	res.hasValue = false
	res.persistedEnvelope = nil
	res.createdAtUnixNano = 0
	res.payloadRevision++
	res.payloadMu.Unlock()
	c.setResultPayloadBytesLocked(res, 0)
	return deps, rerr
}
