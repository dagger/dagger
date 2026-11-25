package artifact

import (
	"context"
	"encoding/binary"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"golang.org/x/sync/errgroup"
)

type AddressRow struct {
	Type        string
	Coordinates []flattening.Coordinate
}

type AddressIndex struct {
	mu       sync.Mutex
	complete bool
	types    map[string]*addressType
	// invalid holds templates whose rows would make the index incomplete; each
	// is discovered before the first plan, since only a nonempty one counts.
	invalid []*addressTemplate
}

// AddressTemplate stands for the rows of one collection artifact whose keys
// are not yet known. Its rows have exactly its known coordinates plus one
// coordinate for each of its dimensions. Discover returns those rows; an index
// calls it only when they could change a plan.
//
// A template counts as able to match a candidate whenever its shape does,
// whatever its keys turn out to be. Resolving an address expands every
// collection whose shape matches its selectors, so a plan whose address
// resolution would expand a collection has always expanded it first: a
// collection that fails to expand costs only the plans that need it, and no
// address points at a collection its plan never saw.
type AddressTemplate struct {
	Type        string
	Coordinates []flattening.Coordinate
	Dimensions  []string
	Discover    func(context.Context) ([]AddressRow, error)
}

type addressTemplate struct {
	*AddressTemplate
	known      map[string]string
	dimensions map[string]bool
}

type addressType struct {
	rows             []map[string]uint32
	values           map[string]uint32
	full             map[string]int
	dimensions       map[string]struct{}
	projections      map[string][]uint64
	withoutDimension map[string][]uint64
	pending          []*addressTemplate
}

func NewAddressIndex(rows []AddressRow, complete bool) *AddressIndex {
	index := &AddressIndex{complete: complete, types: make(map[string]*addressType)}
	for _, row := range rows {
		index.add(row)
	}
	return index
}

// NewLazyAddressIndex indexes rows and templates whose rows are discovered
// only when they could change a plan. Its plans equal those of NewAddressIndex
// over the rows and every template's discovered rows.
func NewLazyAddressIndex(rows []AddressRow, templates []*AddressTemplate) *AddressIndex {
	index := NewAddressIndex(rows, true)
	for _, template := range templates {
		pending := &addressTemplate{AddressTemplate: template, known: map[string]string{}, dimensions: map[string]bool{}}
		var dimensions []flattening.Coordinate
		for _, coordinate := range template.Coordinates {
			pending.known[coordinate.Dimension] = coordinate.Key
			dimensions = append(dimensions, coordinate)
		}
		for _, dimension := range template.Dimensions {
			pending.dimensions[dimension] = true
			dimensions = append(dimensions, flattening.Coordinate{Dimension: dimension})
		}
		if _, valid := addressCoordinates(dimensions); template.Type == "" || !valid {
			index.invalid = append(index.invalid, pending)
			continue
		}
		group := index.group(template.Type)
		for _, coordinate := range dimensions {
			group.dimensions[coordinate.Dimension] = struct{}{}
		}
		group.pending = append(group.pending, pending)
	}
	return index
}

func (index *AddressIndex) group(typ string) *addressType {
	group := index.types[typ]
	if group == nil {
		group = &addressType{
			values:      make(map[string]uint32),
			full:        make(map[string]int),
			dimensions:  make(map[string]struct{}),
			projections: make(map[string][]uint64),
		}
		index.types[typ] = group
	}
	return group
}

func (index *AddressIndex) add(row AddressRow) {
	if row.Type == "" {
		index.complete = false
		return
	}
	group := index.group(row.Type)
	coordinates, valid := addressCoordinates(row.Coordinates)
	if !valid {
		index.complete = false
		return
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
		group.full[signature] = 0
	} else {
		group.full[signature] = rowID + 1
	}
	// Projections are over every row.
	clear(group.projections)
	group.withoutDimension = nil
}

