package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
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
    types { name } uri
    containers: filterTypes(types: ["Container"]) { types { name } uri items { path dimensionKeys { dimension key } uri } }
    nested: filterPath(path: ["docs", "source"]) { uri items { uri } }
    sibling: filterPath(path: ["other-docs", "source"]) { items { uri } }
    optional: filterPath(path: ["optional-docs", "source"]) { items { uri } }
    list: filterPath(path: ["doc-list", "source"]) { items { uri } }
    cycle: filterPath(path: ["docs", "again", "source"]) { items { uri } }
    noType: filterTypes(types: []) { types { name } uri items { uri } }
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
  "types":[{"name":"Artifact"},{"name":"Artifacts"},{"name":"Consumer"},{"name":"Container"},{"name":"Directory"},{"name":"File"},{"name":"Provider"},{"name":"ProviderDocs"}],
  "uri":"dag://",
  "containers":{"types":[{"name":"Container"}],"uri":"dag+container://","items":[
    {"path":["base"],"dimensionKeys":[{"dimension":"type:Container","key":"base"}],"uri":"dag://base"},
    {"path":["broken"],"dimensionKeys":[{"dimension":"type:Container","key":"broken"}],"uri":"dag://broken"},
    {"path":["consumer","base"],"dimensionKeys":[{"dimension":"type:Container","key":"consumer/base"}],"uri":"dag://consumer/base"}
  ]},
  "nested":{"uri":"dag://docs/source","items":[{"uri":"dag://docs/source"}]},
  "sibling":{"items":[{"uri":"dag://other-docs/source"}]},
  "optional":{"items":[]},"list":{"items":[]},"cycle":{"items":[]},
  "noType":{"types":[],"uri":"dag://{}","items":[]},"unknown":{"items":[]},
  "noDimension":{"uri":"dag://?missing","items":[]},"noKey":{"uri":"dag://?missing=anything","items":[]},
  "exact":{"items":[]},"colon":{"items":[]},"slash":{"items":[]},"order":{"items":[]},"prefix":{"items":[{"uri":"dag://docs"}]},
  "first":{"filterPath":{"uri":"dag+container+directory://base","items":[{"uri":"dag://base"}]}},
  "second":{"filterTypes":{"uri":"dag+container+directory://base","items":[{"uri":"dag://base"}]}},
  "contradiction":{"filterTypes":{"uri":"dag://{}","items":[]}},"dimensions":["type:Artifact","type:Artifacts","type:Consumer","type:Container","type:Directory","type:File","type:Provider","type:ProviderDocs"],"dimensionKeys":[]
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
	source := artifactSource(c)
	provider, err := source.File("provider/main.dang").Contents(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("provider/main.dang", strings.Replace(provider, "pub label:", `pub verify: Void @check { null }
  pub generate(ws: Workspace!): Changeset! @generate { ws.changes(ws) }
  pub web: Service! @up { container.from("nginx:alpine").asService }
  pub assistant(base: LLM!): LLM! @agent { base }
  pub label:`, 1))
	ref := workspaceSelectionRemoteRef(ctx, t, c, source)
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

	out, err = base.With(workspaceSelectionDaggerExec("list", "-a", "-f=link", "dag://"+ref+":base")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "dag+container://base\n", out)
	out, err = base.With(workspaceSelectionDaggerExec("check", "-l", "dag://"+ref+":verify")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "--check=verify\n", out)
	_, err = base.With(workspaceSelectionDaggerExec("check", "dag://"+ref+":verify")).Sync(ctx)
	require.NoError(t, err)
	for _, typ := range []string{"Expertise", "expertise"} {
		out, err := base.With(workspaceSelectionDaggerExec("-W", ref, "list", "-a", "-f=link", "--type", typ)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "dag+expertise://assistant\n", out)
	}

	for _, tc := range []struct {
		args  []string
		paths []string
	}{
		{[]string{"list", "-a", "-f=link", "base"}, []string{"base"}},
		{[]string{"check", "-l", "-f=link", "verify"}, []string{"verify"}},
		{[]string{"generate", "-l", "-f=link", "generate"}, []string{"generate"}},
		{[]string{"up", "-l", "-f=link", "web"}, []string{"web"}},
		{[]string{"agent", "-l", "-f=link", "assistant"}, []string{"assistant"}},
		{[]string{"shell", "-l", "-f=link", "base"}, []string{"base"}},
		{[]string{"list", "containers", "-f=link"}, []string{"base", "broken", "consumer/base"}},
	} {
		for _, flag := range []string{"--absolute", "--abs"} {
			t.Run(strings.Join(tc.args, " ")+" "+flag, func(ctx context.Context, t *testctx.T) {
				args := append([]string{"-W", ref}, tc.args...)
				args = append(args, flag)
				out, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
				require.NoError(t, err)
				var want []string
				typ := map[string]string{"list": "container", "check": "check", "generate": "generator", "up": "service", "agent": "expertise", "shell": "container"}[tc.args[0]]
				for _, path := range tc.paths {
					address := strings.Replace(strings.TrimSuffix(uri, "base")+path, "dag://", "dag+"+typ+"://", 1)
					want = append(want, address)
				}
				require.Equal(t, strings.Join(want, "\n")+"\n", out)
			})
		}
	}

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
	requireErrOut(t, err, "address selects another workspace; filters cannot change workspace: dag://github.com/dagger/dagger@main:base")
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
		{"dag://github.com/dagger/dagger@main:base", "address selects another workspace; filters cannot change workspace: dag://github.com/dagger/dagger@main:base"},
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
		{`artifacts(include: ["**/source"]) ` + sel, "dag://**/source/**"},
		{`artifacts(include: ["d*"]) ` + sel, "dag://d*/**"},
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
  pub missingService: Service! { address("missing:serve").service }
}`)
	out, err := base.With(daggerCallAt("./legacy", "base", "file", "--path=/marker", "contents")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "legacy", strings.TrimSpace(out))

	out, err = base.With(daggerExecFail("call", "-m", "./legacy", "missing-service", "hostname")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, `no installed module matches "missing"`)
	require.NotContains(t, out, "write it as a DAG address")
}

func (ArtifactsSuite) TestAddressHints(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().AsWorkspace()
	_, err := ws.Resolve("missing:serve").Service().ID(ctx)
	require.ErrorContains(t, err, "write it as a DAG address: dag://missing/serve")

	_, err = c.Address("missing:serve").Service().ID(ctx)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "write it as a DAG address")
	require.NotContains(t, err.Error(), "no installed module matches")

	_, err = ws.Resolve("missing:workspace").Workspace().ID(ctx)
	require.ErrorContains(t, err, "must be a DAG address such as dag://<module>/<function>")
}

func (ArtifactsSuite) TestWorkspaceHelpDiscoveryFailures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).With(nonNestedDevEngine(c))
	for _, fixture := range []struct {
		name string
		ctr  *dagger.Container
		err  string
	}{
		{name: "engine unavailable", ctr: base.WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "invalid://"), err: `no driver for scheme "invalid"`},
		{name: "invalid workspace", ctr: base.WithNewFile("dagger.toml", "[modules\n"), err: "parse dagger.toml"},
	} {
		t.Run(fixture.name, func(ctx context.Context, t *testctx.T) {
			for _, args := range [][]string{
				{"workspace"}, {"ws", "--help"}, {"ws", "-h"}, {"help", "workspace"},
				{"__complete", "ws", ""}, {"__completeNoDesc", "ws", ""},
			} {
				t.Run(strings.Join(args, " "), func(ctx context.Context, t *testctx.T) {
					out, err := fixture.ctr.With(daggerNonNestedExec(args...)).Stdout(ctx)
					require.NoError(t, err)
					if strings.HasPrefix(args[0], "__complete") {
						require.Contains(t, out, "config")
						require.Contains(t, out, "root")
					} else {
						require.Contains(t, out, "Inspect or configure your workspace.")
					}
				})
			}
			t.Run("shortcut keeps discovery error", func(ctx context.Context, t *testctx.T) {
				out, err := fixture.ctr.With(daggerNonNestedExecFail("list", "containers")).CombinedOutput(ctx)
				require.NoError(t, err, out)
				require.NotContains(t, out, "unknown command")
				require.Contains(t, out, fixture.err)
			})
		})
	}
}

// A legacy caller's generated SDK must retain a check's authored return type.
func (ArtifactsSuite) TestLegacyCheckDependency(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithNewFile("dep/dagger.json", `{"name":"dep","sdk":"dang","engineVersion":"v0.21.5"}`).
		WithNewFile("dep/main.dang", `type Dep {
  pub verify: Container! @check { container.from("alpine:3.22").withExec(["echo", "legacy check"]) }
}`).
		With(withLegacyGoModule(t, c, "legacy", "legacy", "v0.21.5")).
		WithNewFile("legacy/dagger.json", `{"name":"legacy","sdk":"go","source":".","engineVersion":"v0.21.5",
"dependencies":[{"name":"dep","source":"../dep"}]}`).
		WithNewFile("legacy/main.go", `package main
