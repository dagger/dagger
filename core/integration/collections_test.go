package core

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type CollectionsSuite struct{}

func TestCollections(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CollectionsSuite{})
}

const collectionGoSource = `package main
import (
  "context"
  "fmt"
  "dagger/collections/internal/dagger"
)
type Collections struct{}
func (*Collections) Items() *Items {
  return &Items{Names: []string{"b", "a", "c"}, Prefix: "item:"}
}
func (m *Collections) Other() *Items { return m.Items() }
func (*Collections) Empty() *Items { return &Items{Names: []string{}} }
func (m *Collections) SelfSubset(ctx context.Context) ([]string, error) {
  return dag.Collections().Items().Subset([]string{"c"}).Batch().Removed(ctx)
}
func (m *Collections) ArgumentDelta(ctx context.Context) ([]string, error) {
  return dag.Collections().Forward(dag.Collections().Items()).Batch().Added(ctx)
}
func (m *Collections) Forward(items *Items) *Items {
  items.Names = []string{"d"}
  items.Selection = nil
  return items
}
// +collection
type Items struct {
  // +keys
  Names []string
  // +delta
  Selection *dagger.CollectionDelta
  Prefix string
}
// +get
func (items *Items) Lookup(name string) *Item { return &Item{Name: items.Prefix + name} }
func (items *Items) Selected() []string { return items.Names }
func (items *Items) Removed(ctx context.Context) ([]string, error) { return items.Selection.RemovedKeys(ctx) }
func (items *Items) Copy() *Items { return items }
func (items Items) Change(names []string) Items { items.Names = names; items.Selection = nil; return items }
func (items *Items) Fresh() *Items { return &Items{Names: []string{"z"}, Selection: items.Selection} }
func (items *Items) Added(ctx context.Context) ([]string, error) { return items.Selection.AddedKeys(ctx) }
// +check
func (items *Items) DeltaCheck(ctx context.Context) error {
  added, err := items.Selection.AddedKeys(ctx)
  if err != nil { return err }
  removed, err := items.Selection.RemovedKeys(ctx)
  if err != nil { return err }
  if fmt.Sprint(added) != "[d]" || fmt.Sprint(removed) != "[b a]" {
    return fmt.Errorf("unexpected delta: added=%v removed=%v", added, removed)
  }
  return nil
}
type Item struct { Name string }
func (item *Item) File() *dagger.File { return dag.Directory().WithNewFile("value", item.Name).File("value") }
func (*Item) Broken() *dagger.Container { panic("leaf must stay deferred") }
func (item *Item) Parts() *Parts {
  if item.Name != "item:a" { panic("excluded parent must stay deferred") }
  return &Parts{Keys: []string{"x", ""}}
}
// +collection
type Parts struct { Keys []string }
func (*Parts) Get(key string) *Part { return &Part{Name: key} }
type Part struct { Name string }
`

func collectionSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile("dagger.toml", "[modules.collections]\nsource = \"./collections\"\nentrypoint = true\n").
		WithNewFile("collections/dagger.json", `{"name":"collections","engineVersion":"v1.0.0","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("collections/main.go", collectionGoSource)
}

func (CollectionsSuite) TestGoAPI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c)).
		WithWorkdir("/work")
	out, err := ctr.With(daggerQueryAt("./collections", `{
  items {
    keys
    list { name }
    get(key: "a") { name }
    batch { selected removed }
    subset(keys: ["c", "b"]) {
      keys
      batch { selected removed copy { subset(keys: ["c"]) { batch { removed } } } }
      subset(keys: ["c"]) { keys batch { selected removed } }
      empty: subset(keys: []) { keys list batch { selected removed } }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":{
  "keys":["b","a","c"], "list":[{"name":"item:b"},{"name":"item:a"},{"name":"item:c"}],
  "get":{"name":"item:a"}, "batch":{"selected":["b","a","c"],"removed":[]},
  "subset":{"keys":["b","c"],"batch":{"selected":["b","c"],"removed":["a"],"copy":{"subset":{"batch":{"removed":["b","a"]}}}},
    "subset":{"keys":["c"],"batch":{"selected":["c"],"removed":["b","a"]}},
    "empty":{"keys":[],"list":[],"batch":{"selected":[],"removed":["b","a","c"]}}
  }
}}`, out)
	out, err = ctr.With(daggerQueryAt("./collections", `{ selfSubset argumentDelta }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"selfSubset":["b","a"],"argumentDelta":["d"]}`, out)
	out, err = ctr.With(daggerQueryAt("./collections", collectionDeltaQuery)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, collectionDeltaExpected, out)
	out, err = ctr.With(daggerQueryAt("./collections", `{ items { batch { change(names: ["c", "d"]) { batch { deltaCheck { pass } } } } } }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":{"batch":{"change":{"batch":{"deltaCheck":{"pass":true}}}}}}`, out)
}

