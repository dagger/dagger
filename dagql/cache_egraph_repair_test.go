package dagql

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A union's repair walks the terms that named the class it absorbed, not
// every term of the class that survives: teaching an identity that joins a
// new entry's class to a class 200 calls take as their input walks none of
// those 200 terms. Two ways to the union are taught: by a shared content
// digest, when the used class has been joined before and outranks the new
// one; and by the new entry's request, when neither class has been joined
// and the used one would be absorbed by rank alone. Either way, each use
// is then found through the new entry's recipe as well.
func TestUnionRepairWalksOnlyTheAbsorbedClassTerms(t *testing.T) {
	t.Parallel()
	const uses = 200
	for _, tc := range []struct {
		name string
		// base and twin are the used entry's row and the new entry's.
		base, twin func() RemoteHolding
	}{
		{
			name: "by content, the used class joined before",
			base: func() RemoteHolding {
				h := holdingOf("base")
				h.ContentDigest = testDigest("shared-content")
				return h
			},
			twin: func() RemoteHolding {
				h := holdingOf("twin")
				h.ContentDigest = testDigest("shared-content")
				return h
			},
		},
		{
			name: "by request, neither class joined before",
			base: func() RemoteHolding { return holdingOf("base") },
			twin: func() RemoteHolding {
				h := holdingOf("twin")
				h.Request = testDigest("base")
				return h
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cloud := newCloudCache(t)
			a := newCloudApplier(t, cloud)
			base := tc.base()
			a.call(callRow{key: HolderKey{"cache-a", 1}, session: "a", holding: base})
			for i := range uses {
				use := fmt.Sprint("use-", i)
				a.call(callRow{key: HolderKey{"cache-a", uint64(i + 2)}, session: "a", holding: RemoteHolding{
					Recipe:   testDigest(use),
					Request:  testDigest(use),
					Field:    "use",
					TypeName: "Directory",
					Term:     &RemoteTerm{Self: testDigest(use + "-self"), Inputs: []digest.Digest{base.Recipe}},
				}})
			}
			walked := func() uint64 {
				cloud.egraphMu.RLock()
				defer cloud.egraphMu.RUnlock()
				return cloud.egraphRepairWalks
			}
			before := walked()
			twin := tc.twin()
			a.call(callRow{key: HolderKey{"cache-b", 1}, session: "b", holding: twin})
			got := walked() - before

			// The union happened: a use is found through the twin's recipe.
			found := cloud.TermEntries(testDigest("use-7-self"), []digest.Digest{twin.Recipe})
			require.Len(t, found, 1, "a use is found through the twin's recipe once their classes are one")
			require.Equal(t, cloud.TermEntries(testDigest("use-7-self"), []digest.Digest{base.Recipe}), found)

			require.Less(t, got, uint64(3), "the union's repair walked %d terms; the absorbed class named none of the %d uses", got, uses)
		})
	}
}

// The repair of the terms a union moved, with the survivor the class more
// terms take as an input, leaves the e-graph as the repair of every term of
// the survivor did, with the survivor chosen by rank: after every step of
// random rows of three engines, with sessions ending, entries collected and
// classes compacted between them, the two caches hold the same classes, each
// named by its digests, the same terms with the same inputs and outputs, the
// same entries' output classes, and give the same hits for every recipe,
// content and term. The rows' calls share recipes, requests, contents and
// term selves, so unions are frequent: a request of another recipe or a
// shared content joins two classes that terms take as inputs on both sides,
// a term may take one class in two positions, and a union makes terms
// congruent, whose outputs' union makes more. Every term names its inputs'
// roots after every step.
func TestUnionRepairMatchesWholeClassRepair(t *testing.T) {
	t.Parallel()
	for seed := range uint64(30) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Parallel()
			checkUnionRepairMatchesWholeClassRepair(t, seed)
		})
	}
}