import "context"
type Legacy struct{}
func (*Legacy) Output(ctx context.Context) (string, error) {
 return dag.Dep().Verify().Stdout(ctx)
}`)
	out, err := base.With(daggerCallAt("./legacy", "output")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "legacy check", strings.TrimSpace(out))
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

func (ArtifactsSuite) TestArtifactsCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory("/work/selected", artifactSource(c))
	t.Run("list descriptions", func(ctx context.Context, t *testctx.T) {
		source, err := base.File("/work/selected/provider/main.dang").Contents(ctx)
		require.NoError(t, err)
		source = strings.Replace(source, "  pub broken:", "  \"\"\"A container that must not run.\nMore details.\"\"\"\n  pub broken:", 1)
		out, err := base.WithNewFile("/work/selected/provider/main.dang", source).
			With(workspaceSelectionDaggerExec("-W", "/work/selected", "list", "-a", "broken", "broken", "-f=cli")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "--container=broken   # A container that must not run.\n", out)
	})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "-a", "--type", "Container"}, "dag+container://base\ndag+container://broken\ndag+container://consumer/base\n"},
		{[]string{"list", "-a", "--type", "container"}, "dag+container://base\ndag+container://broken\ndag+container://consumer/base\n"},
		{[]string{"list", "-a", "--type", "ProviderDocs"}, "dag+provider-docs://docs\ndag+provider-docs://docs/again\ndag+provider-docs://other-docs\ndag+provider-docs://other-docs/again\n"},
		{[]string{"list", "-a", "--type", "provider-docs"}, "dag+provider-docs://docs\ndag+provider-docs://docs/again\ndag+provider-docs://other-docs\ndag+provider-docs://other-docs/again\n"},
		{[]string{"list", "-a", "--type="}, ""},
		{[]string{"list", "-a", "--type=", "--type=container"}, "dag+container://base\ndag+container://broken\ndag+container://consumer/base\n"},
		{[]string{"list", "-a", "consumer", "--type", "Container", "--type", "File"}, "dag+container://consumer/base\ndag+file://consumer/input\n"},
		{[]string{"list", "-a", "consumer", "--type", "container", "--type", "File"}, "dag+container://consumer/base\ndag+file://consumer/input\n"},
		{[]string{"list", "-a", "docs"}, "dag+directory://docs/source\ndag+provider-docs://docs\ndag+provider-docs://docs/again\n"},
		{[]string{"list", "-a", "dag://docs"}, "dag+directory://docs/source\ndag+provider-docs://docs\ndag+provider-docs://docs/again\n"},
		{[]string{"list", "-a", "provider/docs"}, "dag+directory://docs/source\ndag+provider-docs://docs\ndag+provider-docs://docs/again\n"},
		{[]string{"list", "-a", "provider:docs"}, "dag+directory://docs/source\ndag+provider-docs://docs\ndag+provider-docs://docs/again\n"},
		{[]string{"list", "-a", "**/source"}, "dag+directory://docs/source\ndag+directory://other-docs/source\n"},
		{[]string{"list", "-a", "base", "consumer/base"}, "dag+container://base\ndag+container://consumer/base\n"},
		{[]string{"list", "-a", "dag+container://consumer"}, "dag+container://consumer/base\n"},
		{[]string{"list", "-a", "dag://consumer?missing=anything"}, ""},
		{[]string{"list", "-a", "dag://?go-module=sdk/go"}, ""},
	} {
		t.Run(strings.Join(tc.args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected"}, tc.args...)
			args = append(args, "-f=link")
			out, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
	for _, args := range [][]string{
		{"list", "-a", "--does-not-exist=x"},
		{"list", "-a", "--does-not-exist=x", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected"}, args...)
			_, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
			requireErrOut(t, err, "unknown flag: --does-not-exist")
		})
	}

	reserved := base.
		WithNewFile("/work/selected/dagger.toml", `[modules.provider]
