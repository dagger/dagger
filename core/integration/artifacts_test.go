package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ArtifactsSuite struct{}

func TestArtifacts(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ArtifactsSuite{})
}

func artifactSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile("dagger.toml", `[modules.provider]
source = "./provider"
entrypoint = true
settings.label = "configured"
[modules.consumer]
source = "./consumer"
settings.input = "provider/marker"
`).
		WithNewFile("marker.txt", "original").
		WithNewFile("provider/dagger-module.toml", `name = "provider"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
`).
		WithNewFile("provider/main.dang", `type Provider {
  pub label: String!
  pub base(ws: Workspace!): Container! {
    container.withNewFile("/marker", label + ":" + ws.file("marker.txt").contents)
  }
  pub marker(ws: Workspace!): File! { base(ws).file("/marker") }
  pub docs: Docs! { Docs() }
  pub otherDocs: Docs! { Docs() }
  pub optionalDocs: Docs { null }
  pub docList: [Docs!]! { [Docs()] }
  pub broken: Container! { raise "artifact function was evaluated" }
  pub needsArgument(name: String!): Container! { container.withNewFile("/marker", name) }
}
type Docs {
  pub again: Docs! { self }
  pub source: Directory! { directory.withNewFile("readme", "docs") }
}`).
		WithNewFile("consumer/dagger-module.toml", `name = "consumer"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
`).
		WithNewFile("consumer/main.dang", `type Consumer {
  pub input: File!
  pub base: Container! { container.withFile("/marker", input) }
  pub selected(ws: Workspace!): Artifacts! {
    ws.artifacts.filterPath(["base"])
  }
  pub single(ws: Workspace!): Artifact! { selected(ws).one }
  pub resolve(ws: Workspace!, address: String!): Address! { ws.resolve(address) }

}`)
}

