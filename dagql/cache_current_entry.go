package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
)

// A cache has at most one current entry per recipe digest: the entry that
// entriesByRecipe names. A computed entry is indexed under the recipe of its
// stored call frame when it registers, and boot restore indexes the restored
// entries that were indexed when they were saved. When a computation finishes
// and its recipe already has a current entry, publication does not register a
// second one (initCompletedResult):
//   - a live entry is adopted, and the new value is released (D1), when the
//     publishing session covers the entry's session-resource requirements. A
//     late explicit dependency can raise those without changing the recipe;
//     a session that does not cover them registers its value beside the
//     current entry, not indexed, and its lookups hit that value afterwards;
//   - an entry whose dependency attachment is still open is waited on, then
//     decided again;
//   - an expired entry that nothing uses takes the new value in place: it
//     keeps its number and its retention edge, and its replacement count
//     goes up by one (D9);
//   - any other expired entry is retired: it leaves the index and keeps
//     serving its existing users only, and the new value registers as the
//     recipe's current entry.
//
// An entry known only through holdings, on a cache built from other caches'
// telemetry, has no value to adopt or expire: the new value is stored into it
// the same way, and its replacement count stays, since no value was replaced.
//
// "Nothing uses" an entry means no session, no other entry and no task holds
// it (resultInUseLocked). An entry that another entry depends on is retired,
// whether the dependent's value is decoded or not: a replacement reopens the
// entry's attachment, and a dependent decoded after the new value's
// attachment failed would fail on every hit.

// currentEntryForRecipeLocked returns the recipe's current entry, or nil.
// Requires egraphMu.
func (c *Cache) currentEntryForRecipeLocked(recipe digest.Digest) *sharedResult {
	id, ok := c.entriesByRecipe[recipe]
	if !ok {
		return nil
	}
	return c.resultsByID[id]
}

// resultInUseLocked reports whether anything uses res: a session that still
// records it, or any ownership unit besides its retention edge and other
// caches' holdings. An engine's Cloud holding is no unit: it owns nothing.
// Those units are other entries' dependency edges and offer owners,
// sessions, and the holds of tasks working on the entry: a publication's
// handoff, a part task or demand, a capture, a sharing pass. A lazy attempt
// or a decode runs for a caller that holds the entry through its session or
// through a dependent, so it holds a unit too. Requires egraphMu; nests
// sessionMu.
func (c *Cache) resultInUseLocked(res *sharedResult) bool {
	idle := int64(0)
	for key := range res.holders {
		if key.Cache != cloudCacheID {
			idle++
		}
	}
	if _, retained := c.persistedEdgesByResult[res.id]; retained {
		idle++
	}
	if res.incomingOwnershipCount != idle {
		return true
	}
	// Session release removes a session's ownership units before it deletes
	// its records; a recorded session still counts until then.
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	for _, resultIDs := range c.sessionResultIDsBySession {
		if _, recorded := resultIDs[res.id]; recorded {
			return true
		}
	}
	return false
}

// retireResultLocked takes res out of the recipe index. It stays registered
// and keeps serving the users it has; being expired, it is never a lookup
// candidate again. Requires egraphMu.
func (c *Cache) retireResultLocked(res *sharedResult) {
	c.unindexRecipesLocked(res)
}

// replacedValue is what an in-place replacement leaves for its publication
// to finish: the old value's dependencies, released once the new value's
// dependency edges are in place, and the old value's release, run after
// unlocking and before the new value's attachment (finishValueReplacement).
type replacedValue struct {
	deps    []sharedResultID
	release OnReleaseFunc
}

