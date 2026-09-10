package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

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

	_, err := hostDaggerExec(ctx, t, nested, "workspace", "update")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(nested, "dagger.lock"))
	require.NoError(t, err, "nested dagger.lock should be created")
	for _, p := range []string{"dagger.toml", "keep.txt", filepath.Join("src", "main.go")} {
		_, err := os.Stat(filepath.Join(nested, p))
		require.NoError(t, err, "%s should survive dagger workspace update", p)
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
