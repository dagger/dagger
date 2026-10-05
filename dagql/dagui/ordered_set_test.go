package dagui

import (
	"math/rand"
	"slices"
	"testing"
)

func TestOrderedSetStableForEqualValues(t *testing.T) {
	type value struct {
		key  string
		rank int
	}

	set := NewOrderedSet(func(v value) string { return v.key })
	set.LessFunc = func(a, b value) bool { return a.rank < b.rank }

	set.Add(value{key: "first"})
	set.Add(value{key: "second"})
	set.Add(value{key: "third", rank: 1})

	got := []string{set.Order[0].key, set.Order[1].key, set.Order[2].key}
	want := []string{"first", "second", "third"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

type rankedValue struct {
	key  int
	rank int
}

func newRankedSet() *OrderedSet[int, rankedValue] {
	set := NewOrderedSet(func(v rankedValue) int { return v.key })
	set.LessFunc = func(a, b rankedValue) bool { return a.rank < b.rank }
	return set
}

func setKeys(set *OrderedSet[int, rankedValue]) []int {
	keys := make([]int, len(set.Order))
	for i, v := range set.Order {
		keys[i] = v.key
	}
	return keys
}

// Deferring the sort must leave exactly the order that inserting each value
// in place would have, ties included, however the values are batched.
func TestOrderedSetDeferredSortMatchesInsertion(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for round := range 200 {
		n := rng.Intn(300)
		values := make([]rankedValue, n)
		for i := range values {
			// few distinct ranks, so plenty of ties; some duplicate keys
			values[i] = rankedValue{key: rng.Intn(n + 1), rank: rng.Intn(20)}
		}

		inserted := newRankedSet()
		for _, v := range values {
			inserted.Add(v)
		}

		deferred := newRankedSet()
		for chunk := range slices.Chunk(values, 1+rng.Intn(50)) {
			deferred.deferSort()
			for _, v := range chunk {
				deferred.Add(v)
			}
			deferred.settle()
		}

		if got, want := setKeys(deferred), setKeys(inserted); !slices.Equal(got, want) {
			t.Fatalf("round %d: deferred order = %v, want %v", round, got, want)
		}
	}
}

func TestOrderedSetRemoveWhileDeferred(t *testing.T) {
	ranks := []int{3, 1, 4, 1, 5, 9, 2, 6}
	set := newRankedSet()
	for i := range 4 {
		set.Add(rankedValue{key: i, rank: ranks[i]})
	}
	set.deferSort()
	for i := 4; i < len(ranks); i++ {
		set.Add(rankedValue{key: i, rank: ranks[i]})
	}
	set.Remove(set.Map[0]) // from the sorted prefix
	set.Remove(set.Map[6]) // from the unsorted tail
	set.settle()
	if got, want := setKeys(set), []int{1, 3, 2, 4, 7, 5}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
