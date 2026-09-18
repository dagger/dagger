package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
settings.input = "dag://provider/marker"
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
    types uri
    containers: filterTypes(types: ["Container"]) { types uri items { path dimensionKeys { dimension key } uri } }
    nested: filterPath(path: ["docs", "source"]) { uri items { uri } }
    sibling: filterPath(path: ["other-docs", "source"]) { items { uri } }
    optional: filterPath(path: ["optional-docs", "source"]) { items { uri } }
    list: filterPath(path: ["doc-list", "source"]) { items { uri } }
    cycle: filterPath(path: ["docs", "again", "source"]) { items { uri } }
    noType: filterTypes(types: []) { types uri items { uri } }
    unknown: filterTypes(types: ["Unknown"]) { items { uri } }
    noDimension: filterDimensions(dimensions: ["missing"]) { uri items { uri } }
    noKey: filterDimensionKeys(dimension: "missing", keys: ["anything"]) { uri items { uri } }
    exact: filterPath(path: ["b*"]) { items { uri } }
    colon: filterPath(path: ["docs:source"]) { items { uri } }
    slash: filterPath(path: ["docs/source"]) { items { uri } }
    order: filterPath(path: ["source", "docs"]) { items { uri } }
    prefix: filterPath(path: ["docs"]) { items { uri } }
    first: filterTypes(types: ["Container", "Directory"]) { filterPath(path: ["base"]) { uri items { uri } } }
    second: filterPath(path: ["base"]) { filterTypes(types: ["Container", "Directory"]) { uri items { uri } } }
    contradiction: filterTypes(types: ["Container"]) { filterTypes(types: ["Directory"]) { uri items { uri } } }
    dimensions
    dimensionKeys(dimension: "missing")
  } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"artifacts":{
  "types":["Artifact","Artifacts","Consumer","Container","Directory","File","Provider","ProviderDocs"],
  "uri":"dag://",
  "containers":{"types":["Container"],"uri":"dag+container://","items":[
    {"path":["base"],"dimensionKeys":[],"uri":"dag://base"},
    {"path":["broken"],"dimensionKeys":[],"uri":"dag://broken"},
    {"path":["consumer","base"],"dimensionKeys":[],"uri":"dag://consumer/base"}
  ]},
  "nested":{"uri":"dag://docs/source","items":[{"uri":"dag://docs/source"}]},
  "sibling":{"items":[{"uri":"dag://other-docs/source"}]},
  "optional":{"items":[]},"list":{"items":[]},"cycle":{"items":[]},
  "noType":{"types":[],"uri":"dag://{}","items":[]},"unknown":{"items":[]},
  "noDimension":{"uri":"dag://?missing","items":[]},"noKey":{"uri":"dag://?missing=anything","items":[]},
  "exact":{"items":[]},"colon":{"items":[]},"slash":{"items":[]},"order":{"items":[]},"prefix":{"items":[{"uri":"dag://docs"}]},
  "first":{"filterPath":{"uri":"dag+container+directory://base","items":[{"uri":"dag://base"}]}},
  "second":{"filterTypes":{"uri":"dag+container+directory://base","items":[{"uri":"dag://base"}]}},
  "contradiction":{"filterTypes":{"uri":"dag://{}","items":[]}},"dimensions":[],"dimensionKeys":[]
}}}`, string(*got))
	_, err = testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { filterPath(path: ["absent"]) { one { uri } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.ErrorContains(t, err, "no artifact matches dag://{}")
	_, err = testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { filterTypes(types: ["Container"]) { one { uri } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.ErrorContains(t, err, "dag+container:// matches 3 artifacts:\ndag://base\ndag://broken\ndag://consumer/base")
}

func (ArtifactsSuite) TestURI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts {
    container: filterPath(path: ["base"]) { one {
      uri
      typed: uri(typeAssertion: true)
      pathOnly: uri(dimensionKeys: false)
    } }
    docs: filterPath(path: ["docs", "again"]) { one { typed: uri(typeAssertion: true) } }
  } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"artifacts":{
  "container":{"one":{"uri":"dag://base","typed":"dag+container://base","pathOnly":"dag://base"}},
  "docs":{"one":{"typed":"dag+provider-docs://docs/again"}}
}}}`, string(*got))
	_, err = testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { filterPath(path: ["base"]) { one { uri(absolute: true) } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.ErrorContains(t, err, "no Git address")
}

func (ArtifactsSuite) TestAbsoluteURI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := workspaceSelectionRemoteRef(ctx, t, c, artifactSource(c))
	base := nativeWorkspaceBase(t, c)
	out, err := base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace {
  artifacts { filterPath(path: ["base"]) { one { uri(absolute: true) } } }
} }`, "-W", ref, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	var got struct {
		CurrentWorkspace struct {
			Artifacts struct {
				FilterPath struct{ One struct{ URI string } }
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	uri := got.CurrentWorkspace.Artifacts.FilterPath.One.URI
	// http://<host>/repo.git@main becomes <host>/repo at the resolved commit.
	host := strings.TrimSuffix(strings.TrimPrefix(ref, "http://"), "/repo.git@main")
	require.Regexp(t, regexp.MustCompile(`^dag://`+regexp.QuoteMeta(host)+`/repo@[0-9a-f]{40}:base$`), uri)

	// The current workspace's own absolute address is accepted; any other is reserved.
	out, err = base.With(workspaceSelectionDaggerQuery(fmt.Sprintf(`{ currentWorkspace {
  artifacts { filterUri(uri: %q) { items { uri } } }
  resolve(value: %q) { container { file(path: "/marker") { contents } } }
} }`, uri, uri), "-W", ref, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{
  "artifacts":{"filterUri":{"items":[{"uri":"dag://base"}]}},
  "resolve":{"container":{"file":{"contents":"configured:original"}}}
}}`, out)
	_, err = base.With(workspaceSelectionDaggerQuery(`{ currentWorkspace {
  artifacts { filterUri(uri: "dag://github.com/dagger/dagger@main:base") { items { uri } } }
} }`, "-W", ref, "-m", "core")).Stdout(ctx)
	requireErrOut(t, err, "absolute addresses are not supported yet: dag://github.com/dagger/dagger@main:base")
}

func (ArtifactsSuite) TestFilterURI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	for _, tc := range []struct {
		uri  string
		want []string
	}{
		{"base", []string{"dag://base"}},
		{"dag://base", []string{"dag://base"}},
		{"provider/base", []string{"dag://base"}},
		{"provider:base", []string{"dag://base"}},
		{"dag://provider:docs:source", []string{"dag://docs/source"}},
		{"docs", []string{"dag://docs"}},
		{"docs/**", []string{"dag://docs", "dag://docs/again", "dag://docs/source"}},
		{"**/source", []string{"dag://docs/source", "dag://other-docs/source"}},
		{"{base,consumer/base}", []string{"dag://base", "dag://consumer/base"}},
		{"dag+container://", []string{"dag://base", "dag://broken", "dag://consumer/base"}},
		{"dag+container+file://consumer/**", []string{"dag://consumer/base", "dag://consumer/input"}},
		{"dag+directory://base", nil},
		{"dag://base?missing=anything", nil},
		{"dag://?missing", nil},
		{"dag://", nil}, // all artifacts; checked by count below
	} {
		t.Run(tc.uri, func(ctx context.Context, t *testctx.T) {
			res, err := testutil.QueryWithClient[struct {
				Node struct {
					Artifacts struct {
						FilterURI struct{ Items []struct{ URI string } }
					}
				}
			}](c, t, `query($ws: ID!, $uri: String!) {
  node(id: $ws) { ... on Workspace { artifacts { filterUri(uri: $uri) { items { uri } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "uri": tc.uri}})
			require.NoError(t, err)
			var got []string
			for _, item := range res.Node.Artifacts.FilterURI.Items {
				got = append(got, item.URI)
			}
			if tc.uri == "dag://" {
				require.Len(t, got, 15)
				return
			}
			require.Equal(t, tc.want, got)
		})
	}
	for _, tc := range []struct{ uri, want string }{
		{"dag://github.com/dagger/dagger@main:base", "absolute addresses are not supported yet: dag://github.com/dagger/dagger@main:base"},
		{"https://github.com/dagger/dagger", "not a DAG address"},
		{"dag://github.com/dagger/dagger@main", "tree addresses are not supported"},
	} {
		_, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $uri: String!) {
  node(id: $ws) { ... on Workspace { artifacts { filterUri(uri: $uri) { items { uri } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "uri": tc.uri}})
		require.ErrorContains(t, err, tc.want)
	}
}