func checkUnionRepairMatchesWholeClassRepair(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, seed))
	type arm struct {
		ctx   context.Context
		cloud *Cache
		a     *cloudApplier
	}
	newArm := func(whole bool) *arm {
		// A blob store, as the service's cache has, so that classes compact.
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		cloud.testRepairWholeClass = whole
		return &arm{ctx: ctx, cloud: cloud, a: newCloudApplier(t, cloud)}
	}
	whole, moved := newArm(true), newArm(false)
	arms := []*arm{whole, moved}

	const recipes = 30
	pick := func(prefix string, n int) digest.Digest { return testDigest(fmt.Sprint(prefix, rng.IntN(n))) }
	calls := make([]RemoteHolding, recipes)
	for i := range calls {
		recipe := testDigest(fmt.Sprint("recipe-", i))
		h := RemoteHolding{Recipe: recipe, Request: recipe, Field: "f", TypeName: "Directory"}
		if rng.IntN(4) == 0 {
			h.Request = pick("recipe-", recipes)
		}
		if rng.IntN(2) == 0 {
			h.ContentDigest = pick("content-", 6)
		}
		if rng.IntN(6) != 0 {
			term := &RemoteTerm{Self: pick("self-", 3)}
			for range rng.IntN(4) {
				term.Inputs = append(term.Inputs, pick("recipe-", recipes))
			}
			if len(term.Inputs) > 0 && rng.IntN(4) == 0 {
				term.Inputs = append(term.Inputs, term.Inputs[0])
			}
			h.Term = term
		}
		calls[i] = h
	}
	engines := []CacheID{"cache-a", "cache-b", "cache-c"}
	session := map[CacheID]int{}
	sessionOf := func(e CacheID) string { return fmt.Sprint(e, "-", session[e]) }

	hits := func(c *Cache) []string {
		var out []string
		sorted := func(n []uint64) []uint64 { return slices.Sorted(slices.Values(n)) }
		for i, h := range calls {
			out = append(out, fmt.Sprint("recipe ", i, sorted(c.EquivalentEntries(h.Recipe.String()))))
			if h.ContentDigest != "" {
				out = append(out, fmt.Sprint("content ", i, sorted(c.EquivalentEntries(h.ContentDigest.String()))))
			}
			if h.Term != nil {
				out = append(out, fmt.Sprint("term ", i, sorted(c.TermEntries(h.Term.Self, h.Term.Inputs))))
			}
		}
		return out
	}
	compare := func(step int) {
		t.Helper()
		var contents []eqClassContents
		for _, arm := range arms {
			arm.cloud.egraphMu.Lock()
			require.NoError(t, termsNameRootsErrorLocked(arm.cloud), "step %d", step)
			contents = append(contents, testEqClassContentsLocked(arm.cloud))
			arm.cloud.egraphMu.Unlock()
		}
		require.Equal(t, contents[0], contents[1], "step %d: the e-graphs differ", step)
		require.Equal(t, hits(whole.cloud), hits(moved.cloud), "step %d: the hits differ", step)
	}

	for step := range 300 {
		switch rng.IntN(20) {
		case 0:
			e := engines[rng.IntN(len(engines))]
			for _, arm := range arms {
				arm.a.endSession(e, sessionOf(e))
				arm.a.collect()
			}
			session[e]++
		case 1:
			for _, arm := range arms {
				_, _, err := arm.cloud.CompactEqClasses(arm.ctx)
				require.NoError(t, err)
			}
		default:
			e := engines[rng.IntN(len(engines))]
			c := rng.IntN(len(calls))
			key := HolderKey{e, uint64(c + 1)}
			for _, arm := range arms {
				arm.a.call(callRow{key: key, session: sessionOf(e), holding: calls[c]})
				if step%9 == 0 {
					arm.a.collect()
				}
			}
		}
		compare(step)
	}
	whole.cloud.egraphMu.RLock()
	unions, wholeWalks := whole.cloud.egraphUnions, whole.cloud.egraphRepairWalks
	whole.cloud.egraphMu.RUnlock()
	moved.cloud.egraphMu.RLock()
	movedWalks := moved.cloud.egraphRepairWalks
	moved.cloud.egraphMu.RUnlock()
	t.Logf("seed %d: %d unions; repairs walked %d terms of the survivors, %d moved", seed, unions, wholeWalks, movedWalks)
	require.Greater(t, unions, uint64(10), "the rows join classes")
	require.LessOrEqual(t, movedWalks, wholeWalks)
}

// termsNameRootsErrorLocked checks what the repair of the terms a union
// moved rests on: every live term names its inputs' roots, is indexed under
// each of them, has the digest its self and inputs make, and is indexed
// under that digest. Requires egraphMu.
func termsNameRootsErrorLocked(c *Cache) error {
	for id, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		for _, in := range term.inputEqIDs {
			if in == 0 {
				continue
			}
			if root := c.eqClassRootLocked(in); root != in {
				return fmt.Errorf("term %d names class %d, under root %d", id, in, root)
			}
			if _, ok := c.inputEqClassToTerms[in][id]; !ok {
				return fmt.Errorf("term %d is not indexed under its input %d", id, in)
			}
		}
		if dig := calcEgraphTermDigest(term.selfDigest, term.inputEqIDs); dig != term.termDigest {
			return fmt.Errorf("term %d has digest %s, its inputs make %s", id, term.termDigest, dig)
		}
		if set := c.egraphTermsByTermDigest[term.termDigest]; set == nil || !set.Contains(id) {
			return fmt.Errorf("term %d is not indexed under its digest", id)
		}
	}
	return nil
}