func (ArtifactsSuite) TestMetadataAndFilters(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts {
    types
    containers: filterTypes(types: ["Container"]) { types items { path collectionKeys { collection key } pretty } }
    nested: filterPath(path: ["docs", "source"]) { items { pretty } }
    sibling: filterPath(path: ["other-docs", "source"]) { items { pretty } }
    optional: filterPath(path: ["optional-docs", "source"]) { pretty }
    list: filterPath(path: ["doc-list", "source"]) { pretty }
    cycle: filterPath(path: ["docs", "again", "source"]) { pretty }
    noType: filterTypes(types: []) { types pretty }
    unknown: filterTypes(types: ["Unknown"]) { pretty }
    noCollection: filterCollections(collections: ["missing"]) { pretty }
    noKey: filterCollectionKeys(collection: "missing", keys: ["anything"]) { pretty }
    exact: filterPath(path: ["b*"]) { pretty }
    colon: filterPath(path: ["docs:source"]) { pretty }
    slash: filterPath(path: ["docs/source"]) { pretty }
    order: filterPath(path: ["source", "docs"]) { pretty }
    prefix: filterPath(path: ["docs"]) { pretty }
    first: filterTypes(types: ["Container", "Directory"]) { filterPath(path: ["base"]) { pretty } }
    second: filterPath(path: ["base"]) { filterTypes(types: ["Container", "Directory"]) { pretty } }
    contradiction: filterTypes(types: ["Container"]) { filterTypes(types: ["Directory"]) { pretty } }
    collections
    collectionKeys(collection: "missing")
  } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"artifacts":{
  "types":["Artifact","Artifacts","Consumer","Container","Directory","File","Provider","ProviderDocs"],
  "containers":{"types":["Container"],"items":[
    {"path":["base"],"collectionKeys":[],"pretty":"base"},
    {"path":["broken"],"collectionKeys":[],"pretty":"broken"},
    {"path":["consumer","base"],"collectionKeys":[],"pretty":"consumer/base"}
  ]},
  "nested":{"items":[{"pretty":"docs/source"}]},
  "sibling":{"items":[{"pretty":"other-docs/source"}]},
  "optional":{"pretty":[]},"list":{"pretty":[]},"cycle":{"pretty":[]},
  "noType":{"types":[],"pretty":[]},"unknown":{"pretty":[]},"noCollection":{"pretty":[]},"noKey":{"pretty":[]},
  "exact":{"pretty":[]},"colon":{"pretty":[]},"slash":{"pretty":[]},"order":{"pretty":[]},"prefix":{"pretty":["docs"]},
  "first":{"filterPath":{"pretty":["base"]}},"second":{"filterTypes":{"pretty":["base"]}},
  "contradiction":{"filterTypes":{"pretty":[]}},"collections":[],"collectionKeys":[]
}}}`, string(*got))
	for _, filter := range []string{`filterPath(path: ["absent"])`, `filterTypes(types: ["Container"])`} {
		_, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { `+filter+` { one { pretty } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
		require.ErrorContains(t, err, "expected exactly one artifact")
	}
}

func (ArtifactsSuite) TestModuleObjects(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := artifactSource(c).AsWorkspace()
	for _, tc := range []struct {
		path     []string
		typeName string
	}{
		{[]string{"provider"}, "Provider"},
		{[]string{"consumer"}, "Consumer"},
		{[]string{"docs"}, "ProviderDocs"},
		{[]string{"docs", "again"}, "ProviderDocs"},
	} {
		artifact := ws.Artifacts().FilterPath(tc.path).One()
		id, err := artifact.ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($id: ID!) {
  node(id: $id) { ... on Artifact { value { __typename id } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": id}})
		require.NoError(t, err)
		require.Contains(t, string(*got), `"__typename":"`+tc.typeName+`"`)
	}
}

func (ArtifactsSuite) TestInclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := artifactSource(c).AsWorkspace()
	for _, tc := range []struct {
		patterns []string
		want     []string
	}{
		{[]string{"docs"}, []string{"docs", "docs/again", "docs/source"}},
		{[]string{"provider/docs"}, []string{"docs", "docs/again", "docs/source"}},
		{[]string{"provider:docs"}, []string{"docs", "docs/again", "docs/source"}},
		{[]string{"**/source"}, []string{"docs/source", "other-docs/source"}},
		{[]string{"**:source"}, []string{"docs/source", "other-docs/source"}},
		{[]string{"base", "consumer/base"}, []string{"base", "consumer/base"}},
	} {
		got, err := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: tc.patterns}).Pretty(ctx)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}

func (ArtifactsSuite) TestWorkspaceBindingAndIDs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, address := range []string{"base", "consumer/base"} {
		t.Run(address, func(ctx context.Context, t *testctx.T) {
			var ids []dagger.ID
			for _, contents := range []string{"first", "second"} {
				wsID, err := artifactSource(c).WithNewFile("marker.txt", contents).AsWorkspace().ID(ctx)
				require.NoError(t, err)
				res, err := testutil.QueryWithClient[struct {
					Node struct {
						Artifacts struct {
							Selected struct{ Items []struct{ ID dagger.ID } }
						}
					}
				}](c, t, `query($ws: ID!, $path: [String!]!) {
  node(id: $ws) { ... on Workspace { artifacts {
    selected: filterPath(path: $path) { items { id } }
  } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "path": strings.Split(address, "/")}})
				require.NoError(t, err)
				require.Len(t, res.Node.Artifacts.Selected.Items, 1)
				ids = append(ids, res.Node.Artifacts.Selected.Items[0].ID)
			}
			require.NotEqual(t, ids[0], ids[1])
			for _, i := range []int{0, 1, 0} {
				got, err := testutil.QueryWithClient[struct {
					Node struct {
						Value struct{ File struct{ Contents string } }
					}
				}](c, t, `query($id: ID!) {
  node(id: $id) { ... on Artifact { value { ... on Container { file(path: "/marker") { contents } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": ids[i]}})
				require.NoError(t, err)
				require.Equal(t, "configured:"+[]string{"first", "second"}[i], got.Node.Value.File.Contents)
			}
		})
	}
}

func (ArtifactsSuite) TestModuleBoundary(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory(".", artifactSource(c))
	out, err := base.With(daggerCall("consumer", "selected", "one", "pretty")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "base", strings.TrimSpace(out))
	out, err = base.With(daggerCall("consumer", "resolve", "--address=provider/base", "container", "file", "--path=/marker", "contents")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "configured:original")
}

func (ArtifactsSuite) TestLegacyAddress(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithNewFile("dep/dagger.json", `{"name":"dep","sdk":"dang","engineVersion":"v0.21.5"}`).
		WithNewFile("dep/main.dang", `type Dep {
  pub base: Container! { container.withNewFile("/marker", "legacy") }
}`).
		WithNewFile("legacy/dagger.json", `{"name":"legacy","sdk":"dang","engineVersion":"v0.21.5",
"dependencies":[{"name":"dep","source":"../dep"}]}`).
		WithNewFile("legacy/main.dang", `type Legacy {
  pub base: Container! { address("dep:base").container }
}`)
	out, err := base.With(daggerCallAt("./legacy", "base", "file", "--path=/marker", "contents")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "legacy", strings.TrimSpace(out))
}

func (ArtifactsSuite) TestExplicitEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory(".", artifactSource(c)).
		WithNewFile("extra/dagger-module.toml", "name = \"extra\"\nengineVersion = \"v1.0.0\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("extra/main.dang", `type Extra {
  pub base: Container! { container.withNewFile("/marker", "extra") }
}`)
	out, err := base.With(daggerQueryAt("./extra", `{ currentWorkspace {
  resolve(value: "base") { container { file(path: "/marker") { contents } } }
} }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"resolve":{"container":{"file":{"contents":"extra"}}}}}`, out)
}

func (ArtifactsSuite) TestCoreSelection(ctx context.Context, t *testctx.T) {
	for _, mode := range []string{"local", "remote"} {
		t.Run(mode, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			source := artifactSource(c)
			base := nativeWorkspaceBase(t, c)
			ref := "/work/selected"
			if mode == "remote" {
				ref = workspaceSelectionRemoteRef(ctx, t, c, source)
			} else {
				base = base.WithDirectory(ref, source)
			}
			out, err := base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace {
  artifacts { filterTypes(types: ["Container", "Directory"]) { items { path } } }
  resolve(value: "consumer/base") { container { file(path: "/marker") { contents } } }
} }`, "-W", ref, "-m", "core")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"currentWorkspace":{
  "artifacts":{"filterTypes":{"items":[
    {"path":["base"]}, {"path":["broken"]}, {"path":["consumer","base"]},
    {"path":["docs","source"]}, {"path":["other-docs","source"]}
  ]}},
  "resolve":{"container":{"file":{"contents":"configured:original"}}}
}}`, out)
		})
	}
}

func (ArtifactsSuite) TestCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory("/work/selected", artifactSource(c))
	for _, tc := range []struct {
		command string
		want    string
	}{
		{"containers", "base\nbroken\nconsumer/base\n"},
		{"directories", "docs/source\nother-docs/source\n"},
		{"files", "consumer/input\nmarker\n"},
		{"Artifact", "consumer/single\n"},
		{"Artifacts", "consumer/selected\n"},
		{"provider-docs", "docs\ndocs/again\nother-docs\nother-docs/again\n"},
	} {
		t.Run(tc.command, func(ctx context.Context, t *testctx.T) {
			out, err := base.With(workspaceSelectionDaggerExec("-W", "/work/selected", "workspace", tc.command)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
	for _, args := range [][]string{
		{"workspace", "--help"},
		{"ws", "--help"},
		{"help", "ws"},
	} {
		out, err := base.WithWorkdir("/work/selected").With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
		require.NoError(t, err)
		for _, typeName := range []string{"Container", "Directory", "File", "Artifact", "Artifacts"} {
			require.Contains(t, out, "List "+typeName+" artifacts")
		}
	}
	out, err := base.With(workspaceSelectionDaggerExec("-W", "/work/selected", "ws", "containers", "--help")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "dagger workspace containers [flags]")
	out, err = base.With(workspaceSelectionDaggerExec("__complete", "-W", "/work/selected", "workspace", "dir")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "directories\tList Directory artifacts\n")
}

func (ArtifactsSuite) TestArtifactsCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory("/work/selected", artifactSource(c))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "--type", "Container"}, "base\nbroken\nconsumer/base\n"},
		{[]string{"list", "consumer", "--type", "Container", "--type", "File"}, "consumer/base\nconsumer/input\n"},
		{[]string{"list", "docs"}, "docs\ndocs/again\ndocs/source\n"},
		{[]string{"list", "provider/docs"}, "docs\ndocs/again\ndocs/source\n"},
		{[]string{"list", "provider:docs"}, "docs\ndocs/again\ndocs/source\n"},
		{[]string{"list", "**/source"}, "docs/source\nother-docs/source\n"},
		{[]string{"list", "base", "consumer/base"}, "base\nconsumer/base\n"},
		{[]string{"types"}, "Artifact\nArtifacts\nConsumer\nContainer\nDirectory\nFile\nProvider\nProviderDocs\n"},
		{[]string{"types", "docs"}, "Directory\nProviderDocs\n"},
		{[]string{"collections"}, ""},
		{[]string{"keys", "go-module"}, ""},
		{[]string{"list", "--collection-key", "go-module=sdk/go"}, ""},
	} {
		t.Run(strings.Join(tc.args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected", "artifacts"}, tc.args...)
			out, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
	out, err := base.With(workspaceSelectionDaggerExec("-W", "/work/selected", "artifacts", "--help")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "--type")
	require.Contains(t, out, "List types of matching artifacts")
	out, err = base.With(workspaceSelectionDaggerExec("__complete", "-W", "/work/selected", "artifacts", "--type", "Pro")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "ProviderDocs\n")
	reserved := base.
		WithNewFile("/work/selected/dagger.toml", `[modules.provider]
