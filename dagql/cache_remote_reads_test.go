package dagql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// remoteReadOps are the reads a RemoteReads answers in its hold, as the Cache
// answers them in holds of their own.
type remoteReadOps interface {
	EquivalentEntries(string) []uint64
	TermEntries(digest.Digest, []digest.Digest) []uint64
	EntriesHeld([]uint64, int) []EntryHeld
	CacheHoldings(CacheID, []uint64) []RemoteHeldEntry
	EntryInfo(uint64) (RemoteEntryInfo, bool)
}

var (
	_ remoteReadOps = (*Cache)(nil)
	_ remoteReadOps = (*RemoteReads)(nil)
)

// remoteReadsScript reads, through ops, the classes of the remote writes
// script's recipes, of its content digest and of a digest the cache doesn't
// know, the entries its terms match, and the facts, holdings and
// descriptions of every entry it made.
func remoteReadsScript(ops remoteReadOps) []string {
	var out []string
	note := func(name string, v ...any) { out = append(out, fmt.Sprintf("%s: %+v", name, v)) }
	var numbers []uint64
	for n := range uint64(12) {
		numbers = append(numbers, n)
	}
	for i := range 8 {
		field := fmt.Sprintf("writes-%d", i)
		note("class "+field, ops.EquivalentEntries(testDigest(field).String()))
		note("term "+field, ops.TermEntries(testDigest(field+"-self"), nil))
	}
	note("class content", ops.EquivalentEntries(testDigest("writes-content").String()))
	note("class unknown", ops.EquivalentEntries(testDigest("reads-unknown").String()))
	note("term unknown input", ops.TermEntries(testDigest("writes-1-self"), []digest.Digest{testDigest("reads-unknown")}))
	note("term no self", ops.TermEntries("", nil))
	note("held", ops.EntriesHeld(numbers, 0))
	note("held with keys", ops.EntriesHeld(numbers, 2))
	for _, cache := range []CacheID{"cache-a", "cache-b", "cache-c", "cache-d"} {
		note(fmt.Sprintf("holdings %s", cache), ops.CacheHoldings(cache, numbers))
	}
	for _, n := range numbers {
		info, ok := ops.EntryInfo(n)
		note(fmt.Sprintf("info %d", n), info, ok)
	}
	return out
}

// A run of reads in one hold answers as the same reads do through the Cache
// one by one, on a cache the remote writes script filled: classes joined by
// content digest and by term, holdings of several engine caches, merged
// replies and collections.
func TestRemoteReadsAnswerAsTheCacheDoes(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	remoteWritesScript(t, ctx, c)

	want := remoteReadsScript(c)
	r := c.HoldRemoteReads()
	got := remoteReadsScript(r)
	r.Release()
	require.Equal(t, want, got)
}

// A hold answers every read at one moment: a writer that waits for it
// changes nothing the hold reads until it is released, even across a batch
// of entries, where the Cache's own EntriesHeld gives way to it.
func TestRemoteReadsReadOneMoment(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	for i := range 4 {
		_, err := c.AttachRemoteHolding(ctx, HolderKey{"cache-a", uint64(i + 1)}, holdingOf(fmt.Sprintf("moment-%d", i)))
		require.NoError(t, err)
	}
	var numbers []uint64
	for _, field := range []string{"moment-0", "moment-1", "moment-2", "moment-3"} {
		numbers = append(numbers, c.EquivalentEntries(testDigest(field).String())...)
	}
	require.Len(t, numbers, 4)

	r := c.HoldRemoteReads()
	before := r.EntriesHeld(numbers, 2)
	wrote := make(chan error, 1)
	go func() {
		// A second holding on every entry, and a new entry.
		w := c.HoldRemoteWrites()
		for i := range 4 {
			if _, err := w.AttachRemoteHolding(ctx, HolderKey{"cache-b", uint64(i + 1)}, holdingOf(fmt.Sprintf("moment-%d", i))); err != nil {
				wrote <- err
				return
			}
		}
		_, err := w.AttachRemoteHolding(ctx, HolderKey{"cache-b", 5}, holdingOf("moment-4"))
		wrote <- errors.Join(err, w.Release(ctx))
	}()
	// The hold is let go whatever it saw: a test that failed holding it
	// would keep the writer, and the cache's close, waiting.
	deadline := time.Now().Add(10 * time.Second)
	for !c.egraphMu.Waiting() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	waited := c.egraphMu.Waiting()
	during := r.EntriesHeld(numbers, 2)
	newEntry := r.EquivalentEntries(testDigest("moment-4").String())
	holdingsB := r.CacheHoldings("cache-b", numbers)
	r.Release()
	require.NoError(t, <-wrote)

	require.True(t, waited, "the writer never waited for the hold")
	require.Equal(t, before, during, "the hold read the writer's holdings")
	require.Empty(t, newEntry, "the hold read the writer's new entry")
	require.Empty(t, holdingsB, "the hold read the writer's holdings")
	require.Len(t, c.CacheHoldings("cache-b", numbers), 4, "the write applies once the hold is released")
	require.NotEmpty(t, c.EquivalentEntries(testDigest("moment-4").String()))
}
