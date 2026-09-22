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
		{[]string{"list", "-a", "items/file", "--item=a"}, "dag://items/file?item=a\n"},
		{[]string{"list", "-a", "items/file", "--collections-items=a"}, "dag://items/file?item=a\n"},
		{[]string{"list", "-a", "items/file?item=a", "other/file?item=c"}, "dag://items/file?item=a\ndag://other/file?item=c\n"},
		{[]string{"list", "-a", "items/file?item=a", "--collections-items=c"}, "dag://items/file?item=a\ndag://items/file?item=c\n"},
		{[]string{"list", "collections-items", "items"}, "a\nb\nc\n"},
	} {
		out, err := base.With(daggerExec(tc.args...)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, tc.want, out)
	}
	_, err := base.With(daggerExec("list", "-a", "--item=a")).Stdout(ctx)
	requireErrOut(t, err, "ambiguous dimension")
}

func (CollectionsSuite) TestCheckSelection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := collectionSource(c).WithNewFile("collections/main.go", collectionGoSource+`
// +check
func (item *Item) Verify() error {
  if item.Name == "item:c" { return fmt.Errorf("unselected item c ran") }
  return nil
}
`)
	base := goGitBase(t, c).WithDirectory("/work", source).WithWorkdir("/work")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"query", []string{"dag://items/verify?item=a&item=b"}, "items/verify --item=a\nitems/verify --item=b\n"},
		{"dimension flag", []string{"items/verify", "--item=a", "--item=b"}, "items/verify --item=a\nitems/verify --item=b\n"},
		{"generic flag", []string{"items/verify", "--dimension-key=item=a", "--dimension-key=item=b"}, "items/verify --item=a\nitems/verify --item=b\n"},
		{"query and flag", []string{"items/verify?item=a", "--collections-items=b"}, "items/verify --item=a\nitems/verify --item=b\n"},
		{"separate addresses", []string{"items/verify?item=a", "other/verify?item=b"}, "items/verify --item=a\nother/verify --item=b\n"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerExec(append([]string{"check", "-l", "--all"}, tc.args...)...)).Stdout(ctx)
			require.NoError(t, err)
			require.ElementsMatch(t, strings.Fields(tc.want), strings.Fields(out))
			out, err = base.With(daggerExec(append([]string{"check"}, tc.args...)...)).CombinedOutput(ctx)
			require.NoError(t, err, out)
			require.NotContains(t, out, "unselected item c ran")
		})
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"check", "items/verify", "--unknown=a"}, "unknown flag: --unknown"},
		{[]string{"check", "items/verify", "--dimension-key=invalid"}, "expected DIMENSION=KEY"},
		{[]string{"check", "--item=a"}, "ambiguous dimension"},
	} {
		_, err := base.With(daggerExec(tc.args...)).Stdout(ctx)
		requireErrOut(t, err, tc.want)
	}
	t.Run("shell flags leave unrelated collections deferred", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("shell", "-l", "-a", "items/broken?item=a")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "--collections-items=a\n", out)
		replay, err := base.With(daggerExec("shell", "-l", "-a", strings.TrimSpace(out))).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out, replay)
	})
	t.Run("grouped", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "items/verify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "items/verify\n", out)
		combined, err := base.With(daggerExec("check", "-l", "items/verify")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(combined, "# Use --all to list each key combination."))
		out, err = base.With(daggerExec("check", "-l", "items/verify?item=a&item=b&item=c")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "items/verify --item=b --item=a --item=c\n", out)
		out, err = base.With(daggerExec("check", "-l", "items/verify?item=a&item=b")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "items/verify --item=b --item=a\n", out)
		// Replay each printed row. Item c fails if grouping loses the filter.
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}
			result, err := base.With(daggerExec(append([]string{"check"}, strings.Fields(line)...)...)).CombinedOutput(ctx)
			require.NoError(t, err, result)
		}
	})
	out, err := base.With(daggerExec("check", "items/verify", "--help")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "--item")
	require.Contains(t, out, "--dimension-key")
	require.Contains(t, out, "--all")
}

