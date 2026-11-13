package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/shell"
)

func (CollectionsSuite) TestDirectNestedCollectionTypeColumns(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `package main

type Collections struct{}
func (*Collections) Rows() *Rows { return &Rows{Keys: []string{"top", "bottom"}} }

// +collection
type Rows struct {
  // +keys
  Keys []string
}
func (*Rows) Get(row string) *Row {
  return &Row{Name: row, Columns: []string{"left", "right"}}
}

// +collection
type Row struct {
  Name string
  // +keys
  Columns []string
}
func (r *Row) Get(column string) *Cell { return &Cell{Row: r.Name, Column: column} }

type Cell struct { Row string; Column string }
// +check
func (*Cell) Check() error { panic("check must stay deferred") }
`
	base := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).
		WithWorkdir("/work")
	type key struct{ Dimension, Key string }
	type item struct {
		URI           string
		DimensionKeys []key
	}
	var response struct {
		CurrentWorkspace struct {
			Artifacts struct {
				Cells struct {
					Dimensions []string
					Items      []item
					Listing    string
				}
				Checks struct{ Items []item }
			}
		}
	}
	out, err := base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace { artifacts {
  cells: filterTypes(types: ["CollectionsCell"]) {
    dimensions(all: true)
    items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } }
    listing: __listing(schema: true)
  }
  checks: filterTypes(types: ["Check"]) {
    items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } }
  }
} } }`, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &response))
	cells := response.CurrentWorkspace.Artifacts.Cells
	require.Equal(t, []string{"CollectionsRow", "CollectionsRows", "module", "name"}, cells.Dimensions)
	require.Len(t, cells.Items, 4)
	require.Len(t, response.CurrentWorkspace.Artifacts.Checks.Items, 4)
	wantCells := []map[string]string{}
	wantChecks := []map[string]string{}
	for _, row := range []string{"top", "bottom"} {
		for _, column := range []string{"left", "right"} {
			wantCells = append(wantCells, map[string]string{"module": "collections", "CollectionsRows": row, "CollectionsRow": column, "name": "rows"})
			wantChecks = append(wantChecks, map[string]string{"module": "collections", "CollectionsRows": row, "CollectionsRow": column, "name": "rows.check"})
		}
	}
	coordinateMaps := func(items []item) []map[string]string {
		result := make([]map[string]string, len(items))
		for i, item := range items {
			result[i] = map[string]string{}
			for _, coordinate := range item.DimensionKeys {
				_, duplicate := result[i][coordinate.Dimension]
				require.False(t, duplicate, "repeated coordinate: %s", coordinate.Dimension)
				result[i][coordinate.Dimension] = coordinate.Key
			}
		}
		return result
	}
	require.ElementsMatch(t, wantCells, coordinateMaps(cells.Items))
	require.ElementsMatch(t, wantChecks, coordinateMaps(response.CurrentWorkspace.Artifacts.Checks.Items))
	var schemaRows []struct{ Projection flattening.Field }
	require.NoError(t, json.Unmarshal([]byte(cells.Listing), &schemaRows))
	require.Len(t, schemaRows, 1)
	require.Equal(t, flattening.Field{
		Module: "collections", Type: "CollectionsCell", LocalPath: []string{},
		Collections: []flattening.Collection{
			{Fields: []string{"rows"}, Type: "CollectionsRows", ItemType: "CollectionsRow", KeyName: "row"},
			{Fields: []string{}, Type: "CollectionsRow", ItemType: "CollectionsCell", KeyName: "column"},
		},
	}, schemaRows[0].Projection)

	out, err = base.With(daggerExec("list", "collections-cells", "-a")).Stdout(ctx)
	require.NoError(t, err)
	header := strings.SplitN(out, "\n", 2)[0]
	require.ElementsMatch(t, []string{"COLLECTIONS-ROW", "COLLECTIONS-CELL", "LINK"}, strings.Fields(header))
	out, err = base.With(daggerExec("list", "collections-cells", "-a", "-f=link")).Stdout(ctx)
	require.NoError(t, err)
	links := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, links, 4)
	apiByLink := map[string]item{}
	for _, cell := range cells.Items {
		apiByLink[cell.URI] = cell
	}
	require.Len(t, apiByLink, 4)
	for _, link := range links {
		expected, exists := apiByLink[link]
		require.True(t, exists, "CLI link is absent from API rows: %s", link)
		// Every query starts a new CLI session. No address plan is retained.
		out, err := base.With(workspaceSelectionDaggerQuery(fmt.Sprintf(`{ currentWorkspace { artifacts {
  filterUri(uri: %q) { items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } } }
} } }`, link), "-m", "core")).Stdout(ctx)
		require.NoError(t, err)
		var replay struct {
			CurrentWorkspace struct {
				Artifacts struct{ FilterURI struct{ Items []item } }
			}
		}
		require.NoError(t, json.Unmarshal([]byte(out), &replay))
		require.Len(t, replay.CurrentWorkspace.Artifacts.FilterURI.Items, 1)
		require.ElementsMatch(t, expected.DimensionKeys, replay.CurrentWorkspace.Artifacts.FilterURI.Items[0].DimensionKeys)
	}

	out, err = base.With(daggerExec("list", "collections-cells", "-a", "-f=cli")).Stdout(ctx)
	require.NoError(t, err)
	commands := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, commands, 4)
	replayed := []string{}
	for _, command := range commands {
		args, err := shell.Fields(command, nil)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(args), 3)
		require.Equal(t, []string{"dagger", "list", "collections-cells"}, args[:3])
		args = append(args[1:], "-a", "-f=link")
		out, err := base.With(daggerExec(args...)).Stdout(ctx)
		require.NoError(t, err)
		selected := strings.Split(strings.TrimSpace(out), "\n")
		require.Len(t, selected, 1)
		replayed = append(replayed, selected[0])
	}
	require.ElementsMatch(t, links, replayed)
}

func (CollectionsSuite) TestSameCollectionTypeKeepsSourceFieldNames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `package main

