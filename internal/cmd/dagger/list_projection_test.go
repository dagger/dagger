package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactCLITypeKeyOmission(t *testing.T) {
	item := listedArtifact{
		URI: "dag+check://go/modules/tests/run?go-module=.&go-test=TestFoo",
		DimensionKeys: []struct{ Dimension, Key string }{
			{"module", "go"}, {"go/modules", "."}, {"go/modules/tests", "TestFoo"}, {"type:Check", "go/modules/tests/run"},
		},
	}
	run := artifactListPath{URI: "dag+check://go/modules/tests/run", ModuleName: "go", Dimensions: []string{"module", "go/modules", "go/modules/tests", "type:Check"}}
	sibling := func(uri string) artifactListPath {
		path := run
		path.URI = uri
		return path
	}
	container := artifactListPath{URI: "dag+container://go/modules/tests/container", ModuleName: "go", Dimensions: []string{"module", "go/modules", "go/modules/tests", "type:Container"}}
	for _, tc := range []struct {
		name  string
		paths []artifactListPath
		types []string
		omit  bool
	}{
		{"single check on an item", []artifactListPath{run}, []string{"Check"}, true},
		{"unlisted sibling check", []artifactListPath{run, sibling("dag+check://go/modules/tests/validate")}, []string{"Check"}, false},
		{"empty sibling with the same dimensions", []artifactListPath{run, sibling("dag+check://go/empty/tests/run")}, []string{"Check"}, false},
		{"parent check lacks the test dimension", []artifactListPath{run, {URI: "dag+check://go/modules/check", ModuleName: "go", Dimensions: []string{"module", "go/modules", "type:Check"}}}, []string{"Check"}, true},
		{"descendant check adds another dimension", []artifactListPath{run, {URI: "dag+check://go/modules/tests/parts/run", ModuleName: "go", Dimensions: append(slices.Clone(run.Dimensions), "go/modules/tests/parts")}}, []string{"Check"}, false},
		{"another module does not match", []artifactListPath{run, {URI: "dag+check://other/modules/tests/run", ModuleName: "other", Dimensions: run.Dimensions}}, []string{"Check"}, true},
		{"command type excludes other types", []artifactListPath{run, container}, []string{"Check"}, true},
		{"untyped list keeps type distinction", []artifactListPath{run, container}, nil, false},
		{"untyped list with only one path", []artifactListPath{run}, nil, true},
		{"wrong command type", []artifactListPath{run, container}, []string{"Container"}, false},
		{"duplicate schema paths", []artifactListPath{run, run}, []string{"Check"}, true},
		{"unknown schema", nil, []string{"Check"}, false},
		{"target absent from schema", []artifactListPath{sibling("dag+check://go/modules/tests/validate")}, []string{"Check"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reverse both input orders. No omission can depend on an earlier
			// selector's removal or on the order of schema discovery.
			paths := slices.Clone(tc.paths)
			row := item
			row.DimensionKeys = slices.Clone(item.DimensionKeys)
			for range 2 {
				index, err := newArtifactNameIndex(paths)
				require.NoError(t, err)
				require.Equal(t, tc.omit, canOmitArtifactCLITypeKey(row, index, tc.types))
				slices.Reverse(paths)
				slices.Reverse(row.DimensionKeys)
			}
		})
	}
	t.Run("shell includes containers and directories", func(t *testing.T) {
		row := item
		row.DimensionKeys = slices.Clone(item.DimensionKeys)
		row.DimensionKeys[3] = struct{ Dimension, Key string }{"type:Container", "go/modules/tests/container"}
		paths := []artifactListPath{container, {URI: "dag+directory://go/modules/tests/files", ModuleName: "go", Dimensions: []string{"module", "go/modules", "go/modules/tests", "type:Directory"}}}
		index, err := newArtifactNameIndex(paths)
		require.NoError(t, err)
		require.False(t, canOmitArtifactCLITypeKey(row, index, commandArtifactTypes(&cobra.Command{Use: "shell"})))
	})
	t.Run("cached proof preserves module and dimension scope", func(t *testing.T) {
		paths := []artifactListPath{run, {URI: "dag+check://go/modules/check", ModuleName: "go", Dimensions: []string{"module", "go/modules", "type:Check"}}}
		index, err := newArtifactNameIndex(paths)
		require.NoError(t, err)
		rows := []listedArtifact{item, item, item, item}
		for i := range rows {
			rows[i].DimensionKeys = slices.Clone(item.DimensionKeys)
		}
		rows[1].DimensionKeys[2].Key = "TestBar"
		rows[2].DimensionKeys[0].Key = "other"
		rows[3].DimensionKeys = append(rows[3].DimensionKeys[:2], rows[3].DimensionKeys[3:]...)
		for range 2 {
			omitArtifactCLITypeKeys(rows, index, []string{"Check"})
			for _, row := range rows {
				require.Equal(t, canOmitArtifactCLITypeKey(row, index, []string{"Check"}), row.OmitCLITypeKey)
			}
			slices.Reverse(rows)
		}
	})
	for _, command := range []string{"generate", "up", "agent"} {
		t.Run(command+" keeps module context", func(t *testing.T) {
			typ := commandArtifactTypes(&cobra.Command{Use: command})[0]
			row := listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{{"module", "contributor"}, {"type:" + typ, "contributor/operation"}}}
			path := artifactListPath{URI: "dag://contributor/operation", ModuleName: "contributor", Dimensions: []string{"module", "type:" + typ}}
			index, err := newArtifactNameIndex([]artifactListPath{path})
			require.NoError(t, err)
			require.True(t, canOmitArtifactCLITypeKey(row, index, []string{typ}))
			require.Equal(t, "contributor", row.DimensionKeys[0].Key)
		})
	}
}

func TestArtifactProjectionFlagNames(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "build/skip", Name: "skip", QualifiedName: "build-skip"},
		{Identifier: "go/tests", Name: "go-test", QualifiedName: "go-tests"},
	}
	// list has no --skip option. check does. Both must print the same key flag.
	root := &cobra.Command{Use: "dagger"}
	list := &cobra.Command{Use: "list"}
	check := &cobra.Command{Use: "check"}
	check.Flags().StringArray("skip", nil, "")
	root.AddCommand(list, check)
	for _, cmd := range []*cobra.Command{list, check} {
		require.Equal(t, "artifact-skip", artifactDimensionFlagNames(cmd, defs)[defs[0].Identifier].Key)
		require.Equal(t, "go-test", artifactDimensionFlagNames(cmd, defs)[defs[1].Identifier].Key)
	}
}
