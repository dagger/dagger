package dagql

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// An entry that is the output of one class, which knows one content digest,
// holds that in its indexes' slots: 2000 entries, each the output of a class
// of its own with a content digest, hold under 256 bytes an entry between
// the entry's classes, the class's entries and the class's extra digests.
// With a set of its own for each entry's classes and each class's entries,
// and a Go map for each class's extra digests, they held about 600.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestAnEntrysOneClassHoldsItsSlotsAlone(t *testing.T) {
	const entries = 2000
	ctx := t.Context()
	c := &Cache{}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	classes := make([]eqClassID, entries)
	frames := make([]*ResultCall, entries)
	for i := range entries {
		classes[i] = c.ensureEqClassForDigestLocked(ctx, fmt.Sprint("class-output-", i))
		frames[i] = &ResultCall{ExtraDigests: []call.ExtraDigest{{
			Digest: digest.FromString(fmt.Sprint("class-content-", i)),
			Label:  call.ExtraDigestLabelContent,
		}}}
	}
	before := storedLayersTestLiveHeap()
	for i := range entries {
		id := sharedResultID(i + 1)
		c.addResultOutputEqClassLocked(id, classes[i])
		c.addResultExtraDigestsLocked(id, frames[i])
	}
	held := storedLayersTestLiveHeap() - before
	runtime.KeepAlive(frames)
	for i := range entries {
		_, ok := c.outputEqClassesForResultLocked(sharedResultID(i + 1))[classes[i]]
		require.True(t, ok, "entry %d is not an output of its class", i+1)
		require.Len(t, c.eqClassExtraDigests[classes[i]], 1, "class %d's extra digests", classes[i])
	}
	perEntry := float64(held) / entries
	t.Logf("%d entries of a class each, with a content digest, held %d bytes, %.0f an entry", entries, held, perEntry)
	require.Less(t, perEntry, 256.0, "each entry held %.0f bytes", perEntry)
}
