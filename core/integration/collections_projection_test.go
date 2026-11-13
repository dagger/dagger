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
			"name":         "name",
			"ToolsModules": "tools-module",
			"ToolsCases":   "tools-probe",
		}, dimensions)
		require.Len(t, selection.PathDefinitions, 4)
		for _, path := range selection.PathDefinitions {
			require.Contains(t, path.Dimensions, "ToolsModules")
			for _, dimension := range path.Dimensions {
				require.False(t, strings.HasPrefix(dimension, "/"), dimension)
			}
		}
		for _, platform := range []string{"linux", "windows"} {
			out, err := ctr.With(daggerExec("check", "-l", "--name="+platform+".modules.cases.run", "-f=link")).Stdout(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, strings.TrimSpace(out))
			// Listing a schema path cannot read collection keys or check values.
			replay, err := ctr.With(daggerExec("check", "-l", strings.TrimSpace(out), "-f=link")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, out, replay)
		}
	})

	t.Run("nested selectors preserve the route and parent key", func(ctx context.Context, t *testctx.T) {
		for _, platform := range []string{"linux", "windows"} {
			name := platform + ".modules.cases.run"
			out, err := base.With(daggerExec("check", "-la", "-f=cli",
				"--tools-module=api", "--tools-probe=first", "--name="+name)).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "--tools-module=api")
			require.Contains(t, out, "--tools-probe=first")
			require.Contains(t, out, "--name="+name)
			selectors, _, _ := strings.Cut(out, "#")
			require.True(t, strings.HasPrefix(selectors, "dagger check "), selectors)
			replay := append([]string{"check", "-la", "-f=link"}, strings.Fields(strings.TrimPrefix(selectors, "dagger check "))...)
			links, err := base.With(daggerExec(replay...)).Stdout(ctx)
			require.NoError(t, err)
			link := strings.TrimSpace(links)
			require.Len(t, strings.Split(link, "\n"), 1)
			address, err := url.Parse(link)
			require.NoError(t, err)
			require.Equal(t, "dag+check", address.Scheme)
			selected, err := base.With(daggerExec("check", "-la", "-f=link", link)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, links, selected)
			// Replay the address through a fresh API selection. Shared collection
			// types must preserve the platform route in the full name coordinate.
			metadata, err := base.With(daggerQueryAt("core", `{
    currentWorkspace { artifacts { filterUri(uri: %q) { one {
     dimensionKeys(all: true) { dimension key }
    } } } }
   }`, link)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"currentWorkspace":{"artifacts":{"filterUri":{"one":{"dimensionKeys":[
    {"dimension":"module","key":"tools"},
    {"dimension":"ToolsModules","key":"api"},
    {"dimension":"ToolsCases","key":"first"},
    {"dimension":"name","key":"`+name+`"}
   ]}}}}}`, metadata)
		}
	})
}
