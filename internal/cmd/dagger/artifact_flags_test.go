package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactDimensionFlagsCollectionTypeCollision(t *testing.T) {
	dims := artifact.Dimensions{
		{Identifier: "go/modules", Name: "go-module", QualifiedName: "go-modules", ItemType: "GoModule"},
		{Identifier: "type:GoModule", Kind: "TYPE", Name: "go-module", ItemType: "GoModule"},
	}
	for range 2 {
		names := artifactDimensionFlagNames(&cobra.Command{Use: "check"}, dims)
		require.Equal(t, artifactFlagNames{Key: "go-module", Presence: "go-modules"}, names["go/modules"])
		require.Equal(t, artifactFlagNames{Key: "artifact-go-module"}, names["type:GoModule"])
		slices.Reverse(dims)
	}
}

func TestArtifactDimensionFlagsCollectionQualification(t *testing.T) {
	dims := artifact.Dimensions{
		{Identifier: "/tools/items", Kind: "COLLECTION", Name: "item"},
		{Identifier: "/tools/other", Kind: "COLLECTION", Name: "item"},
		{Identifier: "/tools/parents", Kind: "COLLECTION", Name: "parent"},
		{Identifier: "/tools/parents/items", Kind: "COLLECTION", Name: "item"},
	}
	want := map[string]artifactFlagNames{
		"/tools/items":         {Key: "tools-items-item", Presence: "tools-items"},
		"/tools/other":         {Key: "tools-other-item", Presence: "tools-other"},
		"/tools/parents":       {Key: "tools-parent", Presence: "tools-parents"},
		"/tools/parents/items": {Key: "tools-parents-item", Presence: "tools-parents-items"},
	}
	var permute func(int)
	permute = func(start int) {
		if start == len(dims) {
			names := artifactDimensionFlagNames(&cobra.Command{Use: "check"}, dims)
			for id, flags := range want {
				require.Equal(t, flags, names[id], id)
			}
			return
		}
		for i := start; i < len(dims); i++ {
			dims[start], dims[i] = dims[i], dims[start]
			permute(start + 1)
			dims[start], dims[i] = dims[i], dims[start]
		}
	}
	permute(0)

	objects := artifact.Dimensions{
		{Identifier: "/tools/linux/modules", Name: "module"},
		{Identifier: "/tools/windows/modules", Name: "module"},
		{Identifier: "/tools/linux/modules/cases", Name: "probe"},
		{Identifier: "/tools/windows/modules/cases", Name: "probe"},
	}
	for range 2 {
		names := artifactDimensionFlagNames(&cobra.Command{Use: "check"}, objects)
		require.Equal(t, artifactFlagNames{Key: "tools-linux-module", Presence: "tools-linux-modules"}, names["/tools/linux/modules"])
		require.Equal(t, artifactFlagNames{Key: "tools-windows-module", Presence: "tools-windows-modules"}, names["/tools/windows/modules"])
		require.Equal(t, artifactFlagNames{Key: "tools-linux-probe", Presence: "tools-linux-cases"}, names["/tools/linux/modules/cases"])
		require.Equal(t, artifactFlagNames{Key: "tools-windows-probe", Presence: "tools-windows-cases"}, names["/tools/windows/modules/cases"])
		slices.Reverse(objects)
	}
}

func TestArtifactDimensionFlagsReserved(t *testing.T) {
	dims := artifact.Dimensions{
		{Identifier: "build/skip", Name: "skip", QualifiedName: "build-skip"},
		{Identifier: "go/tests", Name: "go-test", QualifiedName: "go-tests"},
	}
	names := artifactDimensionFlagNames(artifactFlagTestCommand("skip"), dims)
	require.Equal(t, artifactFlagNames{Key: "build-skip", Presence: "build-skip-skip"}, names["build/skip"])
	require.Equal(t, artifactFlagNames{Key: "go-test", Presence: "go-tests"}, names["go/tests"])

	cmd := artifactFlagTestCommand("tools-item", "tools-items-item", "tools-items", "tools-items-items", "dimension-tools-items")
	names = artifactDimensionFlagNames(cmd, artifact.Dimensions{{Identifier: "/tools/items", Name: "item"}})
	require.Equal(t, artifactFlagNames{Key: "dimension-tools-items-2", Presence: "dimension-tools-items-all"}, names["/tools/items"])

	names = artifactDimensionFlagNames(&cobra.Command{Use: "check"}, artifact.Dimensions{{Identifier: "/by-tools/items", Name: "item"}})
	require.Equal(t, artifactFlagNames{Key: "dimension-by-tools-items", Presence: "dimension-by-tools-items-all"}, names["/by-tools/items"])
}

