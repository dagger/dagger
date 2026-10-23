package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

// composeProjectionFixture mirrors the docker compose module: up on each
// service item, keyed by project and service, and up on the services batch,
// keyed by project only.
func composeProjectionFixture() (*Artifacts, artifactCollectionKeyFunc) {
	project := &ArtifactDimension{Identifier: "/docker/compose/projects", Name: "docker-compose-project", QualifiedName: "docker-compose-projects-project"}
	service := &ArtifactDimension{Identifier: "/docker/compose/projects/services", Name: "docker-compose-service", QualifiedName: "docker-compose-projects-services-service"}
	projects := &ModTreeNode{Name: "projects"}
	projectItem := &ModTreeNode{Name: "get", Parent: projects, CollectionDimension: project}
	services := &ModTreeNode{Name: "services", Parent: projectItem}
	serviceItem := &ModTreeNode{Name: "get", Parent: services, CollectionDimension: service}
	all := &Artifacts{Entries: []*Artifact{
		{
			ModuleName: "docker", TypeName: "Service", DimensionKeys: []*ArtifactDimensionKey{},
			Path: []string{"docker", "compose", "projects", "services", "up"},
			Node: &ModTreeNode{Name: "up", Parent: serviceItem},
		},
		{
			ModuleName: "docker", TypeName: "Service", DimensionKeys: []*ArtifactDimensionKey{},
			Path: []string{"docker", "compose", "projects", "services", "batch", "up"},
			Node: &ModTreeNode{Name: "up", Parent: &ModTreeNode{Name: "batch", Parent: services}},
		},
	}}
	all.AllDimensions = all.DimensionDefinitions()
	all.AllPaths = all.PathIndex()
	all.NameEntries()
	return all, func(_ context.Context, receiver *Artifact) ([]collectionKey, error) {
		switch receiver.Node.Name {
		case "projects":
			return []collectionKey{{text: "e2e"}}, nil
		case "services":
			return []collectionKey{{text: "web"}}, nil
		}
		return nil, fmt.Errorf("unexpected collection receiver %q", receiver.Node.Name)
	}
}

func TestArtifactProjection(t *testing.T) {
	all, keys := composeProjectionFixture()
	expanded, err := all.expand(t.Context(), keys)
	require.NoError(t, err)
	var projected, full []string
	for _, entry := range expanded.Entries {
		uri, err := entry.URI(ArtifactURIOpts{DimensionKeys: true})
		require.NoError(t, err)
		projected = append(projected, uri)
		uri, err = entry.URI(ArtifactURIOpts{DimensionKeys: true, Full: true})
		require.NoError(t, err)
		full = append(full, uri)
	}
	require.Equal(t, []string{
		"dag://?service=up&docker-compose-project=e2e&docker-compose-service=web",
		"dag://?service=batch/up&docker-compose-project=e2e",
	}, projected)
	require.Equal(t, []string{
		"dag://?service=docker/compose/projects/services/up&docker-compose-project=e2e&docker-compose-service=web",
		"dag://?service=docker/compose/projects/services/batch/up&docker-compose-project=e2e",
	}, full)
	// An address without keys keeps the full path, its stable name.
	uri, err := all.Entries[0].URI(ArtifactURIOpts{})
	require.NoError(t, err)
	require.Equal(t, "dag://?service=docker/compose/projects/services/up", uri)

	// Both forms select their artifact, and only it.
	for i, entry := range expanded.Entries {
		for _, printed := range []string{projected[i], full[i]} {
			addr, err := dagaddress.Parse(printed)
			require.NoError(t, err)
			selected, err := all.FilterURI(addr)
			require.NoError(t, err)
			selected, err = selected.expand(t.Context(), keys)
			require.NoError(t, err, printed)
			require.Len(t, selected.Entries, 1, printed)
			require.Equal(t, entry.Path, selected.Entries[0].Path, printed)
			require.Equal(t, entry.DimensionKeys, selected.Entries[0].DimensionKeys, printed)
		}
	}
}

// The item's short type key names it only with its service key. Without it,
// the key matches the batch path too, which is an error, never their union.
func TestArtifactAmbiguousTypeKey(t *testing.T) {
	all, keys := composeProjectionFixture()
	addr, err := dagaddress.Parse("dag://?service=up&docker-compose-project=e2e")
	require.NoError(t, err)
	selected, err := all.FilterURI(addr)
	require.NoError(t, err)
	_, err = selected.expand(t.Context(), keys)
	require.ErrorContains(t, err, "service=up is ambiguous: it matches docker/compose/projects/services/batch/up, docker/compose/projects/services/up")

	for address, want := range map[string]string{
		"dag://?service=services/up&docker-compose-project=e2e":                         "docker/compose/projects/services/up",
		"dag://?service=batch/up&docker-compose-project=e2e":                            "docker/compose/projects/services/batch/up",
		"dag://?service=docker/compose/projects/services/up&docker-compose-project=e2e": "docker/compose/projects/services/up",
	} {
		addr, err := dagaddress.Parse(address)
		require.NoError(t, err)
		selected, err := all.FilterURI(addr)
		require.NoError(t, err)
		selected, err = selected.expand(t.Context(), keys)
		require.NoError(t, err, address)
		require.Len(t, selected.Entries, 1, address)
		require.Equal(t, want, selected.Entries[0].DimensionKeys[len(selected.Entries[0].DimensionKeys)-1].Key, address)
	}
}