source = "./provider"
entrypoint = true
`).
		WithNewFile("/work/selected/provider/main.dang", `type Provider {
  pub types: Directory! { directory }
}`)
	out, err := reserved.With(workspaceSelectionDaggerExec("-W", "/work/selected", "list", "-a", "types", "-f=link")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "dag+directory://types\n", out)
}

func (ArtifactsSuite) TestCLIAddressSelections(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).WithDirectory("/work/selected", artifactSource(c))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "-a", "dag+container://docs", "dag+directory://consumer"}, ""},
		{[]string{"list", "-a", "dag+directory://docs", "dag+container://consumer"}, "dag+container://consumer/base\ndag+directory://docs/source\n"},
		{[]string{"list", "-a", "dag://docs?missing=value", "consumer/base"}, "dag+container://consumer/base\n"},
		{[]string{"list", "-a", "docs", "docs/source"}, "dag+directory://docs/source\ndag+provider-docs://docs\ndag+provider-docs://docs/again\n"},
		{[]string{"list", "-a", "dag+directory://docs", "dag+container://consumer", "--type", "Directory"}, "dag+directory://docs/source\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected"}, tc.args...)
			args = append(args, "-f=link")
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
		{[]string{"list", "-a", "provider/docs", "--type", "Directory"}, "dag+directory://provider/docs/source\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"-W", "/work/selected"}, tc.args...)
			args = append(args, "-f=link")
			out, err := base.With(workspaceSelectionDaggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
	out, err := base.With(workspaceSelectionDaggerExec("__complete", "-W", "/work/selected", "list", "-a", "provider/docs", "--type", "Dir")).Stdout(ctx)
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
		{"dag://github.com/dagger/dagger@main:base", "address selects another workspace; filters cannot change workspace"},
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

func (ArtifactsSuite) TestCheckReturnTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name, returnType, body string
		invalid, fails         bool
	}{
		{name: "void", returnType: "Void", body: "null"},
		{name: "failed void", returnType: "Void", body: `raise "check failed"`, fails: true},
		{name: "string", returnType: "String!", body: `"ok"`, invalid: true},
		{name: "nullable string", returnType: "String", body: `"ok"`, invalid: true},
		{name: "null string", returnType: "String", body: "null", invalid: true},
		{name: "integer", returnType: "Int!", body: "42", invalid: true},
		{name: "boolean", returnType: "Boolean!", body: "true", invalid: true},
		{name: "object", returnType: "Container!", body: "container", invalid: true},
		{name: "string list", returnType: "[String!]!", body: `["ok"]`, invalid: true},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			src := c.Directory().WithNewFile("dagger.toml", `[modules.example]
