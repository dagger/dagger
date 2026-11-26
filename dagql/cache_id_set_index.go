package dagql

import (
	"cmp"
	"iter"
)

// idSetIndex maps each key to a set of members, as the e-graph's indexes do:
// the entries seen for a term, the terms seen for an entry, the entries posted
// under a digest. Nearly every key has one member, so a key's only member is
// held in the map's own slot, and a set (idSet) is made only for a key with
// two or more: one member costs its key a slot and nothing else, where a set
// of its own costs two allocations beside the slot. A key whose set falls
// back to one member moves back into a slot. A key is in one of the two maps
// at most. The zero value is empty and ready for use. Like the e-graph, it is
// guarded by egraphMu.
type idSetIndex[K comparable, V cmp.Ordered] struct {
	one  map[K]V
	many map[K]*idSet[V]
}

// idMembers are one key's members, as Members finds them, for reading with
// one lookup: they are the key's until its members next change.
type idMembers[V cmp.Ordered] struct {
	one    V
	hasOne bool
	many   *idSet[V]
}

// Members returns k's members.
func (x *idSetIndex[K, V]) Members(k K) idMembers[V] {
	if only, ok := x.one[k]; ok {
		return idMembers[V]{one: only, hasOne: true}
	}
	return idMembers[V]{many: x.many[k]}
}

// Size is how many members there are.
func (m idMembers[V]) Size() int {
	if m.hasOne {
		return 1
	}
	return m.many.Size()
}

// Contains reports whether v is a member.
func (m idMembers[V]) Contains(v V) bool {
	if m.hasOne {
		return m.one == v
	}
	return m.many.Contains(v)
}

// All yields each member once, in no order a caller may rely on.
func (m idMembers[V]) All() iter.Seq[V] {
	if m.hasOne {
		return func(yield func(V) bool) { yield(m.one) }
	}
	return m.many.All()
}

// Items yields each member once, in ascending order.
func (m idMembers[V]) Items() iter.Seq[V] {
	if m.hasOne {
		return func(yield func(V) bool) { yield(m.one) }
	}
	return m.many.Items()
}

// Insert adds v to k's members, and reports whether k didn't have it.
func (x *idSetIndex[K, V]) Insert(k K, v V) bool {
	if only, ok := x.one[k]; ok {
		if only == v {
			return false
		}
		delete(x.one, k)
		if x.many == nil {
			x.many = make(map[K]*idSet[V])
		}
		x.many[k] = idSetOf(only, v)
		return true
	}
	if set := x.many[k]; set != nil {
		return set.Insert(v)
	}
	if x.one == nil {
		x.one = make(map[K]V)
	}
	x.one[k] = v
	return true
}

// Remove removes v from k's members, and reports whether k had it.
func (x *idSetIndex[K, V]) Remove(k K, v V) bool {
	if only, ok := x.one[k]; ok {
		if only != v {
			return false
		}
		delete(x.one, k)
		return true
	}
	set := x.many[k]
	if !set.Remove(v) {
		return false
	}
	if set.Size() > 1 {
		return true
	}
	delete(x.many, k)
	for last := range set.All() {
		if x.one == nil {
			x.one = make(map[K]V)
		}
		x.one[k] = last
	}
	return true
}

// Move adds every member of src to dst's members and removes src. When dst
// has no set, src's set itself becomes dst's, so a join of a class into one
// with fewer members copies nothing.
func (x *idSetIndex[K, V]) Move(src, dst K) {
	if src == dst {
		return
	}
	if only, ok := x.one[src]; ok {
		delete(x.one, src)
		x.Insert(dst, only)
		return
	}
	set := x.many[src]
	if set == nil {
		return
	}
	delete(x.many, src)
	if dstSet := x.many[dst]; dstSet != nil {
		dstSet.InsertAll(set)
		return
	}
	if only, ok := x.one[dst]; ok {
		delete(x.one, dst)
		set.Insert(only)
	}
	x.many[dst] = set
}

// Clone returns a set of k's members of its own.
func (x *idSetIndex[K, V]) Clone(k K) *idSet[V] {
	out := newIDSet[V]()
	for v := range x.All(k) {
		out.Insert(v)
	}
	return out
}

// Delete removes k and all its members.
func (x *idSetIndex[K, V]) Delete(k K) {
	delete(x.one, k)
	delete(x.many, k)
}

// Contains reports whether k has v.
func (x *idSetIndex[K, V]) Contains(k K, v V) bool {
	if only, ok := x.one[k]; ok {
		return only == v
	}
	return x.many[k].Contains(v)
}

// Size is how many members k has.
func (x *idSetIndex[K, V]) Size(k K) int {
	if _, ok := x.one[k]; ok {
		return 1
	}
	return x.many[k].Size()
}

// All yields each of k's members once, in no order a caller may rely on.
// The caller changes no key's members while it iterates.
func (x *idSetIndex[K, V]) All(k K) iter.Seq[V] {
	return func(yield func(V) bool) {
		if only, ok := x.one[k]; ok {
			yield(only)
			return
		}
		for v := range x.many[k].All() {
			if !yield(v) {
				return
			}
		}
	}
}

// Items yields each of k's members once, in ascending order. The caller
// changes no key's members while it iterates.
func (x *idSetIndex[K, V]) Items(k K) iter.Seq[V] {
	return func(yield func(V) bool) {
		if only, ok := x.one[k]; ok {
			yield(only)
			return
		}
		for v := range x.many[k].Items() {
			if !yield(v) {
				return
			}
		}
	}
}

// Len is how many keys have members.
func (x *idSetIndex[K, V]) Len() int {
	return len(x.one) + len(x.many)
}

// Keys yields each key that has members once, in no order a caller may rely
// on. The caller changes no key's members while it iterates.
func (x *idSetIndex[K, V]) Keys() iter.Seq[K] {
	return func(yield func(K) bool) {
		for k := range x.one {
			if !yield(k) {
				return
			}
		}
		for k := range x.many {
			if !yield(k) {
				return
			}
		}
	}
}
