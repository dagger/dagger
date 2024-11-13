package dagui

import (
	"encoding/json"
)

type OrderedSet[K, V comparable] struct {
	Order    []V
	KeyFunc  func(V) K
	LessFunc func(V, V) bool
	Map      map[K]V
}

func NewSet[T comparable]() *OrderedSet[T, T] {
	return &OrderedSet[T, T]{
		Order:   []T{},
		KeyFunc: func(v T) T { return v },
		Map:     map[T]T{},
	}
}

func NewOrderedSet[K comparable, V comparable](keyFunc func(V) K, vs ...V) *OrderedSet[K, V] {
	set := &OrderedSet[K, V]{
		Order:   []V{},
		KeyFunc: keyFunc,
		Map:     map[K]V{},
	}
	for _, v := range vs {
		set.Add(v)
	}
	return set
}

func NewSpanSet(spans ...*Span) *OrderedSet[SpanID, *Span] {
	set := NewOrderedSet(func(span *Span) SpanID {
		return span.ID
	}, spans...)
	set.LessFunc = func(a, b *Span) bool {
		return a.StartTime.Before(b.StartTime)
	}
	return set
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
	key := set.KeyFunc(value)
	if _, ok := set.Map[key]; ok {
		return false
	}
	set.Map[key] = value
	if set.LessFunc != nil {
		set.Order = insert(set.Order, value, set.LessFunc)
	} else {
		set.Order = append(set.Order, value)
	}
	return true
}

func (set *OrderedSet[K, V]) Remove(value V) bool {
	key := set.KeyFunc(value)
	if _, ok := set.Map[key]; !ok {
		return false
	}
	delete(set.Map, key)
	var removeIdx int
	for i, v := range set.Order {
		if v == value {
			removeIdx = i
			break
		}
	}
	set.Order = append(set.Order[:removeIdx], set.Order[removeIdx+1:]...)
	return true
}

func (set *OrderedSet[K, V]) Clear() {
	set.Order = nil
	clear(set.Map)
}

func insert[T any](slice []T, value T, less func(a, b T) bool) []T {
	// Find insertion point using binary search
	left, right := 0, len(slice)
	for left < right {
		mid := (left + right) / 2
		if less(slice[mid], value) {
			left = mid + 1
		} else {
			right = mid
		}
	}

	// Insert at the found position (left)
	slice = append(slice, value)
	copy(slice[left+1:], slice[left:])
	slice[left] = value
	return slice
}
