package dagql

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/opencontainers/go-digest"
)

// A blob-backed cache's pool, kept.
//
// The pool is every value in the closure of the stored roots
// (storedPoolLocked): what a prune can free, and what persistence saves. A
// prune's two stages measure it, the memory stage by its values' bytes and
// the disk stage by the distinct layer blobs its values' stored parts name
// (cache_stored_prune.go). The cache keeps both measures as it changes, as
// an engine's cache keeps the estimate its own memory stage reads
// (cacheMetadataEstimateLocked), so that reading them walks nothing.
//
// An entry is in the pool when it has a value of its own and its poolRefs
// is above 0: one for its retention edge, and one for each entry in the pool
// that depends on it. Every write of what that reads keeps it current:
// retention edges (poolRootLocked), dependency edges
// (rememberDependencyEdgeLocked, forgetDependencyEdgeLocked), an entry
// gaining or losing its value or leaving the cache (poolSettleLocked), its
// record's size (setStoredRecordBytesLocked) and its stored parts
// (changeStoredPartsLocked). A restore counts it once from the walk
// (poolRecountLocked). The counts rely on the dependency graph having no
// cycle, as the cache's collection, a reference count over the same edges,
// already does. VerifyPoolMeasure checks the kept measure against the walk.

// testCacheClosing, nil but in tests, runs as a cache closes: the package's
// tests check there that a blob-backed cache's kept measure of its pool
// equals a walk (VerifyPoolMeasure).
var testCacheClosing func(*Cache)

// poolMeasure is a blob-backed cache's kept measure of its pool. Guarded by
// egraphMu.
type poolMeasure struct {
	// values and bytes are the pool's values and their bytes, each counted
	// as storedValueBytes counts it.
	values int
	bytes  int64
	// blobs counts, for each distinct layer blob the pool's stored parts
	// name, the entries of the pool whose parts name it, with the blob's
	// size; blobBytes is the sum of their sizes.
	blobs     map[digest.Digest]*poolBlob
	blobBytes int64
}

// poolBlob is one blob of the pool's kept measure: how many of the pool's
// entries name it, and its size.
type poolBlob struct {
	refs int32
	size int64
}

// poolSettleLocked brings res's membership of the pool up to date, and that
// of every entry a change of it reaches through dependencies: an entry
// entering the pool counts its bytes and blobs and holds each of its
// dependencies, and one leaving undoes it. Requires egraphMu for writing.
func (c *Cache) poolSettleLocked(res *sharedResult) {
	if !c.blobBacked || res == nil {
		return
	}
	queue := []*sharedResult{res}
	for len(queue) > 0 {
		res := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		in := res.poolRefs > 0 && c.resultsByID[res.id] == res && res.hasOwnValueLocked()
		if in == res.inPool {
			continue
		}
		res.inPool = in
		sign := int64(1)
		if !in {
			sign = -1
		}
		c.pool.values += int(sign)
		c.pool.bytes += sign * res.storedValueBytes()
		c.poolBlobsLocked(res.storedBlobsLocked(), sign)
		for depID := range res.deps {
			if dep := c.resultsByID[depID]; dep != nil {
				dep.poolRefs += int32(sign)
				queue = append(queue, dep)
			}
		}
	}
}

// poolBlobsLocked counts blobs, an entry's distinct blobs, into the pool's
// blobs (sign 1) or out of them (sign -1). Requires egraphMu for writing.
func (c *Cache) poolBlobsLocked(blobs map[digest.Digest]int64, sign int64) {
	for blob, size := range blobs {
		b := c.pool.blobs[blob]
		if sign > 0 {
			if b == nil {
				if c.pool.blobs == nil {
					c.pool.blobs = map[digest.Digest]*poolBlob{}
				}
				b = &poolBlob{size: size}
				c.pool.blobs[blob] = b
				c.pool.blobBytes += size
			}
			b.refs++
			continue
		}
		// A blob the pool doesn't count can't be taken out of it: the kept
		// measure has drifted, which VerifyPoolMeasure reports.
		if b == nil {
			continue
		}
		if b.refs--; b.refs == 0 {
			c.pool.blobBytes -= b.size
			delete(c.pool.blobs, blob)
		}
	}
}

// poolRootLocked counts a retention edge of res added (sign 1) or removed
// (sign -1) toward its place in the pool. Requires egraphMu for writing.
func (c *Cache) poolRootLocked(res *sharedResult, sign int32) {
	if !c.blobBacked || res == nil {
		return
	}
	res.poolRefs += sign
	c.poolSettleLocked(res)
}

// poolDependencyLocked counts a dependency edge from parent to dep added
// (sign 1) or removed (sign -1): an entry of the pool holds each of its
// dependencies there. Requires egraphMu for writing.
func (c *Cache) poolDependencyLocked(parent, dep *sharedResult, sign int32) {
	if !c.blobBacked || parent == nil || dep == nil || !parent.inPool {
		return
	}
	dep.poolRefs += sign
	c.poolSettleLocked(dep)
}