type Collections struct{}
func (*Collections) Before() *Tests { return &Tests{Keys: []string{"unit"}} }
func (*Collections) After() *Tests { return &Tests{Keys: []string{"unit"}} }

// +collection
type Tests struct {
  // +keys
  Keys []string
}
func (*Tests) Get(name string) *Test { return &Test{} }
type Test struct{}
// +check
func (*Test) Check() error { panic("check must stay deferred") }
`
	base := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).
		WithWorkdir("/work")
	type key struct{ Dimension, Key string }
	type item struct {
		URI           string
		DimensionKeys []key
	}
	out, err := base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace { artifacts {
  filterTypes(types: ["Check"]) {
    items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } }
  }
} } }`, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	// Decode the named selection to keep the response structure explicit.
	var selected struct {
		CurrentWorkspace struct {
			Artifacts struct{ FilterTypes struct{ Items []item } }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &selected))
	items := selected.CurrentWorkspace.Artifacts.FilterTypes.Items
	require.Len(t, items, 2)
	byName := map[string]item{}
	for _, row := range items {
		coordinates := map[string]string{}
		for _, coordinate := range row.DimensionKeys {
			coordinates[coordinate.Dimension] = coordinate.Key
		}
		require.Equal(t, "unit", coordinates["CollectionsTests"])
		require.Equal(t, "collections", coordinates["module"])
		byName[coordinates["name"]] = row
	}
	require.Contains(t, byName, "before.check")
	require.Contains(t, byName, "after.check")
	require.NotEqual(t, byName["before.check"].URI, byName["after.check"].URI)
	for name, expected := range byName {
		out, err := base.With(workspaceSelectionDaggerQuery(fmt.Sprintf(`{ currentWorkspace { artifacts {
  filterUri(uri: %q) { items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } } }
} } }`, expected.URI), "-m", "core")).Stdout(ctx)
		require.NoError(t, err)
		var replay struct {
			CurrentWorkspace struct {
				Artifacts struct{ FilterURI struct{ Items []item } }
			}
		}
		require.NoError(t, json.Unmarshal([]byte(out), &replay))
		require.Len(t, replay.CurrentWorkspace.Artifacts.FilterURI.Items, 1, name)
		require.ElementsMatch(t, expected.DimensionKeys, replay.CurrentWorkspace.Artifacts.FilterURI.Items[0].DimensionKeys)
	}
	out, err = base.With(daggerExec("check", "-la")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "before.check")
	require.Contains(t, out, "after.check")
}