const collectionDeltaQuery = `{ items {
  batch { change(names: ["c", "d"]) {
    keys
    batch { added removed change(names: ["c", "b", "a"]) { batch { added removed } } }
    subset(keys: ["d"]) { batch { added removed } }
  } }
  subset(keys: ["c"]) { batch {
    change(names: ["b", "c", "d"]) { batch { added removed } }
    fresh { keys batch { added removed } }
  } }
} }`

const collectionDeltaExpected = `{"items":{
  "batch":{"change":{
    "keys":["c","d"],
    "batch":{"added":["d"],"removed":["b","a"],"change":{"batch":{"added":[],"removed":[]}}},
    "subset":{"batch":{"added":["d"],"removed":["b","a","c"]}}
  }},
  "subset":{"batch":{
    "change":{"batch":{"added":["d"],"removed":["a"]}},
    "fresh":{"keys":["z"],"batch":{"added":[],"removed":[]}}
  }}
}}`

func (CollectionsSuite) TestCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := goGitBase(t, c).WithDirectory("/work", collectionSource(c)).WithWorkdir("/work")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"artifacts", "list", "items/file", "--item=a"}, "dag://items/file?item=a\n"},
		{[]string{"artifacts", "list", "items/file", "--collections-items=a"}, "dag://items/file?item=a\n"},
		{[]string{"artifacts", "list", "items/file?item=a", "other/file?item=c"}, "dag://items/file?item=a\ndag://other/file?item=c\n"},
		{[]string{"artifacts", "list", "items/file?item=a", "--collections-items=c"}, "dag://items/file?item=a\ndag://items/file?item=c\n"},
		{[]string{"artifacts", "keys", "item", "items"}, "a\nb\nc\n"},
	} {
		out, err := base.With(daggerExec(tc.args...)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, tc.want, out)
	}
	_, err := base.With(daggerExec("artifacts", "list", "--item=a")).Stdout(ctx)
	requireErrOut(t, err, "ambiguous dimension")
}