source = "./example"
entrypoint = true
`).WithNewFile("example/dagger-module.toml", `name = "example"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
`).WithNewFile("example/main.dang", fmt.Sprintf("type Example { pub verify: %s @check { %s } }", tc.returnType, tc.body))
			checkArtifact := src.AsWorkspace().Artifacts().FilterDirectives([]string{"check"}).One()
			loadError, err := checkArtifact.LoadError(ctx)
			require.NoError(t, err)
			if tc.invalid {
				require.Contains(t, loadError, "must return Void")
				return
			}
			require.Empty(t, loadError)
			check := artifactValue[*dagger.Check](ctx, t, c, checkArtifact)
			pass, err := check.Pass(ctx)
			require.NoError(t, err)
			require.Equal(t, !tc.fails, pass)
			if tc.fails {
				checkErr, err := check.Error(ctx)
				require.NoError(t, err)
				require.NotNil(t, checkErr)
				message, err := checkErr.Message(ctx)
				require.NoError(t, err)
				require.Contains(t, message, "check failed")
			}
		})
	}
}

func (ArtifactsSuite) TestCheckProjection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	src := c.Directory().WithNewFile("dagger.toml", `[modules.example]
source = "./example"
entrypoint = true
`).WithNewFile("example/dagger-module.toml", `name = "example"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
`).WithNewFile("example/main.dang", `type Example {
 pub passing: Void @check { null }
 pub failing: Void @check { raise "check failed" }
 """Generate clean files."""
 pub clean(ws: Workspace!): Changeset! @generate { ws.changes(ws) }
 """Regenerate dirty files:
More details."""
 pub dirty(ws: Workspace!): Changeset! @generate { ws.withNewFile("generated", "new").changes(ws) }
 pub broken: Container! { raise "metadata evaluated a leaf" }
 pub edit(ws: Workspace!): Changeset! { ws.withNewFile("edited", "new").changes(ws) }
 pub brokenEdit: Changeset! { raise "unmarked Changeset was evaluated" }
}`)
	wsID, err := src.AsWorkspace().ID(ctx)
	require.NoError(t, err)
	opts := &testutil.QueryOptions{Variables: map[string]any{"ws": wsID}}
	// Staleness belongs to Generator. An ordinary Changeset has no stale check.
	unmarked, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
 node(id: $ws) { ... on Workspace { artifacts { filterUri(uri: "dag://{edit,broken-edit}/stale") {
 items { uri directives }
 } } } }
}`, opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"artifacts":{"filterUri":{"items":[]}}}}`, string(*unmarked))

	metadata, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
 node(id: $ws) { ... on Workspace { artifacts { filterDirectives(directives: ["check"]) { items { uri directives } } } } }
}`, opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"artifacts":{"filterDirectives":{"items":[
 {"uri":"dag://clean/stale","directives":["check"]},
 {"uri":"dag://dirty/stale","directives":["check"]},
 {"uri":"dag://failing","directives":["check"]},
 {"uri":"dag://passing","directives":["check"]}
 ]}}}}`, string(*metadata))
	selected := dagger.Ref[*dagger.Workspace](c, wsID).Artifacts().FilterCheckCommand()
	selectionID, err := selected.ID(ctx)
	require.NoError(t, err)
	opts = &testutil.QueryOptions{Variables: map[string]any{"selection": selectionID}}
	results, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($selection: ID!) {
 node(id: $selection) { ... on Artifacts {
 items { uri value { ... on Check { pass error { message } } } }
 } }
}`, opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"items":[
 {"uri":"dag://clean/stale","value":{"pass":true,"error":null}},
 {"uri":"dag://dirty/stale","value":{"pass":false,"error":{"message":"generated files are not up to date"}}},
 {"uri":"dag://failing","value":{"pass":false,"error":{"message":"check failed"}}},
 {"uri":"dag://passing","value":{"pass":true,"error":null}}
 ]}}`, string(*results))
	values, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($selection: ID!) {
 node(id: $selection) { ... on Artifacts {
 values { artifact { uri } error { message } value { ... on Check { pass } } }
 } }
}`, opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"values":[
 {"artifact":{"uri":"dag://clean/stale"},"value":{"pass":true},"error":null},
 {"artifact":{"uri":"dag://dirty/stale"},"value":null,"error":{"message":"generated files are not up to date"}},
 {"artifact":{"uri":"dag://failing"},"value":null,"error":{"message":"check failed"}},
 {"artifact":{"uri":"dag://passing"},"value":{"pass":true},"error":null}
 ]}}`, string(*values))

	base := nativeWorkspaceBase(t, c).WithDirectory(".", src).With(nonNestedDevEngine(c))
	for _, command := range []string{"generate", "check"} {
		t.Run(command+" list", func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerNonNestedExec(command, "-l")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "=clean")
			require.Contains(t, out, "=dirty")
			if command == "check" {
				require.Contains(t, out, `# staleness check: generate clean files`)
				require.Contains(t, out, `# staleness check: regenerate dirty files`)
			}
			require.NotContains(t, out, "edit")
		})
	}

	// Generator objects are lazy. Explicit generation executes the stored function.
	generators, err := dagger.Ref[*dagger.Workspace](c, wsID).Artifacts().FilterGenerateCommand().AsGenerators(ctx)
	require.NoError(t, err)
	require.Len(t, generators, 2)
	clean, err := generators[0].Stale().Pass(ctx)
	require.NoError(t, err)
	require.True(t, clean)
	_, err = base.With(daggerNonNestedExec("generate", "--generator=clean")).Sync(ctx)
	require.NoError(t, err)
	for _, flag := range []string{"", "--generated=true", "--generated=false"} {
		t.Run("check "+flag, func(ctx context.Context, t *testctx.T) {
			args := []string{"check", "--skip=failing", "--skip=dirty"}
			if flag != "" {
				args = append(args, flag)
			}
			out, err := base.With(daggerNonNestedExec(args...)).CombinedOutput(ctx)
			require.NoError(t, err, out)
			require.NotContains(t, out, "edit")
		})
	}
}

