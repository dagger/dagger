package dagui

import "go.opentelemetry.io/otel/trace"

type OrderedSet[K, V comparable] struct {
	Order   []V
	KeyFunc func(V) K
	Map     map[K]V
}

func (set *OrderedSet[K, V]) Add(value V) {
	key := set.KeyFunc(value)
	if _, ok := set.Map[key]; ok {
		return
	}
	set.Map[key] = value
	set.Order = append(set.Order, value)
}

func (set *OrderedSet[K, V]) Remove(value V) {
	key := set.KeyFunc(value)
	if _, ok := set.Map[key]; !ok {
		return
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
}

func NewOrderedSet[K comparable, V comparable](keyFunc func(V) K) *OrderedSet[K, V] {
	return &OrderedSet[K, V]{
		Order:   []V{},
		KeyFunc: keyFunc,
		Map:     map[K]V{},
	}
}

func NewSpanSet() *OrderedSet[trace.SpanID, *Span] {
	return NewOrderedSet(func(span *Span) trace.SpanID {
		return span.ID
	})
}
