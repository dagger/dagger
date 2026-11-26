package dagql

import "slices"

// resultIDSet is a set of entry numbers held as a list in ascending order:
// eight bytes a number, where a map takes several times that, and over a
// hundred bytes before its first. An entry's dependency edges are two of
// them (sharedResult.deps and depParents), and a large cache holds millions
// of edges. Adding a number moves the numbers after it, and removing one
// moves the shorter side of it. A new entry's number is the highest yet, so
// an entry gaining a new dependent appends to its list; and a prune takes
// the least recently used entries first, the oldest, whose numbers sit at
// the front of their dependencies' lists. Ranging over it yields the
// numbers in order.
type resultIDSet []sharedResultID

// has reports whether s holds id.
func (s resultIDSet) has(id sharedResultID) bool {
	_, ok := slices.BinarySearch(s, id)
	return ok
}

// add adds id to s, and reports whether s didn't hold it.
func (s *resultIDSet) add(id sharedResultID) bool {
	i, ok := slices.BinarySearch(*s, id)
	if ok {
		return false
	}
	*s = slices.Insert(*s, i, id)
	return true
}

// remove removes id from s, and reports whether s held it. It moves the
// numbers on the shorter side of id: those before it one place later, and
// s then starts one later in its array, or those after it one place
// earlier. A set left empty is nil.
func (s *resultIDSet) remove(id sharedResultID) bool {
	list := *s
	i, ok := slices.BinarySearch(list, id)
	if !ok {
		return false
	}
	switch {
	case len(list) == 1:
		*s = nil
	case i < len(list)/2:
		copy(list[1:i+1], list[:i])
		*s = list[1:]
	default:
		copy(list[i:], list[i+1:])
		*s = list[:len(list)-1]
	}
	return true
}
