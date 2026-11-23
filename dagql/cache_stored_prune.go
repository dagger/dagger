package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/opencontainers/go-digest"
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
// ownership unit each. An engine's Cloud and image holdings own nothing. Requires
// egraphMu.
func (res *sharedResult) engineHoldingsLocked() int64 {
	return int64(res.engineHoldersLocked())
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
func (c *Cache) dropPrunedValuesLocked(ctx context.Context, change *RemoteChange, queue []*sharedResult) ([]*sharedResult, int, error) {
	var (
		collect []*sharedResult
		dropped int
		rerr    error
	)
	for len(queue) > 0 {
		res := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if c.resultsByID[res.id] != res || res.incomingOwnershipCount.Load() > res.engineHoldingsLocked() {
			continue
		}
		if res.hasOwnValueLocked() {
			deps, err := c.dropValueLocked(ctx, change, res)
			queue = append(queue, deps...)
			rerr = errors.Join(rerr, err)
			dropped++
		}
		if res.incomingOwnershipCount.Load() == 0 {
			collect = append(collect, res)
		}
	}
	return collect, dropped, rerr
}

// dropValueLocked takes res's value away and leaves it as an entry known only
// through holdings: no record, no stored parts and no dependencies, whose
// ownership it releases. It keeps its number, its identity in the e-graph and
// its holdings, whose executed marks it clears. It returns the released
// dependencies. Requires egraphMu for writing.
func (c *Cache) dropValueLocked(ctx context.Context, change *RemoteChange, res *sharedResult) ([]*sharedResult, error) {
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
	// The cache takes the value back only from a cache that computes it
	// after the drop.
	c.markEntryClassLocked(res.id, StampPruned)
	for _, ref := range res.holderRefsLocked() {
		if ref.h.executed {
			ref.h.executed = false
			c.markHoldingLocked(change, ref.key)
		}
	}
	res.deps = nil
	// The content digests spans taught the entry name the bytes of the
	// value the drop takes. A record that fills the entry again may be
	// another engine's run of the recipe, with other bytes, and until runs
	// are named the cache can't tell, so they go with the value.
	if len(res.taught) > 0 || res.taughtFrom.ok() {
		c.taughtContentDropped.Add(1)
		res.taughtDropped = true
	}
	res.taught = nil
	res.taughtGiven = 0
	res.taughtFrom = entryFrame{}
	// The frame the drop takes is no longer the one the worker is giving
	// digests to: what it records of it is dropped, as after a replacement
	// (applyTaughtContent).
	res.taughtFrame++
	c.changeStoredPartsLocked(res, func() { res.storedParts = nil })
	// The run goes with the value: a record that fills the entry again names
	// its own.
	res.run, res.runRan = nil, runNotRan
	c.setStoredRecordBytesLocked(res, 0)
	res.storedTransfer.Store(nil)
	res.dependencyOwnershipRevision++
	res.transferRevision++
	res.expiresAtUnix = 0
	res.payloadMu.Lock()
	res.imported = false
	res.sessionResourceHandle = ""
	res.boundSession = ""
	res.payloadMu.Unlock()
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
	res.stored = nil
	res.partFacts = nil
	res.createdAtUnixNano = 0
	res.payloadRevision++
	res.payloadMu.Unlock()
	c.setResultPayloadBytesLocked(res, 0)
	c.poolSettleLocked(res)
	return deps, rerr
}

// The pruners' measures on a blob-backed cache.
//
// The pool is every value in the closure of the stored roots: what a prune
// can free, and what persistence saves. The memory stage counts each of its
// values as its record's bytes plus an engine's estimate for an entry. The
// disk stage counts the distinct layer blobs its values' stored parts name,
// each at its descriptor's size: a blob shared by several values is counted
// once, and freed only when the last value that names it goes, as an engine
// counts a shared snapshot. Engines can describe one blob at different
// sizes, and the cache holds such layers apart (storedLayers), but the
// store holds the blob once: a value counts it at the largest size its parts
// give it, and the disk stage counts it once, at the largest size a value
// of the pool gives it, whichever the stage meets first. Neither depends on the e-graph's classes, which
// the Cloud compacts on collection (compactEqClassesAfterCollectionLocked),
// so a prune of a blob-backed cache leaves compaction to it.

// storedPoolLocked returns the pool of a blob-backed cache. Requires
// egraphMu.
func (c *Cache) storedPoolLocked() map[sharedResultID]*sharedResult {
	pool := make(map[sharedResultID]*sharedResult, len(c.persistedEdgesByResult))
	stack := make([]sharedResultID, 0, len(c.persistedEdgesByResult))
	for id := range c.persistedEdgesByResult {
		stack = append(stack, id)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := pool[id]; seen {
			continue
		}
		res := c.resultsByID[id]
		if res == nil || !res.hasOwnValueLocked() {
			continue
		}
		pool[id] = res
		for dep := range res.deps {
			stack = append(stack, dep)
		}
	}
	return pool
}

// storedValueBytes is what the memory stage counts for a value of a
// blob-backed cache. Requires egraphMu.
func (res *sharedResult) storedValueBytes() int64 {
	return res.storedRecordBytes + cacheMetadataResultEstimatedBytes
}

// poolEstimateLocked is the memory stage's measure of a blob-backed cache:
// its pool's values and their bytes, as the cache keeps them
// (cache_pool_measure.go). Requires egraphMu.
func (c *Cache) poolEstimateLocked() CacheMetadataEstimate {
	return CacheMetadataEstimate{ResultCount: c.pool.values, EstimatedBytes: c.pool.bytes}
}

// encodedRecordBytes is a record's size as persistence writes it: its call
// frame's JSON and its envelope's.
func encodedRecordBytes(rec PersistedRecord) int64 {
	frame, _ := json.Marshal(rec.Call)
	envelope, _ := json.Marshal(rec.Envelope)
	return int64(len(frame) + len(envelope))
}

// storedBlobsLocked returns the distinct layer blobs res's stored parts name,
// each at the largest size they give it. Requires egraphMu.
func (res *sharedResult) storedBlobsLocked() map[digest.Digest]int64 {
	if len(res.storedParts) == 0 {
		return nil
	}
	blobs := make(map[digest.Digest]int64)
	for _, part := range res.storedParts {
		for _, layer := range part.layers {
			blob, size := layer.layer.Descriptor.Digest, layer.layer.Descriptor.Size
			if seen, ok := blobs[blob]; !ok || size > seen {
				blobs[blob] = size
			}
		}
	}
	return blobs
}

// storedBlobUsage is the disk stage's measure of a blob-backed cache: each
// value's distinct blobs, as usage identities, their sizes, and the bytes of
// the distinct blobs the pool names.
type storedBlobUsage struct {
	byResult  map[sharedResultID][]string
	sizes     map[string]int64
	poolBytes int64
}

// storedBlobUsageLocked measures a blob-backed cache's blobs. Every value
// counts as a member of the blobs it names, in the pool or not, so a blob
// that a value outside the pool names is never simulated freed. A blob's
// size is the largest a value of the pool gives it, as the pool's kept
// measure counts it (cache_pool_measure.go), or, for a blob only values
// outside the pool name, the largest any gives it. Requires egraphMu.
func (c *Cache) storedBlobUsageLocked(checker *pruneCancellationChecker) (*storedBlobUsage, error) {
	usage := &storedBlobUsage{byResult: map[sharedResultID][]string{}, sizes: map[string]int64{}}
	pool := c.storedPoolLocked()
	inPool := map[string]bool{}
	for id, res := range c.resultsByID {
		if checker != nil {
			if err := checker.check(); err != nil {
				return nil, err
			}
		}
		blobs := res.storedBlobsLocked()
		if len(blobs) == 0 || !res.hasOwnValueLocked() {
			continue
		}
		_, member := pool[id]
		identities := make([]string, 0, len(blobs))
		for blob, size := range blobs {
			identity := blob.String()
			identities = append(identities, identity)
			switch seen, ok := usage.sizes[identity]; {
			case member && !inPool[identity]:
				inPool[identity] = true
				usage.sizes[identity] = size
			case member == inPool[identity] && (!ok || size > seen):
				usage.sizes[identity] = size
			}
		}
		slices.Sort(identities)
		usage.byResult[id] = identities
	}
	for identity := range inPool {
		usage.poolBytes += usage.sizes[identity]
	}
	return usage, nil
}
