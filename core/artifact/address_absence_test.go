package artifact

import (
	"fmt"
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestAddressAbsentSelectors(t *testing.T) {
	coordinate := func(dimension, key string) flattening.Coordinate {
		return flattening.Coordinate{Dimension: dimension, Key: key}
	}
	row := func(coordinates ...flattening.Coordinate) AddressRow {
		return AddressRow{Type: "Check", Coordinates: coordinates}
	}
	present := func(dimension, key string) dagaddress.Pair {
		return dagaddress.Pair{Dimension: dimension, Key: key, HasKey: true}
	}
	absent := func(dimension string) dagaddress.Pair {
		return dagaddress.Pair{Dimension: dimension, Absent: true}
	}
	top := coordinate("Rows", "top")
	bottom := coordinate("Rows", "bottom")
	left := coordinate("Cell", "left")
	right := coordinate("Cell", "right")
	tests := []struct {
		name   string
		rows   []AddressRow
		target AddressRow
		want   []dagaddress.Pair
	}{
		{
			name:   "parent check excludes child cells",
			rows:   []AddressRow{row(top), row(top, left), row(top, right), row(bottom), row(bottom, left), row(bottom, right)},
			target: row(top),
			want:   []dagaddress.Pair{present("Rows", "top"), absent("Cell")},
		},
		{
			name:   "unique child needs only its cell",
			rows:   []AddressRow{row(top), row(top, left), row(bottom), row(bottom, right)},
			target: row(top, left),
			want:   []dagaddress.Pair{present("Cell", "left")},
		},
		{
			name:   "empty key is present",
			rows:   []AddressRow{row(top), row(top, coordinate("Cell", ""))},
			target: row(top, coordinate("Cell", "")),
			want:   []dagaddress.Pair{present("Cell", "")},
		},
		{
			name:   "absence differs from an empty key",
			rows:   []AddressRow{row(top), row(top, coordinate("Cell", ""))},
			target: row(top),
			want:   []dagaddress.Pair{absent("Cell")},
		},
		{
			name:   "one absent selector beats two present selectors",
			rows:   []AddressRow{row(top, left), row(top, right, coordinate("Extra", "")), row(bottom, left, coordinate("Extra", "x"))},
			target: row(top, left),
			want:   []dagaddress.Pair{absent("Extra")},
		},
		{
			name:   "equal count prefers a present selector",
			rows:   []AddressRow{row(top), row(bottom, left)},
			target: row(top),
			want:   []dagaddress.Pair{present("Rows", "top")},
		},
		{
			name:   "absent selector ties use dimension order",
			rows:   []AddressRow{row(), row(coordinate("Zulu", ""), coordinate("Alpha", "x"))},
			target: row(),
			want:   []dagaddress.Pair{absent("Alpha")},
		},
		{
			name:   "multiple absent selectors print in dimension order",
			rows:   []AddressRow{row(), row(coordinate("Zulu", "")), row(coordinate("Alpha", "x"))},
			target: row(),
			want:   []dagaddress.Pair{absent("Alpha"), absent("Zulu")},
		},
		{
			name:   "other types add no absent candidates",
			rows:   []AddressRow{row(top), {Type: "File", Coordinates: []flattening.Coordinate{left}}},
			target: row(top),
			want:   []dagaddress.Pair{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, rows := range [][]AddressRow{test.rows, reversedRows(test.rows)} {
				got, ok := NewAddressIndex(rows, true).Selectors(test.target)
				require.True(t, ok)
				require.Equal(t, test.want, got)
			}
		})
	}
}

func reversedRows(rows []AddressRow) []AddressRow {
	result := slices.Clone(rows)
	slices.Reverse(result)
	return result
}

func TestAddressMinimumSelectorsAgainstReference(t *testing.T) {
	// Each dimension has three states: absent, empty key, and nonempty key.
	// Check every nonempty subset of the nine possible rows.
	dimensions := []string{"A", "B"}
	universe := make([]AddressRow, 0, 9)
	for a := 0; a < 3; a++ {
		for b := 0; b < 3; b++ {
			row := AddressRow{Type: "Check"}
			for i, state := range []int{a, b} {
				if state != 0 {
					key := ""
					if state == 2 {
						key = "x"
					}
					row.Coordinates = append(row.Coordinates, flattening.Coordinate{Dimension: dimensions[i], Key: key})
				}
			}
			universe = append(universe, row)
		}
	}
	for snapshot := 1; snapshot < 1<<len(universe); snapshot++ {
		var rows []AddressRow
		for i, row := range universe {
			if snapshot&(1<<i) != 0 {
				rows = append(rows, row)
			}
		}
		index := NewAddressIndex(rows, true)
		for _, target := range rows {
			got, ok := index.Selectors(target)
			require.True(t, ok, "snapshot %d, target %v", snapshot, target)
			count, absences := referenceMinimum(rows, target)
			require.Len(t, got, count, "snapshot %d, target %v", snapshot, target)
			gotAbsences := 0
			matches := 0
			for _, selector := range got {
				if selector.Absent {
					gotAbsences++
				}
			}
			for _, row := range rows {
				if referenceMatches(row, got) {
					matches++
				}
			}
			require.Equal(t, absences, gotAbsences, "snapshot %d, target %v", snapshot, target)
			require.True(t, referenceMatches(target, got))
			require.Equal(t, 1, matches, "snapshot %d, target %v, selectors %v", snapshot, target, got)
		}
	}
}

func referenceMinimum(rows []AddressRow, target AddressRow) (int, int) {
	candidates := presentSelectors(target.Coordinates)
	for _, dimension := range []string{"A", "B"} {
		present := false
		seen := false
		for _, coordinate := range target.Coordinates {
			present = present || coordinate.Dimension == dimension
		}
		for _, row := range rows {
			for _, coordinate := range row.Coordinates {
				seen = seen || coordinate.Dimension == dimension
			}
		}
		if seen && !present {
			candidates = append(candidates, dagaddress.Pair{Dimension: dimension, Absent: true})
		}
	}
	bestCount, bestAbsent := len(candidates)+1, len(candidates)+1
	for subset := 0; subset < 1<<len(candidates); subset++ {
		var selectors []dagaddress.Pair
		absences := 0
		for i, candidate := range candidates {
			if subset&(1<<i) != 0 {
				selectors = append(selectors, candidate)
				if candidate.Absent {
					absences++
				}
			}
		}
		matches := 0
		for _, row := range rows {
			if referenceMatches(row, selectors) {
				matches++
			}
		}
		if matches == 1 && (len(selectors) < bestCount || len(selectors) == bestCount && absences < bestAbsent) {
			bestCount, bestAbsent = len(selectors), absences
		}
	}
	return bestCount, bestAbsent
}

func referenceMatches(row AddressRow, selectors []dagaddress.Pair) bool {
	for _, selector := range selectors {
		present := false
		key := ""
		for _, coordinate := range row.Coordinates {
			if coordinate.Dimension == selector.Dimension {
				present, key = true, coordinate.Key
			}
		}
		if selector.Absent {
			if present {
				return false
			}
		} else if !present || key != selector.Key {
			return false
		}
	}
	return true
}

func TestAddressManyRequiredAbsentSelectors(t *testing.T) {
	const count = 64
	target := AddressRow{Type: "Check", Coordinates: []flattening.Coordinate{
		{Dimension: "module", Key: "grid"},
		{Dimension: "name", Key: "check"},
	}}
	rows := []AddressRow{target}
	want := make([]dagaddress.Pair, count)
	for i := range count {
		dimension := fmt.Sprintf("Cell%02d", i)
		coordinates := slices.Clone(target.Coordinates)
		coordinates = append(coordinates, flattening.Coordinate{Dimension: dimension, Key: ""})
		rows = append(rows, AddressRow{Type: target.Type, Coordinates: coordinates})
		want[i] = dagaddress.Pair{Dimension: dimension, Absent: true}
	}
	// Each competitor differs in one independent dimension. Every absence is
	// required, so removing any selector would include another artifact.
	for _, snapshot := range [][]AddressRow{rows, reversedRows(rows)} {
		got, ok := NewAddressIndex(snapshot, true).Selectors(target)
		require.True(t, ok)
		require.Equal(t, want, got)
	}
}