func (ArtifactsSuite) TestCommandFilters(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	config := `check-generated = false
[modules.probe]
source = "./probe"
entrypoint = true
check.skip = ["skipped"]
generate.skip = ["skipped-generate"]
up.skip = ["skipped-service", "skipped-unmarked-service"]
`
	source := c.Directory().
		WithNewFile("dagger.toml", config).
		WithNewFile("probe/dagger-module.toml", "name = \"probe\"\nengineVersion = \"latest\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("probe/main.dang", `type Probe {
 pub verify: Void @check { raise "must not run" }
 pub skipped: Void @check { raise "must not run" }
 pub gen: Changeset! @generate { raise "must not run" }
 pub skippedGenerate: Changeset! @generate { raise "must not run" }
 pub edit: Changeset! { raise "must not run" }
 pub serve: Service! @up { raise "must not run" }
 pub skippedService: Service! @up { raise "must not run" }
 pub unmarkedService: Service! { raise "must not run" }
 pub skippedUnmarkedService: Service! { raise "must not run" }
 pub assistant(base: LLM!): LLM! @agent { raise "must not run" }
}`)
	all := source.AsWorkspace().Artifacts()
	id, err := all.ID(ctx)
	require.NoError(t, err)
	got, err := testutil.QueryWithClient[struct {
		Node map[string]struct{ Items []struct{ URI string } }
	}](c, t, `query($id: ID!) { node(id: $id) { ... on Artifacts {
 rawChecks: filterDirectives(directives: ["check"]) { items { uri } }
 checks: filterCheckCommand { items { uri } }
 withGenerated: filterCheckCommand(generated: true) { items { uri } }
 withoutGenerated: filterCheckCommand(generated: false) { items { uri } }
 rawGenerators: filterDirectives(directives: ["generate"]) { items { uri } }
 generators: filterGenerateCommand { items { uri } }
 rawServices: filterDirectives(directives: ["up"]) { items { uri } }
 services: filterUpCommand { items { uri } }
 agents: filterAgentCommand { items { uri } }
} } }`, &testutil.QueryOptions{Variables: map[string]any{"id": id}})
	require.NoError(t, err)
	for name, want := range map[string][]string{
		"rawChecks":        {"dag://gen/stale", "dag://skipped", "dag://skipped-generate/stale", "dag://verify"},
		"checks":           {"dag://verify"},
		"withGenerated":    {"dag://gen/stale", "dag://verify"},
		"withoutGenerated": {"dag://verify"},
		"rawGenerators":    {"dag://gen", "dag://skipped-generate"},
		"generators":       {"dag://gen"},
		"rawServices":      {"dag://serve", "dag://skipped-service"},
		"services":         {"dag://serve", "dag://unmarked-service"},
		"agents":           {"dag://assistant"},
	} {
		var names []string
		for _, item := range got.Node[name].Items {
			names = append(names, item.URI)
		}
		require.Equal(t, want, names, name)
	}
	// A union retains each workspace's generated-check setting.
	enabled := source.WithNewFile("dagger.toml", strings.Replace(config, "check-generated = false", "check-generated = true", 1)).
		AsWorkspace().Artifacts().FilterURI("gen/stale")
	mixed := all.FilterURI("gen/stale").WithArtifacts(enabled).FilterCheckCommand()
	items, err := mixed.WithArtifacts(enabled).Items(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
}

func (ArtifactsSuite) TestParentFiltersAndUnion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := artifactSource(c)
	provider, err := source.File("provider/main.dang").Contents(ctx)
	require.NoError(t, err)
	source = source.WithNewFile("provider/main.dang", strings.Replace(provider, "pub label:", `"""Generate assets."""
  pub gen: Changeset! @generate { raise "metadata evaluated generator" }
  pub edit: Changeset! { raise "metadata evaluated edit" }
  pub label:`, 1))
	all := source.AsWorkspace().Artifacts()
	description, err := all.FilterURI("dag://gen/stale").One().Description(ctx)
	require.NoError(t, err)
	require.Equal(t, `staleness check: generate assets`, description)
	uris := func(ctx context.Context, t *testctx.T, selection *dagger.Artifacts) []string {
		id, err := selection.ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[struct {
			Node struct{ Items []struct{ URI string } }
		}](c, t, `query($id: ID!) { node(id: $id) { ... on Artifacts { items { uri } } } }`,
			&testutil.QueryOptions{Variables: map[string]any{"id": id}})
		require.NoError(t, err)
		result := []string{}
		for _, item := range got.Node.Items {
			result = append(result, item.URI)
		}
		return result
	}
	allURIs := uris(ctx, t, all)
	for _, tc := range []struct {
		name   string
		filter func([]string, bool) *dagger.Artifacts
		match  string
		want   []string
	}{
		{"types", func(names []string, exclude bool) *dagger.Artifacts {
			return all.FilterTypes(names, dagger.ArtifactsFilterTypesOpts{Exclude: exclude})
		}, "Changeset", []string{"dag://edit"}},
		{"directives", func(names []string, exclude bool) *dagger.Artifacts {
			return all.FilterDirectives(names, dagger.ArtifactsFilterDirectivesOpts{Exclude: exclude})
		}, "check", []string{"dag://gen/stale"}},
		{"parent types", func(names []string, exclude bool) *dagger.Artifacts {
			return all.FilterParentTypes(names, dagger.ArtifactsFilterParentTypesOpts{Exclude: exclude})
		}, "Generator", []string{"dag://gen/stale"}},
		{"parent directives", func(names []string, exclude bool) *dagger.Artifacts {
			return all.FilterParentDirectives(names, dagger.ArtifactsFilterParentDirectivesOpts{Exclude: exclude})
		}, "generate", []string{"dag://gen/stale"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			included := tc.filter([]string{tc.match}, false)
			excluded := tc.filter([]string{tc.match}, true)
			require.Equal(t, tc.want, uris(ctx, t, included))
			for _, uri := range uris(ctx, t, excluded) {
				require.NotContains(t, tc.want, uri)
			}
			require.Equal(t, allURIs, uris(ctx, t, included.WithArtifacts(excluded)))
			for _, names := range [][]string{{}, {"Unknown"}} {
				require.Empty(t, uris(ctx, t, tc.filter(names, false)))
				require.Equal(t, allURIs, uris(ctx, t, tc.filter(names, true)))
			}
		})
	}
	t.Run("union preserves addresses and clears branch filters", func(ctx context.Context, t *testctx.T) {
		containers := all.FilterTypes([]string{"Container"})
		directories := all.FilterTypes([]string{"Directory"})
		joined := containers.WithArtifacts(directories).WithArtifacts(containers)
		want := append(uris(ctx, t, containers), uris(ctx, t, directories)...)
		slices.Sort(want)
		require.Equal(t, want, uris(ctx, t, joined))
		uri, err := joined.URI(ctx)
		require.NoError(t, err)
		require.Equal(t, want, uris(ctx, t, all.FilterURI(uri)))
		require.Equal(t, uris(ctx, t, directories), uris(ctx, t, joined.FilterTypes([]string{"Directory"})))
		require.Equal(t, allURIs, uris(ctx, t, all))
	})
	t.Run("command filters use each workspace settings", func(ctx context.Context, t *testctx.T) {
		config, err := source.File("dagger.toml").Contents(ctx)
		require.NoError(t, err)
		skipped := source.WithNewFile("dagger.toml", config+`
[modules.provider.check]
skip = ["gen"]
`).AsWorkspace().Artifacts().FilterURI("dag://gen/stale")
		enabled := all.FilterURI("dag://gen/stale")
		for _, joined := range []*dagger.Artifacts{skipped.WithArtifacts(enabled), enabled.WithArtifacts(skipped)} {
			// Raw directive inclusion and exclusion do not apply workspace settings.
			require.Len(t, uris(ctx, t, joined.FilterDirectives([]string{"check"})), 2)
			require.Empty(t, uris(ctx, t, joined.FilterDirectives([]string{"check"}, dagger.ArtifactsFilterDirectivesOpts{Exclude: true})))
			included := joined.FilterCheckCommand()
			require.Equal(t, []string{"dag://gen/stale"}, uris(ctx, t, included))
			require.Len(t, uris(ctx, t, included.WithArtifacts(enabled)), 1)
			require.Len(t, uris(ctx, t, included.WithArtifacts(skipped)), 2)
		}
	})
	t.Run("union preserves workspace identity", func(ctx context.Context, t *testctx.T) {
		first := all.FilterPath([]string{"base"})
		second := source.WithNewFile("marker.txt", "other").AsWorkspace().Artifacts().FilterPath([]string{"base"})
		joined := first.WithArtifacts(second).WithArtifacts(first)
		items, err := joined.Items(ctx)
		require.NoError(t, err)
		require.Len(t, items, 2)
		for i, want := range []string{"configured:original", "configured:other"} {
			out, err := artifactValue[*dagger.Container](ctx, t, c, &items[i]).File("/marker").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, want, out)
		}
		_, err = joined.URI(ctx)
		require.ErrorContains(t, err, "multiple workspaces")
	})
}

func artifactValue[T dagger.Loadable[T]](ctx context.Context, t *testctx.T, c *dagger.Client, artifact *dagger.Artifact) T {
	t.Helper()
	id, err := artifact.Value().ID(ctx)
	require.NoError(t, err)
	return dagger.Ref[T](c, id)
}

func composeArtifactAgents(ctx context.Context, c *dagger.Client, ws *dagger.Workspace, artifacts *dagger.Artifacts, base ...*dagger.LLM) (*dagger.LLM, error) {
	if artifacts == nil {
		artifacts = ws.Artifacts()
	}
	failures, err := artifacts.FilterURI("*/load").Items(ctx)
	if err != nil {
		return nil, err
	}
	for _, artifact := range failures {
		message, err := artifact.LoadError(ctx)
		if err != nil {
			return nil, err
		}
		if message != "" {
			return nil, fmt.Errorf("%s", message)
		}
	}
	agents, err := artifacts.FilterAgentCommand().AsExpertise(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]*dagger.Expertise, len(agents))
	for i := range agents {
		refs[i] = &agents[i]
	}
	llm := c.LLM().WithWorkspace(ws)
	if len(base) > 0 {
		llm = base[0]
	}
	composed := llm.Compose(refs)
	id, err := composed.ID(ctx)
	if err != nil {
		return nil, err
	}
	return dagger.Ref[*dagger.LLM](c, id), nil
}

func (ArtifactsSuite) TestCheckCachePolicy(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"./probe\"\nentrypoint = true\n").
		WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("probe/main.go", `package main
import ("crypto/rand"; "fmt")
type Probe struct{}
// +check
func (*Probe) Once() error { return fmt.Errorf("once %s", rand.Text()) }
// +check
// +cache="never"
func (*Probe) Every() error { return fmt.Errorf("every %s", rand.Text()) }
`)
	out, err := base.With(daggerQuery(`{
 a: once { error { message } }
 b: once { error { message } }
 currentWorkspace { artifacts(include: ["once"]) { one { value { ... on Check { error { message } } } } } }
}`)).Stdout(ctx)
	require.NoError(t, err)
	var direct struct {
		A, B             struct{ Error struct{ Message string } }
		CurrentWorkspace struct {
			Artifacts struct {
				One struct {
					Value struct{ Error struct{ Message string } }
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &direct))
	require.NotEmpty(t, direct.A.Error.Message)
	require.Equal(t, direct.A.Error.Message, direct.B.Error.Message)
	require.Equal(t, direct.A.Error.Message, direct.CurrentWorkspace.Artifacts.One.Value.Error.Message)

	out, err = base.With(daggerQuery(`{
 currentWorkspace { artifacts(include: ["every"]) {
  a: values { error { message } }
  b: values { error { message } }
 } }
}`)).Stdout(ctx)
	require.NoError(t, err)
	var repeated struct {
		CurrentWorkspace struct {
			Artifacts struct {
				A, B []struct{ Error struct{ Message string } }
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &repeated))
	require.Len(t, repeated.CurrentWorkspace.Artifacts.A, 1)
	require.Len(t, repeated.CurrentWorkspace.Artifacts.B, 1)
	require.NotEqual(t, repeated.CurrentWorkspace.Artifacts.A[0].Error.Message, repeated.CurrentWorkspace.Artifacts.B[0].Error.Message)
}

// Materialization errors belong to each result, before consumers read its value.
func (ArtifactsSuite) TestLazyValueFailures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"./probe\"\nentrypoint = true\n").
		WithNewFile("probe/dagger-module.toml", "name = \"probe\"\nengineVersion = \"v1.0.0\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("probe/main.dang", `type Probe {
 pub brokenDirectory: Directory! { container.from("alpine:3.22").withExec(["sh", "-c", "exit 42"]).rootfs }
 pub brokenFile: File! { container.from("alpine:3.22").withExec(["sh", "-c", "exit 43"]).file("/missing") }
 pub good: Directory! { directory.withNewFile("ok", "ok") }
}`)
	out, err := base.With(daggerQuery(`{
 currentWorkspace { artifacts { filterTypes(types: ["Directory", "File"]) {
  values { artifact { uri } error { message } value { ... on Directory { entries } ... on File { contents } } }
 } } }
}`)).Stdout(ctx)
	require.NoError(t, err)
	var response struct {
		CurrentWorkspace struct {
			Artifacts struct {
				FilterTypes struct {
					Values []struct {
						Artifact struct{ URI string }
						Error    *struct{ Message string }
						Value    json.RawMessage
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &response))
	results := response.CurrentWorkspace.Artifacts.FilterTypes.Values
	require.Len(t, results, 3)
	for i, code := range []string{"42", "43"} {
		require.NotNil(t, results[i].Error)
		require.Contains(t, results[i].Error.Message, code)
		require.JSONEq(t, "null", string(results[i].Value))
	}
	require.Nil(t, results[2].Error)
	require.JSONEq(t, `{ "entries": ["ok"] }`, string(results[2].Value))
}

func (ArtifactsSuite) TestCheckScaleOut(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)
	target := devEngineContainerAsService(devEngineContainer(c))
	source := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding("scaleout-engine", target).
			WithEnvVariable("_DAGGER_TESTS_CLOUD_RUNNER_HOST", "tcp://scaleout-engine:1234")
	}))
	base := engineClientContainer(ctx, t, c, source).
		WithWorkdir("/work").
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "init"}).
		WithNewFile("dagger.toml", `[modules.good]
source = "./good"
settings.expected = "bound"
[modules.bad]
source = "./bad"
entrypoint = true
`).
		WithNewFile("marker", "bound").
		WithNewFile("good/dagger-module.toml", "name = \"good\"\nengineVersion = \"v1.0.0\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("good/main.dang", `type Good {
 pub expected: String! = "default"
 pub verify(ws: Workspace!): Void @check {
  if (ws.file("marker").contents != expected) { raise "workspace binding lost" }
  null
 }
 pub verifyOverlay(ws: Workspace!, expectedMarker: String! = "default"): Void @check {
  if (expectedMarker != "overlay") { raise "explicit argument lost" }
  if (ws.file("marker").contents != expectedMarker) { raise "workspace overlay lost" }
  null
 }
 pub verifyObjects(source: Directory = null, files: [File!]! = [], absent: File = null, text: String! = ""): Void @check {
  if (source == null) { raise "directory argument lost" }
  if (source.file("marker").contents != "objects") { raise "wrong directory" }
  if (files.length != 1) { raise "file list lost" }
  let file = files[0]
  if (file == null) { raise "file argument lost" }
  if (file.contents != "objects") { raise "wrong file" }
  if (absent != null) { raise "null argument changed" }
  if (source.file("id-text").contents != text) { raise "string argument changed" }
  null
 }
 pub fail: Void @check { raise "remote assertion" }
}`).
		WithNewFile("bad/dagger-module.toml", "name = \"bad\"\nengineVersion = \"v1.0.0\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("bad/main.dang", "invalid source")
	out, err := base.With(daggerNonNestedExec("check", "--scale-out", "dag://good/verify")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "dag://good/verify")
	out, err = base.With(daggerNonNestedExecFail("check", "--scale-out", "dag://good/fail")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "remote assertion")
	// -m adds a module outside the workspace configuration.
	out, err = base.WithNewFile("dagger.toml", "[modules]\n").
		WithNewFile("marker", "default").
		With(daggerNonNestedExec("-m", "./good", "check", "--scale-out", "dag://verify")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "dag://verify")
	// The remote query must preserve explicit arguments and the selected overlay.
	out, err = base.WithEnvVariable("_EXPERIMENTAL_DAGGER_CHECKS_SCALE_OUT", "1").
		WithNewFile("scaleout.graphql", `{
 currentWorkspace {
  host: file(path: "marker") { contents }
  withNewFile(path: "marker", contents: "overlay") {
   artifacts(include: ["good/verify-overlay"]) {
    values(arguments: "{\"expectedMarker\":\"overlay\"}") {
     error { message }
     value { ... on Check { pass } }
    }
   }
  }
 }
}`).
		With(daggerNonNestedExec("query", "--no-load-module", "--doc", "scaleout.graphql")).Stdout(ctx)
	require.NoError(t, err, out)
	require.JSONEq(t, `{"currentWorkspace":{"host":{"contents":"bound"},"withNewFile":{"artifacts":{"values":[{"error":null,"value":{"pass":true}}]}}}}`, out)
	// Keep source object handles alive while the remote check consumes them.
	out, err = base.WithEnvVariable("_EXPERIMENTAL_DAGGER_CHECKS_SCALE_OUT", "1").
		WithExec([]string{"apk", "add", "python3"}).
		WithNewFile("object-arguments.py", `import base64, json, os, urllib.request

def query(document, variables=None):
    request = urllib.request.Request(
        "http://127.0.0.1:" + os.environ["DAGGER_SESSION_PORT"] + "/query",
        data=json.dumps({"query": document, "variables": variables or {}}).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": "Basic " + base64.b64encode((os.environ["DAGGER_SESSION_TOKEN"] + ":").encode()).decode(),
        },
    )
    with urllib.request.urlopen(request) as response:
        result = json.load(response)
    assert not result.get("errors"), result
    return result["data"]

