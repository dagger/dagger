package artifact

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

// A lazy index plans exactly as a full index over every row, including the
// rows of templates it never discovers.
func TestLazyAddressIndexMatchesFullIndex(t *testing.T) {
	ctx := context.Background()
	collections := []string{"GoModules", "GoPackages", "GoTests", "Tasks"}
	for seed := range uint64(3000) {
		random := rand.New(rand.NewPCG(seed, 15177))
		pick := func(values ...string) string { return values[random.IntN(len(values))] }
		key := func() string { return pick("", "a", "b", "c", "before.a", "after.b") }
		types := []string{"Check", "Generator"}
		if random.IntN(3) == 0 {
			types = []string{"*"}
		}
		static := func() []flattening.Coordinate {
			var coordinates []flattening.Coordinate
			if random.IntN(5) > 0 {
				coordinates = append(coordinates, flattening.Coordinate{Dimension: flattening.ModuleDimension, Key: pick("go", "sdk", "docs")})
			}
			return append(coordinates, flattening.Coordinate{Dimension: flattening.NameDimension, Key: pick("generate", "test", "lint")})
		}
		var rows, all []AddressRow
		for range random.IntN(8) {
			coordinates := static()
			// Rows outside templates may still carry collection coordinates.
			if random.IntN(4) == 0 {
				coordinates = append(coordinates, flattening.Coordinate{Dimension: pick(collections...), Key: key()})
			}
			row := AddressRow{Type: pick(types...), Coordinates: coordinates}
			rows = append(rows, row)
			all = append(all, row)
		}
		var templates []*AddressTemplate
		var discovered atomic.Int64
		for range random.IntN(6) {
			known := static()
			dimensions := slices.Clone(collections)
			random.Shuffle(len(dimensions), func(i, j int) { dimensions[i], dimensions[j] = dimensions[j], dimensions[i] })
			dimensions = dimensions[:1+random.IntN(3)]
			typ := pick(types...)
			var templateRows []AddressRow
			for range random.IntN(5) {
				coordinates := slices.Clone(known)
				for _, dimension := range dimensions {
					coordinates = append(coordinates, flattening.Coordinate{Dimension: dimension, Key: key()})
				}
				row := AddressRow{Type: typ, Coordinates: coordinates}
				templateRows = append(templateRows, row)
				all = append(all, row)
			}
			templates = append(templates, &AddressTemplate{Type: typ, Coordinates: known, Dimensions: dimensions,
				Discover: func(context.Context) ([]AddressRow, error) {
					discovered.Add(1)
					return templateRows, nil
				}})
		}
		full := NewAddressIndex(all, true)
		lazy := NewLazyAddressIndex(rows, templates)
		targets := slices.Clone(all)
		for range 3 {
			targets = append(targets, AddressRow{Type: pick(types...), Coordinates: append(static(), flattening.Coordinate{Dimension: pick(collections...), Key: key()})})
		}
		random.Shuffle(len(targets), func(i, j int) { targets[i], targets[j] = targets[j], targets[i] })
		for _, target := range targets {
			want, wantOK := full.Selectors(target)
			got, gotOK, err := lazy.PlanSelectors(ctx, target)
			require.NoError(t, err)
			require.Equal(t, wantOK, gotOK, "seed %d target %v", seed, target)
			require.Equal(t, want, got, "seed %d target %v", seed, target)
		}
	}
}

func TestLazyAddressIndexDiscoversOnlyWhatAPlanNeeds(t *testing.T) {
	ctx := context.Background()
	c := func(dimension, key string) flattening.Coordinate {
		return flattening.Coordinate{Dimension: dimension, Key: key}
	}
	module := func(name string) flattening.Coordinate { return c(flattening.ModuleDimension, name) }
	name := func(name string) flattening.Coordinate { return c(flattening.NameDimension, name) }
	goSDK := AddressRow{Type: "*", Coordinates: []flattening.Coordinate{module("go-sdk"), name("generate")}}
	rows := []AddressRow{
		goSDK,
		{Type: "*", Coordinates: []flattening.Coordinate{module("go-sdk"), name("generate.stale")}},
		{Type: "*", Coordinates: []flattening.Coordinate{module("dang-sdk"), name("generate")}},
	}
	var packages []AddressRow
	for _, pkg := range []string{"cmd/dagger", "core", "dagql"} {
		packages = append(packages, AddressRow{Type: "*", Coordinates: []flattening.Coordinate{module("golang"), c("golang-package", pkg), name("generate")}})
	}
	var discoveries atomic.Int64
	templates := []*AddressTemplate{{
		Type: "*", Coordinates: []flattening.Coordinate{module("golang"), name("generate")}, Dimensions: []string{"golang-package"},
		Discover: func(context.Context) ([]AddressRow, error) {
			discoveries.Add(1)
			return packages, nil
		},
	}}
	lazy := NewLazyAddressIndex(rows, templates)
	selectors, ok, err := lazy.PlanSelectors(ctx, goSDK)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, presentSelectors([]flattening.Coordinate{module("go-sdk"), name("generate")}), selectors)
	require.Zero(t, discoveries.Load(), "no Go package can share go-sdk's module")

	selectors, ok, err = lazy.PlanSelectors(ctx, packages[1])
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, presentSelectors([]flattening.Coordinate{c("golang-package", "core")}), selectors)
	require.EqualValues(t, 1, discoveries.Load(), "a package's plan needs its siblings")
}

func TestLazyAddressIndexReportsDiscoveryErrors(t *testing.T) {
	target := AddressRow{Type: "*", Coordinates: []flattening.Coordinate{{Dimension: flattening.NameDimension, Key: "generate"}}}
	lazy := NewLazyAddressIndex([]AddressRow{target}, []*AddressTemplate{{
		Type: "*", Coordinates: []flattening.Coordinate{{Dimension: flattening.NameDimension, Key: "generate"}}, Dimensions: []string{"golang-package"},
		Discover: func(context.Context) ([]AddressRow, error) { return nil, fmt.Errorf("collection failed") },
	}})
	_, ok, err := lazy.PlanSelectors(context.Background(), target)
	require.ErrorContains(t, err, "collection failed")
	require.False(t, ok)
}
