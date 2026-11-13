package artifact

import (
	"encoding/binary"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
)

// AddressRow is one artifact in the workspace snapshot. Type is the URI's
// concrete type constraint. Coordinates contains its complete canonical keys,
// with collection ancestors ordered from parent to child.
type AddressRow struct {
	Type        string
	Coordinates []flattening.Coordinate
}

// AddressIndex finds minimum addresses against a complete, unfiltered snapshot.
// It does not infer key uniqueness from a schema or from a selected set of rows.
// Its input is copied. Selectors may be called concurrently.
type AddressIndex struct {
	mu       sync.Mutex
	complete bool
	types    map[string]*addressType
}

type addressType struct {
	rows        []map[string]uint32
	values      map[string]uint32
	full        map[string]int
	dimensions  map[string]struct{}
	projections map[string][]uint64
}

// NewAddressIndex records every artifact of every type in the snapshot.
// Set complete only when rows includes all workspace artifacts, before filters.
// An incomplete snapshot cannot prove that a shorter address is unique.
func NewAddressIndex(rows []AddressRow, complete bool) *AddressIndex {
	index := &AddressIndex{complete: complete, types: make(map[string]*addressType)}
	for _, row := range rows {
		if row.Type == "" {
			index.complete = false
			continue
		}
		group := index.types[row.Type]
		if group == nil {
			group = &addressType{
				values:      make(map[string]uint32),
				full:        make(map[string]int),
				dimensions:  make(map[string]struct{}),
				projections: make(map[string][]uint64),
			}
			index.types[row.Type] = group
		}
		coordinates, valid := addressCoordinates(row.Coordinates)
		if !valid {
			index.complete = false
			continue
		}
		rowID := len(group.rows)
		values := make(map[string]uint32, len(coordinates))
		for _, coordinate := range coordinates {
			valueID, exists := group.values[coordinate.Key]
			if !exists {
				valueID = uint32(len(group.values) + 1)
				group.values[coordinate.Key] = valueID
			}
			values[coordinate.Dimension] = valueID
			group.dimensions[coordinate.Dimension] = struct{}{}
		}
		group.rows = append(group.rows, values)
		signature := addressSignature(coordinates)
		if _, exists := group.full[signature]; exists {
			group.full[signature] = 0 // Identical coordinates cannot distinguish these rows.
		} else {
			group.full[signature] = rowID + 1
		}
	}
	return index
}