func (CollectionsSuite) TestCommandLists(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := collectionGoSource + `
// +check
func (*Item) Verify() error { return nil }
// +check
func (*Item) OtherCheck() error { return nil }
// +generate
func (*Item) Write() *dagger.Changeset { panic("list evaluated generator") }
// +up
func (*Item) Serve() *dagger.Service { panic("list evaluated service") }
// +agent
func (*Item) Assistant(base *dagger.LLM) *dagger.LLM { panic("list evaluated agent") }
// +check
func (*Part) Verify() error { return nil }
`
	source = strings.Replace(source, `if item.Name != "item:a" { panic("excluded parent must stay deferred") }`, "", 1)
	base := goGitBase(t, c).WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).WithWorkdir("/work")
	for _, tc := range []struct{ command, path string }{
		{"shell", "items/broken"}, {"up", "items/serve"}, {"generate", "items/write"}, {"agent", "items/assistant"},
	} {
		t.Run(tc.command, func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerExec(tc.command, "-l", tc.path)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.path+"\n", out)
			out, err = base.With(daggerExec(tc.command, "-l", "-a", tc.path, "--item=a")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "--collections-items=a\n", out)
			replay, err := base.With(daggerExec(tc.command, "-l", "-a", strings.TrimSpace(out))).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, out, replay)
		})
	}
	t.Run("keep path when another check matches the keys", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "-a", "items/verify", "--item=a")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "items/verify --item=a\n", out)
	})
	t.Run("keep correlated keys separate", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "items/parts/verify?item=a&part=x", "items/parts/verify?item=b&part=")).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "# Use --all")
		require.Len(t, strings.Split(strings.TrimSpace(out), "\n"), 2)
		// Parsing these lines in a shell must preserve the empty key as well.
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			replay, err := base.WithExec([]string{"sh", "-c", "dagger check -l -a " + line}, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, line+"\n", replay)
		}
	})
}

func (CollectionsSuite) TestCommandListSchemaPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := collectionGoSource + `
func (*Collections) Deferred() *Items { panic("collection keys evaluated") }
// Verify this item.
// +check
func (*Item) Verify() error { panic("check evaluated") }
// +generate
func (*Item) Write() *dagger.Changeset { panic("generator evaluated") }
// +up
func (*Item) Serve() *dagger.Service { panic("service evaluated") }
// +agent
func (*Item) Assistant(base *dagger.LLM) *dagger.LLM { panic("agent evaluated") }
`
	base := goGitBase(t, c).WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).WithWorkdir("/work")
	for _, tc := range []struct{ command, field string }{
		{"check", "verify"}, {"generate", "write"}, {"up", "serve"}, {"shell", "broken"}, {"agent", "assistant"},
	} {
		t.Run(tc.command, func(ctx context.Context, t *testctx.T) {
			path := "deferred/" + tc.field
			out, err := base.With(daggerExec(tc.command, "-l", path)).CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, path)
			require.Equal(t, 1, strings.Count(out, "# Use --all to list each key combination."))
			if tc.command == "check" {
				require.Contains(t, out, "# Verify this item.")
			}
			_, err = base.With(daggerExec(tc.command, "-l", "--all", path)).Stdout(ctx)
			requireErrOut(t, err, "collection keys evaluated")
		})
	}
	t.Run("empty collection", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "empty/verify")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "empty/verify")
		out, err = base.With(daggerExec("check", "-l", "--all", "empty/verify")).Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, out)
		out, err = base.With(daggerExec("list", "-a", "empty/verify")).Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, out)
	})
	t.Run("key filters still resolve keys", func(ctx context.Context, t *testctx.T) {
		_, err := base.With(daggerExec("check", "-l", "deferred/verify", "--item=a")).Stdout(ctx)
		requireErrOut(t, err, "collection keys evaluated")
	})
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

func (CollectionsSuite) TestArtifactUnion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	all := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c)).
		WithWorkdir("/work").
		Directory("/work").AsWorkspace().Artifacts()
	left := all.FilterURI("items/file?item=a&item=b").WithoutURI("items/file?item=b")
	right := all.FilterURI("items/file?item=b")
	joined := left.WithArtifacts(right).WithArtifacts(right)
	assertURIs := func(selection *dagger.Artifacts, want []string) {
		t.Helper()
		items, err := selection.Items(ctx)
		require.NoError(t, err)
		got := make([]string, 0, len(items))
		for _, item := range items {
			uri, err := item.URI(ctx)
			require.NoError(t, err)
			got = append(got, uri)
		}
		require.Equal(t, want, got)
	}
	assertURIs(joined, []string{"dag://items/file?item=a", "dag://items/file?item=b"})
	assertURIs(joined.FilterDimensionKeys("item", []string{"b"}), []string{"dag://items/file?item=b"})
	assertURIs(joined.WithoutURI("items/file?item=b"), []string{"dag://items/file?item=a"})
	_, err := joined.URI(ctx)
	require.ErrorContains(t, err, "use the individual artifact addresses")
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