file_id = query('{ directory { withNewFile(path: "marker", contents: "objects") { file(path: "marker") { id } } } }')["directory"]["withNewFile"]["file"]["id"]
directory_id = query('query($text: String!) { directory { withNewFile(path: "marker", contents: "objects") { withNewFile(path: "id-text", contents: $text) { id } } } }', {"text": file_id})["directory"]["withNewFile"]["withNewFile"]["id"]
print(json.dumps(query('query($args: JSON!) { currentWorkspace { artifacts(include: ["good/verify-objects"]) { values(arguments: $args) { error { message } value { ... on Check { pass } } } } } }', {
    "args": json.dumps({"source": directory_id, "files": [file_id], "absent": None, "text": file_id}),
})))
`).
		With(daggerNonNestedExec("--no-load-module", "run", "python3", "object-arguments.py")).Stdout(ctx)
	require.NoError(t, err, out)
	require.JSONEq(t, `{"currentWorkspace":{"artifacts":{"values":[{"error":null,"value":{"pass":true}}]}}}`, out)
	require.NoError(t, c.Close())
	sink.read(func(db *dagui.DB) {
		connections := 0
		for _, span := range db.Spans.Map {
			if span.Name == "starting scale-out session" && !span.IsRunning() {
				connections++
			}
		}
		require.Equal(t, 5, connections)
	})
}

// Artifact discovery validates the environment even without automatic module loading.
func (ArtifactsSuite) TestUnknownEnvironment(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithDirectory(".", artifactSource(c))
	for _, command := range [][]string{
		{"list", "-a"}, {"check", "-l"}, {"generate", "-l"},
		{"up", "-l"}, {"shell", "-l"}, {"agent", "-l"},
	} {
		t.Run(strings.Join(command, " "), func(ctx context.Context, t *testctx.T) {
			args := append([]string{"--env", "missing"}, command...)
			out, err := base.With(daggerExecFail(args...)).CombinedOutput(ctx)
			require.NoError(t, err, out)
			require.Contains(t, out, `env "missing" is not defined`)
		})
	}
}

func (ArtifactsSuite) TestGeneratorCachePolicy(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"./probe\"\nentrypoint = true\n").
		WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("probe/main.go", `package main
