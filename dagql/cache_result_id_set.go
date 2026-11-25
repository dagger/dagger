package dagql

import "slices"

// resultIDSet is a set of entry numbers held as a list in ascending order:
// eight bytes a number, where a map takes several times that, and over a
// hundred bytes before its first. An entry's dependency edges are two of
// them (sharedResult.deps and depParents), and a large cache holds millions
// of edges. Adding or removing a number moves the numbers after it; a new
// entry's number is the highest yet, so an entry gaining a new dependent
// appends to its list. Ranging over it yields the numbers in order.
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

// remove removes id from s, and reports whether s held it. A set left
// empty is nil.
func (s *resultIDSet) remove(id sharedResultID) bool {
	i, ok := slices.BinarySearch(*s, id)
	if !ok {
		return false
	}
	*s = slices.Delete(*s, i, i+1)
	if len(*s) == 0 {
		*s = nil
	}
	return true
}
