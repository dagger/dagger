package dagql

import (
	"maps"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

// postingHeldForTest reports whether the postings hold anything for dig,
// an empty snapshot included.
func postingHeldForTest(p *digestPostings, dig string) bool {
	_, one := p.one[dig]
	_, rest := p.rest[dig]
	return one || rest
}

// postingsHeldForTest is how many digests the postings hold anything for,
// an empty snapshot included.
func postingsHeldForTest(p *digestPostings) int {
	return len(p.one) + len(p.rest)
}

// postingsResetForTest reports whether the postings are as a reset leaves
// them: holding nothing, not even an empty map.
func postingsResetForTest(p *digestPostings) bool {
	return p.one == nil && p.rest == nil
}

func TestCacheImportedDigestPostingsRemainLocalAfterMerge(t *testing.T) {
	f := cacheDerivedIndexWideOutputFixture(t, 4, true)
	defer f.close(t)
	c := f.cache
	c.egraphMu.Lock()
	roots := slices.Sorted(maps.Keys(c.outputEqClassResults))
	assert.Equal(t, 2, len(roots))
	first, second := roots[0], roots[1]
	digests := slices.Collect(c.eqClassToDigests[first].Items())
	assert.Assert(t, len(digests) > 1)
	fromSecond := slices.Collect(c.outputEqClassResults[second].Items())[0]
	before := make(map[string][]sharedResultID)
	for dig := range c.egraphResultsByDigest.Digests() {
		before[dig] = slices.Sorted(c.egraphResultsByDigest.Items(dig))
	}
	c.mergeEqClassesLocked(f.ctx, first, second)
	for dig, want := range before {
		assert.Assert(t, slices.Equal(slices.Sorted(c.egraphResultsByDigest.Items(dig)), want),
			"merging output classes changed the imported posting for %s", dig)
	}
	// Teaching one digest about a result must not teach its imported aliases.
	c.addResultDigestPostingLocked(fromSecond, digests[0])
	assert.Assert(t, c.egraphResultsByDigest.Contains(digests[0], fromSecond))
	assert.Assert(t, !c.egraphResultsByDigest.Contains(digests[1], fromSecond))
	// Repeated insertion must not duplicate a result already in the shared set.
	fromFirst := before[digests[0]][0]
	c.addResultDigestPostingLocked(fromFirst, digests[0])
	assert.Equal(t, len(before[digests[0]])+1, c.egraphResultsByDigest.Size(digests[0]))
	c.egraphMu.Unlock()

	_, removed, err := c.removePersistedEdge(withMetadataPruneContext(f.ctx), fromFirst)
	assert.NilError(t, err)
	assert.Assert(t, removed)
	c.egraphMu.Lock()
	for dig := range c.egraphResultsByDigest.Digests() {
		assert.Assert(t, !c.egraphResultsByDigest.Contains(dig, fromFirst), "collected result remains under %s", dig)
	}
	indexErr := cacheDerivedIndexesErrorLocked(c)
	c.egraphMu.Unlock()
	assert.NilError(t, indexErr)
}
