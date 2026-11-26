package dagql

import (
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// A set of entry numbers answers as a map of them would, through any mix
// of adds and removes, including removes from either end of a long list.
func TestResultIDSetActsAsASet(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	var s resultIDSet
	want := map[sharedResultID]struct{}{}
	for step := range 5000 {
		id := sharedResultID(1 + rng.IntN(500))
		_, had := want[id]
		if rng.IntN(3) == 0 {
			require.Equal(t, had, s.remove(id), "step %d: remove %d", step, id)
			delete(want, id)
		} else {
			require.Equal(t, !had, s.add(id), "step %d: add %d", step, id)
			want[id] = struct{}{}
		}
		require.Equal(t, slices.Sorted(maps.Keys(want)), []sharedResultID(s), "step %d", step)
		require.Equal(t, len(want) == 0, s == nil, "step %d: an empty set is nil", step)
		_, has := want[id]
		require.Equal(t, has, s.has(id), "step %d: has %d", step, id)
	}
}
