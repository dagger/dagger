package flattening

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDimensionFlagsCollectionTypeCollision(t *testing.T) {
	// From TestArtifactDimensionHelp: collection keys take the short name.
	dims := Dimensions{
		{Identifier: "go/modules", Name: "go-module", QualifiedName: "go-modules", ItemType: "GoModule"},
		{Identifier: "type:GoModule", Kind: "TYPE", Name: "go-module", ItemType: "GoModule"},
	}
	for range 2 {
		names := DimensionFlags(dims, nil, nil)
		require.Equal(t, FlagNames{Key: "go-module", Presence: "go-modules"}, names["go/modules"])
		require.Equal(t, FlagNames{Key: "artifact-go-module"}, names["type:GoModule"])
		slices.Reverse(dims)
	}
}

func TestDimensionFlagsCollectionQualification(t *testing.T) {
	// From TestArtifactCollectionFlagNames: parent collections supply identity.
	dims := Dimensions{
		{Identifier: "/tools/items", Kind: "COLLECTION", Name: "item"},
		{Identifier: "/tools/other", Kind: "COLLECTION", Name: "item"},
		{Identifier: "/tools/parents", Kind: "COLLECTION", Name: "parent"},
		{Identifier: "/tools/parents/items", Kind: "COLLECTION", Name: "item"},
	}
	want := map[string]FlagNames{
		"/tools/items":         {Key: "tools-items-item", Presence: "tools-items"},
		"/tools/other":         {Key: "tools-other-item", Presence: "tools-other"},
		"/tools/parents":       {Key: "tools-parent", Presence: "tools-parents"},
		"/tools/parents/items": {Key: "tools-parents-item", Presence: "tools-parents-items"},
	}
	var permute func(int)
	permute = func(start int) {
		if start == len(dims) {
			names := DimensionFlags(dims, nil, nil)
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

	// From TestCollectionSelectorNames: object fields qualify sibling routes.
	objects := Dimensions{
		{Identifier: "/tools/linux/modules", Name: "module"},
		{Identifier: "/tools/windows/modules", Name: "module"},
		{Identifier: "/tools/linux/modules/cases", Name: "probe"},
		{Identifier: "/tools/windows/modules/cases", Name: "probe"},
	}
	for range 2 {
		names := DimensionFlags(objects, nil, nil)
		require.Equal(t, FlagNames{Key: "tools-linux-module", Presence: "tools-linux-modules"}, names["/tools/linux/modules"])
		require.Equal(t, FlagNames{Key: "tools-windows-module", Presence: "tools-windows-modules"}, names["/tools/windows/modules"])
		require.Equal(t, FlagNames{Key: "tools-linux-probe", Presence: "tools-linux-cases"}, names["/tools/linux/modules/cases"])
		require.Equal(t, FlagNames{Key: "tools-windows-probe", Presence: "tools-windows-cases"}, names["/tools/windows/modules/cases"])
		slices.Reverse(objects)
	}
}

func TestDimensionFlagsReserved(t *testing.T) {
	// From TestArtifactProjectionFlagNames: peer command flags are reserved.
	dims := Dimensions{
		{Identifier: "build/skip", Name: "skip", QualifiedName: "build-skip"},
		{Identifier: "go/tests", Name: "go-test", QualifiedName: "go-tests"},
	}
	names := DimensionFlags(dims, map[string]bool{"skip": true}, nil)
	require.Equal(t, FlagNames{Key: "build-skip", Presence: "build-skip-skip"}, names["build/skip"])
	require.Equal(t, FlagNames{Key: "go-test", Presence: "go-tests"}, names["go/tests"])

	reserved := map[string]bool{
		"tools-item": true, "tools-items-item": true, "tools-items": true, "tools-items-items": true,
		"dimension-tools-items": true, "unused": false,
	}
	before := maps.Clone(reserved)
	names = DimensionFlags(Dimensions{{Identifier: "/tools/items", Name: "item"}}, reserved, nil)
	require.Equal(t, FlagNames{Key: "dimension-tools-items-2", Presence: "dimension-tools-items-all"}, names["/tools/items"])
	require.Equal(t, before, reserved)

	names = DimensionFlags(Dimensions{{Identifier: "/by-tools/items", Name: "item"}}, nil, nil)
	require.Equal(t, FlagNames{Key: "dimension-by-tools-items", Presence: "dimension-by-tools-items-all"}, names["/by-tools/items"])
}

func TestFlatteningModuleFlags(t *testing.T) {
	// From TestArtifactSelectorFlags, with casing and alias conflicts added.
	dims := Dimensions{
		{Identifier: "module", Kind: "MODULE", Name: "module"},
		{Identifier: "go/tests", Kind: "COLLECTION", Name: "go-test"},
	}
	modules := []string{"go", "playwright", "test", "format", "check", "go-tests", "docs", "go", "DocsAPI", "GoSDK", "go-sdk", "by-tools", ""}
	reserved := map[string]bool{"format": true, "unused": false}
	before := maps.Clone(reserved)
	want := map[string]string{
		"go": "go", "playwright": "playwright", "test": "test", "format": "by-format",
		"check": "by-check", "go-tests": "by-go-tests", "docs": "docs", "DocsAPI": "docs-api",
		"GoSDK": "by-GoSDK", "go-sdk": "by-go-sdk", "by-tools": "by-by-tools",
	}
	for range 2 {
		require.Equal(t, want, ModuleFlags(modules, dims, reserved, nil))
		require.Equal(t, before, reserved)
		slices.Reverse(modules)
		slices.Reverse(dims)
	}
}

func TestFlatteningSelectorDimensions(t *testing.T) {
	want := Dimensions{
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check"},
		{Identifier: "type:Generator", Kind: "TYPE", Name: "generator", ItemType: "Generator"},
		{Identifier: "type:Service", Kind: "TYPE", Name: "service", ItemType: "Service"},
		{Identifier: "type:Container", Kind: "TYPE", Name: "container", ItemType: "Container"},
		{Identifier: "type:Directory", Kind: "TYPE", Name: "directory", ItemType: "Directory"},
		{Identifier: "type:Expertise", Kind: "TYPE", Name: "expertise", ItemType: "Expertise"},
	}
	require.Equal(t, want, SelectorDimensions(nil, nil))
	require.Equal(t, map[string]FlagNames{
		"type:Check": {Key: "check"}, "type:Generator": {Key: "generator"},
		"type:Service": {Key: "service"}, "type:Container": {Key: "container"},
		"type:Directory": {Key: "directory"}, "type:Expertise": {Key: "expertise"},
	}, DimensionFlags(nil, nil, nil))

	existing := &Dimension{Identifier: "type:Check", Kind: "TYPE", Name: "custom-check", ItemType: "Check", KeyName: "name"}
	input := make(Dimensions, 1, 8)
	input[0] = existing
	before := *existing
	selected := SelectorDimensions(input, nil)
	require.Len(t, selected, 6)
	require.Same(t, existing, selected[0])
	require.Equal(t, before, *existing)
	require.Len(t, input, 1)
	require.Nil(t, input[:cap(input)][1], "appending selectors must not alter the input array")
	require.Equal(t, selected, SelectorDimensions(selected, nil))
}

func TestCurrentFlagSpelling(t *testing.T) {
	require.Equal(t, "e2e-test", ModuleFlags([]string{"e2eTest"}, nil, nil, nil)["e2eTest"])
	reserved := map[string]bool{
		"e2e-test-item": true, "e2e-test-items-item": true,
		"e2e-test-items": true, "e2e-test-items-items": true,
	}
	names := DimensionFlags(Dimensions{{Identifier: "/e2e-test/items", Name: "item"}}, reserved, nil)
	require.Equal(t, FlagNames{Key: "dimension-e2e-test-items", Presence: "dimension-e2e-test-items-all"}, names["/e2e-test/items"])
}

func TestFlagNamingPolicy(t *testing.T) {
	name := func(value string) string { return "policy-" + latestName(value) }
	require.Equal(t, "policy-check", SelectorDimensions(nil, name)[0].Name)
	require.Equal(t, FlagNames{Key: "policy-check"}, DimensionFlags(nil, nil, name)["type:Check"])
	// Module allocation must reserve selectors made with the same policy.
	require.Equal(t, "by-Check", ModuleFlags([]string{"Check"}, nil, nil, name)["Check"])
	reserved := map[string]bool{
		"tools-item": true, "tools-items-item": true,
		"tools-items": true, "tools-items-items": true,
	}
	names := DimensionFlags(Dimensions{{Identifier: "/tools/items", Name: "item"}}, reserved, name)
	require.Equal(t, FlagNames{Key: "dimension-policy-tools-items", Presence: "dimension-policy-tools-items-all"}, names["/tools/items"])
}