// replaceResultValueInPlaceLocked installs fresh's value into cur, an expired
// entry that nothing uses (D9), or one with no value. cur keeps its identity:
// its number, its recipe index key, its retention edge, its e-graph identity
// and other caches' holdings. It takes the new value, its call frame,
// requirements and accounting, and whether the value came from a record: a
// computed value clears imported, a merged record sets it with its envelope.
// It drops its offers, which belonged to the old value, and counts the
// replacement of a value. The new value's expiry is set by the caller, a
// publication or a merge; the retention edge's expiry restarts from the new
// value's, because the edge keeps the earlier of two expiries and the old one
// has passed. Its dependency edges are forgotten here; the caller decrements them
// after adding the new value's, so a dependency both values share is never
// collected in between. Requires egraphMu.
func (c *Cache) replaceResultValueInPlaceLocked(ctx context.Context, cur, fresh *sharedResult, retentionExpiresAtUnix int64) (replacedValue, collectionQueue, error) {
	var (
		queue collectionQueue
		rerr  error
	)
	// The stored parts, like the offers, belonged to the old value.
	cur.storedParts = nil
	cur.storedRecordBytes = fresh.storedRecordBytes
	for _, offer := range cur.partOffersLocked() {
		more, err := c.retirePartOfferLocked(ctx, cur, offer.record.Address)
		queue = append(queue, more...)
		rerr = errors.Join(rerr, err)
	}
	old := replacedValue{release: cur.onRelease}
	for depID := range cur.deps {
		c.forgetDependencyEdgeLocked(cur.id, depID)
		old.deps = append(old.deps, depID)
	}
	cur.deps = nil
	cur.dependencyOwnershipRevision++
	cur.transferRevision++
	if !cur.noValueLocked() {
		cur.replacements++
	}
	cur.imported = fresh.imported
	cur.inlineBorrow = fresh.inlineBorrow
	cur.sessionResourceHandle = fresh.sessionResourceHandle
	cur.requiredSessionResources = fresh.requiredSessionResources
	cur.expiresAtUnix = 0
	cur.recordType = fresh.recordType
	cur.description = fresh.description
	cur.storeResultCall(fresh.loadResultCall())

	cur.payloadMu.Lock()
	cur.self = fresh.self
	cur.isObject = fresh.isObject
	cur.objClass = fresh.objClass
	// A nil new value leaves hasValue false, as its fresh publication would.
	cur.hasValue = fresh.hasValue
	cur.persistedEnvelope = fresh.persistedEnvelope
	cur.payloadRevision++
	// fresh was never registered, so its own lease cleanup does nothing.
	cur.onRelease = joinOnRelease(c.resultSnapshotLeaseCleanup(cur), fresh.onRelease)
	cur.createdAtUnixNano = fresh.createdAtUnixNano
	cur.lastUsedAtUnixNano = fresh.lastUsedAtUnixNano
	cur.cacheUsageSizeByIdentity = nil
	cur.cacheUsageRecordTypeByID = nil
	cur.payloadMu.Unlock()

	if edge, retained := c.persistedEdgesByResult[cur.id]; retained && !edge.unpruneable {
		edge.expiresAtUnix = retentionExpiresAtUnix
		c.persistedEdgesByResult[cur.id] = edge
	}
	return old, queue, rerr
}

// finishValueReplacement releases the value res held before an in-place
// replacement, and resets the per-value state the new value starts without:
// its snapshot links, completed parts, part gate, lazy evaluation and decode
// state. It runs after the replacement's critical section and before the new
// value's attachment and snapshot-lease sync: the old release removes the
// leases the old links name, which share the entry's lease IDs, and the sync
// then attaches the new value's leases afresh. Nothing used the old value, and
// the entry's open attachment keeps new readers waiting until the new value
// is in place.
func (c *Cache) finishValueReplacement(ctx context.Context, res *sharedResult, release OnReleaseFunc) error {
	err := runOnReleaseFuncs(ctx, []OnReleaseFunc{release})

	res.payloadMu.Lock()
	res.snapshotOwnerLinks = nil
	res.snapshotLinkIntent = nil
	res.snapshotLeaseCleanupRoles = nil
	res.payloadMu.Unlock()
	res.completeParts.Store(nil)
	res.partGate.gate.Store(nil)
	res.partGate.active.Store(false)
	res.partGate.restoredDelegation.Store(false)

	res.lazyMu.Lock()
	res.lazyWhole = lazyGroupState{}
	res.lazyPartGroups = nil
	res.lazyEvalComplete = false
	res.lazyMu.Unlock()

	res.persistDecodeMu.Lock()
	res.persistDecodeErr = nil
	res.persistDecodeRetry = false
	res.persistLeaseSyncPending = false
	res.persistDecodeMu.Unlock()
	return err
}

// releaseReplacedDependenciesLocked drops the ownership the replaced value's
// dependency edges held and returns the entries left to collect. The caller
// runs it once the new value's edges are in place. Requires egraphMu.
func (c *Cache) releaseReplacedDependenciesLocked(ctx context.Context, replaced replacedValue, queue collectionQueue) (collectionQueue, error) {
	var rerr error
	for _, depID := range replaced.deps {
		dep := c.resultsByID[depID]
		if dep == nil {
			continue
		}
		var err error
		queue, err = c.decrementIncomingOwnershipLocked(ctx, dep, queue)
		rerr = errors.Join(rerr, err)
	}
	return queue, rerr
}

// indexRestoredEntries indexes, under the recipe of its stored frame, every
// restored entry that its recipe's index named when it was saved, before any
// caller is admitted. The others are restored with their numbers and edges
// and keep serving: a retired entry, for what depends on it, and an entry a
// session published beside its recipe's current entry, for that session's
// lookups. Deriving a recipe reads other entries' frames under egraphMu, so
// the digests are derived first and indexed in one critical section.
func (c *Cache) indexRestoredEntries(indexed map[sharedResultID]struct{}) error {
	c.egraphMu.RLock()
	rows := make([]*sharedResult, 0, len(indexed))
	for id := range indexed {
		if res := c.resultsByID[id]; res != nil {
			rows = append(rows, res)
		}
	}
	c.egraphMu.RUnlock()
	slices.SortFunc(rows, compareSharedResults)
	recipes := make([]digest.Digest, len(rows))
	for i, res := range rows {
		frame := res.loadResultCall()
		if frame == nil {
			return fmt.Errorf("index restored result %d: missing call frame", res.id)
		}
		recipe, err := frame.deriveRecipeDigest(c)
		if err != nil {
			return fmt.Errorf("index restored result %d: %w", res.id, err)
		}
		recipes[i] = recipe
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for i, res := range rows {
		c.indexRecipeLocked(recipes[i], res)
	}
	return nil
}