const batchCollectionSource = `package main
import (
  "context"
  "fmt"
  "strings"
  "dagger/collections/internal/dagger"
)
type Collections struct{}
func (*Collections) Items() *Items { return &Items{Keys: []string{"b", "a", "c"}, All: []string{"b", "a", "c"}} }
func (*Collections) Numbers() *Numbers { return &Numbers{Keys: []int{2, 1, 3}} }
func (*Collections) Parents() *Parents { return &Parents{Keys: []string{"left", "right"}} }
// +collection
type Parents struct { Keys []string }
func (*Parents) Get(key string) *Parent { return &Parent{Name: key} }
type Parent struct { Name string }
func (p *Parent) Items() *Items { return &Items{Keys: []string{p.Name, "common", "c"}, All: []string{p.Name, "common", "c"}} }
// +collection
type Items struct {
  Keys []string
  All []string
  // +delta
  Delta *dagger.CollectionDelta
}
func (*Items) Get(key string) *Item { return &Item{Name: key} }
// +check
func (items *Items) Verify(ctx context.Context) error {
  if len(items.Keys) != 2 { return fmt.Errorf("expected two keys in one batch, got %v", items.Keys) }
  removed, err := items.Delta.RemovedKeys(ctx)
  if err != nil { return err }
  var expected []string
  for _, key := range items.All {
    found := false
    for _, selected := range items.Keys { if key == selected { found = true } }
    if !found { expected = append(expected, key) }
  }
  if fmt.Sprint(removed) != fmt.Sprint(expected) { return fmt.Errorf("bad delta: %v != %v", removed, expected) }
  for _, key := range items.Keys { if key == "c" { return fmt.Errorf("unselected key c ran") } }
  return nil
}
// +generate
func (items *Items) Write() *dagger.Changeset {
  return dag.Directory().WithNewFile("selected", strings.Join(items.Keys, ",")).Changes(dag.Directory())
}
// +check
func (items *Items) OnlyBatch(ctx context.Context) error { return items.Verify(ctx) }
// +collection
type Numbers struct { Keys []int }
func (*Numbers) Get(key int) *Number { return &Number{} }
// +check
func (numbers *Numbers) Verify() error {
  if fmt.Sprint(numbers.Keys) != "[2 1]" { return fmt.Errorf("bad numeric subset: %v", numbers.Keys) }
  return nil
}
type Number struct{}
// +check
func (*Number) Verify() error { return fmt.Errorf("replaced numeric check ran") }
type Item struct { Name string }
// +check
func (*Item) Verify() error { return fmt.Errorf("replaced item check ran") }
// +generate
func (*Item) Write() *dagger.Changeset { panic("replaced item generator ran") }
// +check
func (item *Item) Other() error {
  if item.Name == "c" { return fmt.Errorf("unselected fallback ran") }
  return nil
}
`

