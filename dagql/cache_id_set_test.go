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
	for step := range 20000 {
		// Grow past the list's limit, then shrink back below it.
		span := 40 + 4*idSetListMax*(step/5000%2)
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
		require.Equal(t, slices.Sorted(maps.Keys(want)), slices.Collect(s.Items()), "step %d", step)
		require.ElementsMatch(t, slices.Collect(maps.Keys(want)), slices.Collect(s.All()), "step %d", step)
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
	var none *idSet[uint64]
	require.Zero(t, none.Size())
	require.False(t, none.Contains(1))
	require.Empty(t, slices.Collect(none.All()))
}
