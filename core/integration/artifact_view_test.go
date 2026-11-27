package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func artifactViewWorkspace(t *testctx.T, c *dagger.Client, source string) *core.Container {
	t.Helper()
	return nativeWorkspaceBase(t, c).
		WithNewFile("dagger.toml", "[modules.view]\nsource = \"./view\"\nentrypoint = true\n").
		WithNewFile("view/dagger-module.toml", "name = \"view\"\nengineVersion = \"v1.0.0\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("view/main.dang", source)
}

const nestedArtifactViewSource = `type View {
  packages: Packages! { Packages(names: ["left", "right", "unused"]) }
}
type Packages @collection {
  pub names: [String!]! @keys
  pub selection: CollectionDelta @delta
  lookup(name: String!): Package! @get { Package(name: name, parent: self) }
  removed: [String!]! { selection!.removedKeys }
}
type Package {
  pub name: String!
  pub parent: Packages!
  tests: Cases! {
    if (name == "unused") { raise "excluded parent evaluated" }
    if (name == "left") { Cases(names: ["Foo", "Bar", "Other"], parent: self) }
    else { Cases(names: ["Bar", "Other"], parent: self) }
  }
}
type Cases @collection {
  pub names: [String!]! @keys
  pub parent: Package!
  pub selection: CollectionDelta @delta
  lookup(name: String!): Case! @get { Case(name: name, parent: self) }
  removed: [String!]! { selection!.removedKeys }
  owner: Package! { parent }
  results: Container! { container.withNewFile("/selected", names[0]!) }
}
type Case {
  pub name: String!
  pub parent: Cases!
  verify: Void @check {
    if (parent.owner.parent.names.length != 2) { raise "parent view lost" }
    if (parent.owner.name == "left" and parent.names.length != 2) { raise "case view lost" }
    if (parent.owner.name == "right" and parent.names.length != 1) { raise "case view lost" }
    parent.results.file("/selected").contents
    if (name == "Bar") { raise "Bar assertion failed" }
    null
  }
}`

func (ArtifactsSuite) TestFilteredViewReferences(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := artifactViewWorkspace(t, c, nestedArtifactViewSource)
	out, err := base.With(daggerQuery(`{
 currentWorkspace { artifacts { filterTypes(types: ["ViewCase"]) {
  filterDimensionKeys(dimension: "ViewPackages", keys: ["left", "right"]) {
   selected: filterDimensionKeys(dimension: "ViewCases", keys: ["Foo", "Bar", "absent"]) {
    items { value { ... on ViewCase {
     name parent { keys removed results { id } owner { name parent { keys removed } } }
    } } }
    withoutUri(uri: "?name=.&ViewPackages=left&ViewCases=Bar") {
     items { value { ... on ViewCase { name parent { keys results { id } } } } }
    }
    intersection: filterDimensionKeys(dimension: "ViewCases", keys: ["Bar"]) {
     items { value { ... on ViewCase { name parent { keys removed } } } }
    }
    empty: filterDimensionKeys(dimension: "ViewCases", keys: ["Other"]) { items { uri } }
   }
   other: filterDimensionKeys(dimension: "ViewCases", keys: ["Other"]) {
    items { value { ... on ViewCase { name parent { keys removed } } } }
   }
  }
 } } }
 packages { keys removed get(key: "left") { tests { keys removed } } }
}`)).Stdout(ctx)
	require.NoError(t, err, out)
	type item struct {
		Value struct {
			Name   string
			Parent struct {
				Keys, Removed []string
				Results       struct{ ID string }
				Owner         struct {
					Name   string
					Parent struct{ Keys, Removed []string }
				}
			}
		}
	}
	var got struct {
		CurrentWorkspace struct {
			Artifacts struct {
				FilterTypes struct {
					FilterDimensionKeys struct {
						Selected struct {
							Items        []item
							WithoutURI   struct{ Items []item }
							Intersection struct{ Items []item }
							Empty        struct{ Items []item }
						}
						Other struct{ Items []item }
					}
				}
			}
		}
		Packages struct {
			Keys, Removed []string
			Get           struct {
				Tests struct{ Keys, Removed []string }
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	views := got.CurrentWorkspace.Artifacts.FilterTypes.FilterDimensionKeys
	require.Len(t, views.Selected.Items, 3)
	for i, entry := range views.Selected.Items {
		parent := entry.Value.Parent
		require.Equal(t, []string{"left", "right"}, parent.Owner.Parent.Keys)
		require.Equal(t, []string{"unused"}, parent.Owner.Parent.Removed)
		require.Equal(t, []string{"Other"}, parent.Removed)
		if i < 2 {
			require.Equal(t, "left", parent.Owner.Name)
			require.Equal(t, []string{"Foo", "Bar"}, parent.Keys)
		} else {
			require.Equal(t, "right", parent.Owner.Name)
			require.Equal(t, []string{"Bar"}, parent.Keys)
		}
	}
	shared := views.Selected.Items[0].Value.Parent.Results.ID
	require.NotEmpty(t, shared)
	require.Equal(t, shared, views.Selected.Items[1].Value.Parent.Results.ID)
	require.Len(t, views.Selected.WithoutURI.Items, 2)
	require.Equal(t, "Foo", views.Selected.WithoutURI.Items[0].Value.Name)
	require.Equal(t, []string{"Foo", "Bar"}, views.Selected.WithoutURI.Items[0].Value.Parent.Keys)
	require.Equal(t, shared, views.Selected.WithoutURI.Items[0].Value.Parent.Results.ID)
	require.Empty(t, views.Selected.Empty.Items)
	for _, view := range []struct {
		items []item
		key   string
	}{{views.Selected.Intersection.Items, "Bar"}, {views.Other.Items, "Other"}} {
		require.Len(t, view.items, 2)
		for _, entry := range view.items {
			require.Equal(t, view.key, entry.Value.Name)
			require.Equal(t, []string{view.key}, entry.Value.Parent.Keys)
		}
	}
	require.Equal(t, []string{"left", "right", "unused"}, got.Packages.Keys)
	require.Empty(t, got.Packages.Removed)
	require.Equal(t, []string{"Foo", "Bar", "Other"}, got.Packages.Get.Tests.Keys)
	require.Empty(t, got.Packages.Get.Tests.Removed)
}

func (ArtifactsSuite) TestFilteredViewCollectionOrigins(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := artifactViewWorkspace(t, c, `type View {
  pub shared: Cases! = Cases(names: ["a", "b", "c"])
  first: Cases! { shared }
  second: Cases! { Cases(names: ["b", "c", "d"], other: shared) }
  alias: Cases! { first }
}
type Cases @collection {
  pub names: [String!]! @keys
  pub selection: CollectionDelta @delta
  other: Cases = null
  peer: [String!]! { other!.names }
  lookup(name: String!): Case! @get { Case(name: name, parent: self, alias: self) }
  removed: [String!]! { selection!.removedKeys }
  bound: LLM! { llm.withTools(currentNode) }
}
type Case {
  pub name: String!
  pub parent: Cases!
  pub alias: Cases!
}
`)

	out, err := base.With(daggerQuery(`{
 currentWorkspace { artifacts { filterTypes(types: ["ViewCase"]) {
  first: filterUri(uri: "?ViewCases=first.a&ViewCases=first.b") {
   items { value { ... on ViewCase { name alias { keys } parent {
    keys removed narrow: subset(keys: ["b"]) { keys removed }
   } } } }
  }
  second: filterUri(uri: "?ViewCases=second.b&ViewCases=second.d") {
   items { value { ... on ViewCase { name alias { keys } parent { keys removed peer } } } }
  }
 } } }
 second { keys }
 first { keys subset(keys: ["b", "c"]) { bound { artifacts { filterTypes(types: ["ViewCase"]) {
  filterDimensionKeys(dimension: "ViewCases", keys: ["c", "absent"]) {
   items { value { ... on ViewCase { name parent { keys removed } } } }
  }
 } } } } }
}`)).Stdout(ctx)
	require.NoError(t, err, out)
	require.JSONEq(t, `{
 "currentWorkspace":{"artifacts":{"filterTypes":{
  "first":{"items":[
   {"value":{"name":"a","alias":{"keys":["a","b"]},"parent":{"keys":["a","b"],"removed":["c"],"narrow":{"keys":["b"],"removed":["a","c"]}}}},
   {"value":{"name":"b","alias":{"keys":["a","b"]},"parent":{"keys":["a","b"],"removed":["c"],"narrow":{"keys":["b"],"removed":["a","c"]}}}}
  ]},
  "second":{"items":[
   {"value":{"name":"b","alias":{"keys":["b","d"]},"parent":{"keys":["b","d"],"removed":["c"],"peer":[]}}},
   {"value":{"name":"d","alias":{"keys":["b","d"]},"parent":{"keys":["b","d"],"removed":["c"],"peer":[]}}}
  ]}
 }}},
 "second":{"keys":["b","c","d"]},
 "first":{"keys":["a","b","c"],"subset":{"bound":{"artifacts":{"filterTypes":{"filterDimensionKeys":{"items":[
  {"value":{"name":"c","parent":{"keys":["c"],"removed":["a","b"]}}}
 ]}}}}}}
}`, out)
}

func (ArtifactsSuite) TestCollectionRouteListing(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := artifactViewWorkspace(t, c, `type View {
  before: Cases! { Cases(names: ["unit"]) }
  after: Cases! { Cases(names: ["unit"]) }
}
type Cases @collection {
  pub names: [String!]! @keys
  lookup(name: String!): Case! @get { Case(name: name) }
}
type Case {
  pub name: String!
  verify: Void @check { null }
}
`)
	for _, all := range []bool{false, true} {
		args := []string{"check", "-l"}
		key := "*"
		if all {
			args = append(args, "--all")
			key = "unit"
		}
		out, err := base.With(daggerExec(args...)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "VIEW-CASE")
		require.Regexp(t, `(?m)^before\.`+regexp.QuoteMeta(key)+`\s`, out)
		require.Regexp(t, `(?m)^after\.`+regexp.QuoteMeta(key)+`\s`, out)
	}
}

func (ArtifactsSuite) TestFilteredViewChecks(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := artifactViewWorkspace(t, c, nestedArtifactViewSource)
	selection := "?name=verify&view-package=left&view-package=right&view-case=Foo&view-case=Bar"
	out, err := base.With(daggerExec("check", "-la", "-f=link", selection)).Stdout(ctx)
	require.NoError(t, err, out)
	links := strings.Fields(out)
	require.Len(t, links, 3)

	var query strings.Builder
	query.WriteString("{ currentWorkspace { artifacts {")
	for i, link := range links {
		fmt.Fprintf(&query, "copy%d: filterUri(uri: %q) { one { dimensionKeys(all: true) { dimension key } } }", i, link)
	}
	query.WriteString("} } }")
	out, err = base.With(daggerQuery(query.String())).Stdout(ctx)
	require.NoError(t, err, out)
	var copied struct {
		CurrentWorkspace struct {
			Artifacts map[string]struct {
				One struct {
					DimensionKeys []struct{ Dimension, Key string }
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal([]byte(out), &copied))
	var copiedCoordinates []map[string]string
	for _, item := range copied.CurrentWorkspace.Artifacts {
		coordinates := map[string]string{}
		for _, key := range item.One.DimensionKeys {
			coordinates[key.Dimension] = key.Key
		}
		copiedCoordinates = append(copiedCoordinates, coordinates)
	}
	require.ElementsMatch(t, []map[string]string{
		{"module": "view", "ViewPackages": "left", "ViewCases": "Foo", "name": "verify"},
		{"module": "view", "ViewPackages": "left", "ViewCases": "Bar", "name": "verify"},
		{"module": "view", "ViewPackages": "right", "ViewCases": "Bar", "name": "verify"},
	}, copiedCoordinates)

	pathSelection := "packages/tests/verify?view-package=left&view-package=right&view-case=Foo&view-case=Bar"
	out, err = base.With(daggerExec("check", "-la", "-f=link", pathSelection)).Stdout(ctx)
	require.NoError(t, err, out)
	require.ElementsMatch(t, links, strings.Fields(out))
	out, err = base.With(daggerQuery(`{
 currentWorkspace { artifacts { filterUri(uri: "` + selection + `") {
  asChecks { sync { pass error { message } } }
 } } }
}`)).Stdout(ctx)
	require.NoError(t, err, out)
	require.JSONEq(t, `{"currentWorkspace":{"artifacts":{"filterUri":{"asChecks":[
 {"sync":{"pass":true,"error":null}},
 {"sync":{"pass":false,"error":{"message":"Bar assertion failed"}}},
 {"sync":{"pass":false,"error":{"message":"Bar assertion failed"}}}
 ]}}}}`, out)
	out, err = base.With(daggerExecFail("--progress=report", "check", "--generated=false", "packages/tests/verify",
		"--view-package=left", "--view-package=right", "--view-case=Foo", "--view-case=Bar")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "Bar assertion failed")
	require.NotContains(t, out, "view lost")
	require.Contains(t, out, "1 passed")

	t.Run("cloud retains the whole view for each item", func(ctx context.Context, t *testctx.T) {
		target := devEngineContainerAsService(devEngineContainer(c))
		source := devEngineContainerAsService(devEngineContainer(c, func(ctr *core.Container) *core.Container {
			return ctr.WithServiceBinding("scaleout-engine", target).
				WithEnvVariable("_DAGGER_TESTS_CLOUD_RUNNER_HOST", "tcp://scaleout-engine:1234")
		}))
		out, err := engineClientContainer(ctx, t, c, source).
			WithDirectory("/work", base.Directory("/work")).WithWorkdir("/work").
			With(daggerNonNestedExecFail("--progress=report", "check", "--scale-out", "--generated=false", selection)).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Bar assertion failed")
		require.NotContains(t, out, "view lost")
		require.Contains(t, out, "1 passed")
	})
}

func (ArtifactsSuite) TestFilteredViewPrivateCollection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := artifactViewWorkspace(t, c, `type View {
  cases: Cases! { Cases(names: ["a", "b", "c"]) }
}
type Cases @collection {
  pub names: [String!]! @keys
  pub selection: CollectionDelta @delta
  lookup(name: String!): Case! @get { Case(name: name, parent: self) }
}
type Case {
  pub name: String!
  parent: Cases!
  selected: [String!]! { parent.names }
  removed: [String!]! { parent.selection!.removedKeys }
}
`)
	out, err := base.With(daggerQuery(`{
 currentWorkspace { artifacts { filterTypes(types: ["ViewCase"]) {
  filterDimensionKeys(dimension: "ViewCases", keys: ["b"]) {
   items { value { ... on ViewCase { name selected removed } } }
  }
 } } }
 cases { keys }
}`)).Stdout(ctx)
	require.NoError(t, err, out)
	require.JSONEq(t, `{"currentWorkspace":{"artifacts":{"filterTypes":{"filterDimensionKeys":{"items":[{"value":{"name":"b","selected":["b"],"removed":["a","c"]}}]}}}},"cases":{"keys":["a","b","c"]}}`, out)
}

// A setting is resolved as without a selection, even when resolving it reads
// another instance of the collection the selection narrows.
func (ArtifactsSuite) TestFilteredViewSkipsSettingResolution(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nativeWorkspaceBase(t, c).
		WithNewFile("dagger.toml", `[modules.lib]
source = "./lib"
settings.base = "dag://provider/base"
[modules.provider]
source = "./provider"
`).
		WithNewFile("lib/dagger-module.toml", "name = \"lib\"\nengineVersion = \"v1.0.0\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("lib/main.dang", `type Lib {
  pub base: Container!
  pub packages: Packages! { Packages(names: ["a", "b"], base: base) }
}
type Packages @collection {
  pub names: [String!]! @keys
  pub base: Container!
  lookup(name: String!): Package! @get { Package(name: name, base: base) }
}
type Package {
  pub name: String!
  pub base: Container!
  test: Void @check {
    if (base.file("/marker").contents != "b") { raise "wrong base" }
    null
  }
}
`).
		WithNewFile("provider/dagger-module.toml", `name = "provider"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
[[dependencies]]
name = "lib"
source = "../lib"
`).
		WithNewFile("provider/main.dang", `type Provider {
  pub base: Container! {
    container.withNewFile("/marker", lib(base: container).packages.get("b").name)
  }
}
`)
	for _, tc := range []struct {
		args   []string
		passed string
	}{
		{nil, "2 passed"},
		{[]string{"--lib-package=a"}, "1 passed"},
		{[]string{"--lib-package=b"}, "1 passed"},
	} {
		out, err := base.With(daggerExec(append([]string{"--progress=report", "check"}, tc.args...)...)).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, tc.passed)
	}
}

func (ArtifactsSuite) TestCollectionArgumentCheckUnderInstalledName(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := `type View {
  packages: Pkgs! { Pkgs(names: ["a", "b"]) }
}
type Pkgs @collection {
  pub names: [String!]! @keys
  lookup(name: String!): Pkg! @get { Pkg(name: name) }
}
type Pkg {
  pub name: String!
  test(selection: Pkgs!): Void @check {
    if (selection.names.join(",") != "a,b") { raise "wrong selection: " + selection.names.join(",") }
    null
  }
}`
	for _, name := range []string{"view", "renamed"} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			base := artifactViewWorkspace(t, c, source).
				WithNewFile("dagger.toml", fmt.Sprintf("[modules.%s]\nsource = \"./view\"\n", name))
			out, err := base.With(daggerExec("check", "-l", "--all", "-f=link")).Stdout(ctx)
			require.NoError(t, err, out)
			require.Equal(t, 2, strings.Count(out, "dag+check://"), out)
			out, err = base.With(daggerExec("check")).CombinedOutput(ctx)
			require.NoError(t, err, out)
		})
	}
}
