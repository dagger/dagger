package core

import (
	"context"
	"encoding/json"

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
  filterDimensionKeys(dimension: "/view/packages", keys: ["left", "right"]) {
   selected: filterDimensionKeys(dimension: "/view/packages/tests", keys: ["Foo", "Bar", "absent"]) {
    items { value { ... on ViewCase {
     name parent { keys removed results { id } owner { name parent { keys removed } } }
    } } }
    withoutUri(uri: "packages/tests?view-package=left&view-case=Bar") {
     items { value { ... on ViewCase { name parent { keys results { id } } } } }
    }
    intersection: filterDimensionKeys(dimension: "/view/packages/tests", keys: ["Bar"]) {
     items { value { ... on ViewCase { name parent { keys removed } } } }
    }
    empty: filterDimensionKeys(dimension: "/view/packages/tests", keys: ["Other"]) { items { uri } }
   }
   other: filterDimensionKeys(dimension: "/view/packages/tests", keys: ["Other"]) {
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
  second: Cases! { Cases(names: ["b", "c", "d"]) }
  alias: Cases! { first }
}
type Cases @collection {
  pub names: [String!]! @keys
  pub selection: CollectionDelta @delta
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
  first: filterDimensionKeys(dimension: "/view/first", keys: ["a", "b"]) {
   items { value { ... on ViewCase { name alias { keys } parent {
    keys removed narrow: subset(keys: ["b"]) { keys removed }
   } } } }
  }
  second: filterDimensionKeys(dimension: "/view/second", keys: ["d"]) {
   items { value { ... on ViewCase { name alias { keys } parent { keys removed } } } }
  }
 } } }
 second { keys }
 first { keys subset(keys: ["b", "c"]) { bound { artifacts { filterTypes(types: ["ViewCase"]) {
  filterDimensionKeys(dimension: "view-case", keys: ["c", "absent"]) {
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
   {"value":{"name":"d","alias":{"keys":["d"]},"parent":{"keys":["d"],"removed":["b","c"]}}}
  ]}
 }}},
 "second":{"keys":["b","c","d"]},
 "first":{"keys":["a","b","c"],"subset":{"bound":{"artifacts":{"filterTypes":{"filterDimensionKeys":{"items":[
  {"value":{"name":"c","parent":{"keys":["c"],"removed":["a","b"]}}}
 ]}}}}}}
}`, out)
}

func (ArtifactsSuite) TestFilteredViewChecks(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := artifactViewWorkspace(t, c, nestedArtifactViewSource)
	selection := "packages/tests/verify?view-package=left&view-package=right&view-case=Foo&view-case=Bar"
	out, err := base.With(daggerExec("check", "-la", "-f=link", selection)).Stdout(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "view-case=Foo")
	require.Contains(t, out, "view-case=Bar")
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
