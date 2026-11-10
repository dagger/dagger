package core

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ArtifactsSuite) TestInlineCollectionProvenance(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = './probe'\n").
		WithNewFile("probe/dagger-module.toml", "name = 'probe'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n").
		WithNewFile("probe/main.dang", `type Probe {
 let catalogue(ws: Workspace!): String {
   ws.file("/catalogue.txt").contents
   null
 }
 pub packages(ws: Workspace!): Packages! {
   catalogue(ws)
   catalogue(ws)
   Packages(names: ["pkg"], entries: [Package(tests: Tests(names: ["one"], input: ws.file("/src/input.txt")))])
 }
}
type Packages @collection {
 names: [String!]! @keys
 entries: [Package!]!
 function(name: String!): Package! @get { entries[0]! }
}
type Package { tests: Tests! }
type Tests @collection {
 names: [String!]! @keys
 let input: File!
 function(name: String!): Test! @get { Test(input: input) }
}
type Test {
 let input: File!
 pub verify: Void @check { raise "check must not run" }
}`).
		WithNewFile("catalogue.txt", "catalogue").
		WithNewFile("src/input.txt", "source").
		WithNewFile("README.md", "unrelated")
	ws, err := source.AsWorkspace().ID(ctx)
	require.NoError(t, err)

	type item struct {
		Path       []string
		Provenance struct{ Issues []string }
	}
	type selection struct{ Items []item }
	got, err := testutil.QueryWithClient[struct {
		Node struct {
			Artifacts struct {
				FilterTypes struct {
					All, Source, Catalogue, Readme selection
				}
			}
		}
	}](c, t, `query($ws: ID!) {
 node(id: $ws) { ... on Workspace { artifacts { filterTypes(types: ["Check"]) {
  all: filterAffectedByPath(paths: ["/"]) { items { path provenance { issues } } }
  source: filterAffectedByPath(paths: ["/src/input.txt"]) { items { path } }
  catalogue: filterAffectedByPath(paths: ["/catalogue.txt"]) { items { path } }
  readme: filterAffectedByPath(paths: ["/README.md"]) { items { path } }
 } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": ws}})
	require.NoError(t, err)
	checks := got.Node.Artifacts.FilterTypes
	require.Len(t, checks.All.Items, 1)
	require.Empty(t, checks.All.Items[0].Provenance.Issues)
	require.Len(t, checks.Source.Items, 1)
	require.Len(t, checks.Catalogue.Items, 1)
	require.Empty(t, checks.Readme.Items)
}

func (ArtifactsSuite) TestInputProvenanceBeforeExecution(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = './probe'\nmain = true\n[modules.bound]\nsource = './bound'\n").
		WithNewFile("probe/dagger-module.toml", "name = 'probe'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n").
		WithNewFile("probe/main.dang", "# "+rand.Text()+`
type Probe {
 pub byFile(input: File! @defaultPath(path: "/src/input.txt")): Void @check {
   raise "check must not run"
 }
 pub docs(input: Directory! @defaultPath(path: "/docs") @ignorePatterns(patterns: ["*.tmp"])): Void @check {
   raise "check must not run"
 }
 pub unfiltered(input: Directory! @defaultPath(path: "/docs")): Void @check {
   raise "check must not run"
 }
 pub whole(ws: Workspace!): Void @check { raise "check must not run" }
 pub code: Void @check { raise "check must not run" }
 pub receiver(input: File! @defaultPath(path: "/src/input.txt")): Holder! { Holder(input) }
 let overlay(ws: Workspace!): Workspace! {
   ws.withFile("/prepared-input.txt", ws.file("/src/input.txt"))
 }
 pub prepared(ws: Workspace!): Holder! { Holder(overlay(ws).file("/prepared-input.txt")) }
 pub discarded(ws: Workspace!): Flag! {
   ws.file("/discarded.txt").contents
   Flag(value: false, label: "discarded")
 }
 pub scalar(ws: Workspace!): Flag! {
   Flag(value: ws.file("/scalar.txt").contents == "yes", label: "scalar")
 }
 pub unused(ws: Workspace!): Flag! { Flag(value: false, label: "unused") }
 pub retained(ws: Workspace!): Deferred! { Deferred(WorkspaceBox(ws)) }
 pub gen(input: Directory! @defaultPath(path: "/generated") @ignorePatterns(patterns: ["*.tmp"])): Changeset! @generate {
   raise "generator must not run"
 }
 pub same(
   left: File! @defaultPath(path: "/left/input.txt"),
   right: File! @defaultPath(path: "/right/input.txt"),
 ): Void @check { raise "check must not run" }
}
type Holder {
 let input: File!
 new(input: File!) { self.input = input; self }
 pub nested: Leaf! { Leaf(input) }
}
type Leaf {
 let input: File!
 new(input: File!) { self.input = input; self }
 pub verify: Void @check { raise "check must not run" }
}
type Flag {
 let value: Boolean!
 let label: String!
 new(value: Boolean!, label: String!) { self.value = value; self.label = label; self }
 pub verify: Void @check { raise "check must not run" }
}
type WorkspaceBox {
 let ws: Workspace!
 new(ws: Workspace!) { self.ws = ws; self }
}
type Deferred {
 let box: WorkspaceBox!
 new(box: WorkspaceBox!) { self.box = box; self }
 pub verify: Void @check { raise "check must not run" }
}`).
		WithNewFile("bound/dagger.json", `{"name":"bound","engineVersion":"v1.0.0-beta.20","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("bound/main.go", `package main

import "dagger/bound/internal/dagger"

type Bound struct {
 Input *dagger.File // +private
}

func New(ws *dagger.Workspace) *Bound {
 return &Bound{Input: ws.File("/src/input.txt")}
}

// +check
func (*Bound) Verify() error { panic("check must not run") }
`).
		WithNewFile("src/input.txt", "input").
		WithNewFile("discarded.txt", "discarded input").
		WithNewFile("scalar.txt", "yes").
		WithNewFile("docs/readme.md", "documentation").
		WithNewFile("docs/ignored.tmp", "ignored").
		WithNewFile("generated/current.txt", "generated input").
		WithNewFile("left/input.txt", "equal contents").
		WithNewFile("right/input.txt", "equal contents")
	ws, err := source.AsWorkspace().ID(ctx)
	require.NoError(t, err)

	type selector struct {
		Path             string
		Include, Exclude []string
		Kind             string
		Origin           string
		Conservative     bool
	}
	type item struct {
		Path       []string
		Provenance struct {
			Selectors []selector
			Issues    []string
		}
	}
	selected := func(t *testctx.T, client *dagger.Client, workspace core.ID, path string) []item {
		t.Helper()
		got, err := testutil.QueryWithClient[struct {
			Node struct {
				Artifacts struct {
					FilterTypes struct {
						FilterAffectedByPath struct{ Items []item }
					}
				}
			}
		}](client, t, `query($ws: ID!, $path: WorkspacePath!) {
 node(id: $ws) { ... on Workspace { artifacts { filterTypes(types: ["Check"]) {
  filterAffectedByPath(paths: [$path]) {
   items { path provenance { selectors { path include exclude kind origin conservative } issues } }
  }
 } } } }
}`, &testutil.QueryOptions{Variables: map[string]any{"ws": workspace, "path": path}})
		require.NoError(t, err)
		return got.Node.Artifacts.FilterTypes.FilterAffectedByPath.Items
	}

	wildcards := []string{"whole", "retained/verify"}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"src/input.txt", []string{"by-file", "receiver/nested/verify", "prepared/nested/verify", "bound/verify"}},
		{"discarded.txt", []string{"discarded/verify"}},
		{"scalar.txt", []string{"scalar/verify"}},
		{"docs/new.md", []string{"docs", "unfiltered"}},
		{"docs/ignored.tmp", []string{"unfiltered"}},
		{"unrelated.txt", nil},
		{"generated/new.txt", []string{"gen/stale"}},
		{"probe/main.dang", []string{"by-file", "docs", "unfiltered", "code", "receiver/nested/verify", "prepared/nested/verify", "discarded/verify", "scalar/verify", "unused/verify", "gen/stale", "same"}},
		{".env", []string{"by-file", "docs", "unfiltered", "code", "receiver/nested/verify", "prepared/nested/verify", "discarded/verify", "scalar/verify", "unused/verify", "gen/stale", "same", "bound/verify"}},
	} {
		t.Run(tc.path, func(ctx context.Context, t *testctx.T) {
			items := selected(t, c, ws, tc.path)
			var paths []string
			for _, item := range items {
				require.Empty(t, item.Provenance.Issues, "%s: %+v", strings.Join(item.Path, "/"), item.Provenance.Selectors)
				paths = append(paths, strings.Join(item.Path, "/"))
				if strings.Join(item.Path, "/") == "whole" {
					require.True(t, slices.ContainsFunc(item.Provenance.Selectors, func(s selector) bool {
						return s.Path == "/" && s.Kind == "WORKSPACE_READ" && s.Origin == "argument ws" && !s.Conservative
					}))
				}
				if strings.Join(item.Path, "/") == "retained/verify" {
					require.True(t, slices.ContainsFunc(item.Provenance.Selectors, func(s selector) bool {
						return s.Path == "/" && s.Kind == "WORKSPACE_READ" && strings.Contains(s.Origin, "receiver") && !s.Conservative
					}))
				}
			}
			require.ElementsMatch(t, append(append([]string{}, wildcards...), tc.want...), paths, "%+v", items)
		})
	}

	// Equal input contents must retain both paths on repeated metadata queries.
	for range 2 {
		for _, path := range []string{"left/input.txt", "right/input.txt"} {
			var same *item
			for _, item := range selected(t, c, ws, path) {
				if strings.Join(item.Path, "/") == "same" {
					same = &item
				}
			}
			require.NotNil(t, same, path)
			var origins []string
			for _, selector := range same.Provenance.Selectors {
				origins = append(origins, selector.Path)
			}
			require.Contains(t, origins, "/left/input.txt")
			require.Contains(t, origins, "/right/input.txt")
		}
	}

	// Read the same physical file through Workspace and Host to prove aliasing.
	repo := t.TempDir()
	// Keep this alias separate from the parallel table's input values.
	hostContents := rand.Text()
	_, err = source.
		WithNewFile("dagger.toml", "[modules.probe]\nsource = './probe'\nmain = true\n[modules.bound]\nsource = './bound'\n[modules.gitstate]\nsource = './gitstate'\n").
		WithNewFile("gitstate/dagger-module.toml", "name = 'gitstate'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n").
		WithNewFile("gitstate/main.dang", `type Gitstate {
 let clean: Boolean!
 new(ws: Workspace!) { self.clean = ws.git.uncommitted.isEmpty; self }
 pub verify: Void @check { raise "check must not run" }
}`).
		WithNewFile("left/input.txt", hostContents).
		WithNewFile("right/input.txt", hostContents).Export(ctx, repo)
	require.NoError(t, err)
	initGitRepo(ctx, t, repo)
	runGit(ctx, t, repo, "add", ".")
	runGit(ctx, t, repo, "commit", "-m", "initial")
	hostClient := connect(ctx, t, dagger.WithWorkdir(repo))
	hostWorkspace := core.NewQuery(hostClient).CurrentWorkspace()
	hostWorkspaceID, err := hostWorkspace.ID(ctx)
	require.NoError(t, err)
	config, err := hostWorkspace.ConfigFile(ctx)
	require.NoError(t, err)
	require.Equal(t, "dagger.toml", config)
	require.Len(t, selected(t, hostClient, hostWorkspaceID, "/"), 15)
	var gitState *item
	for _, item := range selected(t, hostClient, hostWorkspaceID, "unrelated.txt") {
		if strings.Join(item.Path, "/") == "gitstate/verify" {
			gitState = &item
		}
	}
	require.NotNil(t, gitState)
	require.Empty(t, gitState.Provenance.Issues)
	require.True(t, slices.ContainsFunc(gitState.Provenance.Selectors, func(s selector) bool {
		return s.Path == "/" && s.Kind == "WORKSPACE_READ" && strings.Contains(strings.ToLower(s.Origin), "git") && !s.Conservative
	}), "%+v", gitState.Provenance.Selectors)
	for _, path := range []string{"left/input.txt", "right/input.txt"} {
		require.True(t, slices.ContainsFunc(selected(t, hostClient, hostWorkspaceID, path), func(i item) bool {
			return strings.Join(i.Path, "/") == "same"
		}), path)
	}
	for _, item := range selected(t, hostClient, hostWorkspaceID, "unrelated.txt") {
		require.NotEqual(t, "same", strings.Join(item.Path, "/"), "before raw Host access: issues=%v selectors=%+v", item.Provenance.Issues, item.Provenance.Selectors)
	}
	hostPath := filepath.Join(repo, "left", "input.txt")
	hostFile := core.NewQuery(hostClient).Host().File(hostPath)
	_, err = hostFile.ID(ctx)
	require.NoError(t, err)
	hostDigest, err := hostFile.Digest(ctx)
	require.NoError(t, err)
	hostName, err := hostFile.Name(ctx)
	require.NoError(t, err)
	for _, path := range []string{"/left/input.txt", "/right/input.txt"} {
		input := core.Ref[*core.Workspace](core.NewQuery(hostClient), hostWorkspaceID).File(path)
		digest, err := input.Digest(ctx)
		require.NoError(t, err)
		name, err := input.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, name, hostName, "the cache content identity includes the file path")
		require.Equal(t, digest, hostDigest, "the alias regression requires equal contents and metadata: %s", path)
	}
	var same *item
	for _, item := range selected(t, hostClient, hostWorkspaceID, "unrelated.txt") {
		if strings.Join(item.Path, "/") == "same" {
			same = &item
		}
	}
	require.NotNil(t, same, "an unmapped Host origin must keep the check selected")
	require.Contains(t, strings.Join(same.Provenance.Issues, "\n"), "Host")
}
