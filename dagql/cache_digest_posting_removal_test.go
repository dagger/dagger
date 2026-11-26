package dagql

import (
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

func TestRestoredDigestPostingRemovalKeepsRuntimePostingsAfterMerge(t *testing.T) {
	f := cacheDerivedIndexWideOutputFixture(t, 4, true)
	defer f.close(t)
	c := f.cache
	c.egraphMu.Lock()
	roots := slices.Sorted(c.outputEqClassResults.Keys())
	assert.Equal(t, 2, len(roots))
	first, second := roots[0], roots[1]
	survivor := slices.Collect(c.outputEqClassResults.Items(first))[0]
	removed := slices.Collect(c.outputEqClassResults.Items(second))
	digests := slices.Collect(c.eqClassToDigests[second].Items())
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
	assert.DeepEqual(t, slices.Sorted(c.egraphResultsByDigest.Items(digests[0])), []sharedResultID{survivor})
	for _, dig := range digests[1:] {
		assert.Assert(t, !postingHeldForTest(&c.egraphResultsByDigest, dig), "empty imported posting remains under %s", dig)
	}
	assert.NilError(t, cacheDerivedIndexesErrorLocked(c))
}

func TestRestoredDigestPostingRemovalAcrossOriginalGroups(t *testing.T) {
	c := &Cache{}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	c.egraphParents = []eqClassID{0, 1, 2}
	c.eqClassToDigests[1] = idSetOf("first")
	c.eqClassToDigests[2] = idSetOf("second")
	c.outputEqClassResults.Insert(1, 1)
	c.outputEqClassResults.Insert(1, 2)
	c.outputEqClassResults.Insert(2, 1)
	c.outputEqClassResults.Insert(2, 3)
	c.restoreDigestPostingsLocked()
	c.removeResultDigestsLocked(1, map[eqClassID]struct{}{1: {}, 2: {}})
	assert.DeepEqual(t, slices.Sorted(c.egraphResultsByDigest.Items("first")), []sharedResultID{2})
	assert.DeepEqual(t, slices.Sorted(c.egraphResultsByDigest.Items("second")), []sharedResultID{3})
	c.removeResultDigestsLocked(2, map[eqClassID]struct{}{1: {}})
	assert.Assert(t, !postingHeldForTest(&c.egraphResultsByDigest, "first"))
	assert.Assert(t, c.egraphResultsByDigest.Contains("second", 3))
	c.removeResultDigestsLocked(3, map[eqClassID]struct{}{2: {}})
	assert.Equal(t, 0, postingsHeldForTest(&c.egraphResultsByDigest))
}