func (CollectionsSuite) TestArtifacts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws, err := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c)).
		WithWorkdir("/work").
		Directory("/work").AsWorkspace().ID(ctx)
	require.NoError(t, err)
	got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($ws: ID!) {
  node(id: $ws) { ... on Workspace { artifacts {
    dimensionDefinitions { identifier name qualifiedName }
    collection: filterUri(uri: "items") { items { uri dimensionKeys { dimension key } } }
    selected: filterUri(uri: "items?item=a") { items { uri dimensionKeys { dimension key } } }
    files: filterUri(uri: "items/file?item=a") { items { uri value { ... on File { contents } } } }
    evaluated: filterUri(uri: "items/file?item=a") { values { artifact { uri } value { ... on File { contents } } error { message } } }
    excluded: filterUri(uri: "items/file?item=a&item=b") { withoutUri(uri: "items/file?item=a") { items { uri } } }
    lazy: filterUri(uri: "items/broken?item=b") { items { uri } }
    nested: filterUri(uri: "items/parts?item=a&part=x") { items { uri dimensionKeys { dimension key } } }
    emptyKey: filterUri(uri: "items/parts?item=a&part=") { items { uri } }
    childKeys: filterUri(uri: "items/parts?item=a&part") { dimensionKeys(dimension: "part") }
    first: filterDimensionKeys(dimension: "item", keys: ["c"]) {
      filterPath(path: ["items", "file"]) { uri items { uri } }
    }
    second: filterPath(path: ["items", "file"]) {
      filterDimensionKeys(dimension: "item", keys: ["c"]) { uri items { uri } }
    }
  } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": ws}})
	require.NoError(t, err)
	require.JSONEq(t, `{"node":{"artifacts":{
  "dimensionDefinitions":[
    {"identifier":"Collections.empty","name":"item","qualifiedName":"collections-empty"},
    {"identifier":"Collections.items","name":"item","qualifiedName":"collections-items"},
    {"identifier":"Collections.other","name":"item","qualifiedName":"collections-other"},
    {"identifier":"CollectionsItem.parts","name":"part","qualifiedName":"item-parts"}
  ],
  "collection":{"items":[{"uri":"dag://items","dimensionKeys":[]}]},
  "selected":{"items":[{"uri":"dag://items?item=a","dimensionKeys":[{"dimension":"Collections.items","key":"a"}]}]},
  "files":{"items":[{"uri":"dag://items/file?item=a","value":{"contents":"item:a"}}]},
  "evaluated":{"values":[{"artifact":{"uri":"dag://items/file?item=a"},"value":{"contents":"item:a"},"error":null}]},
  "excluded":{"withoutUri":{"items":[{"uri":"dag://items/file?item=b"}]}},
  "lazy":{"items":[{"uri":"dag://items/broken?item=b"}]},
  "nested":{"items":[{"uri":"dag://items/parts?item=a&part=x","dimensionKeys":[{"dimension":"Collections.items","key":"a"},{"dimension":"CollectionsItem.parts","key":"x"}]}]},
  "emptyKey":{"items":[{"uri":"dag://items/parts?item=a&part="}]},
  "childKeys":{"dimensionKeys":["","x"]},
  "first":{"filterPath":{"uri":"dag://items/file?Collections.items=c","items":[{"uri":"dag://items/file?item=c"}]}},
  "second":{"filterDimensionKeys":{"uri":"dag://items/file?Collections.items=c","items":[{"uri":"dag://items/file?item=c"}]}}
}}}`, string(*got))
}

func (CollectionsSuite) TestMainCollection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	out, err := goGitBase(t, c).
		WithNewFile("dagger.json", `{"name":"test","engineVersion":"v1.0.0","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("main.go", `package main
// +collection
type Test struct { Keys []string }
func New() *Test { return &Test{Keys: []string{"a", "b"}} }
func (*Test) Get(name string) *Item { return &Item{Name: name} }
func (tests *Test) Count() int { return len(tests.Keys) }
type Item struct { Name string }
`).
		With(daggerQuery(`{ keys get(key: "a") { name } subset(keys: ["b"]) { batch { count } } }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"keys":["a","b"],"get":{"name":"a"},"subset":{"batch":{"count":1}}}`, out)
}

func (CollectionsSuite) TestEnumKeys(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	out, err := goGitBase(t, c).
		WithNewFile("dagger.json", `{"name":"test","engineVersion":"v1.0.0","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("main.go", `package main
import (
  "context"
  "dagger/test/internal/dagger"
)
type Color string
const (
  ColorRed Color = "red"
  ColorBlue Color = "blue"
)
// +collection
type Test struct {
  Keys []Color
  Delta *dagger.CollectionDelta
}
func New() *Test { return &Test{Keys: []Color{ColorRed, ColorBlue}} }
func (*Test) Get(color Color) *Item { return &Item{Name: string(color)} }
func (colors *Test) Removed(ctx context.Context) ([]string, error) { return colors.Delta.RemovedKeys(ctx) }
type Item struct { Name string }
`).
		With(daggerQuery(`{ keys get(key: RED) { name } subset(keys: [BLUE]) { keys get(key: BLUE) { name } batch { removed } } }`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"keys":["RED","BLUE"],"get":{"name":"red"},"subset":{"keys":["BLUE"],"get":{"name":"blue"},"batch":{"removed":["RED"]}}}`, out)
}

func (CollectionsSuite) TestSDKs(ctx context.Context, t *testctx.T) {
	for _, sdk := range []string{"python", "typescript", "dang"} {
		t.Run(sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := goGitBase(t, c)
			if sdk != "dang" {
				ctr = ctr.With(withModuleFixture(t, c, ".", sdk+"/constructor-fields-only"))
			}
			ctr = ctr.WithNewFile("dagger.json", fmt.Sprintf(`{"name":"test","engineVersion":"v1.0.0","sdk":{"source":%q},"source":"."}`, sdk))
			source := map[string]string{"python": collectionPythonSource, "typescript": collectionTypeScriptSource, "dang": collectionDangSource}[sdk]
			ctr = ctr.WithNewFile(sdkSourceFile(sdk), source)
			out, err := ctr.With(daggerQuery(`{ items {
  keys
  get(key: "a") { name }
  batch { selected removed }
  subset(keys: ["c", "b"]) {
    keys
    batch { selected removed copy { subset(keys: ["c"]) { batch { removed } } } }
  }
} }`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"items":{
  "keys":["b","a","c"], "get":{"name":"item:a"},
  "batch":{"selected":["b","a","c"],"removed":[]},
  "subset":{"keys":["b","c"],"batch":{"selected":["b","c"],"removed":["a"],
    "copy":{"subset":{"batch":{"removed":["b","a"]}}}}}
}}`, out)
			out, err = ctr.With(daggerQuery(collectionDeltaQuery)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, collectionDeltaExpected, out)
		})
	}
}

const collectionPythonSource = `from typing import Self
from dataclasses import replace
from dagger import CollectionDelta, collection, delta, field, function, get, keys, object_type

@object_type
class Item:
    name: str = field()

@object_type
@collection
class Items:
    names: list[str] = keys()
    selection: CollectionDelta | None = delta()
    prefix: str = field(default="item:")

    @function
    @get
    def lookup(self, name: str) -> Item:
        return Item(name=self.prefix + name)

    @function
    def selected(self) -> list[str]:
        return self.names

    @function
    async def removed(self) -> list[str]:
        assert self.selection is not None
        return await self.selection.removed_keys()

    @function
    def copy(self) -> Self:
        return self

    @function
    def change(self, names: list[str]) -> Self:
        return replace(self, names=names)

    @function
    def fresh(self) -> Self:
        return Items(names=["z"])

    @function
    async def added(self) -> list[str]:
        assert self.selection is not None
        return await self.selection.added_keys()

@object_type
class Test:
    @function
    def items(self) -> Items:
        return Items(names=["b", "a", "c"])
`

const collectionTypeScriptSource = `import { CollectionDelta, collection, delta, field, func, get, keys, object } from "@dagger.io/dagger"
@object()
export class Item {
  @field() name: string
  constructor(name: string) { this.name = name }
}
@collection()
export class Items {
  @keys() names: string[] = ["b", "a", "c"]
  @delta() selection?: CollectionDelta
  @field() prefix: string = "item:"
  @get() lookup(name: string): Item { return new Item(this.prefix + name) }
  @func() selected(): string[] { return this.names }
  @func() async removed(): Promise<string[]> { return await this.selection!.removedKeys() }
  @func() copy(): Items { return this }
  @func() change(names: string[]): Items { return Object.assign(new Items(), this, {names, selection: undefined}) }
  @func() fresh(): Items { const items = new Items(); items.names = ["z"]; items.selection = this.selection; return items }
  @func() async added(): Promise<string[]> { return await this.selection!.addedKeys() }
}
@object()
export class Test {
  @func() items(): Items { return new Items() }
}
`

const collectionDangSource = `type Test {
  items: Items! { Items(names: ["b", "a", "c"], prefix: "item:") }
}
type Items @collection {
  pub names: [String!]! @keys
  pub selection: CollectionDelta @delta
  pub prefix: String!
  lookup(name: String!): Item! @get { Item(name: prefix + name) }
  selected: [String!]! { names }
  removed: [String!]! { selection!.removedKeys }
  copy: Items! { self }
  change(names: [String!]!): Items! {
    self.names = names
    self.selection = null
    self
  }
  fresh: Items! { Items(names: ["z"], prefix: prefix) }
  added: [String!]! { selection!.addedKeys }
}
type Item { pub name: String! }
`
