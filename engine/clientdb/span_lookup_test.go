package clientdb

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSpanLookupMatchesReference feeds random span snapshots -- canonical
// and malformed span IDs, the same ID in several traces, late parents and
// cause links, parent cycles, empty parents, check/test/agent markers --
// to the interned index and to the string-keyed one it replaced
// (refSpanLookup), and requires every query to answer the same, after every
// batch.
func TestSpanLookupMatchesReference(t *testing.T) {
	for seed := range uint64(40) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, seed*7+1))
			ids := []string{"", "x", "ROOT", "0000000000000000", "FFFFFFFFFFFFFFFF", "abc"}
			for i := range 30 {
				ids = append(ids, fmt.Sprintf("%016x", uint64(i+1)*0x9e3779b97f4a7c15))
			}
			traces := []string{"t1", "t2", "t3"}
			markers := [][]byte{nil, nil, nil, checkNameAttrMarker, testCaseNameAttrMarker, testSuiteNameAttrMarker, agentIDAttrMarker}
			pick := func() string { return ids[rng.IntN(len(ids))] }

			got, want := newSpanLookup(), newRefSpanLookup()
			gotTree, wantTree := map[string]bool{}, map[string]bool{}
			var gotMemo placementMemo
			var wantMemo refPlacementMemo
			gotArrivals, wantArrivals := 0, 0
			var row int64
			for batch := range 12 {
				got.mu.Lock()
				want.mu.Lock()
				for range 1 + rng.IntN(8) {
					row++
					r := Span{ID: row, TraceID: traces[rng.IntN(len(traces))], SpanID: pick(),
						Attributes: markers[rng.IntN(len(markers))]}
					if rng.IntN(4) > 0 {
						r.ParentSpanID = validString(pick())
					}
					var targets []string
					for range rng.IntN(3) {
						if target := pick(); target != r.SpanID && !slices.Contains(targets, target) {
							targets = append(targets, target)
						}
					}
					got.addLocked(r, targets)
					want.addLocked(r, targets)
				}
				got.gen.Add(1)
				want.gen.Add(1)
				got.mu.Unlock()
				want.mu.Unlock()

				if batch == 3 {
					// Resolve a live tree, as a log scope does.
					root := pick()
					for id := range got.logScope(root) {
						gotTree[id] = true
					}
					for id := range want.logScope(root) {
						wantTree[id] = true
					}
					require.Equal(t, wantTree, gotTree)
					unplaced := []string{pick(), pick()}
					gotArrivals, _ = got.closeBeneath(gotTree, unplaced)
					var still []string
					wantArrivals, still = want.closeBeneath(wantTree, unplaced)
					_, gotStill := got.closeBeneath(gotTree, unplaced)
					require.Equal(t, wantTree, gotTree)
					require.Equal(t, wantArrivals, gotArrivals)
					require.Equal(t, still, gotStill)
				}
				if batch > 3 {
					unplaced := []string{pick(), pick()}
					gotAdded, gotCovered, gotStill := got.placeArrivals(gotTree, gotArrivals, &gotMemo, unplaced)
					wantAdded, wantCovered, wantStill := want.placeArrivals(wantTree, wantArrivals, &wantMemo, unplaced)
					require.ElementsMatch(t, wantAdded, gotAdded)
					require.Equal(t, wantCovered, gotCovered)
					require.Equal(t, wantStill, gotStill)
					require.Equal(t, wantTree, gotTree)
					gotArrivals, wantArrivals = gotCovered, wantCovered
				}

				require.Equal(t, want.generation(), got.generation())
				require.Equal(t, want.allSpanIDs(), got.allSpanIDs())
				wantChecks, wantTests := want.markedSpanIDs()
				gotChecks, gotTests := got.markedSpanIDs()
				require.Equal(t, wantChecks, gotChecks)
				require.Equal(t, wantTests, gotTests)
				require.Equal(t, want.agentSpanIDs(), got.agentSpanIDs())
				scope := map[string]bool{pick(): true, pick(): true}
				for _, id := range append(slices.Clone(ids), "unknown") {
					msg := fmt.Sprintf("batch %d, span %q", batch, id)
					for _, trace := range append(slices.Clone(traces), "t4") {
						wantRow, wantOK := want.first(trace, id)
						gotRow, gotOK := got.first(trace, id)
						require.Equal(t, wantOK, gotOK, msg)
						require.Equal(t, wantRow, gotRow, msg)
					}
					require.Equal(t, want.hasSpan(id), got.hasSpan(id), msg)
					require.Equal(t, want.hasDescendants(id), got.hasDescendants(id), msg)
					require.ElementsMatch(t, want.causalChildrenOf(id), got.causalChildrenOf(id), msg)
					require.ElementsMatch(t, want.causalParentsOf(id), got.causalParentsOf(id), msg)
					require.Equal(t, want.logScope(id), got.logScope(id), msg)
					require.Equal(t, want.parentSubtree(id), got.parentSubtree(id), msg)
					require.Equal(t, want.directChildren(id), got.directChildren(id), msg)
					require.ElementsMatch(t, want.children[id], got.childrenLocked(id), msg)
					require.Equal(t, want.parent[id], got.parentName(id), msg)
					set := map[string]struct{}{id: {}, pick(): {}}
					require.Equal(t, want.ancestorClosure(set), got.ancestorClosure(set), msg)
					require.Equal(t, want.latestRowIDs(set), got.latestRowIDs(set), msg)
					// Placement, with memos kept across calls as a reader keeps
					// them.
					wantKnown, wantBeneath, wantGen := want.place(id, scope, &wantMemo)
					gotKnown, gotBeneath, gotGen := got.place(id, scope, &gotMemo)
					require.Equal(t, []any{wantKnown, wantBeneath, wantGen}, []any{gotKnown, gotBeneath, gotGen}, msg)
				}
				require.Equal(t, want.latestRowIDs(maps.Collect(func(yield func(string, struct{}) bool) {
					for _, id := range ids {
						if !yield(id, struct{}{}) {
							return
						}
					}
				})), got.latestRowIDs(want.allSpanIDs()))
			}
		})
	}
}

// TestIDTableRoundTrips checks that every ID, canonical or not, comes back
// exactly as interned, numbered in the order first seen.
func TestIDTableRoundTrips(t *testing.T) {
	for prefix, ids := range map[string][]string{
		"": {"0123456789abcdef", "0123456789ABCDEF", "0123456789abcde", "0123456789abcdef0", "", "x",
			"0000000000000000", "ffffffffffffffff", "g123456789abcdef", "xxh3:0123456789abcdef"},
		"xxh3:": {"", "xxh3:0123456789abcdef", "0123456789abcdef", "xxh3:0123456789ABCDEF", "xxh4:0123456789abcdef",
			"xxh3:0123456789abcde", "sha256:0123", "xxh3:ffffffffffffffff"},
	} {
		table := idTable{prefix: prefix}
		for i, id := range ids {
			n := table.intern(id)
			require.Equal(t, uint32(i), n, id)
			require.Equal(t, n, table.intern(id), id)
			got, ok := table.lookup(id)
			require.True(t, ok, id)
			require.Equal(t, n, got, id)
			require.Equal(t, id, table.name(n), id)
			require.True(t, table.in(n, map[string]bool{id: true}), id)
			require.False(t, table.in(n, map[string]bool{id + "0": true}), id)
		}
		require.Equal(t, len(ids), table.count())
		_, ok := table.lookup(prefix + "fedcba9876543210")
		require.False(t, ok)
	}
}
