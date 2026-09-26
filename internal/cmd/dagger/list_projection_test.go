package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestArtifactCollectionTypeKeys(t *testing.T) {
	item := listedArtifact{
		URI: "dag+go-test://go/modules/tests?go-module=./api&go-test=TestHealth", CollectionItem: true,
		DimensionKeys: []struct{ Dimension, Key string }{{"go/modules", "./api"}, {"go/modules/tests", "TestHealth"}, {"type:GoTest", "go/modules/tests"}},
	}
	path := artifactListPath{URI: "dag+check://go/modules/tests/run", Dimensions: []string{"go/modules", "go/modules/tests", "type:Check"}}
	for _, tc := range []struct {
		name  string
		paths []artifactListPath
		omit  bool
	}{
		{"descendant operations", []artifactListPath{path}, true},
		{"module checks do not match a test key", []artifactListPath{path, {URI: "dag+check://go/modules/test", Dimensions: []string{"go/modules", "type:Check"}}}, true},
		{"empty sibling counts", []artifactListPath{path, {URI: "dag+check://other/tests/run", Dimensions: path.Dimensions}}, false},
		{"unknown schema", nil, false},
		{"path boundaries", []artifactListPath{{URI: "dag+check://go/modules/tests-extra/run", Dimensions: path.Dimensions}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := item
			other.DimensionKeys = slices.Clone(item.DimensionKeys)
			other.DimensionKeys[0].Key = "other"
			items := []listedArtifact{item, other}
			require.NoError(t, omitCollectionTypeKeys(items, tc.paths))
			for _, got := range items {
				require.Equal(t, tc.omit, got.OmitTypeKey)
				require.Equal(t, item.URI, got.URI, "display must preserve the complete link")
			}
		})
	}
}

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
	t.Run("schema matrix and repeated keys keep their filters", func(t *testing.T) {
		index, err := newArtifactNameIndex([]artifactListPath{run})
		require.NoError(t, err)
		matrix := item
		matrix.DimensionKeys = []struct{ Dimension, Key string }{{"module", "go"}, {"type:Check", "go/modules/tests/run"}}
		matrix.Presence = []string{"go/modules", "go/modules/tests"}
		require.True(t, canOmitArtifactCLITypeKey(matrix, index, []string{"Check"}))
		group := item
		group.DimensionKeys = append(slices.Clone(item.DimensionKeys), struct{ Dimension, Key string }{"go/modules/tests", "TestBar"})
		require.True(t, canOmitArtifactCLITypeKey(group, index, []string{"Check"}))
		group.CollectionItem = true
		require.False(t, canOmitArtifactCLITypeKey(group, index, []string{"Check"}))
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

func TestArtifactListProduct(t *testing.T) {
	row := func(module, test string) listedArtifact {
		return listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{{"go/modules", module}, {"go/modules/tests", test}}}
	}
	t.Run("full product collapses", func(t *testing.T) {
		items := []listedArtifact{row("api", "health"), row("api", "ready"), row("worker", "health"), row("worker", "ready")}
		require.True(t, artifactRowsFormProduct(items))
		require.Len(t, artifactListRows(items, false), 1)
		require.Len(t, artifactListRows(items, true), 4)
	})
	t.Run("diagonal pairs cannot become independent flags", func(t *testing.T) {
		items := []listedArtifact{row("api", "health"), row("worker", "ready")}
		require.False(t, artifactRowsFormProduct(items))
		require.Len(t, artifactListRows(items, false), 2)
	})
	t.Run("duplicate rows cannot fill missing pairs", func(t *testing.T) {
		items := []listedArtifact{row("api", "health"), row("api", "health"), row("worker", "ready"), row("worker", "ready")}
		require.False(t, artifactRowsFormProduct(items))
	})
	t.Run("missing dimension is not an empty key", func(t *testing.T) {
		missing := listedArtifact{DimensionKeys: []struct{ Dimension, Key string }{{"go/modules", "api"}}}
		require.False(t, artifactRowsFormProduct([]listedArtifact{row("api", ""), missing}))
	})
	t.Run("empty keys can form a product", func(t *testing.T) {
		require.True(t, artifactRowsFormProduct([]listedArtifact{row("api", ""), row("worker", "")}))
	})
	t.Run("order does not change identity", func(t *testing.T) {
		a, b := row("api", "health"), row("worker", "health")
		slices.Reverse(b.DimensionKeys)
		require.True(t, artifactRowsFormProduct([]listedArtifact{a, b}))
	})
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
		require.Equal(t, "build-skip", artifactDimensionFlagNames(cmd, defs)[defs[0].Identifier].Key)
		require.Equal(t, "go-test", artifactDimensionFlagNames(cmd, defs)[defs[1].Identifier].Key)
	}
}

func TestArtifactCollectionFlagNames(t *testing.T) {
	defs := artifact.Dimensions{
		{Identifier: "/tools/items", Kind: "COLLECTION", Name: "item"},
		{Identifier: "/tools/other", Kind: "COLLECTION", Name: "item"},
		{Identifier: "/tools/parents", Kind: "COLLECTION", Name: "parent"},
		{Identifier: "/tools/parents/items", Kind: "COLLECTION", Name: "item"},
	}
	for range 2 {
		names := artifactDimensionFlagNames(&cobra.Command{Use: "check"}, defs)
		require.Equal(t, artifactFlagNames{Key: "tools-items-item", Presence: "tools-items"}, names["/tools/items"])
		require.Equal(t, artifactFlagNames{Key: "tools-other-item", Presence: "tools-other"}, names["/tools/other"])
		require.Equal(t, artifactFlagNames{Key: "tools-parents-item", Presence: "tools-parents-items"}, names["/tools/parents/items"])
		slices.Reverse(defs)
	}
}
