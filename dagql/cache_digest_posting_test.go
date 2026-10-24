package dagql

import (
	"maps"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

func TestCacheImportedDigestPostingsRemainLocalAfterMerge(t *testing.T) {
	f := cacheDerivedIndexWideOutputFixture(t, 4, true)
	defer f.close(t)
	c := f.cache
	c.egraphMu.Lock()
	roots := slices.Sorted(maps.Keys(c.outputEqClassResults))
	assert.Equal(t, 2, len(roots))
	first, second := roots[0], roots[1]
	digests := slices.Sorted(maps.Keys(c.eqClassToDigests[first]))
	assert.Assert(t, len(digests) > 1)
	fromSecond := slices.Sorted(maps.Keys(c.outputEqClassResults[second]))[0]
	before := make(map[string][]sharedResultID)
	for dig, posting := range c.egraphResultsByDigest {
		before[dig] = slices.Sorted(posting.Items())
	}
	c.mergeEqClassesLocked(f.ctx, first, second)
	for dig, want := range before {
		assert.Assert(t, slices.Equal(slices.Sorted(c.egraphResultsByDigest[dig].Items()), want),
			"merging output classes changed the imported posting for %s", dig)
	}
	// Teaching one digest about a result must not teach its imported aliases.
	c.addResultDigestPostingLocked(fromSecond, digests[0], resultDigestPostingExact)
	assert.Assert(t, c.egraphResultsByDigest[digests[0]].Contains(fromSecond))
	assert.Assert(t, !c.egraphResultsByDigest[digests[1]].Contains(fromSecond))
	// Repeated insertion must not duplicate a result already in the shared set.
	fromFirst := before[digests[0]][0]
	c.addResultDigestPostingLocked(fromFirst, digests[0], resultDigestPostingExact)
	assert.Equal(t, len(before[digests[0]])+1, c.egraphResultsByDigest[digests[0]].Size())
	c.egraphMu.Unlock()

	_, removed, err := c.removePersistedEdge(withMetadataPruneContext(f.ctx), fromFirst)
	assert.NilError(t, err)
	assert.Assert(t, removed)
	c.egraphMu.Lock()
	for dig, posting := range c.egraphResultsByDigest {
		assert.Assert(t, !posting.Contains(fromFirst), "collected result remains under %s", dig)
	}
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.NilError(t, indexErr)
}
