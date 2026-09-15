package dagui

import "testing"

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