func (CollectionsSuite) TestBatchReplacement(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := goGitBase(t, c).WithDirectory("/work", collectionSource(c).
		WithNewFile("collections/main.go", batchCollectionSource)).WithWorkdir("/work")
	all := base.Directory("/work").AsWorkspace().Artifacts()
	type evaluation struct {
		Artifact struct{ URI string }
		Value    json.RawMessage
		Error    *struct{ Message string }
	}
	evaluate := func(t *testctx.T, selection *dagger.Artifacts) []evaluation {
		t.Helper()
		id, err := selection.ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[struct{ Node struct{ Values []evaluation } }](c, t, `query($id: ID!) {
 node(id: $id) { ... on Artifacts { values {
  artifact { uri } error { message }
  value { ... on Check { pass } ... on Changeset { after { file(path: "selected") { contents } } } }
 } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": id}})
		require.NoError(t, err)
		return got.Node.Values
	}
	for _, tc := range []struct {
		name      string
		selection *dagger.Artifacts
		count     int
	}{
		{"selected checks", all.FilterURI("items/verify?item=a&item=b"), 1},
		{"numeric keys", all.FilterURI("numbers/verify?number=1&number=2"), 1},
		{"union", all.FilterURI("items/verify?item=a").WithArtifacts(all.FilterURI("items/verify?item=b")), 1},
		{"exclusion", all.FilterURI("items/verify").WithoutURI("items/verify?item=c"), 1},
		{"fallback", all.FilterURI("items/other?item=a&item=b"), 2},
		{"nested parents", all.FilterURI("parents/items/verify?item=left&item=right&item=common"), 2},
		{"no matches", all.FilterURI("items/verify?item=missing"), 0},
		{"empty filter", all.FilterURI("items/verify").FilterDimensionKeys("item", []string{}), 0},
		{"batch only", all.FilterURI("items/only-batch?item=a&item=b"), 1},
		{"combined checks", all.FilterURI("items/*?item=a&item=b").FilterCheckCommand().FilterParentTypes([]string{"Changeset"}, dagger.ArtifactsFilterParentTypesOpts{Exclude: true}), 4},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			results := evaluate(t, tc.selection)
			require.Len(t, results, tc.count)
			for _, result := range results {
				require.Nil(t, result.Error, "%s: %+v", result.Artifact.URI, result.Error)
				require.JSONEq(t, `{"pass":true}`, string(result.Value))
			}
		})
	}
	t.Run("saved batch result", func(ctx context.Context, t *testctx.T) {
		results, err := all.FilterURI("items/verify?item=a&item=b").Values(ctx)
		require.NoError(t, err)
		require.Len(t, results, 1)
		failure, err := results[0].Error(ctx)
		require.NoError(t, err)
		require.Nil(t, failure)
		id, err := results[0].Artifact().ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($id: ID!) {
 node(id: $id) { ... on Artifact { value { ... on Check { pass } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": id}})
		require.NoError(t, err)
		require.JSONEq(t, `{"node":{"value":{"pass":true}}}`, string(*got))
		// The result address must reproduce the complete batch on another engine.
		uri, err := results[0].Artifact().URI(ctx)
		require.NoError(t, err)
		replayed := evaluate(t, all.FilterURI(uri))
		require.Len(t, replayed, 1)
		require.Nil(t, replayed[0].Error)
	})
	t.Run("generator", func(ctx context.Context, t *testctx.T) {
		results := evaluate(t, all.FilterURI("items/write?item=a&item=b"))
		require.Len(t, results, 1)
		require.Nil(t, results[0].Error)
		require.JSONEq(t, `{"after":{"file":{"contents":"b,a"}}}`, string(results[0].Value))
	})
	t.Run("generator stale check", func(ctx context.Context, t *testctx.T) {
		results := evaluate(t, all.FilterURI("items/write/stale?item=a&item=b"))
		require.Len(t, results, 1)
		require.NotNil(t, results[0].Error)
		require.Contains(t, results[0].Error.Message, "generated files are not up to date")
		require.NotContains(t, results[0].Error.Message, "replaced item generator ran")
	})
	t.Run("direct item", func(ctx context.Context, t *testctx.T) {
		id, err := all.FilterURI("items/verify?item=a").One().ID(ctx)
		require.NoError(t, err)
		got, err := testutil.QueryWithClient[json.RawMessage](c, t, `query($id: ID!) {
 node(id: $id) { ... on Artifact { value { ... on Check { pass error { message } } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"id": id}})
		require.NoError(t, err)
		require.Contains(t, string(*got), `"pass":false`)
		require.Contains(t, string(*got), "replaced item check ran")
	})
	t.Run("CLI query and flags", func(ctx context.Context, t *testctx.T) {
		for _, args := range [][]string{
			{"check", "--generated=false", "items/verify?item=a&item=b"},
			{"check", "--generated=false", "items/verify", "--item=a", "--item=b"},
			{"check", "--generated=false", "items/verify?item=a", "items/verify?item=b"},
		} {
			out, err := base.With(daggerExec(args...)).CombinedOutput(ctx)
			require.NoError(t, err, out)
		}
	})
}

func (CollectionsSuite) TestBatchScaleOut(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	target := devEngineContainerAsService(devEngineContainer(c))
	source := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding("scaleout-engine", target).
			WithEnvVariable("_DAGGER_TESTS_CLOUD_RUNNER_HOST", "tcp://scaleout-engine:1234")
	}))
	base := engineClientContainer(ctx, t, c, source).
		WithWorkdir("/work").WithExec([]string{"apk", "add", "git"}).WithExec([]string{"git", "init"}).
		WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", batchCollectionSource))
	for _, uri := range []string{
		"items/verify?item=a&item=b",
		"parents/items/verify?item=left&item=right&item=common",
	} {
		out, err := base.With(daggerNonNestedExec("check", "--scale-out", "--generated=false", uri)).CombinedOutput(ctx)
		require.NoError(t, err, out)
	}
}