source = "./provider"
entrypoint = true
`).
		WithNewFile("/work/selected/provider/main.dang", `type Provider {
  pub types: Directory! { directory }
}`)
	out, err = reserved.With(workspaceSelectionDaggerExec("-W", "/work/selected", "artifacts", "list", "types")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "types\n", out)
}

func (ArtifactsSuite) TestResolution(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	for _, address := range []string{"base", "provider/base", "consumer/base", "provider:base", "consumer:base"} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { container { file(path: "/marker") { contents } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": address}})
		require.NoError(t, err)
		require.Contains(t, string(*got), "configured:original")
	}
	for _, address := range []string{"docs/source", "provider/docs/source", "docs:source", "provider:docs:source"} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { directory { file(path: "readme") { contents } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": address}})
		require.NoError(t, err)
		require.Contains(t, string(*got), `"contents":"docs"`)
	}
	for _, address := range []string{"provider/", "provider/missing", "provider/docs/missing", "provider/marker", "provider/needs-argument", "provider:missing", "broken"} {
		_, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { container { id } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": address}})
		require.Error(t, err, address)
		require.Contains(t, err.Error(), "resolve module reference", address)
		require.NotContains(t, err.Error(), "docker.io", address)
	}
	for _, image := range []string{alpineImage, "docker.io/library/" + alpineImage} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $image: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $image) { container { imageRef } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "image": image}})
		require.NoError(t, err)
		require.Contains(t, string(*got), "alpine")
	}
}

func (ArtifactsSuite) TestAddressIDs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var ids []dagger.ID
	for _, contents := range []string{"first", "second"} {
		wsID, err := artifactSource(c).WithNewFile("marker.txt", contents).AsWorkspace().ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[struct {
			Node struct{ Resolve struct{ ID dagger.ID } }
		}](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { resolve(value: "consumer/base") { id } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
		require.NoError(t, err)
		ids = append(ids, got.Node.Resolve.ID)
	}
	require.NotEqual(t, ids[0], ids[1])
	for _, i := range []int{0, 1, 0} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($id: ID!) {
  node(id: $id) { ... on Address { container { file(path: "/marker") { contents } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": ids[i]}})
		require.NoError(t, err)
		require.Contains(t, string(*got), "configured:"+[]string{"first", "second"}[i])
	}
}

func (ArtifactsSuite) TestWorkspaceEdits(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory(".", artifactSource(c))
	for _, query := range []string{
		`{ currentWorkspace { withNewFile(path: "marker.txt", contents: "edited") { artifacts { filterPath(path: ["base"]) { one { value { ... on Container { file(path: "/marker") { contents } } } } } } } } }`,
		`{ currentWorkspace { withNewFile(path: "marker.txt", contents: "edited") { resolve(value: "consumer/base") { container { file(path: "/marker") { contents } } } } } }`,
	} {
		out, err := base.With(daggerQuery(query)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "configured:edited")
	}
	config := `[modules.provider]
