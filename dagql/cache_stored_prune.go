package dagql

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

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
	var n int64
	for _, ref := range res.holderRefsLocked() {
		if !senderCache(ref.key.Cache) {
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
		if c.resultsByID[res.id] != res || res.incomingOwnershipCount.Load() > res.engineHoldingsLocked() {
			continue
		}
		if res.hasOwnValueLocked() {
			deps, err := c.dropValueLocked(ctx, res)
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
func (c *Cache) dropValueLocked(ctx context.Context, res *sharedResult) ([]*sharedResult, error) {
	var (
		deps []*sharedResult
		rerr error
	)
	for _, depID := range res.deps {
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
		if ref.h.is(heldExecuted) {
			ref.h.setFlag(heldExecuted, false)
			c.markHoldingLocked(ref.key)
		}
	}
	res.deps = nil
	// The content digests spans taught the entry name the bytes of the
	// value the drop takes. A record that fills the entry again may be
	// another engine's run of the recipe, with other bytes, and until runs
	// are named the cache can't tell, so they go with the value.
	if t := res.taughtContent; t != nil {
		if len(t.digests) > 0 || t.from.ok() {
			c.taughtContentDropped.Add(1)
			res.taughtDropped = true
		}
		t.digests = nil
		t.given = 0
		t.from = entryFrame{}
		// The frame the drop takes is no longer the one the worker is
		// giving digests to: what it records of it is dropped, as after a
		// replacement (applyTaughtContent).
		t.frame++
	}
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
// of the pool gives it, whichever the stage meets first. Neither depends on
// the e-graph's classes.

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
		for _, dep := range res.deps {
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
	_, size := encodedRecord(rec)
	return size
}

// encodedRecord returns rec's envelope's JSON, nil if it can't be encoded,
// and rec's size as encodedRecordBytes measures it.
func encodedRecord(rec PersistedRecord) ([]byte, int64) {
	frame, _ := json.Marshal(rec.Call)
	envelope, err := json.Marshal(rec.Envelope)
	if err != nil {
		envelope = nil
	}
	return envelope, int64(len(frame) + len(envelope))
}

// storedBlob is a layer blob a stored part names, at the size it gives it.
type storedBlob struct {
	digest digest.Digest
	size   int64
}

// storedBlobsLocked returns the distinct layer blobs res's stored parts name,
// each at the largest size they give it, sorted by digest, in blobs, which it
// empties first: a walk of many entries passes the same slice for each.
// Requires egraphMu.
func (res *sharedResult) storedBlobsLocked(blobs []storedBlob) []storedBlob {
	blobs = blobs[:0]
	for _, part := range res.storedParts {
		for _, layer := range part.layers {
			blobs = append(blobs, storedBlob{digest: layer.layer.Descriptor.Digest, size: layer.layer.Descriptor.Size})
		}
	}
	if len(blobs) < 2 {
		return blobs
	}
	// Each blob's largest size first, which compacting keeps.
	slices.SortFunc(blobs, func(a, b storedBlob) int {
		return cmp.Or(strings.Compare(string(a.digest), string(b.digest)), cmp.Compare(b.size, a.size))
	})
	return slices.CompactFunc(blobs, func(a, b storedBlob) bool { return a.digest == b.digest })
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
	var blobs []storedBlob
	for id, res := range c.resultsByID {
		if checker != nil {
			if err := checker.check(); err != nil {
				return nil, err
			}
		}
		blobs = res.storedBlobsLocked(blobs)
		if len(blobs) == 0 || !res.hasOwnValueLocked() {
			continue
		}
		_, member := pool[id]
		identities := make([]string, 0, len(blobs))
		for _, blob := range blobs {
			identity, size := blob.digest.String(), blob.size
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
