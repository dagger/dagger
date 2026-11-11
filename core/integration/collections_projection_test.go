package core

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (CollectionsSuite) TestCollectionPathProjection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `package main
type Tools struct{}
func (*Tools) Linux() *Profile { return &Profile{} }
func (*Tools) Windows() *Profile { return &Profile{} }
type Profile struct{}
func (*Profile) Modules() *Modules { return &Modules{Names: []string{"api", "worker"}} }
// +collection
type Modules struct {
  // +keys
  Names []string
}
func (*Modules) Get(path string) *ToolsModule { return &ToolsModule{Path: path} }
type ToolsModule struct { Path string }
// +check
func (*ToolsModule) Verify() error { panic("check evaluated") }
func (m *ToolsModule) Cases() *Cases {
  if m.Path != "api" { panic("excluded parent evaluated") }
  return &Cases{Names: []string{"first", "second"}}
}
// +collection
type Cases struct {
  // +keys
  Names []string
}
func (*Cases) Get(name string) *Probe { return &Probe{} }
type Probe struct{}
// +check
func (*Probe) Run() error { panic("check evaluated") }
`
	base := goGitBase(t, c).
		WithNewFile("/work/dagger.toml", "[modules.tools]\nsource = \"./tools\"\n").
		WithNewFile("/work/tools/dagger.json", `{"name":"tools","engineVersion":"v1.0.0-beta.20","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("/work/tools/go.mod", "module dagger/tools\n\ngo 1.26.6\n").
		WithNewFile("/work/tools/main.go", source).
		WithWorkdir("/work")

	t.Run("schema paths stay distinct without reading keys", func(ctx context.Context, t *testctx.T) {
		ctr := base.WithNewFile("tools/main.go", strings.Replace(source,
			`return &Modules{Names: []string{"api", "worker"}}`,
			`panic("collection evaluated")`, 1))
		ws, err := ctr.Directory("/work").AsWorkspace().ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { filterTypes(types: ["Check"]) {
    dimensionDefinitions { identifier name kind }
    pathDefinitions { uri dimensions }
  } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": ws}})
		require.NoError(t, err)
		var result struct {
			Node struct {
				Artifacts struct {
					FilterTypes struct {
						DimensionDefinitions []struct{ Identifier, Name, Kind string }
						PathDefinitions      []struct {
							URI        string
							Dimensions []string
						}
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal(*got, &result))
		selection := result.Node.Artifacts.FilterTypes
		dimensions := map[string]string{}
		for _, dimension := range selection.DimensionDefinitions {
			if dimension.Identifier == "name" {
				require.Equal(t, "NAME", dimension.Kind)
			}
			if dimension.Identifier != "module" && !strings.HasPrefix(dimension.Identifier, "type:") {
				dimensions[dimension.Identifier] = dimension.Name
			}
		}
		require.Equal(t, map[string]string{
			"name":                         "name",
			"/tools/linux/modules":         "tools-module",
			"/tools/linux/modules/cases":   "tools-probe",
			"/tools/windows/modules":       "tools-module",
			"/tools/windows/modules/cases": "tools-probe",
		}, dimensions)
		paths := map[string][]string{}
		for _, path := range selection.PathDefinitions {
			paths[path.URI] = path.Dimensions
		}
		for _, platform := range []string{"linux", "windows"} {
			prefix := "tools/" + platform + "/modules"
			require.ElementsMatch(t, []string{"module", "/" + prefix, "type:Check"}, paths["dag://?check="+prefix+"/verify"])
			require.ElementsMatch(t, []string{"module", "/" + prefix, "/" + prefix + "/cases", "type:Check"}, paths["dag://?check="+prefix+"/cases/run"])
			out, err := ctr.With(daggerExec("check", "-l", "--tools-"+platform+"-cases", "-f=link")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "dag+check://"+prefix+"/cases/run\n", out)
		}
	})

	t.Run("nested selectors preserve the route and parent key", func(ctx context.Context, t *testctx.T) {
		for _, platform := range []string{"linux", "windows"} {
			out, err := base.With(daggerExec("check", "-la", "-f=cli",
				"--tools-"+platform+"-module=api", "--tools-"+platform+"-probe=first")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "--tools-"+platform+"-module=api")
			require.Contains(t, out, "--tools-"+platform+"-probe=first")
			selectors, _, _ := strings.Cut(out, "#")
			require.True(t, strings.HasPrefix(selectors, "dagger check "), selectors)
			replay := append([]string{"check", "-la", "-f=link"}, strings.Fields(strings.TrimPrefix(selectors, "dagger check "))...)
			links, err := base.With(daggerExec(replay...)).Stdout(ctx)
			require.NoError(t, err)
			link := strings.TrimSpace(links)
			path, query, ok := strings.Cut(link, "?")
			require.True(t, ok, link)
			require.Equal(t, "dag+check://tools/"+platform+"/modules/cases/run", path)
			keys, err := url.ParseQuery(query)
			require.NoError(t, err)
			require.Equal(t, url.Values{
				"tools-" + platform + "-module": {"api"},
				"tools-" + platform + "-probe":  {"first"},
			}, keys)
			selected, err := base.With(daggerExec("check", "-la", "-f=link", link)).Stdout(ctx)
			require.NoError(t, err)
			// The explicit path narrows schema loading. Its fallback retains
			// exact dimension identifiers instead of allocating local aliases.
			require.Equal(t, "dag+check://tools/"+platform+"/modules/cases/run?/tools/"+platform+"/modules=api&/tools/"+platform+"/modules/cases=first\n", selected)
		}
	})
}
