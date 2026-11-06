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

func (ArtifactsSuite) TestInputProvenanceBeforeExecution(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = './probe'\nmain = true\n[modules.broad]\nsource = './broad'\n").
		WithNewFile("probe/dagger-module.toml", "name = 'probe'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n").
		WithNewFile("probe/main.dang", "# "+rand.Text()+`
type Probe {
 pub byFile(input: File! @defaultPath(path: "/src/input.txt")): Void @check {
   raise "check must not run"
 }
 pub docs(input: Directory! @defaultPath(path: "/docs") @ignorePatterns(patterns: ["*.tmp"])): Void @check {
   raise "check must not run"
 }
 pub whole(ws: Workspace!): Void @check { raise "check must not run" }
 pub code: Void @check { raise "check must not run" }
 pub receiver(input: File! @defaultPath(path: "/src/input.txt")): Holder! { Holder(input) }
 pub broad(ws: Workspace!): Holder! { Holder(ws.file("/src/input.txt")) }
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
}`).
		WithNewFile("broad/dagger-module.toml", "name = 'broad'\nengineVersion = 'v1.0.0-beta.20'\n[runtime]\nsource = 'dang'\n").
		WithNewFile("broad/main.dang", `type Broad {
 new(ws: Workspace!) { raise "constructor must not run" }
 pub verify: Void @check { raise "check must not run" }
}`).
		WithNewFile("src/input.txt", "input").
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

	wildcards := []string{"whole", "broad/nested/verify", "broad/verify"}
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"src/input.txt", []string{"by-file", "receiver/nested/verify"}},
		{"docs/new.md", []string{"docs"}},
		{"docs/ignored.tmp", nil},
		{"unrelated.txt", nil},
		{"generated/new.txt", []string{"gen/stale"}},
		{"probe/main.dang", []string{"by-file", "docs", "code", "receiver/nested/verify", "gen/stale", "same"}},
	} {
		t.Run(tc.path, func(ctx context.Context, t *testctx.T) {
			items := selected(t, c, ws, tc.path)
			var paths []string
			for _, item := range items {
				require.Empty(t, item.Provenance.Issues, strings.Join(item.Path, "/"))
				paths = append(paths, strings.Join(item.Path, "/"))
				if strings.Join(item.Path, "/") == "whole" {
					require.True(t, slices.ContainsFunc(item.Provenance.Selectors, func(s selector) bool {
						return s.Path == "/" && s.Kind == "WORKSPACE_READ" && s.Origin == "argument ws" && !s.Conservative
					}))
				}
				if strings.Join(item.Path, "/") == "broad/verify" {
					require.True(t, slices.ContainsFunc(item.Provenance.Selectors, func(s selector) bool {
						return s.Path == "/" && s.Kind == "WORKSPACE_READ" && strings.Contains(s.Origin, "argument ws") && !s.Conservative
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
	_, err = source.WithNewFile("left/input.txt", hostContents).
		WithNewFile("right/input.txt", hostContents).Export(ctx, repo)
	require.NoError(t, err)
	initGitRepo(ctx, t, repo)
	hostClient := connect(ctx, t, dagger.WithWorkdir(repo))
	hostWorkspace := core.NewQuery(hostClient).CurrentWorkspace()
	hostWorkspaceID, err := hostWorkspace.ID(ctx)
	require.NoError(t, err)
	config, err := hostWorkspace.ConfigFile(ctx)
	require.NoError(t, err)
	require.Equal(t, "dagger.toml", config)
	require.Len(t, selected(t, hostClient, hostWorkspaceID, "/"), 9)
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
