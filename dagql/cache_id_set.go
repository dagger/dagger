package dagql

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
)

// idSetListMax is the most members an idSet holds as a sorted list. One
// larger holds them in a map.
const idSetListMax = 64

// idSet is a set of the cache's numbers or digests, as the e-graph's
// indexes keep them, most of which hold one member and few more than a
// handful. While it holds at most idSetListMax it is a sorted list: eight
// bytes a number, where a map takes over a hundred before its first key.
// Past that it is a map, so that joining a small set into a large one, as
// a union of classes does, costs the small one's size, and so does adding
// or removing a member. A nil *idSet is empty for reading.
type idSet[K cmp.Ordered] struct {
	list []K
	big  map[K]struct{}
}

// newIDSet returns an empty set.
func newIDSet[K cmp.Ordered]() *idSet[K] {
	return &idSet[K]{}
}

// Size is how many members s holds.
func (s *idSet[K]) Size() int {
	if s == nil {
		return 0
	}
	if s.big != nil {
		return len(s.big)
	}
	return len(s.list)
}

// Empty reports whether s holds no member.
func (s *idSet[K]) Empty() bool {
	return s.Size() == 0
}

// Contains reports whether s holds k.
func (s *idSet[K]) Contains(k K) bool {
	if s == nil {
		return false
	}
	if s.big != nil {
		_, ok := s.big[k]
		return ok
	}
	_, ok := slices.BinarySearch(s.list, k)
	return ok
}

// Insert adds k to s, and reports whether s didn't hold it.
func (s *idSet[K]) Insert(k K) bool {
	if s.big != nil {
		if _, ok := s.big[k]; ok {
			return false
		}
		s.big[k] = struct{}{}
		return true
	}
	i, ok := slices.BinarySearch(s.list, k)
	if ok {
		return false
	}
	if len(s.list) < idSetListMax {
		s.list = slices.Insert(s.list, i, k)
		return true
	}
	s.big = make(map[K]struct{}, len(s.list)+1)
	for _, m := range s.list {
		s.big[m] = struct{}{}
	}
	s.big[k] = struct{}{}
	s.list = nil
	return true
}

// Remove removes k from s, and reports whether s held it. A set that has
// become a map stays one.
func (s *idSet[K]) Remove(k K) bool {
	if s == nil {
		return false
	}
	if s.big != nil {
		if _, ok := s.big[k]; !ok {
			return false
		}
		delete(s.big, k)
		return true
	}
	i, ok := slices.BinarySearch(s.list, k)
	if !ok {
		return false
	}
	s.list = slices.Delete(s.list, i, i+1)
	return true
}

// InsertAll adds every member of o to s, at the cost of o's size while s is
// a map.
func (s *idSet[K]) InsertAll(o *idSet[K]) {
	for k := range o.All() {
		s.Insert(k)
	}
}

// All yields each member of s once, in no order a caller may rely on: in
// ascending order while s is a list, in a map's order once it is a map.
func (s *idSet[K]) All() iter.Seq[K] {
	return func(yield func(K) bool) {
		if s == nil {
			return
		}
		if s.big != nil {
			for k := range s.big {
				if !yield(k) {
					return
				}
			}
			return
		}
		for _, k := range s.list {
			if !yield(k) {
				return
			}
		}
	}
}

// Items yields each member of s once, in ascending order. A set that has
// become a map sorts its members to do so.
func (s *idSet[K]) Items() iter.Seq[K] {
	return func(yield func(K) bool) {
		if s == nil {
			return
		}
		list := s.list
		if s.big != nil {
			list = slices.Sorted(maps.Keys(s.big))
		}
		for _, k := range list {
			if !yield(k) {
				return
			}
		}
	}
}

// Clone returns a copy of s.
func (s *idSet[K]) Clone() *idSet[K] {
	if s == nil {
		return newIDSet[K]()
	}
	return &idSet[K]{list: slices.Clone(s.list), big: maps.Clone(s.big)}
}

// String lists s's members in ascending order.
func (s *idSet[K]) String() string {
	return fmt.Sprint(slices.Collect(s.Items()))
}

// idSetOf returns a set of ks.
func idSetOf[K cmp.Ordered](ks ...K) *idSet[K] {
	s := newIDSet[K]()
	for _, k := range ks {
		s.Insert(k)
	}
	return s
}