// A union that makes terms congruent unions their outputs, and that union
// makes more terms congruent, in a chain; a term that takes one class in two
// positions meets one that takes two classes the union joins. Under either
// repair: A and B join by a shared content; f(A) and f(B) then are one, so X
// and Y are; g(X) and g(Y) then are one, so P and Q are; and h(A, B) and
// h(B, B) are one, so S and U are.
func TestUnionRepairFollowsCongruenceChains(t *testing.T) {
	t.Parallel()
	for _, whole := range []bool{true, false} {
		t.Run(fmt.Sprint("whole=", whole), func(t *testing.T) {
			t.Parallel()
			cloud := newCloudCache(t)
			cloud.testRepairWholeClass = whole
			a := newCloudApplier(t, cloud)
			n := uint64(0)
			row := func(recipe, content, self string, inputs ...string) {
				n++
				h := RemoteHolding{Recipe: testDigest(recipe), Request: testDigest(recipe), Field: recipe, TypeName: "Directory"}
				if content != "" {
					h.ContentDigest = testDigest(content)
				}
				if self != "" {
					h.Term = &RemoteTerm{Self: testDigest(self)}
					for _, in := range inputs {
						h.Term.Inputs = append(h.Term.Inputs, testDigest(in))
					}
				}
				a.call(callRow{key: HolderKey{"cache-a", n}, session: "a", holding: h})
			}
			same := func(x, y string) bool {
				gx, gy := cloud.EquivalentEntries(testDigest(x).String()), cloud.EquivalentEntries(testDigest(y).String())
				return len(gx) == 2 && slices.Equal(slices.Sorted(slices.Values(gx)), slices.Sorted(slices.Values(gy)))
			}
			row("A", "", "a-self")
			row("B", "", "b-self")
			row("X", "", "f", "A")
			row("Y", "", "f", "B")
			row("P", "", "g", "X")
			row("Q", "", "g", "Y")
			row("S", "", "h", "A", "B")
			row("U", "", "h", "B", "B")
			for _, pair := range [][2]string{{"A", "B"}, {"X", "Y"}, {"P", "Q"}, {"S", "U"}} {
				require.False(t, same(pair[0], pair[1]), "%s and %s are apart before the union", pair[0], pair[1])
			}
			row("A", "ab", "a-self")
			row("B", "ab", "b-self")
			for _, pair := range [][2]string{{"A", "B"}, {"X", "Y"}, {"P", "Q"}, {"S", "U"}} {
				require.True(t, same(pair[0], pair[1]), "%s and %s are one after the union", pair[0], pair[1])
			}
			cloud.egraphMu.Lock()
			require.NoError(t, termsNameRootsErrorLocked(cloud))
			cloud.egraphMu.Unlock()
		})
	}
}

// A union into a class that many terms take as an input, on a local
// engine's cache: a new result whose content another result already has
// joins its class to that one, which uses calls take as their receiver. The
// survivor's terms are walked by the repair of the whole class, and not by
// the repair of the terms the union moved.
func BenchmarkUnionIntoAUsedClass(b *testing.B) {
	for _, uses := range []int{100, 10_000} {
		for _, whole := range []bool{true, false} {
			b.Run(fmt.Sprintf("uses=%d/whole=%v", uses, whole), func(b *testing.B) {
				f := &cacheDerivedIndexFixture{setupSession: "union-into-used"}
				f.cache, f.ctx = newCacheDerivedIndexFixtureCache(b, f.setupSession, "")
				defer f.close(b)
				f.cache.testRepairWholeClass = whole
				content := call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("union-into-used-content")}
				base := cacheDerivedIndexPublishPersistedResult(b, f, cacheTestIntCall("base-request"), cacheTestIntCall("base-response", content), 0)
				for i := range uses {
					use := cacheDerivedIndexPublishPersistedResult(b, f, cacheTestIntCall(fmt.Sprint("use-request-", i)), cacheTestIntCall(fmt.Sprint("use-response-", i)), i)
					structural := &ResultCall{
						Kind:     ResultCallKindField,
						Type:     NewResultCallType(Int(0).Type()),
						Field:    fmt.Sprint("use-", i),
						Receiver: &ResultCallRef{ResultID: uint64(base.cacheSharedResult().id)},
					}
					if err := f.cache.TeachCallEquivalentToResult(f.ctx, f.setupSession, structural, use); err != nil {
						b.Fatal(err)
					}
				}
				f.cache.egraphMu.RLock()
				walks := f.cache.egraphRepairWalks
				f.cache.egraphMu.RUnlock()
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					i++
					twin := fmt.Sprint("twin-", i)
					cacheDerivedIndexPublishPersistedResult(b, f, cacheTestIntCall(twin+"-request"), cacheTestIntCall(twin+"-response", content), uses+i)
				}
				f.cache.egraphMu.RLock()
				b.ReportMetric(float64(f.cache.egraphRepairWalks-walks)/float64(i), "terms-walked/op")
				f.cache.egraphMu.RUnlock()
			})
		}
	}
}