// absorb replaces discovered templates with their rows.
func (index *AddressIndex) absorb(templates []*addressTemplate, rows [][]AddressRow) {
	for i, template := range templates {
		found := false
		index.invalid = slices.DeleteFunc(index.invalid, func(t *addressTemplate) bool {
			found = found || t == template
			return t == template
		})
		if group := index.types[template.Type]; group != nil {
			group.pending = slices.DeleteFunc(group.pending, func(t *addressTemplate) bool {
				found = found || t == template
				return t == template
			})
		}
		if !found {
			continue // another caller absorbed it first
		}
		for _, row := range rows[i] {
			index.add(row)
		}
	}
}

// PlanSelectors is Selectors for an index with templates: it discovers the
// templates whose rows could change target's plan, and only those.
func (index *AddressIndex) PlanSelectors(ctx context.Context, target AddressRow) ([]dagaddress.Pair, bool, error) {
	for {
		index.mu.Lock()
		selectors, ok, need := index.selectors(target)
		index.mu.Unlock()
		if len(need) == 0 {
			return selectors, ok, nil
		}
		rows := make([][]AddressRow, len(need))
		group, gctx := errgroup.WithContext(ctx)
		for i, template := range need {
			group.Go(func() error {
				discovered, err := template.Discover(gctx)
				rows[i] = discovered
				return err
			})
		}
		if err := group.Wait(); err != nil {
			return nil, false, err
		}
		index.mu.Lock()
		index.absorb(need, rows)
		index.mu.Unlock()
	}
}

func (index *AddressIndex) Selectors(target AddressRow) ([]dagaddress.Pair, bool) {
	if index == nil {
		return nil, false
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	selectors, ok, _ := index.selectors(target)
	return selectors, ok
}

// selectors finds target's plan, or the templates it must discover first.
// Without templates it never needs any.
func (index *AddressIndex) selectors(target AddressRow) (_ []dagaddress.Pair, _ bool, need []*addressTemplate) {
	if len(index.invalid) > 0 {
		return nil, false, slices.Clone(index.invalid)
	}
	if !index.complete {
		return nil, false, nil
	}
	group := index.types[target.Type]
	if group == nil {
		return nil, false, nil
	}
	depth := make(map[string]int, len(target.Coordinates))
	for i, coordinate := range target.Coordinates {
		if !flattening.IsStaticDimension(coordinate.Dimension) {
			depth[coordinate.Dimension] = i
		}
	}
	coordinates, valid := addressCoordinates(target.Coordinates)
	if !valid {
		return nil, false, nil
	}
	keys := make(map[string]string, len(coordinates))
	for _, coordinate := range coordinates {
		keys[coordinate.Dimension] = coordinate.Key
	}
	// A template of the same shape could hold target's row or a duplicate.
	if need := group.colliders(keys, slices.Collect(maps.Keys(group.dimensions))); len(need) > 0 {
		return nil, false, need
	}
	rowID := group.full[addressSignature(coordinates)] - 1
	if rowID < 0 {
		return nil, false, nil
	}
	if len(group.rows) == 1 {
		if len(group.pending) > 0 {
			return nil, false, slices.Clone(group.pending)
		}
		return []dagaddress.Pair{}, true, nil
	}
	unique := func(dimensions []string) bool {
		if len(need) > 0 {
			return false
		}
		if !group.unique(dimensions, rowID) {
			return false
		}
		need = group.colliders(keys, dimensions)
		return len(need) == 0
	}
	slices.SortStableFunc(coordinates, func(a, b flattening.Coordinate) int {
		priority := func(dimension string) int {
			switch dimension {
			case flattening.ModuleDimension:
				return 2
			case flattening.NameDimension:
				return 1
			default:
				return 0
			}
		}
		if difference := priority(a.Dimension) - priority(b.Dimension); difference != 0 {
			return difference
		}
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
		if unique([]string{coordinate.Dimension}) {
			return []dagaddress.Pair{{Dimension: coordinate.Dimension, Key: coordinate.Key, HasKey: true}}, true, nil
		}
		if len(need) > 0 {
			return nil, false, need
		}
	}
	absent := slices.DeleteFunc(slices.Sorted(maps.Keys(group.dimensions)), func(id string) bool {
		_, exists := group.rows[rowID][id]
		return exists
	})
	var chosen []string
	freePresent, freeAbsent := present, absent
	requiredPresent, requiredAbsent := 0, 0
	requiredKnown := false
	findRequired := func() {
		required := group.requiredDimensions(rowID, unique)
		if len(need) > 0 {
			return
		}
		chosen = slices.Sorted(maps.Keys(required))
		freePresent = slices.DeleteFunc(slices.Clone(present), func(id string) bool { return required[id] })
		freeAbsent = slices.DeleteFunc(slices.Clone(absent), func(id string) bool { return required[id] })
		requiredPresent = len(present) - len(freePresent)
		requiredAbsent = len(absent) - len(freeAbsent)
		requiredKnown = true
	}
	if !unique(present) {
		if len(need) > 0 {
			return nil, false, need
		}
		findRequired()
		if len(need) > 0 {
			return nil, false, need
		}
	}
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
			if len(need) > 0 {
				return false
			}
		}
		return false
	}
	for size := max(1, len(chosen)); size <= len(present)+len(absent); size++ {
		if size == 3 && !requiredKnown {
			findRequired()
			if len(need) > 0 {
				return nil, false, need
			}
			size = max(size, len(chosen))
		}
		for missing := max(requiredAbsent, size-len(present)); missing <= min(size-requiredPresent, len(absent)); missing++ {
			found := choose(freePresent, 0, size-missing-requiredPresent, func() bool {
				return choose(freeAbsent, 0, missing-requiredAbsent, func() bool { return unique(chosen) })
			})
			if len(need) > 0 {
				return nil, false, need
			}
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
				return selectors, true, nil
			}
		}
	}
	return nil, false, nil
}

