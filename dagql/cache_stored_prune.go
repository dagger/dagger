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
	res.storedRecordBytes = 0
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

// The pruners' measures on a blob-backed cache.
//
// The pool is every value in the closure of the stored roots: what a prune
// can free, and what persistence saves. The memory stage counts each of its
// values as its record's bytes plus an engine's estimate for an entry. The
// disk stage counts the distinct layer blobs its values' stored parts name,
// each at its descriptor's size: a blob shared by several values is counted
// once, and freed only when the last value that names it goes, as an engine
// counts a shared snapshot. Neither depends on the e-graph's classes, which
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
// its pool's values and their bytes. Requires egraphMu.
func (c *Cache) poolEstimateLocked() CacheMetadataEstimate {
	pool := c.storedPoolLocked()
	estimate := CacheMetadataEstimate{ResultCount: len(pool)}
	for _, res := range pool {
		estimate.EstimatedBytes += res.storedValueBytes()
	}
	return estimate
}

// encodedRecordBytes is a record's size as persistence writes it: its call
// frame's JSON and its envelope's.
func encodedRecordBytes(rec PersistedRecord) int64 {
	frame, _ := json.Marshal(rec.Call)
	envelope, _ := json.Marshal(rec.Envelope)
	return int64(len(frame) + len(envelope))
}

// storedBlobsLocked returns the distinct layer blobs res's stored parts name,
// with their sizes. Requires egraphMu.
func (res *sharedResult) storedBlobsLocked() map[digest.Digest]int64 {
	if len(res.storedParts) == 0 {
		return nil
	}
	blobs := make(map[digest.Digest]int64)
	for _, part := range res.storedParts {
		for _, layer := range part.Chain.Layers {
			blobs[layer.Descriptor.Digest] = layer.Descriptor.Size
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
// that a value outside the pool names is never simulated freed. Requires
// egraphMu.
func (c *Cache) storedBlobUsageLocked(checker *pruneCancellationChecker) (*storedBlobUsage, error) {
	usage := &storedBlobUsage{byResult: map[sharedResultID][]string{}, sizes: map[string]int64{}}
	pool := c.storedPoolLocked()
	counted := map[string]bool{}
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
		_, inPool := pool[id]
		identities := make([]string, 0, len(blobs))
		for blob, size := range blobs {
			identity := blob.String()
			identities = append(identities, identity)
			usage.sizes[identity] = size
			if inPool && !counted[identity] {
				counted[identity] = true
				usage.poolBytes += size
			}
		}
		slices.Sort(identities)
		usage.byResult[id] = identities
	}
	return usage, nil
}