import ("crypto/rand"; "fmt"; "dagger/probe/internal/dagger")
type Probe struct{}
// +generate
// +cache="never"
func (*Probe) Fresh() *dagger.Changeset {
 return dag.Directory().WithNewFile("nonce", rand.Text()).Changes(dag.Directory())
}
// +generate
// +cache="never"
func (*Probe) Broken() (*dagger.Changeset, error) { return nil, fmt.Errorf("generation %s", rand.Text()) }
`)
	out, err := base.With(daggerQuery(`{
 currentWorkspace { artifacts(include: ["fresh"]) { filterTypes(types: ["Generator"]) {
  a: values { value { ... on Generator { first: changeset { after { file(path: "nonce") { contents } } } second: changeset { after { file(path: "nonce") { contents } } } } } }
  b: values { value { ... on Generator { first: changeset { after { file(path: "nonce") { contents } } } second: changeset { after { file(path: "nonce") { contents } } } } } }
 } } }
}`)).Stdout(ctx)
	require.NoError(t, err)
	type result struct {
		Value struct {
			First, Second struct {
				After struct{ File struct{ Contents string } }
			}
		}
	}
	var got struct {
		CurrentWorkspace struct {
			Artifacts struct{ FilterTypes struct{ A, B []result } }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	a, b := got.CurrentWorkspace.Artifacts.FilterTypes.A, got.CurrentWorkspace.Artifacts.FilterTypes.B
	require.Len(t, a, 1)
	require.Len(t, b, 1)
	for _, run := range []result{a[0], b[0]} {
		require.NotEmpty(t, run.Value.First.After.File.Contents)
		require.Equal(t, run.Value.First.After.File.Contents, run.Value.Second.After.File.Contents)
	}
	require.NotEqual(t, a[0].Value.First.After.File.Contents, b[0].Value.First.After.File.Contents)
	out, err = base.With(daggerQuery(`{
 currentWorkspace { artifacts(include: ["broken/stale"]) { one { value { ... on Check {
  a: error { message }
  b: error { message }
 } } } } }
}`)).Stdout(ctx)
	require.NoError(t, err)
	var failed struct {
		CurrentWorkspace struct {
			Artifacts struct {
				One struct {
					Value struct{ A, B struct{ Message string } }
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &failed))
	messages := failed.CurrentWorkspace.Artifacts.One.Value
	require.Contains(t, messages.A.Message, "generation ")
	require.Contains(t, messages.B.Message, "generation ")
	require.NotEqual(t, messages.A.Message, messages.B.Message)
}
