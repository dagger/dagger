package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Regression tests for dagger/dagger#14057: a changeset diffed against a
// sparse host base must not widen the export to a whole directory subtree.

func (LockfileSuite) TestUpdateFromNestedConfigKeepsSiblings(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	// Root has a dagger.toml but no dagger.lock, so detection places the lock
	// next to the nested dagger.toml.
	writeEmptyWorkspaceConfig(t, workdir)
	nested := filepath.Join(workdir, "demo")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "dagger.toml"), []byte{}, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "keep.txt"), []byte("keep"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "src", "main.go"), []byte("package main"), 0o600))

	_, err := hostDaggerExec(ctx, t, nested, "lock", "update")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(nested, "dagger.lock"))
	require.NoError(t, err, "nested dagger.lock should be created")
	for _, p := range []string{"dagger.toml", "keep.txt", filepath.Join("src", "main.go")} {
		_, err := os.Stat(filepath.Join(nested, p))
		require.NoError(t, err, "%s should survive dagger lock update", p)
	}
}

func (LockfileSuite) TestWithoutFileKeepsSiblings(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	sub := filepath.Join(workdir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "drop.txt"), []byte("drop"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "keep.txt"), []byte("keep"), 0o600))

	queryPath := writeQueryDoc(t, workdir, "without.graphql", `{
  currentWorkspace {
    withoutFile(path: "sub/drop.txt") {
      changes { removedPaths }
      export
    }
  }
}`)
	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)
	var got struct {
		CurrentWorkspace struct {
			WithoutFile struct {
				Changes struct {
					RemovedPaths []string `json:"removedPaths"`
				} `json:"changes"`
			} `json:"withoutFile"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal(out, &got), "query output: %s", out)
	require.Equal(t, []string{"sub/drop.txt"}, got.CurrentWorkspace.WithoutFile.Changes.RemovedPaths)

	_, err = os.Stat(filepath.Join(sub, "drop.txt"))
	require.True(t, os.IsNotExist(err), "drop.txt should be removed")
	_, err = os.Stat(filepath.Join(sub, "keep.txt"))
	require.NoError(t, err, "keep.txt should survive withoutFile of a sibling")
}

func (LockfileSuite) TestNestedEditKeepsDirectoryPermissions(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	hostGitInit(t, workdir)
	writeEmptyWorkspaceConfig(t, workdir)
	outer := filepath.Join(workdir, "a")
	inner := filepath.Join(outer, "b")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "edit.txt"), []byte("old"), 0o600))
	// Distinct modes, neither of them withNewDirectory's default: seeding the
	// delta root must reproduce each level's own permissions, or the changeset
	// reads the parents as modified and the export chmods them.
	require.NoError(t, os.Chmod(outer, 0o750))
	require.NoError(t, os.Chmod(inner, 0o755))

	queryPath := writeQueryDoc(t, workdir, "edit.graphql", `{
  currentWorkspace {
    withNewFile(path: "a/b/edit.txt", contents: "new") {
      export
    }
  }
}`)
	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", queryPath)
	require.NoError(t, err)

	contents, err := os.ReadFile(filepath.Join(inner, "edit.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))

	for dir, want := range map[string]os.FileMode{outer: 0o750, inner: 0o755} {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, want, info.Mode().Perm(), "%s permissions", dir)
	}
}

func (LockfileSuite) TestGenerateRemovalKeepsSiblings(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name    string
		filter  []string
		created []string
	}{
		{name: "one generator", filter: []string{"gen/remove-generated"}},
		// Several generator results always go through the changeset merge.
		{name: "two generators", created: []string{"other.txt"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			hostGitInit(t, workdir)
			files := map[string]string{
				"dagger.toml": `[modules.gen]
source = ".dagger/modules/gen"
`,
				".dagger/modules/gen/dagger-module.toml": `name = "gen"
engineVersion = "latest"

[runtime]
source = "dang"
`,
				".dagger/modules/gen/main.dang": `type Gen {
  pub removeGenerated(ws: Workspace!): Changeset! @generate {
    ws.withoutFile("out/deep/gen.txt").changes(ws)
  }
  pub addOther(ws: Workspace!): Changeset! @generate {
    ws.withNewFile("other.txt", "other").changes(ws)
  }
}
`,
				"out/deep/gen.txt":  "generated",
				"out/deep/keep.txt": "keep",
				"out/keep.txt":      "keep",
			}
			for p, contents := range files {
				full := filepath.Join(workdir, p)
				require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
				require.NoError(t, os.WriteFile(full, []byte(contents), 0o600))
			}

			out, err := hostDaggerExec(ctx, t, workdir, append([]string{"generate", "--no-apply"}, tc.filter...)...)
			require.NoError(t, err)
			preview := ansi.Strip(string(out))
			require.Regexp(t, `(?m)^\s*out/deep/gen\.txt\s+-1$`, preview)
			require.NotRegexp(t, `(?m)^\s*out/\s+-`, preview, "preview must not remove the whole out/ directory")

			_, err = hostDaggerExec(ctx, t, workdir, append([]string{"generate", "-y"}, tc.filter...)...)
			require.NoError(t, err)

			_, err = os.Stat(filepath.Join(workdir, "out", "deep", "gen.txt"))
			require.True(t, os.IsNotExist(err), "gen.txt should be removed")
			for _, p := range append([]string{filepath.Join("out", "keep.txt"), filepath.Join("out", "deep", "keep.txt")}, tc.created...) {
				_, err := os.Stat(filepath.Join(workdir, p))
				require.NoError(t, err, "%s should exist after generate", p)
			}
		})
	}
}
