package dagui

import (
	"encoding/json"
	"iter"
	"slices"
	"sort"
)

type OrderedSet[K comparable, V any] struct {
	Order    []V
	KeyFunc  func(V) K
	LessFunc func(V, V) bool

	// Map indexes Order by key. A small set (see newSmallSpanSet) leaves it
	// nil until it outgrows smallSetMax values, so look values up with Has
	// and Get rather than reading Map directly.
	Map map[K]V

	// deferring is set between deferSort and settle. While it is set, Add
	// appends to Order instead of inserting in place, and Order is sorted
	// only up to sorted; settle sorts the rest and merges it in.
	deferring bool

	// small sets search Order linearly until they hold more than
	// smallSetMax values, sparing the map for the many sets that stay tiny.
	small bool

	// frozen marks the shared empty span set (emptySpanSet), which must
	// never be added to.
	frozen bool

	sorted int
}

// smallSetMax is the most values a small set holds before indexing them in
// a map. A linear scan over this many keys costs about as much as hashing
// one.
const smallSetMax = 8

func NewSet[T comparable]() *OrderedSet[T, T] {
	return &OrderedSet[T, T]{
		Order:   []T{},
		KeyFunc: func(v T) T { return v },
		Map:     map[T]T{},
	}
}

func NewOrderedSet[K comparable, V any](keyFunc func(V) K, vs ...V) *OrderedSet[K, V] {
	set := &OrderedSet[K, V]{
		KeyFunc: keyFunc,
	}
	for _, v := range vs {
		set.Add(v)
	}
	return set
}

func NewSpanSet(spans ...*Span) *OrderedSet[SpanID, *Span] {
	set := NewOrderedSet(spanKeyFunc)
	set.LessFunc = byStartTime
	for _, span := range spans {
		set.Add(span)
	}
	return set
}

// newSmallSpanSet returns a span set that expects to stay small: it skips
// the map index until it holds more than smallSetMax spans.
func newSmallSpanSet() SpanSet {
	return &OrderedSet[SpanID, *Span]{
		KeyFunc:  spanKeyFunc,
		LessFunc: byStartTime,
		small:    true,
	}
}

// emptySpanSet is the shared, immutable empty set every span relation (a
// Span's ChildSpans, RunningSpans and so on) starts out as. Most of them stay
// empty forever, so they share this one instead of each allocating its own,
// and reads need no nil checks. Writes go through addSpan and removeSpan,
// which swap in a set of the span's own on the first add; adding to
// emptySpanSet directly panics.
var emptySpanSet = &OrderedSet[SpanID, *Span]{
	KeyFunc:  spanKeyFunc,
	LessFunc: byStartTime,
	small:    true,
	frozen:   true,
}

// SpanSetAdd adds span to the span relation *set, giving the relation a set
// of its own first if it still shares emptySpanSet. It reports whether span
// was added. Use it for every write to a Span's relation sets (ChildSpans,
// RevealedSpans, ErrorOrigins, ProgressSpans, ...).
func SpanSetAdd(set *SpanSet, span *Span) bool {
	if *set == nil || (*set).frozen {
		*set = newSmallSpanSet()
	}
	return (*set).Add(span)
}

// SpanSetRemove removes span from the span relation *set, returning the
// relation to the shared emptySpanSet once it is empty so its storage can be
// collected. It reports whether span was removed.
func SpanSetRemove(set *SpanSet, span *Span) bool {
	if *set == nil || (*set).frozen {
		return false
	}
	if !(*set).Remove(span) {
		return false
	}
	if len((*set).Order) == 0 && !(*set).deferring {
		*set = emptySpanSet
	}
	return true
}

func byStartTime(a, b *Span) bool {
	return a.StartTime.Before(b.StartTime)
}

func spanKeyFunc(span *Span) SpanID {
	return span.ID
}

func (set *OrderedSet[K, V]) MarshalJSON() ([]byte, error) {
	return json.Marshal(set.Order)
}

func (set *OrderedSet[K, V]) UnmarshalJSON(p []byte) error {
	var vs []V
	if err := json.Unmarshal(p, &vs); err != nil {
		return err
	}
	for _, v := range vs {
		set.Add(v)
	}
	return nil
}

func (set *OrderedSet[K, V]) Add(value V) bool {
	if set.frozen {
		panic("dagui: Add on the shared empty span set; use SpanSetAdd")
	}
	key := set.KeyFunc(value)
	if set.Has(key) {
		return false
	}
	if set.Map != nil || !set.small || len(set.Order) >= smallSetMax {
		if set.Map == nil {
			// first value of a regular set, or a small set outgrowing its
			// linear search
			set.Map = make(map[K]V, len(set.Order)+1)
			for _, v := range set.Order {
				set.Map[set.KeyFunc(v)] = v
			}
		}
		set.Map[key] = value
	}
	if set.LessFunc != nil && !set.deferring {
		set.Order = insert(set.Order, value, set.LessFunc)
	} else {
		set.Order = append(set.Order, value)
	}
	return true
}