func (CollectionsSuite) TestCrossModuleCollectionLabelsStayStable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `package main
import "dagger/%s/internal/dagger"

type %s struct{}
func (*%s) %s() *%s { return &%s{Keys: []string{"one", "two"}} }

// +collection
type %s struct {
  // +keys
  Keys []string
}
func (*%s) Get(name string) *dagger.File { panic("file must stay deferred") }
`
	dir := collectionSource(c).
		WithNewFile("dagger.toml", "[modules.alpha]\nsource = \"./alpha\"\n[modules.beta]\nsource = \"./beta\"\n")
	for _, module := range []struct{ name, root, collection string }{
		{"alpha", "Alpha", "Packages"}, {"beta", "Beta", "Dependencies"},
	} {
		dir = dir.
			WithNewFile(module.name+"/dagger.json", fmt.Sprintf(`{"name":%q,"engineVersion":"v1.0.0-beta.20","sdk":{"source":"go"},"source":"."}`, module.name)).
			WithNewFile(module.name+"/go.mod", "module dagger/"+module.name+"\n\ngo 1.26.6\n").
			WithNewFile(module.name+"/main.go", fmt.Sprintf(source, module.name, module.root, module.root, module.collection, module.collection, module.collection, module.collection, module.collection))
	}
	base := goGitBase(t, c).WithDirectory("/work", dir).WithWorkdir("/work")
	type definition struct{ Identifier, Name, Kind string }
	type selection struct {
		DimensionDefinitions []definition
		Items                []struct {
			URI           string
			DimensionKeys []struct{ Dimension, Key string }
		}
	}
	out, err := base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace {
  all: artifacts { filterTypes(types: ["File"]) {
    dimensionDefinitions { identifier name kind }
    items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } }
  } }
  module: artifacts { filterUri(uri: "dag://?module=alpha") { filterTypes(types: ["File"]) {
    dimensionDefinitions { identifier name kind }
    items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } }
  } } }
  source: artifacts(include: ["alpha"]) { filterTypes(types: ["File"]) {
    dimensionDefinitions { identifier name kind }
    items { uri(typeAssertion: true) dimensionKeys(all: true) { dimension key } }
  } }
} }`, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	var decoded struct {
		CurrentWorkspace struct {
			All    struct{ FilterTypes selection }
			Module struct {
				FilterURI struct{ FilterTypes selection }
			}
			Source struct{ FilterTypes selection }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &decoded))
	all := decoded.CurrentWorkspace.All.FilterTypes
	module := decoded.CurrentWorkspace.Module.FilterURI.FilterTypes
	sourceSelection := decoded.CurrentWorkspace.Source.FilterTypes
	require.Len(t, all.Items, 4)
	for _, selected := range []selection{all, module, sourceSelection} {
		labels := map[string]string{}
		for _, definition := range selected.DimensionDefinitions {
			if definition.Kind == "COLLECTION" {
				labels[definition.Identifier] = definition.Name
			}
		}
		require.Equal(t, "alpha-packages", labels["AlphaPackages"])
		if len(selected.Items) == 4 {
			require.Equal(t, "beta-dependencies", labels["BetaDependencies"])
		}
	}
	for _, selected := range []selection{module, sourceSelection} {
		require.Len(t, selected.Items, 2)
		for _, item := range selected.Items {
			found := false
			for _, candidate := range all.Items {
				if slices.Equal(candidate.DimensionKeys, item.DimensionKeys) {
					found = true
				}
			}
			require.True(t, found, "filtered coordinates are absent from full selection: %v", item.DimensionKeys)
		}
	}
	for _, args := range [][]string{{"list", "files", "-a"}, {"list", "files", "-a", "--module=alpha"}} {
		out, err := base.With(daggerExec(args...)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.SplitN(out, "\n", 2)[0], "ALPHA-PACKAGES")
		require.NotContains(t, strings.SplitN(out, "\n", 2)[0], "FILE ")
	}
}

func (CollectionsSuite) TestFullSchemaIncludesExcludeUnrelatedLoadFailure(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := collectionSource(c).
		WithNewFile("dagger.toml", "[modules.alpha]\nsource = \"./alpha\"\n[modules.beta]\nsource = \"./beta\"\n").
		WithNewFile("alpha/dagger.json", `{"name":"alpha","engineVersion":"v1.0.0-beta.26","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("alpha/go.mod", "module dagger/alpha\n\ngo 1.26.6\n").
		WithNewFile("alpha/main.go", `package main
type Alpha struct{}
// +check
func (*Alpha) Verify() error { panic("check must stay deferred") }
`).
		WithNewFile("beta/dagger.json", `{"name":"beta","engineVersion":"v1.0.0-beta.26","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("beta/go.mod", "module dagger/beta\n\ngo 1.26.6\n").
		WithNewFile("beta/main.go", "package main\ntype Beta struct{}\nthis is invalid Go\n")
	base := goGitBase(t, c).WithDirectory("/work", source).WithWorkdir("/work")
	out, err := base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace {
  alpha: artifacts(include: ["alpha"]) { items { path loadError } }
  full: artifacts { items { path loadError } }
} }`, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	type item struct {
		Path      []string
		LoadError string
	}
	var response struct {
		CurrentWorkspace struct {
			Alpha, Full struct{ Items []item }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &response))
	require.Equal(t, []item{{Path: []string{"alpha", "verify"}}}, response.CurrentWorkspace.Alpha.Items)
	require.Len(t, response.CurrentWorkspace.Full.Items, 2)
	foundBeta := false
	for _, row := range response.CurrentWorkspace.Full.Items {
		if row.Path[0] == "beta" {
			foundBeta = true
			require.Equal(t, []string{"beta", "load"}, row.Path)
			require.NotEmpty(t, row.LoadError)
		} else {
			require.Equal(t, item{Path: []string{"alpha", "verify"}}, row)
		}
	}
	require.True(t, foundBeta, "full discovery must retain the beta load failure")
}

func (CollectionsSuite) TestSharedRowAndCellCheckAddresses(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `package main

type Collections struct{}
func (*Collections) Rows() *Rows { return &Rows{Keys: []string{"top"}} }

// +collection
type Rows struct {
  // +keys
  Keys []string
}
func (*Rows) Get(row string) *Row { return &Row{Keys: []string{"left", "right"}} }

// +collection
type Row struct {
  // +keys
  Keys []string
}
func (*Row) Get(column string) *Cell { return &Cell{} }
// +check
func (*Row) Check() { panic("row check must stay deferred") }

type Cell struct{}
// +check
func (*Cell) Check() { panic("cell check must stay deferred") }
`
	base := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).
		WithWorkdir("/work")
	for _, expanded := range []bool{false, true} {
		t.Run(fmt.Sprintf("expanded=%t", expanded), func(ctx context.Context, t *testctx.T) {
			args := []string{"check", "-l"}
			count := 2
			if expanded {
				args = append(args, "-a")
				count = 3
			}
			out, err := base.With(daggerExec(append(slices.Clone(args), "-f=link")...)).Stdout(ctx)
			require.NoError(t, err)
			links := strings.Split(strings.TrimSpace(out), "\n")
			require.Len(t, links, count)
			require.Contains(t, out, "!")
			unique := slices.Clone(links)
			slices.Sort(unique)
			require.Len(t, slices.Compact(unique), count)
			for _, link := range links {
				out, err := base.With(daggerExec(append(slices.Clone(args), "-f=link", link)...)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, link+"\n", out)
				if expanded {
					out, err := base.With(workspaceSelectionDaggerQuery(fmt.Sprintf(`{ currentWorkspace { artifacts {
  filterUri(uri: %q) { items { uri(typeAssertion: true) } }
} } }`, link), "-m", "core")).Stdout(ctx)
					require.NoError(t, err)
					var response struct {
						CurrentWorkspace struct {
							Artifacts struct {
								FilterURI struct{ Items []struct{ URI string } }
							}
						}
					}
					require.NoError(t, json.Unmarshal([]byte(out), &response))
					require.Len(t, response.CurrentWorkspace.Artifacts.FilterURI.Items, 1)
				}
			}
			out, err = base.With(daggerExec(append(slices.Clone(args), "-f=cli")...)).Stdout(ctx)
			require.NoError(t, err)
			commands := strings.Split(strings.TrimSpace(out), "\n")
			require.Len(t, commands, count)
			replayed := []string{}
			for _, command := range commands {
				commandArgs, err := shell.Fields(command, nil)
				require.NoError(t, err)
				require.Equal(t, []string{"dagger", "check"}, commandArgs[:2])
				commandArgs = append(commandArgs[1:], "-l", "-f=link")
				if expanded {
					commandArgs = append(commandArgs, "-a")
				}
				out, err := base.With(daggerExec(commandArgs...)).Stdout(ctx)
				require.NoError(t, err)
				selected := strings.Split(strings.TrimSpace(out), "\n")
				require.Len(t, selected, 1)
				replayed = append(replayed, selected[0])
			}
			require.ElementsMatch(t, links, replayed)
		})
	}
}
