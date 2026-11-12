package artifact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathNeedsAbsentDimension(t *testing.T) {
	parent := Path{Module: "grid", Type: "type:Check", Key: "grid/rows/check", Dimensions: []string{"Rows"}}
	child := parent
	child.Dimensions = []string{"Rows", "Row"}
	t.Run("parent cannot exclude a direct nested child", func(t *testing.T) {
		require.True(t, PathNeedsAbsentDimension(Paths{parent, child}, parent))
		require.False(t, PathNeedsAbsentDimension(Paths{parent, child}, child))
	})
	for _, tc := range []struct {
		name      string
		candidate Path
	}{
		{"other module", Path{Module: "other", Type: child.Type, Key: child.Key, Dimensions: child.Dimensions}},
		{"other type", Path{Module: child.Module, Type: "type:Container", Key: child.Key, Dimensions: child.Dimensions}},
		{"other path", Path{Module: child.Module, Type: child.Type, Key: "grid/other/check", Dimensions: child.Dimensions}},
		{"different ancestry", Path{Module: child.Module, Type: child.Type, Key: child.Key, Dimensions: []string{"OtherRows", "Row"}}},
		{"legacy route alias", Path{Module: child.Module, Type: child.Type, Key: child.Key, Dimensions: []string{"Rows", "/grid/rows"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, PathNeedsAbsentDimension(Paths{parent, tc.candidate}, parent))
		})
	}
	t.Run("legacy aliases do not hide canonical ancestry", func(t *testing.T) {
		aliased := parent
		aliased.Dimensions = []string{"Rows", "/grid/rows"}
		require.True(t, PathNeedsAbsentDimension(Paths{aliased, child}, aliased))
	})
}