// Has reports whether the set holds a value with the given key. A nil set is
// empty.
func (set *OrderedSet[K, V]) Has(key K) bool {
	_, ok := set.Get(key)
	return ok
}

// Get returns the value with the given key, if the set holds one. A nil set
// is empty.
func (set *OrderedSet[K, V]) Get(key K) (V, bool) {
	if set == nil {
		var zero V
		return zero, false
	}
	if set.Map != nil || !set.small {
		v, ok := set.Map[key]
		return v, ok
	}
	for _, v := range set.Order {
		if set.KeyFunc(v) == key {
			return v, true
		}
	}
	var zero V
	return zero, false
}

// Len returns the number of values in the set. A nil set is empty.
func (set *OrderedSet[K, V]) Len() int {
	if set == nil {
		return 0
	}
	return len(set.Order)
}

func (set *OrderedSet[K, V]) Remove(value V) bool {
	if set.frozen {
		return false
	}
	key := set.KeyFunc(value)
	if !set.Has(key) {
		return false
	}
	delete(set.Map, key)
	var removeIdx int
	for i, v := range set.Order {
		if set.KeyFunc(v) == key {
			removeIdx = i
			break
		}
	}
	set.Order = slices.Delete(set.Order, removeIdx, removeIdx+1)
	if removeIdx < set.sorted {
		set.sorted--
	}
	if len(set.Order) == 0 {
		// Go maps never shrink, and a set that once held many values (say,
		// an ancestor's RunningSpans) would otherwise pin their storage
		// forever.
		set.Order = nil
		set.Map = nil
	}
	return true
}

func (set *OrderedSet[K, V]) Clear() {
	if set.frozen {
		return
	}
	set.Order = nil
	set.sorted = 0
	clear(set.Map)
}

func (set *OrderedSet[K, V]) Iter() iter.Seq[V] {
	return func(f func(V) bool) {
		for _, v := range set.Order {
			if !f(v) {
				break
			}
		}
	}
}

// deferSort makes Add append without sorting until settle is called.
//
// Inserting into a sorted slice is O(n) when the value doesn't belong at the
// end, so adding a large number of values out of order -- e.g. importing a
// trace whose spans don't arrive in start-time order -- is quadratic. Between
// deferSort and settle, Order still holds every value, but only a prefix of it
// is sorted; settle sorts the appended values once and merges them in.
//
// Values are ordered by their sort key when settled rather than when added,
// which only differs for values whose key changes in between.
func (set *OrderedSet[K, V]) deferSort() {
	if set.deferring || set.LessFunc == nil {
		return
	}
	set.deferring = true
	set.sorted = len(set.Order)
}

// settle ends a deferSort, leaving Order sorted. Values that compare equal
// keep the order they were added in, exactly as if each had been inserted in
// place.
//
// Comparisons are what cost here (for a span set each one chases two
// pointers), not moving values around: so each new value is placed by binary
// search, and the values after it are moved up in one block.
func (set *OrderedSet[K, V]) settle() {
	if !set.deferring {
		return
	}
	set.deferring = false
	sorted := set.sorted
	set.sorted = 0

	less := set.LessFunc
	tail := set.Order[sorted:]
	if len(tail) == 0 {
		return
	}
	slices.SortStableFunc(tail, func(a, b V) int {
		switch {
		case less(a, b):
			return -1
		case less(b, a):
			return 1
		default:
			return 0
		}
	})

	// Merge the sorted tail into the sorted prefix from the back. Each tail
	// value lands after the prefix values that sort before or equal to it
	// (those were added earlier), and the prefix values after it shift up
	// to make room. An in-order append moves nothing.
	tail = slices.Clone(tail)
	hi, w := sorted, len(set.Order)
	for j := len(tail) - 1; j >= 0; j-- {
		t := tail[j]
		pos := sort.Search(hi, func(i int) bool {
			return less(t, set.Order[i])
		})
		w -= hi - pos
		copy(set.Order[w:], set.Order[pos:hi])
		w--
		set.Order[w] = t
		hi = pos
	}
}

func insert[T any](slice []T, value T, less func(a, b T) bool) []T {
	// Find insertion point using binary search. Insert after existing values that
	// compare equal so callers keep insertion order for ties.
	left, right := 0, len(slice)
	for left < right {
		mid := (left + right) / 2
		if less(value, slice[mid]) {
			right = mid
		} else {
			left = mid + 1
		}
	}

	// Insert at the found position (left)
	slice = append(slice, value)
	copy(slice[left+1:], slice[left:])
	slice[left] = value
	return slice
}
