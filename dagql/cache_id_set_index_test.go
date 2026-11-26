package dagql

import (
	"cmp"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// An idSetIndex answers as a map of sets would, through random inserts,
// removes, deletes and moves of one key's members into another's, that move
// keys between one member and many, across
// the size where a key's set becomes a map. After every step each key with
// members is in exactly one of its two maps, in the set map only with two
// or more members.
func TestIDSetIndexActsAsAMapOfSets(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(5, 6))
	var x idSetIndex[uint64, uint64]
	want := map[uint64]map[uint64]struct{}{}
	check := func(step int) {
		require.Equal(t, len(want), x.Len(), "step %d", step)
		require.ElementsMatch(t, slices.Collect(maps.Keys(want)), slices.Collect(x.Keys()), "step %d", step)
		for k, members := range want {
			_, inOne := x.one[k]
			set, inMany := x.many[k]
			require.NotEqual(t, inOne, inMany, "step %d: key %d in one %v, many %v", step, k, inOne, inMany)
			if inMany {
				require.Greater(t, set.Size(), 1, "step %d: key %d has a set of %d", step, k, set.Size())
			}
			require.Equal(t, len(members), x.Size(k), "step %d: key %d", step, k)
			sorted := slices.Sorted(maps.Keys(members))
			require.Equal(t, sorted, slices.Collect(x.Items(k)), "step %d: key %d", step, k)
			require.Equal(t, sorted, slices.Sorted(x.All(k)), "step %d: key %d", step, k)
		}
		for k := range x.one {
			require.Contains(t, want, k, "step %d", step)
		}
		for k := range x.many {
			require.Contains(t, want, k, "step %d", step)
		}
	}
	for step := range 20000 {
		k := uint64(rng.IntN(24))
		// Most keys stay small; every 5000 steps, members spread past the
		// size where a set becomes a map, then shrink back.
		span := 3
		if step/5000%2 == 1 {
			span = 3 * idSetListMax
		}
		v := uint64(rng.IntN(span))
		members := want[k]
		_, had := members[v]
		switch op := rng.IntN(20); {
		case op == 0:
			x.Delete(k)
			delete(want, k)
		case op == 2:
			// A join renames one member of a key's to another.
			to := uint64(rng.IntN(span))
			_, hadTo := members[to]
			require.Equal(t, hadTo, x.Rename(k, v, to), "step %d: rename %d to %d in %d", step, v, to, k)
			// Renaming a member to itself changes nothing.
			if len(members) > 0 && v != to {
				delete(members, v)
				members[to] = struct{}{}
			}
		case op == 1:
			// A join moves one key's members into another's.
			dst := uint64(rng.IntN(24))
			x.Move(k, dst)
			if k != dst && len(members) > 0 {
				if want[dst] == nil {
					want[dst] = map[uint64]struct{}{}
				}
				for m := range members {
					want[dst][m] = struct{}{}
				}
				delete(want, k)
			}
			clone := x.Clone(dst)
			require.Equal(t, slices.Sorted(maps.Keys(want[dst])), slices.Collect(clone.Items()), "step %d: move %d into %d", step, k, dst)
			// The clone is a set of its own: changing it leaves the index.
			clone.Insert(1 << 40)
			require.False(t, x.Contains(dst, 1<<40), "step %d: a clone shares its key's set", step)
		case op < 9:
			require.Equal(t, had, x.Remove(k, v), "step %d: remove %d from %d", step, v, k)
			delete(members, v)
			if len(members) == 0 {
				delete(want, k)
			}
		default:
			require.Equal(t, !had, x.Insert(k, v), "step %d: insert %d into %d", step, v, k)
			if members == nil {
				members = map[uint64]struct{}{}
				want[k] = members
			}
			members[v] = struct{}{}
		}
		_, has := want[k][v]
		require.Equal(t, has, x.Contains(k, v), "step %d: %d has %d", step, k, v)
		if step%10 == 0 {
			check(step)
		}
	}
	check(-1)

	// A key grown past the size where its set becomes a map, then shrunk to
	// one member, holds that member in its slot again.
	var grown idSetIndex[string, uint64]
	for v := range uint64(idSetListMax + 1) {
		require.True(t, grown.Insert("k", v))
	}
	require.NotNil(t, grown.many["k"].big, "the set became a map")
	for v := range uint64(idSetListMax) {
		require.True(t, grown.Remove("k", v))
	}
	require.NotContains(t, grown.many, "k", "the last member left the set")
	require.Equal(t, uint64(idSetListMax), grown.one["k"], "the last member is back in its slot")
	require.Equal(t, []uint64{idSetListMax}, slices.Collect(grown.Items("k")))
	require.True(t, grown.Remove("k", idSetListMax))
	require.Zero(t, grown.Len())

	// A key with no members reads as empty, in an index that was never
	// written as in one that was.
	var empty idSetIndex[string, uint64]
	require.Zero(t, empty.Size("none"))
	require.False(t, empty.Contains("none", 1))
	require.False(t, empty.Remove("none", 1))
	require.Empty(t, slices.Collect(empty.All("none")))
	require.Empty(t, slices.Collect(empty.Keys()))
	empty.Delete("none")
	require.Zero(t, empty.Len())
}

// idSetIndexResetForTest reports whether x is as a reset leaves it: holding
// nothing, not even an empty map.
func idSetIndexResetForTest[K comparable, V cmp.Ordered](x *idSetIndex[K, V]) bool {
	return x.one == nil && x.many == nil
}
