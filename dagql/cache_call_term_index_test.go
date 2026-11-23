package dagql

import (
	"context"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
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
// Cloud stores or not, and one that replaces its term's call, which names
// an input the Cloud doesn't store, with one naming an input of the same
// class that it does. Each insert finds the entry keeping n+1 calls: the
// calls, and their inputs' classes, are made before the timer runs, and
// what an insert added is dropped with the timer stopped.
func BenchmarkKeepCallOnALargeEntry(b *testing.B) {
	// The benchmark times the keeping, not the tests' check of each index
	// against a scan. Tests are over when benchmarks start.
	verify := verifyKeptCallTerms
	verifyKeptCallTerms = false
	defer func() { verifyKeptCallTerms = verify }()
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
		b.Run(fmt.Sprintf("%d kept, replaces a call the Cloud can't place", n), func(b *testing.B) {
			ctx, c := storedPartTestCache(b, WithBlobStore())
			c.egraphMu.Lock()
			defer c.egraphMu.Unlock()
			entry, mk := largeKeptCallEntry(b, c, n, false)
			keepOn(c, entry, mk(n))
			b.ResetTimer()
			b.StopTimer()
			for i := 0; i < b.N; i++ {
				// A call of a new term over an input the Cloud doesn't
				// store, whose class has an input that it does.
				j := n + 1 + i
				stored := digest.FromString(fmt.Sprint("input-", j, " stored"))
				res := &sharedResult{id: c.takeResultIDs(1), persistedEnvelope: &PersistedResultEnvelope{}}
				c.putResultLocked(res)
				c.indexRecipeLocked(stored, res)
				in := digest.FromString(fmt.Sprint("input-", j)).String()
				c.mergeEqClassesLocked(ctx, c.ensureEqClassForDigestLocked(ctx, in), c.ensureEqClassForDigestLocked(ctx, stored.String()))
				first := mk(j)
				keepOn(c, entry, first)
				kept := mk(j)
				kept.id.recipe = digest.FromString(fmt.Sprint("stored-", j))
				kept.id.inputs = []digest.Digest{stored}
				b.StartTimer()
				keepOn(c, entry, kept)
				b.StopTimer()
				forgetKept(c, entry, first, kept)
			}
		})
		for _, m := range []int{100, 1000} {
			for _, own := range []bool{true, false} {
				b.Run(fmt.Sprintf("%d kept, old term over an unstored receiver of a class of %d stored entries, kept call placed by its own %v", n, m, own), func(b *testing.B) {
					benchmarkKeepOverALargeClass(b, n, m, own)
				})
			}
		}
	}
}