source = "./provider"
entrypoint = true
settings.label = "changed"
[modules.consumer]
source = "./consumer"
settings.input = "provider:marker"
`
	out, err := base.With(daggerQuery(`{ currentWorkspace { withNewFile(path: "dagger.toml", contents: %q) {
  resolve(value: "consumer/base") { container { file(path: "/marker") { contents } } }
} } }`, config)).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "changed:original")
	out, err = base.With(daggerQuery(`{ currentWorkspace { withNewFile(path: "dagger.toml", contents: "[modules]\n") {
  artifacts { pretty }
} } }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"withNewFile":{"artifacts":{"pretty":[]}}}}`, out)
	_, err = base.With(daggerQuery(`{ address(value: "provider/marker") { file { contents } } }`)).Stdout(ctx)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "resolve module reference")
}

func (ArtifactsSuite) TestAliasAndShorthandSetting(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).WithNewFile("dagger.toml", `[modules.provider]
source = "./provider"
entrypoint = true
settings.label = "configured"
[modules.alias]
source = "./provider"
settings.label = "aliased"
[modules.consumer]
source = "./consumer"
settings.input = "marker"
`).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	for address, want := range map[string]string{"alias/base": "aliased:original", "consumer/base": "configured:original"} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, fmt.Sprintf(`query($ws: ID!) {
  node(id: $ws) { ... on Workspace { resolve(value: %q) { container { file(path: "/marker") { contents } } } } }
}`, address), &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
		require.NoError(t, err)
		require.Contains(t, string(*got), want)
	}
}

func (ArtifactsSuite) TestAmbiguousEntrypoints(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).WithNewFile("dagger.toml", `[modules.first]
source = "./provider"
entrypoint = true
settings.label = "first"
[modules.second]
source = "./provider"
entrypoint = true
settings.label = "second"
`).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	_, err = testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { items { path } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.ErrorContains(t, err, "ambiguous artifact path")
}