func TestArtifactModuleFlagNames(t *testing.T) {
	dims := artifact.Dimensions{
		{Identifier: "module", Kind: "MODULE", Name: "module"},
		{Identifier: "go/tests", Kind: "COLLECTION", Name: "go-test"},
	}
	modules := []string{"go", "playwright", "test", "format", "check", "go-tests", "docs", "go", "DocsAPI", "GoSDK", "go-sdk", "by-tools", ""}
	paths := make([]artifactListPath, 0, len(modules))
	for _, module := range modules {
		paths = append(paths, artifactListPath{ModuleName: module})
	}
	cmd := artifactFlagTestCommand("format")
	want := map[string]string{
		"go": "go", "playwright": "playwright", "test": "test", "format": "by-format",
		"check": "by-check", "go-tests": "by-go-tests", "docs": "docs", "DocsAPI": "docs-api",
		"GoSDK": "by-GoSDK", "go-sdk": "by-go-sdk", "by-tools": "by-by-tools",
	}
	for range 2 {
		require.Equal(t, want, artifactModuleFlagNames(cmd, dims, paths))
		slices.Reverse(paths)
		slices.Reverse(dims)
	}
}

func TestArtifactSelectorDimensions(t *testing.T) {
	want := artifact.Dimensions{
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
		{Identifier: "type:Generator", Kind: "TYPE", Name: "generator", ItemType: "Generator"},
		{Identifier: "type:Service", Kind: "TYPE", Name: "service", ItemType: "Service"},
		{Identifier: "type:Container", Kind: "TYPE", Name: "container", ItemType: "Container"},
		{Identifier: "type:Directory", Kind: "TYPE", Name: "directory", ItemType: "Directory"},
		{Identifier: "type:Expertise", Kind: "TYPE", Name: "expertise", ItemType: "Expertise"},
	}
	require.Equal(t, want, artifactSelectorDimensions(nil))
	require.Equal(t, map[string]artifactFlagNames{
		"type:Check": {Key: "check"}, "type:Generator": {Key: "generator"},
		"type:Service": {Key: "service"}, "type:Container": {Key: "container"},
		"type:Directory": {Key: "directory"}, "type:Expertise": {Key: "expertise"},
	}, artifactDimensionFlagNames(&cobra.Command{Use: "check"}, nil))

	existing := &artifact.Dimension{Identifier: "type:Check", Kind: "TYPE", Name: "custom-check", ItemType: "Check", KeyName: "name"}
	input := make(artifact.Dimensions, 1, 8)
	input[0] = existing
	before := *existing
	selected := artifactSelectorDimensions(input)
	require.Len(t, selected, 6)
	require.Same(t, existing, selected[0])
	require.Equal(t, before, *existing)
	require.Len(t, input, 1)
	require.Nil(t, input[:cap(input)][1], "appending selectors must not alter the input array")
	require.Equal(t, selected, artifactSelectorDimensions(selected))
}

func TestArtifactFlagSpelling(t *testing.T) {
	cmd := artifactFlagTestCommand("e2e-test-item", "e2e-test-items-item", "e2e-test-items", "e2e-test-items-items")
	require.Equal(t, "e2e-test", artifactModuleFlagNames(cmd, nil, []artifactListPath{{ModuleName: "e2eTest"}})["e2eTest"])
	names := artifactDimensionFlagNames(cmd, artifact.Dimensions{{Identifier: "/e2e-test/items", Name: "item"}})
	require.Equal(t, artifactFlagNames{Key: "dimension-e2e-test-items", Presence: "dimension-e2e-test-items-all"}, names["/e2e-test/items"])
}

func TestArtifactCanonicalDimensionFlags(t *testing.T) {
	dimensions := artifact.Dimensions{
		{Kind: "COLLECTION", Identifier: "GoPackages", CollectionType: "GoPackages", Name: "go-package", QualifiedName: "GoPackages"},
		{Kind: "COLLECTION", Identifier: "Tests", CollectionType: "Tests", Name: "thing", QualifiedName: "Tests"},
		{Kind: "COLLECTION", Identifier: "Other", CollectionType: "Other", Name: "tests", QualifiedName: "Other"},
	}
	names := artifactDimensionFlagNames(&cobra.Command{Use: "check"}, dimensions)
	require.Equal(t, artifactFlagNames{Key: "go-package", Presence: "go-packages"}, names["GoPackages"])
	require.Equal(t, artifactFlagNames{Key: "thing", Presence: "dimension-tests-all"}, names["Tests"])
	require.Equal(t, artifactFlagNames{Key: "tests", Presence: "other"}, names["Other"])
}

func artifactFlagTestCommand(reserved ...string) *cobra.Command {
	cmd := &cobra.Command{Use: "check"}
	for _, name := range reserved {
		cmd.Flags().Bool(name, false, "")
	}
	return cmd
}