func (ArtifactsSuite) TestSelectorRoundTrip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	const sel = "{ selector: uri items { uri } }"
	for _, tc := range []struct {
		selection string
		uri       string
	}{
		{`artifacts ` + sel, "dag://"},
		{`artifacts(include: ["docs"]) ` + sel, "dag://docs/**"},
		{`artifacts(include: ["docs", "provider:base"]) ` + sel, "dag://{docs/**,provider/base/**}"},
		{`artifacts(include: ["**/source"]) ` + sel, "dag://**/source"},
		{`artifacts { filterTypes(types: ["Container", "Directory"]) ` + sel + ` }`, "dag+container+directory://"},
		{`artifacts { filterPath(path: ["docs", "source"]) ` + sel + ` }`, "dag://docs/source"},
		{`artifacts(include: ["docs"]) { filterPath(path: ["docs", "again"]) ` + sel + ` }`, "dag://docs/again"},
		{`artifacts(include: ["docs"]) { filterTypes(types: ["Directory"]) ` + sel + ` }`, "dag+directory://docs/**"},
		{`artifacts { filterUri(uri: "dag+container://consumer/**") ` + sel + ` }`, "dag+container://consumer/**"},
		{`artifacts { filterPath(path: ["base"]) { filterTypes(types: ["Directory"]) ` + sel + ` } }`, "dag+directory://base"},
		{`artifacts { filterTypes(types: []) ` + sel + ` }`, "dag://{}"},
		{`artifacts { filterPath(path: []) ` + sel + ` }`, "dag://{}"},
		{`artifacts { filterPath(path: ["docs", "Source"]) ` + sel + ` }`, "dag://{}"},
		{`artifacts { filterPath(path: ["docs", "*"]) ` + sel + ` }`, "dag://{}"},
		{`artifacts { filterUri(uri: "docs/**") { filterUri(uri: "consumer/**") ` + sel + ` } }`, "dag://{}"},
	} {
		t.Run(tc.uri, func(ctx context.Context, t *testctx.T) {
			got, err := testutil.QueryWithClient[json.RawMessage](c, t,
				`query($ws: ID!) { node(id: $ws) { ... on Workspace { `+tc.selection+` } } }`,
				&testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
			require.NoError(t, err)
			selector := regexp.MustCompile(`"selector":"([^"]*)"`).FindStringSubmatch(string(*got))
			require.NotNil(t, selector)
			require.Equal(t, tc.uri, selector[1])
			var items []string
			for _, match := range regexp.MustCompile(`"uri":"([^"]*)"`).FindAllStringSubmatch(string(*got), -1) {
				items = append(items, match[1])
			}

			again, err := testutil.QueryWithClient[struct {
				Node struct {
					Artifacts struct {
						FilterURI struct{ Items []struct{ URI string } }
					}
				}
			}](c, t, `query($ws: ID!, $uri: String!) {
  node(id: $ws) { ... on Workspace { artifacts { filterUri(uri: $uri) { items { uri } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "uri": selector[1]}})
			require.NoError(t, err)
			var roundTrip []string
			for _, item := range again.Node.Artifacts.FilterURI.Items {
				roundTrip = append(roundTrip, item.URI)
			}
			require.Equal(t, items, roundTrip)
		})
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

func (ArtifactsSuite) TestValueAfterDiscoveringSessionEnds(ctx context.Context, t *testctx.T) {
	// Discovery caches the module tree with the server that discovered it.
	// Another session that adopts the cached artifact must still evaluate it
	// after the discovering session released its results.
	discoverer := connect(ctx, t)
	wsID, err := artifactSource(discoverer).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	discovered, err := testutil.QueryWithClient[struct {
		Node struct {
			Artifacts struct {
				FilterPath struct{ One struct{ ID dagger.ID } }
			}
		}
	}](discoverer, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts { filterPath(path: ["base"]) { one { id } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}})
	require.NoError(t, err)
	artifactID := discovered.Node.Artifacts.FilterPath.One.ID

	consumer := connect(ctx, t)
	got, err := testutil.QueryWithClient[json.RawMessage](consumer, t, `query($id: ID!) {
  node(id: $id) { ... on Artifact { uri } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": artifactID}})
	require.NoError(t, err)
	require.Contains(t, string(*got), `"uri":"dag://base"`)
	require.NoError(t, discoverer.Close())

	got, err = testutil.QueryWithClient[json.RawMessage](consumer, t, `query($id: ID!) {
  node(id: $id) { ... on Artifact { value { ... on Container { file(path: "/marker") { contents } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": artifactID}})
	require.NoError(t, err)
	require.Contains(t, string(*got), "configured:original")
}

func (ArtifactsSuite) TestInclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := artifactSource(c).AsWorkspace()
	for _, tc := range []struct {
		patterns []string
		want     []string
	}{
		{[]string{"docs"}, []string{"dag://docs", "dag://docs/again", "dag://docs/source"}},
		{[]string{"provider/docs"}, []string{"dag://docs", "dag://docs/again", "dag://docs/source"}},
		{[]string{"provider:docs"}, []string{"dag://docs", "dag://docs/again", "dag://docs/source"}},
		{[]string{"**/source"}, []string{"dag://docs/source", "dag://other-docs/source"}},
		{[]string{"**:source"}, []string{"dag://docs/source", "dag://other-docs/source"}},
		{[]string{"base", "consumer/base"}, []string{"dag://base", "dag://consumer/base"}},
	} {
		items, err := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: tc.patterns}).Items(ctx)
		require.NoError(t, err)
		got := []string{}
		for i := range items {
			uri, err := items[i].URI(ctx)
			require.NoError(t, err)
			got = append(got, uri)
		}
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
	out, err := base.With(daggerCall("consumer", "selected", "one", "uri")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "dag://base", strings.TrimSpace(out))
	out, err = base.With(daggerCall("consumer", "resolve", "--address=dag://provider/base", "container", "file", "--path=/marker", "contents")).Stdout(ctx)
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
  resolve(value: "dag://base") { container { file(path: "/marker") { contents } } }
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
  resolve(value: "dag://consumer/base") { container { file(path: "/marker") { contents } } }
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
		{"containers", "dag://base\ndag://broken\ndag://consumer/base\n"},
		{"directories", "dag://docs/source\ndag://other-docs/source\n"},
		{"files", "dag://consumer/input\ndag://marker\n"},
		{"Artifact", "dag://consumer/single\n"},
		{"Artifacts", "dag://consumer/selected\n"},
		{"provider-docs", "dag://docs\ndag://docs/again\ndag://other-docs\ndag://other-docs/again\n"},
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
		{[]string{"list", "--type", "Container"}, "dag://base\ndag://broken\ndag://consumer/base\n"},
		{[]string{"list", "consumer", "--type", "Container", "--type", "File"}, "dag://consumer/base\ndag://consumer/input\n"},
		{[]string{"list", "docs"}, "dag://docs\ndag://docs/again\ndag://docs/source\n"},
		{[]string{"list", "dag://docs"}, "dag://docs\ndag://docs/again\ndag://docs/source\n"},
		{[]string{"list", "provider/docs"}, "dag://docs\ndag://docs/again\ndag://docs/source\n"},
		{[]string{"list", "provider:docs"}, "dag://docs\ndag://docs/again\ndag://docs/source\n"},
		{[]string{"list", "**/source"}, "dag://docs/source\ndag://other-docs/source\n"},
		{[]string{"list", "base", "consumer/base"}, "dag://base\ndag://consumer/base\n"},
		{[]string{"list", "dag+container://consumer"}, "dag://consumer/base\n"},
		{[]string{"list", "dag://consumer?missing=anything"}, ""},
		{[]string{"types"}, "Artifact\nArtifacts\nConsumer\nContainer\nDirectory\nFile\nProvider\nProviderDocs\n"},
		{[]string{"types", "docs"}, "Directory\nProviderDocs\n"},
		{[]string{"dimensions"}, ""},
		{[]string{"keys", "go-module"}, ""},
		{[]string{"list", "--dimension-key", "go-module=sdk/go"}, ""},
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
	require.Contains(t, out, "--dimension-key")
	require.Contains(t, out, "List types of matching artifacts")
	require.Contains(t, out, "List dimensions of matching artifacts")
	out, err = base.With(workspaceSelectionDaggerExec("__complete", "-W", "/work/selected", "artifacts", "--type", "Pro")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "ProviderDocs\n")
	_, err = base.With(workspaceSelectionDaggerExec("-W", "/work/selected", "artifacts", "list", "dag://github.com/dagger/dagger@main:base")).Sync(ctx)
	requireErrOut(t, err, "absolute addresses are not supported yet")
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
	require.Equal(t, "dag://types\n", out)
}

func (ArtifactsSuite) TestCLIAddressSelections(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory("/work/selected", artifactSource(c))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "dag+container://docs", "dag+directory://consumer"}, ""},
		{[]string{"list", "dag+directory://docs", "dag+container://consumer"}, "dag://consumer/base\ndag://docs/source\n"},
		{[]string{"list", "dag://docs?missing=value", "consumer/base"}, "dag://consumer/base\n"},
		{[]string{"list", "docs", "docs/source"}, "dag://docs\ndag://docs/again\ndag://docs/source\n"},
		{[]string{"list", "dag+directory://docs", "dag+container://consumer", "--type", "Directory"}, "dag://docs/source\n"},
		{[]string{"types", "dag+directory://docs", "dag+container://consumer"}, "Container\nDirectory\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected", "artifacts"}, tc.args...)
			out, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
}

func (ArtifactsSuite) TestCLIExcludedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := artifactSource(c).WithNewFile("dagger.toml", `[modules.provider]
source = "./provider"
settings.label = "configured"
[modules.invalid]
source = "./does-not-exist"
`)
	base := nativeWorkspaceBase(t, c).WithDirectory("/work/selected", source)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "provider/docs", "--type", "Directory"}, "dag://provider/docs/source\n"},
		{[]string{"types", "provider/docs"}, "Directory\nProviderDocs\n"},
		{[]string{"dimensions", "provider/docs"}, ""},
		{[]string{"keys", "missing", "provider/docs"}, ""},
	} {
		t.Run(strings.Join(tc.args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected", "artifacts"}, tc.args...)
			out, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
	out, err := base.With(workspaceSelectionDaggerExec("__complete", "-W", "/work/selected", "artifacts", "list", "provider/docs", "--type", "Dir")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Directory\n")
}

func (ArtifactsSuite) TestResolution(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	wsID, err := artifactSource(c).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	for _, address := range []string{"dag://base", "dag://provider/base", "dag://consumer/base", "dag://provider:base", "dag://consumer:base", "dag+container://base"} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { container { file(path: "/marker") { contents } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": address}})
		require.NoError(t, err, address)
		require.Contains(t, string(*got), "configured:original")
	}
	for _, address := range []string{"dag://docs/source", "dag://provider/docs/source", "dag://docs:source", "dag://provider:docs:source", "dag+directory://docs/source"} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { directory { file(path: "readme") { contents } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": address}})
		require.NoError(t, err, address)
		require.Contains(t, string(*got), `"contents":"docs"`)
	}
	for _, tc := range []struct{ address, want string }{
		{"dag://provider/", "no artifact matches"},
		{"dag://provider/missing", "no artifact matches"},
		{"dag://provider/docs/missing", "no artifact matches"},
		{"dag://provider/marker", "artifact is a File, not a Container"},
		{"dag://provider/needs-argument", "no artifact matches"},
		{"dag://provider:missing", "no artifact matches"},
		{"dag://broken", "artifact function was evaluated"},
		{"dag+container://docs/source", "dag://docs/source is a Directory, not container"},
		{"dag+directory://docs/source", "artifact is a Directory, not a Container"},
		{"dag://**/source", "matches 2 artifacts:\ndag://docs/source\ndag://other-docs/source"},
		{"dag://", "matches 15 artifacts:\n"},
		{"dag://github.com/dagger/dagger@main:base", "absolute addresses are not supported yet"},
	} {
		_, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { container { id } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": tc.address}})
		require.Error(t, err, tc.address)
		require.Contains(t, err.Error(), tc.want, tc.address)
		require.NotContains(t, err.Error(), "docker.io", tc.address)
	}
	// Without the scheme, a value keeps its external meaning.
	for _, image := range []string{alpineImage, "docker.io/library/" + alpineImage} {
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $image: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $image) { container { imageRef } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "image": image}})
		require.NoError(t, err)
		require.Contains(t, string(*got), "alpine")
	}
	for _, address := range []string{"base", "provider/base", "provider:base"} {
		_, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!, $address: String!) {
  node(id: $ws) { ... on Workspace { resolve(value: $address) { container { id } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": wsID, "address": address}})
		require.Error(t, err, address)
		require.NotContains(t, err.Error(), "artifact", address)
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
  node(id: $ws) { ... on Workspace { resolve(value: "dag://consumer/base") { id } } }
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
		`{ currentWorkspace { withNewFile(path: "marker.txt", contents: "edited") { resolve(value: "dag://consumer/base") { container { file(path: "/marker") { contents } } } } } }`,
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
settings.input = "dag://provider:marker"
`
	out, err := base.With(daggerQuery(`{ currentWorkspace { withNewFile(path: "dagger.toml", contents: %q) {
  resolve(value: "dag://consumer/base") { container { file(path: "/marker") { contents } } }
} } }`, config)).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "changed:original")
	out, err = base.With(daggerQuery(`{ currentWorkspace { withNewFile(path: "dagger.toml", contents: "[modules]\n") {
  artifacts { items { uri } }
} } }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"withNewFile":{"artifacts":{"items":[]}}}}`, out)
	_, err = base.With(daggerQuery(`{ address(value: "provider/marker") { file { contents } } }`)).Stdout(ctx)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "artifact")
	_, err = base.With(daggerQuery(`{ address(value: "dag://provider/marker") { file { contents } } }`)).Stdout(ctx)
	requireErrOut(t, err, "a DAG address needs a workspace; use Workspace.resolve")
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
settings.input = "dag://marker"
`).AsWorkspace().ID(ctx)
	require.NoError(t, err)
	for address, want := range map[string]string{"dag://alias/base": "aliased:original", "dag://consumer/base": "configured:original"} {
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
