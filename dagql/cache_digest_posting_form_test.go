package dagql

import (
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// digestPostings answer as a map of a digest's exact results and its shared
// snapshot would, through random inserts, removes and restored snapshots.
// After every step a digest is in one of its two maps at most, and in the
// slot map only while its sole posting is one exact result.
func TestDigestPostingsActAsTheirPostings(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(7, 8))
	digests := []string{"a", "b", "c", "d", "e", "f"}
	var p digestPostings
	exact := map[string]map[sharedResultID]struct{}{}
	snapshot := map[string]*idSet[sharedResultID]{}
	want := func(dig string) []sharedResultID {
		members := map[sharedResultID]struct{}{}
		for id := range exact[dig] {
			members[id] = struct{}{}
		}
		for id := range snapshot[dig].All() {
			members[id] = struct{}{}
		}
		return slices.Sorted(maps.Keys(members))
	}
	for step := range 20000 {
		dig := digests[rng.IntN(len(digests))]
		id := sharedResultID(1 + rng.IntN(6))
		switch op := rng.IntN(20); {
		case op == 0:
			// A restore makes the digest's postings a snapshot shared with
			// the next digest, as a restored class's digests share theirs.
			members := idSetOf(id, sharedResultID(1+rng.IntN(6)))
			next := digests[(slices.Index(digests, dig)+1)%len(digests)]
			for _, d := range []string{dig, next} {
				delete(p.one, d)
				p.putRest(d, &digestPosting{imported: members})
				delete(exact, d)
				snapshot[d] = members
			}
		case op < 9:
			p.Remove(dig, id)
			delete(exact[dig], id)
			// The snapshot is shared: removing from it removes from
			// every digest that holds it.
			snapshot[dig].Remove(id)
		default:
			had := slices.Contains(want(dig), id)
			require.Equal(t, !had, p.Insert(dig, id), "step %d: insert %d under %s", step, id, dig)
			if !had {
				if exact[dig] == nil {
					exact[dig] = map[sharedResultID]struct{}{}
				}
				exact[dig][id] = struct{}{}
			}
		}
		for _, d := range digests {
			members := want(d)
			require.Equal(t, members, slices.Sorted(p.Items(d)), "step %d: %s", step, d)
			require.Equal(t, len(members), p.Size(d), "step %d: %s", step, d)
			for id := range sharedResultID(8) {
				require.Equal(t, slices.Contains(members, id), p.Contains(d, id), "step %d: %s has %d", step, d, id)
			}
			_, inOne := p.one[d]
			posting, inRest := p.rest[d]
			require.False(t, inOne && inRest, "step %d: %s in both maps", step, d)
			if inRest && posting.imported == nil {
				require.Greater(t, posting.exact.Size(), 1, "step %d: %s holds one exact result outside its slot", step, d)
			}
		}
		var listed []string
		for d := range p.Digests() {
			listed = append(listed, d)
		}
		var nonEmpty []string
		for _, d := range digests {
			if len(want(d)) > 0 {
				nonEmpty = append(nonEmpty, d)
			}
		}
		require.ElementsMatch(t, nonEmpty, listed, "step %d", step)
		require.Equal(t, len(nonEmpty), p.Len(), "step %d", step)
	}
}
