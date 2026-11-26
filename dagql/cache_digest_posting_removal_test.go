package dagql

import (
	"maps"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

func TestRestoredDigestPostingRemovalKeepsRuntimePostingsAfterMerge(t *testing.T) {
	f := cacheDerivedIndexWideOutputFixture(t, 4, true)
	defer f.close(t)
	c := f.cache
	c.egraphMu.Lock()
	roots := slices.Sorted(maps.Keys(c.outputEqClassResults))
	assert.Equal(t, 2, len(roots))
	first, second := roots[0], roots[1]
	survivor := slices.Sorted(maps.Keys(c.outputEqClassResults[first]))[0]
	removed := slices.Sorted(maps.Keys(c.outputEqClassResults[second]))
	digests := slices.Sorted(maps.Keys(c.eqClassToDigests[second]))
	assert.Assert(t, len(digests) > 1)
	c.mergeEqClassesLocked(f.ctx, first, second)
	// A new observation applies only to this digest, even after the classes merge.
	c.addResultDigestPostingLocked(survivor, digests[0])
	c.egraphMu.Unlock()

	for _, id := range removed {
		_, didRemove, err := c.removePersistedEdge(withMetadataPruneContext(f.ctx), id)
		assert.NilError(t, err)
		assert.Assert(t, didRemove)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	assert.DeepEqual(t, slices.Sorted(c.egraphResultsByDigest[digests[0]].Items()), []sharedResultID{survivor})
	for _, dig := range digests[1:] {
		assert.Assert(t, c.egraphResultsByDigest[dig] == nil, "empty imported posting remains under %s", dig)
	}
	assert.NilError(t, cacheDerivedIndexesErrorLocked(c))
}

func TestRestoredDigestPostingRemovalAcrossOriginalGroups(t *testing.T) {
	c := &Cache{}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	c.egraphParents = []eqClassID{0, 1, 2}
	c.eqClassToDigests[1] = map[string]struct{}{"first": {}}
	c.eqClassToDigests[2] = map[string]struct{}{"second": {}}
	c.outputEqClassResults[1] = map[sharedResultID]struct{}{1: {}, 2: {}}
	c.outputEqClassResults[2] = map[sharedResultID]struct{}{1: {}, 3: {}}
	c.restoreDigestPostingsLocked()
	c.removeResultDigestsLocked(1, map[eqClassID]struct{}{1: {}, 2: {}})
	assert.DeepEqual(t, slices.Sorted(c.egraphResultsByDigest["first"].Items()), []sharedResultID{2})
	assert.DeepEqual(t, slices.Sorted(c.egraphResultsByDigest["second"].Items()), []sharedResultID{3})
	c.removeResultDigestsLocked(2, map[eqClassID]struct{}{1: {}})
	assert.Assert(t, c.egraphResultsByDigest["first"] == nil)
	assert.Assert(t, c.egraphResultsByDigest["second"].Contains(3))
	c.removeResultDigestsLocked(3, map[eqClassID]struct{}{2: {}})
	assert.Equal(t, 0, len(c.egraphResultsByDigest))
}
