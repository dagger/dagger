package daggercmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestListedArtifactKeysPreservesDimensionAndOrder(t *testing.T) {
	for _, size := range []int{0, 1, 16, 17, 100} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var items []listedArtifact
			var want []dagaddress.Pair
			for i := range size {
				key := fmt.Sprintf("test %d", i)
				if i == 0 {
					key = "" // An empty key is still a key.
				}
				for _, dimension := range []string{"Go.tests", "Dang.tests"} {
					items = append(items, listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{
						{dimension, key}, {dimension, key},
					}})
					want = append(want, dagaddress.Pair{Dimension: dimension, Key: key, HasKey: true})
				}
			}
			// Duplicates recur after the membership index has been constructed.
			items = append(items, slices.Clone(items)...)
			before, err := json.Marshal(items)
			require.NoError(t, err)
			require.Equal(t, want, listedArtifactKeys(items))
			after, err := json.Marshal(items)
			require.NoError(t, err)
			require.Equal(t, before, after, "formatting must not mutate discovery results")
		})
	}
}

// A filtered listing without --all collapses a whole collection into one row,
// e.g. every test of one module. Its keys are collected into a single union.
func BenchmarkListedArtifactKeys(b *testing.B) {
	for _, size := range []int{1, 10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			row := make([]listedArtifact, size)
			for i := range row {
				row[i].DimensionKeys = []struct{ Dimension, Key string }{
					{"Go.modules", "."}, {"Go.tests", fmt.Sprintf("Test%05d", i)},
				}
			}
			b.ReportAllocs()
			var keys []dagaddress.Pair
			for b.Loop() {
				keys = listedArtifactKeys(row)
			}
			require.Len(b, keys, size+1)
		})
	}
}
