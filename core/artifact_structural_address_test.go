package core

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestArtifactAbsentPlanUsesCatalogAlias(t *testing.T) {
	parent := &Artifact{ModuleName: "grid", TypeName: "Check", Path: []string{"grid", "check"}}
	child := parent.Clone()
	child.DimensionKeys = []*ArtifactDimensionKey{{Dimension: "Cell", Key: "left"}}
	originalNames := map[string]string{"module": "module"}
	parent.DimensionNames = originalNames
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{parent, child}, AllDimensions: artifact.Dimensions{{Identifier: "Cell", Name: "cell", CollectionType: "Cell", Kind: "COLLECTION"}}}
	coordinates := parent.CanonicalCoordinates()
	catalog.PrepareAddressPlans()
	require.Equal(t, []dagaddress.Pair{{Dimension: "Cell", Absent: true}}, parent.addressPlan(true).Selectors)
	require.NotContains(t, originalNames, "Cell")
	uri, err := parent.URI(ArtifactURIOpts{Prune: true, DimensionKeys: true, TypeAssertion: true})
	require.NoError(t, err)
	require.Equal(t, "dag+check://?!cell", uri)
	persisted := parent.Clone()
	persisted.LoadFailure = &ModuleLoadFailure{}
	encoded, err := persisted.EncodePersistedObject(t.Context(), nil)
	require.NoError(t, err)
	decoded, err := (&Artifact{}).DecodePersistedObject(t.Context(), dagql.NewPersistDecodeContext(nil, 0, nil), encoded.JSON)
	require.NoError(t, err)
	restored := decoded.(*Artifact)
	require.Equal(t, coordinates, restored.CanonicalCoordinates())
	require.Empty(t, restored.DimensionKeys)
	uri, err = restored.URI(ArtifactURIOpts{Prune: true, DimensionKeys: true, TypeAssertion: true})
	require.NoError(t, err)
	require.Equal(t, "dag+check://?!cell", uri)
}

func TestArtifactSyntheticStaleHasIndependentAbsenceProof(t *testing.T) {
	generator := &Artifact{ModuleName: "grid", TypeName: "Generator", Path: []string{"grid", "generate"}}
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{generator}, AllPaths: artifact.Paths{
		{Module: "grid", Type: "type:Generator", Key: "grid/generate"},
		{Module: "grid", Type: "type:Check", Key: "grid/generate/stale"},
		{Module: "grid", Type: "type:Check", Key: "grid/generate/stale", Dimensions: []string{"Cell"}},
	}}
	require.NoError(t, catalog.NameEntries())
	require.Empty(t, generator.links.TypedAbsent)
	stale := generator.StaleArtifact()
	require.Equal(t, []string{"Cell"}, stale.links.TypedAbsent)
	for _, opts := range []ArtifactURIOpts{{Prune: true, TypeAssertion: true}, {Prune: true, TypeAssertion: true, Full: true}, {TypeAssertion: true}} {
		uri, err := stale.URI(opts)
		require.NoError(t, err)
		addr, err := dagaddress.Parse(uri)
		require.NoError(t, err)
		require.Contains(t, addr.Query, dagaddress.Pair{Dimension: "Cell", Absent: true})
		if opts.Prune && !opts.Full {
			require.Equal(t, "grid/generate/stale", addr.Path)
		}
	}
}

func TestArtifactPathDefinitionsRetainModuleAndStructure(t *testing.T) {
	cell := &ArtifactDimension{Identifier: "Cell", CollectionType: "Cell", Kind: "COLLECTION", Name: "cell", ItemType: "Check"}
	parent := &Artifact{ModuleName: "alpha", TypeName: "Check", Path: []string{"shared"}}
	child := &Artifact{ModuleName: "alpha", TypeName: "Check", Path: []string{"shared"}, Node: &ModTreeNode{CollectionDimension: cell}}
	other := &Artifact{ModuleName: "beta", TypeName: "Check", Path: []string{"shared"}}
	catalog := &Artifacts{CompleteSchema: true, Entries: []*Artifact{parent, child, other}, AllPaths: artifact.Paths{
		{Module: "alpha", Type: "type:Check", Key: "shared"},
		{Module: "alpha", Type: "type:Check", Key: "shared", Dimensions: []string{"Cell"}},
		{Module: "beta", Type: "type:Check", Key: "shared"},
	}}
	require.NoError(t, catalog.NameEntries())
	for _, opts := range []ArtifactURIOpts{{Prune: true, TypeAssertion: true}, {Prune: true, TypeAssertion: true, Full: true}, {TypeAssertion: true}} {
		paths, err := catalog.PathDefinitions(opts)
		require.NoError(t, err)
		require.Len(t, paths, 3)
		for _, path := range paths {
			addr, err := dagaddress.Parse(path.URI)
			require.NoError(t, err)
			require.Contains(t, addr.Query, dagaddress.Pair{Dimension: "module", Key: path.ModuleName, HasKey: true})
		}
	}
}

func TestArtifactIncompleteWorkspaceAddressKeepsModule(t *testing.T) {
	workspace, err := dagql.NewResultForCall(&Workspace{Address: "file:///ws"}, &dagql.ResultCall{})
	require.NoError(t, err)
	for _, tc := range []struct {
		name                string
		workspace, complete bool
		wantModule          bool
	}{
		{"incomplete workspace", true, false, true},
		{"complete workspace", true, true, false},
		{"bound value", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := &Artifact{ModuleName: "app", TypeName: "Check", Path: []string{"unit"}}
			if tc.workspace {
				entry.Workspace = dagql.ObjectResult[*Workspace]{Result: workspace}
			}
			catalog := &Artifacts{CompleteSchema: tc.complete, Entries: []*Artifact{entry}}
			catalog.AllPaths = catalog.PathIndex()
			require.NoError(t, catalog.NameEntries())
			for _, opts := range []ArtifactURIOpts{{Prune: true, TypeAssertion: true}, {Full: true, TypeAssertion: true}, {Full: true}} {
				uri, err := entry.URI(opts)
				require.NoError(t, err)
				addr, err := dagaddress.Parse(uri)
				require.NoError(t, err)
				require.Equal(t, tc.wantModule, slices.Contains(addr.Query, dagaddress.Pair{Dimension: "module", Key: "app", HasKey: true}), uri)
			}
		})
	}
}
