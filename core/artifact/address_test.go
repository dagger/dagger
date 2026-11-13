package artifact

import (
	"fmt"
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestAddressMinimumSelectors(t *testing.T) {
	c := func(dimension, key string) flattening.Coordinate {
		return flattening.Coordinate{Dimension: dimension, Key: key}
	}
	module := c("module", "go")
	parent := c("GoPackages", "api")
	child := c("GoTests", "unit")
	name := c(flattening.NameDimension, "run")
	row := func(coordinates ...flattening.Coordinate) AddressRow {
		return AddressRow{Type: "Check", Coordinates: coordinates}
	}
	tests := []struct {
		name   string
		rows   []AddressRow
		target AddressRow
		want   []flattening.Coordinate
	}{
		{
			name:   "type alone identifies the only artifact",
			rows:   []AddressRow{row(module, parent, child, name), {Type: "File", Coordinates: []flattening.Coordinate{module, parent}}},
			target: row(module, parent, child, name), want: []flattening.Coordinate{},
		},
		{
			name:   "globally unique child drops its parent",
			rows:   []AddressRow{row(module, parent, child, name), row(module, c(parent.Dimension, "cli"), c(child.Dimension, "lint"), name)},
			target: row(module, parent, child, name), want: []flattening.Coordinate{child},
		},
		{
			name: "duplicate children need their parents",
			rows: []AddressRow{
				row(module, parent, child, name),
				row(module, c(parent.Dimension, "cli"), child, name),
				row(module, parent, c(child.Dimension, "lint"), name),
			},
			target: row(module, parent, child, name), want: []flattening.Coordinate{parent, child},
		},
		{
			name:   "collection outranks a unique name and module",
			rows:   []AddressRow{row(module, parent, child, name), row(c("module", "other"), c(parent.Dimension, "cli"), c(child.Dimension, "lint"), c(name.Dimension, "other"))},
			target: row(module, parent, child, name), want: []flattening.Coordinate{child},
		},
		{
			name:   "name outranks module",
			rows:   []AddressRow{row(module, name), row(c("module", "other"), c(name.Dimension, "other"))},
			target: row(module, name), want: []flattening.Coordinate{name},
		},
		{
			name:   "module distinguishes equal static names",
			rows:   []AddressRow{row(module, name), row(c("module", "other"), name)},
			target: row(module, name), want: []flattening.Coordinate{module},
		},
		{
			name:   "stale and test fields keep the name",
			rows:   []AddressRow{row(module, parent, name), row(module, parent, c(name.Dimension, "go/packages/stale")), row(module, c(parent.Dimension, "cli"), name)},
			target: row(module, parent, name), want: []flattening.Coordinate{parent, name},
		},
		{
			name:   "missing cells do not match a coordinate",
			rows:   []AddressRow{row(module, parent, name), row(module, name)},
			target: row(module, parent, name), want: []flattening.Coordinate{parent},
		},
		{
			name:   "empty keys differ from absent coordinates",
			rows:   []AddressRow{row(module, c(parent.Dimension, ""), name), row(module, name)},
			target: row(module, c(parent.Dimension, ""), name), want: []flattening.Coordinate{c(parent.Dimension, "")},
		},
		{
			name:   "empty keys differ from nonempty keys",
			rows:   []AddressRow{row(module, c(parent.Dimension, ""), name), row(module, parent, name)},
			target: row(module, c(parent.Dimension, ""), name), want: []flattening.Coordinate{c(parent.Dimension, "")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selectors, ok := NewAddressIndex(test.rows, true).Selectors(test.target)
			require.True(t, ok)
			require.Equal(t, presentSelectors(test.want), selectors)
		})
	}
}

func TestAddressMinimumCardinality(t *testing.T) {
	// Keeping the preferred collection alone does not identify the target.
	// A two-pair address must beat every three-pair address, regardless of ties.
	dimensions := []string{"/go/a", "/go/a/b", "/go/a/b/c", "name", "module"}
	row := func(values ...string) AddressRow {
		coordinates := make([]flattening.Coordinate, len(dimensions))
		for i, dimension := range dimensions {
			coordinates[i] = flattening.Coordinate{Dimension: dimension, Key: values[i]}
		}
		return AddressRow{Type: "Check", Coordinates: coordinates}
	}
	target := row("0", "0", "0", "0", "0")
	rows := []AddressRow{
		target,
		row("1", "0", "0", "1", "0"),
		row("1", "0", "0", "0", "1"),
		row("0", "1", "0", "1", "0"),
		row("0", "1", "0", "0", "1"),
		row("0", "0", "1", "1", "0"),
		row("0", "0", "1", "0", "1"),
	}
	selectors, ok := NewAddressIndex(rows, true).Selectors(target)
	require.True(t, ok)
	require.Equal(t, presentSelectors(target.Coordinates[3:]), selectors)
}

func TestAddressRequiresFullSnapshot(t *testing.T) {
	first := AddressRow{Type: "Check", Coordinates: []flattening.Coordinate{{Dimension: "module", Key: "go"}, {Dimension: "name", Key: "run"}}}
	second := AddressRow{Type: "Check", Coordinates: []flattening.Coordinate{{Dimension: "module", Key: "go"}, {Dimension: "name", Key: "lint"}}}
	index := NewAddressIndex([]AddressRow{first, second}, true)
	// Filtering the displayed rows must not construct a new address index.
	for _, selection := range [][]AddressRow{{first}, {first, second}, {second, first}} {
		for _, target := range selection {
			selectors, ok := index.Selectors(target)
			require.True(t, ok)
			require.Equal(t, presentSelectors(target.Coordinates[1:]), selectors)
		}
	}
	_, ok := NewAddressIndex([]AddressRow{first}, false).Selectors(first)
	require.False(t, ok)
	_, ok = index.Selectors(AddressRow{Type: "Check", Coordinates: first.Coordinates[:1]})
	require.False(t, ok, "a partial target is not a row in the snapshot")
	_, ok = index.Selectors(AddressRow{Type: "File", Coordinates: first.Coordinates})
	require.False(t, ok)
	_, ok = NewAddressIndex([]AddressRow{first, first}, true).Selectors(first)
	require.False(t, ok, "duplicate full coordinates cannot identify one artifact")
	_, ok = NewAddressIndex([]AddressRow{first, {Type: "Check", Coordinates: []flattening.Coordinate{{Dimension: "module", Key: "a"}, {Dimension: "module", Key: "b"}}}}, true).Selectors(first)
	require.False(t, ok, "invalid snapshot rows must not be silently discarded")
}

func TestAddressIndexCopiesSnapshot(t *testing.T) {
	first := AddressRow{Type: "Check", Coordinates: []flattening.Coordinate{{Dimension: "module", Key: "go"}, {Dimension: "name", Key: "run"}}}
	second := AddressRow{Type: "Check", Coordinates: slices.Clone(first.Coordinates)}
	second.Coordinates[1].Key = "lint"
	index := NewAddressIndex([]AddressRow{first, second}, true)
	second.Coordinates[1].Key = "run"
	selectors, ok := index.Selectors(first)
	require.True(t, ok)
	require.Equal(t, presentSelectors(first.Coordinates[1:]), selectors)
}

func TestAddressTypeConstraint(t *testing.T) {
	coordinates := []flattening.Coordinate{
		{Dimension: flattening.ModuleDimension, Key: "go"},
		{Dimension: "GoPackages", Key: "api"},
		{Dimension: flattening.NameDimension, Key: "run"},
	}
	check := AddressRow{Type: "Check", Coordinates: coordinates}
	container := AddressRow{Type: "Container", Coordinates: coordinates}
	index := NewAddressIndex([]AddressRow{check, container}, true)
	for _, target := range []AddressRow{check, container} {
		selectors, ok := index.Selectors(target)
		require.True(t, ok, "different types with equal coordinates are distinct")
		require.Empty(t, selectors, "the URI type alone identifies this artifact")
	}
}

func TestAddressUntypedDuplicateCoordinates(t *testing.T) {
	// The caller uses one shared type group for an address without a type.
	duplicate := AddressRow{Type: "*", Coordinates: []flattening.Coordinate{
		{Dimension: flattening.ModuleDimension, Key: "go"},
		{Dimension: flattening.NameDimension, Key: "run"},
	}}
	unique := AddressRow{Type: "*", Coordinates: []flattening.Coordinate{
		{Dimension: flattening.ModuleDimension, Key: "go"},
		{Dimension: flattening.NameDimension, Key: "lint"},
	}}
	index := NewAddressIndex([]AddressRow{duplicate, duplicate, unique}, true)
	_, ok := index.Selectors(duplicate)
	require.False(t, ok, "equal full coordinates cannot identify either artifact")
	selectors, ok := index.Selectors(unique)
	require.True(t, ok, "duplicate rows do not invalidate other artifacts")
	require.Equal(t, presentSelectors(unique.Coordinates[1:]), selectors)
}

func BenchmarkAddressMinimumSelectors(b *testing.B) {
	const count = 109349
	rows := make([]AddressRow, count)
	for i := range rows {
		rows[i] = AddressRow{Type: "Check", Coordinates: []flattening.Coordinate{
			{Dimension: "module", Key: "go"},
			{Dimension: "/go/a", Key: "shared"},
			{Dimension: "/go/a/b", Key: "shared"},
			{Dimension: "/go/a/b/c", Key: "shared"},
			{Dimension: "/go/a/b/c/d", Key: "shared"},
			{Dimension: "/go/a/b/c/d/e", Key: fmt.Sprint(i / 1000)},
			{Dimension: "/go/a/b/c/d/e/f", Key: fmt.Sprint(i % 1000)},
			{Dimension: flattening.NameDimension, Key: "run"},
		}}
	}
	b.ResetTimer()
	for b.Loop() {
		index := NewAddressIndex(rows, true)
		for _, row := range rows {
			selectors, ok := index.Selectors(row)
			if !ok || len(selectors) != 2 {
				b.Fatalf("expected two selectors: %v, %v", selectors, ok)
			}
		}
	}
}

// presentSelectors preserves the coordinate order in expected selectors.
func presentSelectors(coordinates []flattening.Coordinate) []dagaddress.Pair {
	selectors := make([]dagaddress.Pair, len(coordinates))
	for i, coordinate := range coordinates {
		selectors[i] = dagaddress.Pair{Dimension: coordinate.Dimension, Key: coordinate.Key, HasKey: true}
	}
	return selectors
}
