package artifact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathAbsentDimensions(t *testing.T) {
	parent := Path{Module: "grid", Type: "type:Check", Key: "grid/rows/check", Dimensions: []string{"Rows"}}
	child := parent
	child.Dimensions = []string{"Rows", "Row"}
	t.Run("parent excludes a direct nested child", func(t *testing.T) {
		require.Equal(t, []string{"Row"}, PathAbsentDimensions(Paths{parent, child}, parent, true))
		require.Empty(t, PathAbsentDimensions(Paths{parent, child}, child, true))
	})
	for _, tc := range []struct {
		name      string
		candidate Path
	}{
		{"other module", Path{Module: "other", Type: child.Type, Key: child.Key, Dimensions: child.Dimensions}},
		{"other path", Path{Module: child.Module, Type: child.Type, Key: "grid/other/check", Dimensions: child.Dimensions}},
		{"different ancestry", Path{Module: child.Module, Type: child.Type, Key: child.Key, Dimensions: []string{"OtherRows", "Row"}}},
		{"legacy route alias", Path{Module: child.Module, Type: child.Type, Key: child.Key, Dimensions: []string{"Rows", "/grid/rows"}}},
		{"static dimensions", Path{Module: child.Module, Type: child.Type, Key: child.Key, Dimensions: []string{"Rows", "module", "name", "type:Check"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, typed := range []bool{false, true} {
				require.Empty(t, PathAbsentDimensions(Paths{parent, tc.candidate}, parent, typed))
			}
		})
	}
	t.Run("typed addresses exclude other types", func(t *testing.T) {
		other := child
		other.Type = "type:Container"
		require.Empty(t, PathAbsentDimensions(Paths{parent, other}, parent, true))
		require.Equal(t, []string{"Row"}, PathAbsentDimensions(Paths{parent, other}, parent, false))
	})
	t.Run("legacy aliases do not hide canonical ancestry", func(t *testing.T) {
		aliased := parent
		aliased.Dimensions = []string{"Rows", "/grid/rows"}
		require.Equal(t, []string{"Row"}, PathAbsentDimensions(Paths{aliased, child}, aliased, true))
	})
	t.Run("extra dimensions are sorted and unique", func(t *testing.T) {
		first, second := child, child
		first.Dimensions = []string{"Rows", "Zulu", "Alpha", "Alpha"}
		second.Dimensions = []string{"Rows", "Alpha", "Middle"}
		want := []string{"Alpha", "Middle", "Zulu"}
		require.Equal(t, want, PathAbsentDimensions(Paths{parent, first, second}, parent, true))
		require.Equal(t, want, PathAbsentDimensions(Paths{second, first, parent}, parent, true))
	})
	t.Run("all target dimensions are required", func(t *testing.T) {
		target, candidate := child, child
		target.Dimensions = []string{"Rows", "Cells"}
		candidate.Dimensions = []string{"Rows", "Extra"}
		require.Empty(t, PathAbsentDimensions(Paths{target, candidate}, target, true))
	})
}

func TestPathPresenceDimensions(t *testing.T) {
	target := Path{Module: "grid", Type: "type:Check", Key: "grid/rows/check", Dimensions: []string{"Rows", "Cells"}}
	parent := target
	parent.Dimensions = []string{"Rows"}
	t.Run("child must have its collection dimension", func(t *testing.T) {
		require.Equal(t, []string{"Cells"}, PathPresenceDimensions(Paths{parent, target}, target, true))
		require.Empty(t, PathPresenceDimensions(Paths{parent, target}, parent, true))
	})
	t.Run("different schema ancestry still needs target presence", func(t *testing.T) {
		candidate := target
		candidate.Dimensions = []string{"OtherRows", "OtherCells"}
		require.Equal(t, []string{"Cells", "Rows"}, PathPresenceDimensions(Paths{candidate, target}, target, true))
	})
	t.Run("typed addresses exclude other types", func(t *testing.T) {
		candidate := parent
		candidate.Type = "type:Container"
		require.Empty(t, PathPresenceDimensions(Paths{candidate, target}, target, true))
		require.Equal(t, []string{"Cells"}, PathPresenceDimensions(Paths{candidate, target}, target, false))
	})
	for _, tc := range []struct {
		name      string
		candidate Path
	}{
		{"other module", Path{Module: "other", Type: target.Type, Key: target.Key}},
		{"other path", Path{Module: target.Module, Type: target.Type, Key: "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, typed := range []bool{false, true} {
				require.Empty(t, PathPresenceDimensions(Paths{target, tc.candidate}, target, typed))
			}
		})
	}
	t.Run("only canonical collection dimensions are returned", func(t *testing.T) {
		aliased := target
		aliased.Dimensions = []string{"Rows", "/grid/rows", "module", "name", "type:Check"}
		candidate := target
		candidate.Dimensions = nil
		require.Equal(t, []string{"Rows"}, PathPresenceDimensions(Paths{aliased, candidate}, aliased, true))
	})
	t.Run("dimensions are sorted and unique", func(t *testing.T) {
		first, second := target, target
		first.Dimensions = []string{"Cells"}
		second.Dimensions = nil
		want := []string{"Cells", "Rows"}
		require.Equal(t, want, PathPresenceDimensions(Paths{first, target, second, second}, target, true))
		require.Equal(t, want, PathPresenceDimensions(Paths{second, target, first}, target, true))
	})
}

func TestPathNeedsModule(t *testing.T) {
	target := Path{Module: "grid", Type: "type:Check", Key: "rows/check", Dimensions: []string{"Rows"}}
	for _, test := range []struct {
		name           string
		candidate      Path
		typed, untyped bool
	}{
		{name: "target alone", candidate: target},
		{name: "same module", candidate: Path{Module: target.Module, Type: target.Type, Key: target.Key}},
		{name: "other public path", candidate: Path{Module: "other", Type: target.Type, Key: "other/check"}},
		{name: "same type in another module", candidate: Path{Module: "other", Type: target.Type, Key: target.Key}, typed: true, untyped: true},
		{name: "other type in another module", candidate: Path{Module: "other", Type: "type:Container", Key: target.Key}, untyped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := Paths{target, test.candidate}
			require.Equal(t, test.typed, PathNeedsModule(paths, target, true))
			require.Equal(t, test.untyped, PathNeedsModule(paths, target, false))
		})
	}
}
