package dagql

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A link between a term and the entry seen for it holds only its two index
// slots: linking 2000 terms to an entry each holds under 160 bytes a link.
// With a Go map for each term's entries and each entry's terms, and a copy of
// the term's input provenance on each link, a link held about 500 bytes.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestATermEntryLinkHoldsItsSlotsAlone(t *testing.T) {
	const links = 2000
	ctx := t.Context()
	c := &Cache{}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	entries := make([]*sharedResult, links)
	terms := make([]egraphTermID, links)
	provenance := []egraphInputProvenanceKind{egraphInputProvenanceKindDigest}
	for i := range links {
		out := c.ensureEqClassForDigestLocked(ctx, fmt.Sprint("link-output-", i))
		termID := c.nextEgraphTermID
		c.nextEgraphTermID++
		c.egraphTerms[termID] = newEgraphTerm(termID, digest.FromString(fmt.Sprint("link-self-", i)), nil, out)
		// The term's own provenance and the entry's class are there before
		// the link, so that the link is all that is measured.
		c.termInputProvenance[termID] = provenance
		res := &sharedResult{id: sharedResultID(i + 1)}
		c.addResultOutputEqClassLocked(res.id, out)
		entries[i], terms[i] = res, termID
	}
	before := storedLayersTestLiveHeap()
	for i := range links {
		c.associateResultWithTermLocked(ctx, entries[i], terms[i], provenance)
	}
	held := storedLayersTestLiveHeap() - before
	runtime.KeepAlive(entries)
	for i := range links {
		_, linked := c.termIDsForResultLocked(entries[i].id)[terms[i]]
		require.True(t, linked, "entry %d is not linked to its term", entries[i].id)
	}
	perLink := float64(held) / links
	t.Logf("linking %d terms to an entry each held %d bytes, %.0f a link", links, held, perLink)
	require.Less(t, perLink, 160.0, "each link held %.0f bytes", perLink)
}

// A digest posted for one entry holds only its slot in the postings, and
// its place in the entry's list of posted digests: posting 2000 digests for
// one entry holds under 80 bytes a digest. With a posting and a set of its
// own for each digest, it held about 110.
//
// Not parallel: it measures the process's live heap.
func TestADigestPostedForOneEntryHoldsItsSlotAlone(t *testing.T) {
	const digests = 2000
	const entry = sharedResultID(1)
	c := &Cache{}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	names := make([]string, digests)
	for i := range digests {
		names[i] = digest.FromString(fmt.Sprint("posted-", i)).String()
	}
	before := storedLayersTestLiveHeap()
	for _, dig := range names {
		c.addResultDigestPostingLocked(entry, dig)
	}
	held := storedLayersTestLiveHeap() - before
	runtime.KeepAlive(names)
	require.Equal(t, names, c.resultIndexedDigests[entry], "every digest is posted for the entry once")
	perDigest := float64(held) / digests
	t.Logf("posting %d digests for one entry held %d bytes, %.0f a digest", digests, held, perDigest)
	require.Less(t, perDigest, 80.0, "each digest held %.0f bytes", perDigest)
}
