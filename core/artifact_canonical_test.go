package core

import (
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func TestArtifactCanonicalLocalPath(t *testing.T) {
	module := &ModTreeNode{Name: "docker", Parent: &ModTreeNode{}}
	compose := &ModTreeNode{Name: "compose", Parent: module}
	projects := &ModTreeNode{Name: "projects", Parent: compose}
	project := &ModTreeNode{Name: "get", Parent: projects, CollectionDimension: &ArtifactDimension{Identifier: "/docker/compose/projects"}}
	services := &ModTreeNode{Name: "services", Parent: project}
	service := &ModTreeNode{Name: "get", Parent: services, CollectionDimension: &ArtifactDimension{Identifier: "/docker/compose/projects/services"}}
	health := &ModTreeNode{Name: "healthCheck", Parent: service}
	check := &ModTreeNode{Name: "run", Parent: health}
	for _, tc := range []struct {
		name string
		node *ModTreeNode
		want []string
	}{
		{name: "module root", node: module},
		{name: "module field", node: compose, want: []string{"compose"}},
		{name: "direct item", node: service},
		{name: "nearest item and literal fields", node: check, want: []string{"healthCheck", "run"}},
		{name: "collection function", node: &ModTreeNode{Name: "allServices", Parent: services}, want: []string{"services", "allServices"}},
		{name: "bound root", node: &ModTreeNode{Name: "boundAlias"}},
		{name: "bound field", node: &ModTreeNode{Name: "healthCheck", Parent: &ModTreeNode{Name: "boundAlias"}}, want: []string{"healthCheck"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := &Artifact{ModuleName: "docker", Node: tc.node, Path: tc.node.CLIPath(), TypeName: "Check"}
			require.Equal(t, tc.want, entry.LocalPath())
		})
	}
	// Workspace main paths omit the module in command input. Local names do
	// not depend on this shorthand, or on the current CLI field spelling.
	module.WorkspaceMain = true
	entry := &Artifact{ModuleName: "docker", Node: check, Path: check.CommandCLIPath(), TypeName: "Check"}
	require.Equal(t, "healthCheck.run", entry.StaticDimensionKey("name"))
	bound := &Artifact{Node: &ModTreeNode{Name: "boundAlias"}, Path: []string{"bound-alias"}, TypeName: "Check"}
	require.Empty(t, bound.LocalPath())
}

func TestArtifactCanonicalStalePath(t *testing.T) {
	module := &ModTreeNode{Name: "app", Parent: &ModTreeNode{}, WorkspaceMain: true}
	generate := &ModTreeNode{Name: "clientCode", Parent: module}
	for _, path := range [][]string{generate.CLIPath(), generate.CommandCLIPath()} {
		entry := &Artifact{ModuleName: "app", Node: generate, Path: path, TypeName: "Generator"}
		require.Equal(t, []string{"clientCode", "stale"}, entry.StaleArtifact().LocalPath())
		require.Equal(t, "clientCode.stale", entry.StaleArtifact().StaticDimensionKey("name"))
	}
	root := &Artifact{Node: &ModTreeNode{}, TypeName: "Generator"}
	require.Equal(t, []string{"stale"}, root.StaleArtifact().LocalPath())
	mainRoot := &Artifact{ModuleName: "app", Node: module, TypeName: "Generator"}
	require.Equal(t, []string{"stale"}, mainRoot.StaleArtifact().LocalPath())
}

func TestArtifactCanonicalPathWithoutNode(t *testing.T) {
	for _, path := range [][]string{{"app", "projects", "services", "healthCheck", "run"}, {"projects", "services", "healthCheck", "run"}} {
		entry := &Artifact{ModuleName: "app", Path: path, TypeName: "Check", DimensionKeys: []*ArtifactDimensionKey{
			{Dimension: "/app/projects", Key: "e2e"}, {Dimension: "/app/projects/services", Key: "web"},
		}}
		require.Equal(t, []string{"healthCheck", "run"}, entry.LocalPath())
		require.Equal(t, []*ArtifactDimensionKey{
			{Dimension: "module", Key: "app"}, {Dimension: "/app/projects", Key: "e2e"},
			{Dimension: "/app/projects/services", Key: "web"}, {Dimension: "name", Key: "healthCheck.run"},
		}, entry.CanonicalDimensionKeys())
	}
}

func TestArtifactCanonicalNameFilters(t *testing.T) {
	all := &Artifacts{Entries: []*Artifact{
		{ModuleName: "app", Path: []string{"app", "unit", "check"}, TypeName: "Check"},
		{ModuleName: "app", Path: []string{"app", "slow", "check"}, TypeName: "Check"},
	}}
	all.AllDimensions = all.DimensionDefinitions()
	selected, err := all.FilterDimensionKeys("name", []string{"unit.check"}).SchemaSelection()
	require.NoError(t, err)
	require.Len(t, selected.Entries, 1)
	require.Equal(t, []string{"unit.check"}, selected.DimensionKeys("name"))
	require.Equal(t, []string{"name"}, all.Dimensions())
	require.Empty(t, selected.Dimensions())
	require.Equal(t, []string{"module", "name"}, selected.SchemaDimensions())
	require.Empty(t, collectionDimensions(selected.Entries[0]))
	require.True(t, artifact.IsStaticDimension("name"))
	union, err := all.FilterDimensions([]string{"name", "unknown"}).SchemaSelection()
	require.NoError(t, err)
	require.Len(t, union.Entries, 2)
	keys, err := all.ExpandDimensionKeys(t.Context(), "name")
	require.NoError(t, err)
	require.Equal(t, []string{"slow.check", "unit.check"}, keys)
}

func TestArtifactCanonicalNameFiltersKeepLoadFailures(t *testing.T) {
	failure := &Artifact{ModuleName: "broken", Path: []string{"broken", "load"}, TypeName: "Check", LoadFailure: &ModuleLoadFailure{}}
	all := &Artifacts{Entries: []*Artifact{failure}}
	selected, err := all.FilterDimensionKeys("name", []string{"unit.check"}).SchemaSelection()
	require.NoError(t, err)
	require.Equal(t, all.Entries, selected.Entries)
	empty, err := all.FilterDimensionKeys("name", []string{}).SchemaSelection()
	require.NoError(t, err)
	require.Empty(t, empty.Entries)
	all.exact = true
	exact, err := all.FilterDimensionKeys("name", []string{"unit.check"}).SchemaSelection()
	require.NoError(t, err)
	require.Empty(t, exact.Entries)
}

func TestArtifactCollectionNameAliasReservesStaticName(t *testing.T) {
	dimension := (*ArtifactDimension)((flattening.Collection{Fields: []string{"names"}, ItemType: "Name"}).Dimension("app", nil, nil))
	entry := &Artifact{ModuleName: "app", TypeName: "Check", Node: &ModTreeNode{
		Name: "run", Parent: &ModTreeNode{CollectionDimension: dimension},
	}}
	all := &Artifacts{Entries: []*Artifact{entry}}
	all.AllDimensions = all.DimensionDefinitions()
	all.NameEntries()
	require.Equal(t, "app-name", entry.dimensionName(dimension.Identifier))
	resolved, err := all.ResolveDimension(entry.dimensionName(dimension.Identifier))
	require.NoError(t, err)
	require.Equal(t, dimension.Identifier, resolved)
	resolved, err = all.ResolveDimension("name")
	require.NoError(t, err)
	require.Equal(t, artifact.NameDimension, resolved)
}
