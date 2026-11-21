package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestArtifactAbsentDimension(t *testing.T) {
	all := collectionArtifactFixture(t)
	for _, tc := range []struct {
		name      string
		selection *Artifacts
		count     int
	}{
		{"absent", all.WithoutDimensions([]string{"app-items"}), 4},
		{"unknown", all.WithoutDimensions([]string{"missing"}), 6},
		{"empty list", all.WithoutDimensions(nil), 6},
		{"all absent", all.WithoutDimensions([]string{"app-items", "app-other"}), 2},
		{"chained alias", all.WithoutDimensions([]string{"app-items"}).WithoutDimensions([]string{"app/items"}), 4},
		{"contradiction", all.WithoutDimensions([]string{"app-items"}).FilterDimensions([]string{"app/items"}), 0},
		{"reverse contradiction", all.FilterDimensions([]string{"app-items"}).WithoutDimensions([]string{"app/items"}), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := tc.selection.SchemaSelection()
			require.NoError(t, err)
			require.Len(t, selected.Entries, tc.count)
			addr, err := dagaddress.Parse(selected.URI())
			require.NoError(t, err)
			roundtrip, err := all.FilterURI(addr)
			require.NoError(t, err)
			roundtrip, err = roundtrip.SchemaSelection()
			require.NoError(t, err)
			require.Len(t, roundtrip.Entries, tc.count)
		})
	}
	empty := ""
	all.Entries[1].Node.CollectionKey = &empty
	selected, err := all.WithoutDimensions([]string{"app-items"}).SchemaSelection()
	require.NoError(t, err)
	require.Len(t, selected.Entries, 4)
	selected, err = all.FilterDimensionKeys("app-items", []string{""}).expand(t.Context(), func(context.Context, *Artifact) ([]collectionKey, error) {
		t.Fatal("unexpected key enumeration")
		return nil, nil
	})
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Equal(t, "", *selected.Entries[0].Node.CollectionKey)
}

func TestArtifactAbsentDimensionDoesNotEnumerate(t *testing.T) {
	for _, nested := range []bool{false, true} {
		all, _ := parallelCollectionFixture(nested)
		selected, err := all.WithoutDimensions([]string{"app/modules/tests"}).expand(t.Context(), func(context.Context, *Artifact) ([]collectionKey, error) {
			t.Fatal("excluded collection receiver was evaluated")
			return nil, nil
		})
		require.NoError(t, err)
		require.Empty(t, selected.Entries)
	}
}

func TestArtifactAbsentDimensionPersistence(t *testing.T) {
	selected := (&Artifacts{}).WithoutDimensions([]string{"Checks"})
	encoded, err := selected.EncodePersistedObject(t.Context(), nil)
	require.NoError(t, err)
	decoded, err := (&Artifacts{}).DecodePersistedObject(t.Context(), dagql.NewPersistDecodeContext(nil, 0, nil), encoded.JSON)
	require.NoError(t, err)
	require.Equal(t, selected.Selector, decoded.(*Artifacts).Selector)
	require.Equal(t, "dag://?!Checks", decoded.(*Artifacts).URI())
}

func TestArtifactAbsentDimensionLoadFailure(t *testing.T) {
	failure := &Artifact{ModuleName: "broken", Path: []string{"broken"}, LoadFailure: &ModuleLoadFailure{}}
	all := &Artifacts{Entries: []*Artifact{failure}}
	selected, err := all.WithoutDimensions([]string{"Checks"}).SchemaSelection()
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	addr, err := dagaddress.Parse("dag://?!Checks")
	require.NoError(t, err)
	remaining, err := all.WithoutURI(addr)
	require.NoError(t, err)
	require.Len(t, remaining.Entries, 1)
	selected, err = all.WithoutDimensions([]string{"Checks"}).FilterDimensions([]string{"Checks"}).SchemaSelection()
	require.NoError(t, err)
	require.Empty(t, selected.Entries)
	selected, err = all.WithoutDimensions([]string{"module"}).SchemaSelection()
	require.NoError(t, err)
	require.Empty(t, selected.Entries)
}

func TestArtifactAbsentLegacyTypeKeepsLoadFailure(t *testing.T) {
	failure := &Artifact{ModuleName: "broken", TypeName: "Check", Path: []string{"broken", "load"}, LoadFailure: &ModuleLoadFailure{}}
	all := &Artifacts{Entries: []*Artifact{failure}}
	selected, err := all.WithoutDimensions([]string{"type:Check"}).SchemaSelection()
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	addr, err := dagaddress.Parse("dag://?!type:Check")
	require.NoError(t, err)
	remaining, err := all.WithoutURI(addr)
	require.NoError(t, err)
	require.Len(t, remaining.Entries, 1)
}
