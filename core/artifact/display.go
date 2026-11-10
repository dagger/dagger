package artifact

import (
	"maps"
	"slices"

	"github.com/dagger/dagger/core/artifact/flattening"
)

// VisibleDimensions lists columns whose key sets differ across rows. A
// missing cell differs from every present key, including the empty string.
// Repeated coordinates are compared as sets, independent of their order.
func VisibleDimensions(rows [][]flattening.Coordinate) []string {
	type column struct {
		first   map[string]struct{}
		present int
		varies  bool
	}
	columns := map[string]*column{}
	for _, row := range rows {
		cells := map[string]map[string]struct{}{}
		for _, coordinate := range row {
			keys := cells[coordinate.Dimension]
			if keys == nil {
				keys = map[string]struct{}{}
				cells[coordinate.Dimension] = keys
			}
			keys[coordinate.Key] = struct{}{}
		}
		for dimension, keys := range cells {
			col := columns[dimension]
			if col == nil {
				col = &column{first: keys}
				columns[dimension] = col
			}
			col.present++
			col.varies = col.varies || !maps.Equal(col.first, keys)
		}
	}
	result := []string{}
	for dimension, col := range columns {
		if col.varies || col.present < len(rows) {
			result = append(result, dimension)
		}
	}
	slices.Sort(result)
	return result
}