// benchmarkKeepOverALargeClass keeps, on an entry keeping n calls, a call of
// a term the entry keeps a call of, whose input the Cloud stores no entry of
// and knows only by its class, which has m stored entries: a client-specific
// receiver equal in content to receivers other sessions made. The term's
// kept call is placed by its own stored input (own), or by its class too.
func benchmarkKeepOverALargeClass(b *testing.B, n, m int, own bool) {
	ctx, c := storedPartTestCache(b, WithBlobStore())
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	entry, mk := largeKeptCallEntry(b, c, n, true)
	class := digest.FromString("stored input").String()
	for i := 0; i < m; i++ {
		d := digest.FromString(fmt.Sprint("member-", i))
		res := &sharedResult{id: c.takeResultIDs(1), persistedEnvelope: &PersistedResultEnvelope{}}
		c.putResultLocked(res)
		c.indexRecipeLocked(d, res)
		member := c.ensureEqClassForDigestLocked(ctx, d.String())
		c.addResultOutputEqClassLocked(res.id, member)
		c.mergeEqClassesLocked(ctx, member, c.ensureEqClassForDigestLocked(ctx, class))
	}
	// The term's kept call, of a term none of the n calls is: over the stored
	// input itself, or over an input of the class the Cloud stores no entry
	// of.
	term := digest.FromString("the term")
	first := mk(0)
	first.id.self, first.id.recipe = term, digest.FromString("first")
	if !own {
		in := digest.FromString("unstored first")
		c.mergeEqClassesLocked(ctx, c.ensureEqClassForDigestLocked(ctx, in.String()), c.ensureEqClassForDigestLocked(ctx, class))
		first.id.inputs = []digest.Digest{in}
	}
	keepOn(c, entry, first)
	calls := make([]*keptCall, b.N)
	for i := range calls {
		in := digest.FromString(fmt.Sprint("unstored-", i))
		c.mergeEqClassesLocked(ctx, c.ensureEqClassForDigestLocked(ctx, in.String()), c.ensureEqClassForDigestLocked(ctx, class))
		calls[i] = mk(0)
		calls[i].id.self, calls[i].id.recipe = term, digest.FromString(fmt.Sprint("again-", i))
		calls[i].id.inputs = []digest.Digest{in}
	}
	// A class gaining a digest drops the entry's index; one keep builds it.
	keepOn(c, entry, mk(n+1))
	b.ResetTimer()
	for _, kept := range calls {
		keepOn(c, entry, kept)
	}
	b.StopTimer()
	if entry.calls[first.id.recipe] != first {
		b.Fatal("the term's kept call was replaced")
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

// An entry's term index keeps what a scan of its calls would on every keep:
// on random sequences of keeps, new classes for inputs, unions of their
// classes, inputs the Cloud comes to store, which make a call replace its
// term's call the Cloud can't place, drops and compactions, the entry keeps
// the calls it keeps when each keep scans its calls, as before the index,
// and the index agrees with a scan after every step. The sequences drop
// the index by each of a new class, a union and a compaction, and keep
// calls that join terms and that replace a call.
func TestACallTermIndexKeepsWhatAScanKeeps(t *testing.T) {
	t.Parallel()
	var seen sync.Map
	saw := func(what string) {
		n, _ := seen.LoadOrStore(what, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
	}
	t.Run("sequences", func(t *testing.T) {
		for seed := int64(1); seed <= 40; seed++ {
			t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
				t.Parallel()
				callTermIndexSequence(t, seed, saw)
			})
		}
	})
	for _, what := range []string{"index dropped by class", "index dropped by union", "index dropped by compact", "keep joined", "keep replaced", "drop"} {
		n, ok := seen.Load(what)
		require.True(t, ok && n.(*atomic.Int64).Load() > 0, "no sequence had a %s", what)
	}
}

// callTermIndexSequence runs TestACallTermIndexKeepsWhatAScanKeeps's
// sequence of seed, and tells saw what each step did.
func callTermIndexSequence(t *testing.T, seed int64, saw func(string)) {
	type side struct {
		ctx   context.Context
		c     *Cache
		entry *sharedResult
	}
	newSide := func() *side {
		ctx, c := storedPartTestCache(t, WithBlobStore())
		c.egraphMu.Lock()
		t.Cleanup(c.egraphMu.Unlock)
		c.initEgraphLocked()
		entry := &sharedResult{id: c.takeResultIDs(1)}
		c.putResultLocked(entry)
		return &side{ctx: ctx, c: c, entry: entry}
	}
	// index keeps its term index; scan drops it before each keep,
	// so that each keep scans the entry's calls.
	index, scan := newSide(), newSide()
	rng := rand.New(rand.NewSource(seed))
	pool := make([]digest.Digest, 12)
	for i := range pool {
		pool[i] = digest.FromString(fmt.Sprint("input-", i))
	}
	selves := []digest.Digest{digest.FromString("self-0"), digest.FromString("self-1"), digest.FromString("self-2")}
	var steps []string
	for step := 0; step < 300; step++ {
		var do func(s *side) any
		var kind string
		switch n := rng.Intn(100); {
		case n < 50:
			id := callIdentity{field: "f", recipe: digest.FromString(fmt.Sprint("call-", step)), self: selves[rng.Intn(len(selves))]}
			for range 1 + rng.Intn(2) {
				id.inputs = append(id.inputs, pool[rng.Intn(len(pool))])
				id.witnesses = append(id.witnesses, false)
			}
			kind = "keep"
			steps = append(steps, fmt.Sprint("keep ", id.recipe.Encoded()[:6], " ", id.inputs))
			do = func(s *side) any {
				kept := &keptCall{id: id}
				return s.c.keepCallLocked(&RemoteChange{}, s.entry, RemoteHolding{Recipe: digest.FromString("entry"), Request: id.recipe}, kept)
			}
		case n < 60:
			in := pool[rng.Intn(len(pool))]
			kind = "class"
			steps = append(steps, fmt.Sprint("class ", in))
			do = func(s *side) any { return s.c.ensureEqClassForDigestLocked(s.ctx, in.String()) }
		case n < 78:
			a, b := pool[rng.Intn(len(pool))], pool[rng.Intn(len(pool))]
			kind = "union"
			steps = append(steps, fmt.Sprint("union ", a, " ", b))
			do = func(s *side) any {
				return s.c.mergeEqClassesLocked(s.ctx, s.c.ensureEqClassForDigestLocked(s.ctx, a.String()), s.c.ensureEqClassForDigestLocked(s.ctx, b.String()))
			}
		case n < 88:
			in := pool[rng.Intn(len(pool))]
			kind = "store"
			steps = append(steps, fmt.Sprint("store ", in))
			do = func(s *side) any {
				if s.c.currentEntryForRecipeLocked(in) != nil {
					return nil
				}
				res := &sharedResult{id: s.c.takeResultIDs(1), persistedEnvelope: &PersistedResultEnvelope{}}
				s.c.putResultLocked(res)
				s.c.indexRecipeLocked(in, res)
				return nil
			}
		case n < 95:
			pick := rng.Intn(1 << 20)
			kind = "drop"
			steps = append(steps, fmt.Sprint("drop ", pick))
			do = func(s *side) any {
				if len(s.entry.calls) == 0 {
					return nil
				}
				recipes := slices.Sorted(maps.Keys(s.entry.calls))
				recipe := recipes[pick%len(recipes)]
				s.c.dropKeptCallLocked(s.entry, s.entry.calls[recipe])
				return recipe
			}
		default:
			live := pool[:rng.Intn(len(pool))]
			kind = "compact"
			steps = append(steps, fmt.Sprint("compact, live ", len(live)))
			do = func(s *side) any {
				outputs := map[eqClassID]struct{}{}
				for _, in := range live {
					if root := s.c.eqClassRootLocked(s.c.egraphDigestToClass[in.String()]); root != 0 {
						outputs[root] = struct{}{}
					}
				}
				s.c.resultOutputEqClasses[s.entry.id] = outputs
				compacted, _, _ := s.c.compactEqClassesLocked(true)
				return compacted
			}
		}
		scan.entry.callTermIndex = nil
		indexed := index.entry.callTermIndex != nil
		_, joined := index.c.joinedTermCallsLocked(index.entry)
		before := slices.Sorted(maps.Keys(index.entry.calls))
		result := do(index)
		require.Equal(t, do(scan), result, "step %d: %s", step, steps[step])
		switch after := index.entry.calls; {
		case indexed && index.entry.callTermIndex == nil:
			saw("index dropped by " + kind)
		case kind == "keep" && len(joined) > 0 && len(after) <= len(before):
			saw("keep joined")
		case kind == "keep" && len(joined) == 0 && len(after) == len(before) && !slices.Equal(before, slices.Sorted(maps.Keys(after))):
			saw("keep replaced")
		case kind == "drop" && result != nil:
			saw("drop")
		}
		require.Equal(t, slices.Sorted(maps.Keys(scan.entry.calls)), slices.Sorted(maps.Keys(index.entry.calls)), "step %d: %s\nsteps: %v", step, steps[step], steps)
		if index.entry.callTermIndex != nil {
			fresh, joined := index.c.joinedTermCallsLocked(index.entry)
			require.Empty(t, joined, "step %d: %s", step, steps[step])
			require.Equal(t, fresh, index.entry.callTermIndex, "step %d: %s", step, steps[step])
		}
	}
}