// Selectors returns the fewest predicates that identify target in its type.
// A predicate matches a present key or an absent dimension. Ties prefer fewer
// absence tests, then deeper collection keys, name, and module. Present pairs
// retain coordinate order; absent dimensions follow in identifier order.
// A true result with no pairs means the type alone identifies the artifact.
// A false result means the snapshot is incomplete, target is missing, or its
// full coordinates do not identify exactly one artifact.
func (index *AddressIndex) Selectors(target AddressRow) ([]dagaddress.Pair, bool) {
	if index == nil {
		return nil, false
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	if !index.complete {
		return nil, false
	}
	group := index.types[target.Type]
	if group == nil {
		return nil, false
	}
	depth := make(map[string]int, len(target.Coordinates))
	for i, coordinate := range target.Coordinates {
		if !flattening.IsStaticDimension(coordinate.Dimension) {
			depth[coordinate.Dimension] = i
		}
	}
	coordinates, valid := addressCoordinates(target.Coordinates)
	if !valid {
		return nil, false
	}
	rowID := group.full[addressSignature(coordinates)] - 1
	if rowID < 0 {
		return nil, false
	}
	if len(group.rows) == 1 {
		return []dagaddress.Pair{}, true
	}
	slices.SortStableFunc(coordinates, func(a, b flattening.Coordinate) int {
		priority := func(dimension string) int {
			switch {
			case dimension == flattening.ModuleDimension:
				return 2
			case dimension == flattening.NameDimension:
				return 1
			default:
				return 0
			}
		}
		if difference := priority(a.Dimension) - priority(b.Dimension); difference != 0 {
			return difference
		}
		// Canonical coordinates list collection ancestors from parent to child.
		if priority(a.Dimension) == 0 {
			if difference := depth[b.Dimension] - depth[a.Dimension]; difference != 0 {
				return difference
			}
		}
		return strings.Compare(a.Dimension, b.Dimension)
	})
	present := make([]string, len(coordinates))
	for i, coordinate := range coordinates {
		present[i] = coordinate.Dimension
		if group.unique([]string{coordinate.Dimension}, rowID) {
			return []dagaddress.Pair{{Dimension: coordinate.Dimension, Key: coordinate.Key, HasKey: true}}, true
		}
	}
	absent := slices.DeleteFunc(slices.Sorted(maps.Keys(group.dimensions)), func(id string) bool {
		_, exists := group.rows[rowID][id]
		return exists
	})
	// If another row differs in only one dimension, every valid address must
	// test that dimension. Keep these required predicates out of the search.
	// This also handles a parent with many independent collection children.
	required := map[string]bool{}
	if !group.unique(present, rowID) {
		for candidate, row := range group.rows {
			if candidate == rowID {
				continue
			}
			difference, count := "", 0
			for id := range group.dimensions {
				if row[id] != group.rows[rowID][id] {
					difference, count = id, count+1
					if count > 1 {
						break
					}
				}
			}
			if count == 1 {
				required[difference] = true
			}
		}
	}
	chosen := slices.Sorted(maps.Keys(required))
	freePresent := slices.DeleteFunc(slices.Clone(present), func(id string) bool { return required[id] })
	freeAbsent := slices.DeleteFunc(slices.Clone(absent), func(id string) bool { return required[id] })
	requiredPresent := len(present) - len(freePresent)
	requiredAbsent := len(absent) - len(freeAbsent)
	var choose func(candidates []string, start, remaining int, done func() bool) bool
	choose = func(candidates []string, start, remaining int, done func() bool) bool {
		if remaining == 0 {
			return done()
		}
		for i := start; i <= len(candidates)-remaining; i++ {
			chosen = append(chosen, candidates[i])
			if choose(candidates, i+1, remaining-1, done) {
				return true
			}
			chosen = chosen[:len(chosen)-1]
		}
		return false
	}
	for size := max(1, len(chosen)); size <= len(present)+len(absent); size++ {
		for missing := max(requiredAbsent, size-len(present)); missing <= min(size-requiredPresent, len(absent)); missing++ {
			found := choose(freePresent, 0, size-missing-requiredPresent, func() bool {
				return choose(freeAbsent, 0, missing-requiredAbsent, func() bool { return group.unique(chosen, rowID) })
			})
			if found {
				selectors := make([]dagaddress.Pair, 0, size)
				for _, coordinate := range target.Coordinates {
					if slices.Contains(chosen, coordinate.Dimension) {
						selectors = append(selectors, dagaddress.Pair{Dimension: coordinate.Dimension, Key: coordinate.Key, HasKey: true})
					}
				}
				for _, id := range absent {
					if slices.Contains(chosen, id) {
						selectors = append(selectors, dagaddress.Pair{Dimension: id, Absent: true})
					}
				}
				return selectors, true
			}
		}
	}
	return nil, false
}

// unique builds each projection once, across all matching rows. Keeping only
// unique-row bits bounds persistent storage to one bit per row per projection.
// For n rows and d dimensions in one type, the worst-case cost is O(n*d*2^d).
// Common single-key results and mandatory predicates avoid that search.
func (group *addressType) unique(dimensions []string, rowID int) bool {
	dimensions = slices.Sorted(slices.Values(dimensions))
	var encoded []byte
	for _, dimension := range dimensions {
		encoded = addressAppend(encoded, dimension)
	}
	projection := string(encoded)
	bits, exists := group.projections[projection]
	if !exists {
		matches := make(map[string]int, len(group.rows))
		encoded = make([]byte, 4*len(dimensions))
		for candidate, row := range group.rows {
			for i, dimension := range dimensions {
				// Value IDs start at one. Zero means the coordinate is absent.
				binary.LittleEndian.PutUint32(encoded[4*i:], row[dimension])
			}
			key := string(encoded)
			if _, exists := matches[key]; exists {
				matches[key] = 0
			} else {
				matches[key] = candidate + 1
			}
		}
		bits = make([]uint64, (len(group.rows)+63)/64)
		for _, candidate := range matches {
			if candidate != 0 {
				candidate--
				bits[candidate/64] |= uint64(1) << (candidate % 64)
			}
		}
		group.projections[projection] = bits
	}
	return bits[rowID/64]&(uint64(1)<<(rowID%64)) != 0
}

func addressCoordinates(coordinates []flattening.Coordinate) ([]flattening.Coordinate, bool) {
	coordinates = slices.Clone(coordinates)
	slices.SortFunc(coordinates, func(a, b flattening.Coordinate) int {
		return strings.Compare(a.Dimension, b.Dimension)
	})
	for i, coordinate := range coordinates {
		if coordinate.Dimension == "" || i > 0 && coordinates[i-1].Dimension == coordinate.Dimension {
			return nil, false
		}
	}
	return coordinates, true
}

func addressSignature(coordinates []flattening.Coordinate) string {
	var encoded []byte
	for _, coordinate := range coordinates {
		encoded = addressAppend(encoded, coordinate.Dimension)
		encoded = addressAppend(encoded, coordinate.Key)
	}
	return string(encoded)
}

func addressAppend(encoded []byte, value string) []byte {
	encoded = binary.AppendUvarint(encoded, uint64(len(value)))
	return append(encoded, value...)
}
