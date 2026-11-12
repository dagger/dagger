package core

import (
	"context"
	"encoding/json"
	"fmt"
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
			wantCells = append(wantCells, map[string]string{"module": "collections", "CollectionsRows": row, "CollectionsRow": column, "name": "."})
			wantChecks = append(wantChecks, map[string]string{"module": "collections", "CollectionsRows": row, "CollectionsRow": column, "name": "check"})
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
