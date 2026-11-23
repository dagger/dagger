package dagql

import (
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// largeKeptCallEntry is an entry of c keeping n calls of distinct terms,
// each over an input with a class: by its own input digest, or, with
// stored, all over one input the Cloud stores, each with a self digest of
// its own. It returns the entry and a maker of more such calls.
func largeKeptCallEntry(tb testing.TB, c *Cache, n int, stored bool) (*sharedResult, func(i int) *keptCall) {
	tb.Helper()
	ctx := tb.Context()
	c.initEgraphLocked()
	entry := &sharedResult{id: c.takeResultIDs(1)}
	c.putResultLocked(entry)
	storedInput := digest.FromString("stored input")
	if stored {
		in := &sharedResult{id: c.takeResultIDs(1), persistedEnvelope: &PersistedResultEnvelope{}}
		c.putResultLocked(in)
		c.indexRecipeLocked(storedInput, in)
		c.ensureEqClassForDigestLocked(ctx, storedInput.String())
	}
	mk := func(i int) *keptCall {
		self := digest.FromString("self")
		in := digest.FromString(fmt.Sprint("input-", i))
		if stored {
			self, in = digest.FromString(fmt.Sprint("self-", i)), storedInput
		} else {
			c.ensureEqClassForDigestLocked(ctx, in.String())
		}
		return &keptCall{id: callIdentity{field: "f", recipe: digest.FromString(fmt.Sprint("call-", i, "-", stored)), self: self, inputs: []digest.Digest{in}, witnesses: []bool{false}}}
	}
	entry.calls = make(map[digest.Digest]*keptCall, n)
	for i := 0; i < n; i++ {
		k := mk(i)
		entry.calls[k.id.recipe] = k
		c.indexKeptCallLocked(entry, k)
	}
	return entry, mk
}

// keepOn keeps kept on entry, as a span reporting it does.
func keepOn(c *Cache, entry *sharedResult, kept *keptCall) {
	c.keepCallLocked(&RemoteChange{}, entry, RemoteHolding{Recipe: digest.FromString("entry"), Request: kept.id.recipe}, kept)
}

// Keeping a call on an entry that keeps 10,000 calls takes the new call's
// term key, not every kept call's: under the Cloud cache's write hold, a
// scan of every kept call on each insert cost about 5.7 ms on such an
// entry. The first call kept after the entry's calls change otherwise, as
// here where they are set directly, may key them all once.
func TestKeepingACallOnALargeEntryKeysItsOwnTerm(t *testing.T) {
	t.Parallel()
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprint("inputs stored: ", stored), func(t *testing.T) {
			t.Parallel()
			_, c := storedPartTestCache(t, WithBlobStore())
			c.egraphMu.Lock()
			defer c.egraphMu.Unlock()
			const n = 10000
			entry, mk := largeKeptCallEntry(t, c, n, stored)
			keyed := 0
			c.testCallTermKeyed = func() { keyed++ }
			keepOn(c, entry, mk(n))
			for i, kept := range []*keptCall{mk(n + 1), mk(0)} {
				keyed = 0
				if i == 1 {
					// A call of a term the entry keeps, under a request of its own.
					kept.id.recipe = digest.FromString("again")
				}
				keepOn(c, entry, kept)
				require.LessOrEqual(t, keyed, 2, "keys taken to keep one call on %d", n)
			}
			require.Len(t, entry.calls, n+2, "two new terms kept, the old term's call not")
		})
	}
}

// BenchmarkKeepCallOnALargeEntry keeps one more call on an entry keeping n
// calls of distinct terms: of a new term or one it keeps, over inputs the
// Cloud stores or not. Each insert finds the entry keeping n+1 calls: the
// calls, and their inputs' classes, are made before the timer runs, and
// what an insert added is dropped with the timer stopped.
func BenchmarkKeepCallOnALargeEntry(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		for _, stored := range []bool{false, true} {
			for _, old := range []bool{false, true} {
				b.Run(fmt.Sprintf("%d kept, inputs stored %v, old term %v", n, stored, old), func(b *testing.B) {
					_, c := storedPartTestCache(b, WithBlobStore())
					c.egraphMu.Lock()
					defer c.egraphMu.Unlock()
					entry, mk := largeKeptCallEntry(b, c, n, stored)
					keepOn(c, entry, mk(n))
					calls := make([]*keptCall, b.N)
					for i := range calls {
						if old {
							calls[i] = mk(0)
							calls[i].id.recipe = digest.FromString(fmt.Sprint("again-", i))
						} else {
							calls[i] = mk(n + 1 + i)
						}
					}
					b.ResetTimer()
					for _, kept := range calls {
						keepOn(c, entry, kept)
						b.StopTimer()
						forgetKept(c, entry, kept)
						b.StartTimer()
					}
				})
			}
		}
	}
}

// forgetKept drops each of calls that entry still keeps.
func forgetKept(c *Cache, entry *sharedResult, calls ...*keptCall) {
	for _, kept := range calls {
		if entry.calls[kept.id.recipe] == kept {
			c.dropKeptCallLocked(entry, kept)
		}
	}
}