// setStoredRecordBytesLocked sets the size of res's stored record, keeping
// the pool's bytes. Requires egraphMu for writing.
func (c *Cache) setStoredRecordBytesLocked(res *sharedResult, n int64) {
	if res.inPool {
		c.pool.bytes += n - res.storedRecordBytes
	}
	res.storedRecordBytes = n
}

// changeStoredPartsLocked runs change, a write of res's stored parts, keeping
// the pool's blobs. Requires egraphMu for writing.
func (c *Cache) changeStoredPartsLocked(res *sharedResult, change func()) {
	if !res.inPool {
		change()
		return
	}
	c.poolBlobsLocked(res.storedBlobsLocked(), -1)
	change()
	c.poolBlobsLocked(res.storedBlobsLocked(), 1)
}

// poolRecountLocked counts the pool's kept measure afresh from the walk, as
// a restore, which writes every entry at once, leaves it. Requires egraphMu
// for writing.
func (c *Cache) poolRecountLocked() {
	if !c.blobBacked {
		return
	}
	walked := c.walkPoolLocked()
	c.pool = walked.measure
	for id, res := range c.resultsByID {
		_, res.inPool = walked.members[id]
		res.poolRefs = walked.refs[id]
	}
}

// walkedPool is the pool as a walk finds it: its measure, its members, and
// what holds each entry there.
type walkedPool struct {
	measure poolMeasure
	members map[sharedResultID]*sharedResult
	refs    map[sharedResultID]int32
}

// walkPoolLocked measures the pool by walking it from the stored roots
// (storedPoolLocked), and counts what holds each entry there. It is what
// the kept measure must equal. Requires egraphMu.
func (c *Cache) walkPoolLocked() walkedPool {
	w := walkedPool{members: c.storedPoolLocked(), refs: map[sharedResultID]int32{}}
	for id := range c.persistedEdgesByResult {
		if c.resultsByID[id] != nil {
			w.refs[id]++
		}
	}
	for _, res := range w.members {
		w.measure.values++
		w.measure.bytes += res.storedValueBytes()
		for blob, size := range res.storedBlobsLocked() {
			b := w.measure.blobs[blob]
			if b == nil {
				if w.measure.blobs == nil {
					w.measure.blobs = map[digest.Digest]*poolBlob{}
				}
				b = &poolBlob{size: size}
				w.measure.blobs[blob] = b
				w.measure.blobBytes += size
			}
			b.refs++
		}
		for depID := range res.deps {
			if c.resultsByID[depID] != nil {
				w.refs[depID]++
			}
		}
	}
	return w
}

// VerifyPoolMeasure checks a blob-backed cache's kept measure of its pool
// against a walk of it, under one hold of the graph lock for reading: the
// pool's values, bytes and blobs, and each entry's membership and what
// holds it there. It returns nil when they agree, and otherwise what
// differs, the first few entries named. The walk is what the kept measure
// replaces, so it costs what one did: tests call it, nothing else.
func (c *Cache) VerifyPoolMeasure() error {
	if !c.blobBacked {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	walked := c.walkPoolLocked()
	var errs []error
	kept := c.pool
	if kept.values != walked.measure.values || kept.bytes != walked.measure.bytes {
		errs = append(errs, fmt.Errorf("pool: kept %d values, %d bytes; walked %d values, %d bytes",
			kept.values, kept.bytes, walked.measure.values, walked.measure.bytes))
	}
	if kept.blobBytes != walked.measure.blobBytes || len(kept.blobs) != len(walked.measure.blobs) {
		errs = append(errs, fmt.Errorf("pool blobs: kept %d blobs, %d bytes; walked %d blobs, %d bytes",
			len(kept.blobs), kept.blobBytes, len(walked.measure.blobs), walked.measure.blobBytes))
	}
	for blob, b := range walked.measure.blobs {
		if k := kept.blobs[blob]; k == nil || k.refs != b.refs || k.size != b.size {
			errs = append(errs, fmt.Errorf("pool blob %s: kept %+v, walked %+v", blob, k, *b))
			break
		}
	}
	var named int
	for _, id := range slices.Sorted(maps.Keys(c.resultsByID)) {
		res := c.resultsByID[id]
		_, member := walked.members[id]
		if res.inPool == member && res.poolRefs == walked.refs[id] {
			continue
		}
		errs = append(errs, fmt.Errorf("entry %d: kept in pool %t held by %d; walked in pool %t held by %d",
			id, res.inPool, res.poolRefs, member, walked.refs[id]))
		if named++; named == 5 {
			break
		}
	}
	return errors.Join(errs...)
}
