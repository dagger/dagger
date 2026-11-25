package dagql

import (
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// An idSet answers as a map of the same members would, through random
// inserts and removes on both sides of the size where it becomes a map,
// and Items yields them in order.
func TestIDSetActsAsASet(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(3, 4))
	s := newIDSet[uint64]()
	want := map[uint64]struct{}{}
	for step := range 8000 {
		// Grow past the list's limit, then shrink back below it.
		span := 40 + 4*idSetListMax*(step/2000%2)
		k := uint64(1 + rng.IntN(span))
		_, had := want[k]
		if rng.IntN(3) == 0 {
			require.Equal(t, had, s.Remove(k), "step %d: remove %d", step, k)
			delete(want, k)
		} else {
			require.Equal(t, !had, s.Insert(k), "step %d: insert %d", step, k)
			want[k] = struct{}{}
		}
		require.Equal(t, len(want), s.Size(), "step %d", step)
		if step%50 == 0 {
			require.Equal(t, slices.Sorted(maps.Keys(want)), slices.Collect(s.Items()), "step %d", step)
			require.Equal(t, slices.Sorted(maps.Keys(want)), slices.Sorted(s.All()), "step %d", step)
		}
		_, has := want[k]
		require.Equal(t, has, s.Contains(k), "step %d: contains %d", step, k)
	}
	o := newIDSet[uint64]()
	for k := range uint64(2 * idSetListMax) {
		o.Insert(k * 7)
	}
	s.InsertAll(o)
	for k := range o.All() {
		require.True(t, s.Contains(k))
	}

	// A clone is its own set, whether its source is a list or a map.
	clone := o.Clone()
	require.True(t, clone.Remove(7))
	require.True(t, o.Contains(7), "removing from a clone leaves its source")
	// Removing the clone's first member moves the rest within its array, so
	// a clone that shared its source's array would change the source.
	small := idSetOf[uint64](1, 2)
	smallClone := small.Clone()
	require.True(t, smallClone.Remove(1))
	require.True(t, smallClone.Insert(3))
	require.Equal(t, []uint64{1, 2}, slices.Collect(small.Items()), "changing a clone leaves its source")

	// A set that became a map answers as a set while it drains, below the
	// list's limit and to empty.
	for k := range uint64(2 * idSetListMax) {
		require.True(t, o.Remove(k*7), "remove %d", k*7)
		require.Equal(t, int(2*idSetListMax-k-1), o.Size())
		require.False(t, o.Contains(k*7))
	}
	require.True(t, o.Empty())
	require.Empty(t, slices.Collect(o.Items()))
	require.True(t, o.Insert(5))
	require.Equal(t, []uint64{5}, slices.Collect(o.Items()))
	var none *idSet[uint64]
	require.Zero(t, none.Size())
	require.False(t, none.Contains(1))
	require.Empty(t, slices.Collect(none.All()))
}