// colliders are the pending templates that could hold a row equal to the
// target's on dimensions, absent dimensions included.
func (group *addressType) colliders(keys map[string]string, dimensions []string) []*addressTemplate {
	var need []*addressTemplate
	for _, template := range group.pending {
		if template.couldMatch(keys, dimensions) {
			need = append(need, template)
		}
	}
	return need
}

func (template *addressTemplate) couldMatch(keys map[string]string, dimensions []string) bool {
	for _, dimension := range dimensions {
		key, present := keys[dimension]
		if known, ok := template.known[dimension]; ok {
			if !present || known != key {
				return false
			}
		} else if template.dimensions[dimension] != present {
			return false
		}
	}
	return true
}

// requiredDimensions are the dimensions without which rowID is not unique.
// With every row known, one projection per omitted dimension serves every
// row. Otherwise unique decides each test, so a lazy index can stop to
// discover rows; the caller checks for that before using the result.
func (group *addressType) requiredDimensions(rowID int, unique func([]string) bool) map[string]bool {
	required := map[string]bool{}
	if len(group.pending) == 0 {
		if group.withoutDimension == nil {
			dimensions := slices.Sorted(maps.Keys(group.dimensions))
			group.withoutDimension = make(map[string][]uint64, len(dimensions))
			for i, dimension := range dimensions {
				others := slices.Delete(slices.Clone(dimensions), i, i+1)
				group.withoutDimension[dimension] = group.uniqueRows(others)
			}
		}
		for dimension, bits := range group.withoutDimension {
			if bits[rowID/64]&(uint64(1)<<(rowID%64)) == 0 {
				required[dimension] = true
			}
		}
		return required
	}
	dimensions := slices.Sorted(maps.Keys(group.dimensions))
	for i, dimension := range dimensions {
		others := slices.Delete(slices.Clone(dimensions), i, i+1)
		if !unique(others) {
			required[dimension] = true
		}
	}
	return required
}

func (group *addressType) unique(dimensions []string, rowID int) bool {
	bits := group.uniqueRows(dimensions)
	return bits[rowID/64]&(uint64(1)<<(rowID%64)) != 0
}

func (group *addressType) uniqueRows(dimensions []string) []uint64 {
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
	return bits
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
