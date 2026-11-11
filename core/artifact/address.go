package artifact

import (
	"encoding/binary"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/artifact/flattening"
)

// AddressRow is one artifact in the workspace snapshot. Type is the URI's
// concrete type constraint. Coordinates contains its complete canonical keys.
type AddressRow struct {
	Type        string
	Coordinates []flattening.Coordinate
}

// AddressIndex finds minimum addresses against a complete, unfiltered snapshot.
// It does not infer key uniqueness from a schema or from a selected set of rows.
// Its input is copied. Keys may be called concurrently.
type AddressIndex struct {
	mu       sync.Mutex
	complete bool
	types    map[string]*addressType
}

type addressType struct {
	rows        []map[string]uint32
	values      map[string]uint32
	full        map[string]int
	dimensions  map[string][]int
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
				dimensions:  make(map[string][]int),
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
			group.dimensions[coordinate.Dimension] = append(group.dimensions[coordinate.Dimension], rowID)
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

// Keys returns the fewest coordinate pairs that identify exactly target in its
// type. Ties prefer collections, with deeper collections first, then the type
// name and finally the module. Returned pairs retain target's canonical order.
// A true result with no pairs means the type alone identifies the artifact.
// A false result means the snapshot is incomplete, target is missing, or its
// full coordinates do not identify exactly one artifact.
func (index *AddressIndex) Keys(target AddressRow) ([]flattening.Coordinate, bool) {
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
	coordinates, valid := addressCoordinates(target.Coordinates)
	if !valid {
		return nil, false
	}
	rowID := group.full[addressSignature(coordinates)] - 1
	if rowID < 0 {
		return nil, false
	}
	if len(group.rows) == 1 {
		return []flattening.Coordinate{}, true
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
		// Canonical collection identifiers contain the complete parent path.
		if priority(a.Dimension) == 0 {
			if difference := strings.Count(b.Dimension, "/") - strings.Count(a.Dimension, "/"); difference != 0 {
				return difference
			}
		}
		return strings.Compare(a.Dimension, b.Dimension)
	})
	var chosen []string
	var search func(start, remaining int) bool
	search = func(start, remaining int) bool {
		if remaining == 0 {
			return group.unique(chosen, rowID)
		}
		for i := start; i <= len(coordinates)-remaining; i++ {
			chosen = append(chosen, coordinates[i].Dimension)
			if search(i+1, remaining-1) {
				return true
			}
			chosen = chosen[:len(chosen)-1]
		}
		return false
	}
	for size := 1; size <= len(coordinates); size++ {
		if search(0, size) {
			keys := make([]flattening.Coordinate, 0, size)
			for _, coordinate := range target.Coordinates {
				if slices.Contains(chosen, coordinate.Dimension) {
					keys = append(keys, coordinate)
				}
			}
			return keys, true
		}
	}
	return nil, false
}

// unique builds each projection once, across all matching rows. Keeping only
// unique-row bits bounds persistent storage to one bit per row per projection.
// For n rows with the same d dimensions, all queries cost O(n*d*2^d), rather
// than scanning the snapshot separately for every artifact.
func (group *addressType) unique(dimensions []string, rowID int) bool {
	dimensions = slices.Sorted(slices.Values(dimensions))
	var encoded []byte
	for _, dimension := range dimensions {
		encoded = addressAppend(encoded, dimension)
	}
	projection := string(encoded)
	bits, exists := group.projections[projection]
	if !exists {
		candidates := group.dimensions[dimensions[0]]
		for _, dimension := range dimensions[1:] {
			if rows := group.dimensions[dimension]; len(rows) < len(candidates) {
				candidates = rows
			}
		}
		matches := make(map[string]int, len(candidates))
		encoded = make([]byte, 4*len(dimensions))
		for _, candidate := range candidates {
			row := group.rows[candidate]
			present := true
			for i, dimension := range dimensions {
				value, exists := row[dimension]
				if !exists {
					present = false
					break
				}
				binary.LittleEndian.PutUint32(encoded[4*i:], value)
			}
			if !present {
				continue
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
