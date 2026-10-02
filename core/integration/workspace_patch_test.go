package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// gitPatch makes edit in a scratch git repository holding files, and returns
// the patch `git diff --binary` writes for it.
func gitPatch(ctx context.Context, t *testctx.T, files map[string]string, edit func(dir string)) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return string(out)
	}
	git("init", "-q")
	for name, contents := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644))
	}
	git("add", "-A")
	git("commit", "-qm", "base", "--allow-empty")
	edit(dir)
	git("add", "-A")
	return git("diff", "--cached", "--binary", "-M")
}

func writeTestFile(t *testctx.T, root, name, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644))
}

// patchTestFiles is a tree for a patch to add to, modify, delete from, rename
// within and change a binary file of. Each directory a deletion empties of the
// patch's files holds another file, which must survive.
var patchTestFiles = map[string]string{
	"keep.txt":         "keep\n",
	"sub/mod.txt":      "one\ntwo\nthree\n",
	"sub/sibling.txt":  "sibling\n",
	"gone/del.txt":     "bye\n",
	"gone/sibling.txt": "stays\n",
	"ren/from.txt":     strings.Repeat("renamed content\n", 20),
	"ren/sibling.txt":  "stays\n",
	"bin.dat":          "\x00\x01\x02binary\x00",
}

// patchTestEdit is the patch's edit of patchTestFiles.
func patchTestEdit(t *testctx.T) func(dir string) {
	return func(dir string) {
		writeTestFile(t, dir, "sub/mod.txt", "one\nTWO\nthree\n")
		writeTestFile(t, dir, "new/added.txt", "added\n")
		require.NoError(t, os.Remove(filepath.Join(dir, "gone/del.txt")))
		require.NoError(t, os.Rename(filepath.Join(dir, "ren/from.txt"), filepath.Join(dir, "ren/to.txt")))
		writeTestFile(t, dir, "bin.dat", "\x00\x03\x04binary changed\x00")
	}
}

// patchTestWant is patchTestFiles with patchTestEdit applied, and the paths
// it removes.
var (
	patchTestWant = map[string]string{
		"keep.txt":         "keep\n",
		"sub/mod.txt":      "one\nTWO\nthree\n",
		"sub/sibling.txt":  "sibling\n",
		"new/added.txt":    "added\n",
		"gone/sibling.txt": "stays\n",
		"ren/to.txt":       strings.Repeat("renamed content\n", 20),
		"ren/sibling.txt":  "stays\n",
		"bin.dat":          "\x00\x03\x04binary changed\x00",
	}
	patchTestRemoved = []string{"gone/del.txt", "ren/from.txt"}
)

func requireWorkspaceFiles(ctx context.Context, t *testctx.T, ws *dagger.Workspace, want map[string]string, removed []string) {
	t.Helper()
	for name, contents := range want {
		got, err := ws.File("/" + name).Contents(ctx)
		require.NoError(t, err, name)
		require.Equal(t, contents, got, name)
	}
	for _, name := range removed {
		_, err := ws.File("/" + name).Contents(ctx)
		require.Error(t, err, "%s must be removed", name)
	}
}

func (WorkspaceSuite) TestWorkspaceWithPatchFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory()
	for name, contents := range patchTestFiles {
		source = source.WithNewFile(name, contents)
	}
	patch := c.Directory().WithNewFile("change.patch", gitPatch(ctx, t, patchTestFiles, patchTestEdit(t))).File("change.patch")

	t.Run("value workspace", func(ctx context.Context, t *testctx.T) {
		requireWorkspaceFiles(ctx, t, source.AsWorkspace().WithPatchFile(patch), patchTestWant, patchTestRemoved)
	})

	t.Run("paths are relative to the root, whatever the cwd", func(ctx context.Context, t *testctx.T) {
		ws := source.AsWorkspace(dagger.DirectoryAsWorkspaceOpts{Cwd: "sub"}).WithPatchFile(patch)
		requireWorkspaceFiles(ctx, t, ws, patchTestWant, patchTestRemoved)
		// The cwd is kept: relative reads resolve from it.
		got, err := ws.File("mod.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "one\nTWO\nthree\n", got)
	})

	t.Run("an empty patch changes nothing", func(ctx context.Context, t *testctx.T) {
		empty := c.Directory().WithNewFile("empty.patch", "").File("empty.patch")
		ws := source.AsWorkspace().WithPatchFile(empty)
		requireWorkspaceFiles(ctx, t, ws, patchTestFiles, nil)
		unchanged, err := ws.Changes().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, unchanged, "an empty patch must leave no changes")
	})

	t.Run("conflicts", func(ctx context.Context, t *testctx.T) {
		drifted := source.WithNewFile("sub/mod.txt", "one\ndrifted\nthree\n").AsWorkspace()
		_, err := drifted.WithPatchFile(patch).File("/sub/mod.txt").Contents(ctx)
		require.Error(t, err, "a hunk that no longer applies fails by default")

		got, err := drifted.WithPatchFile(patch, dagger.WorkspaceWithPatchFileOpts{
			OnConflict: dagger.PatchConflictLeaveConflictMarkers,
		}).File("/sub/mod.txt").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, got, "<<<<<<< workspace")
		require.Contains(t, got, "drifted")
		require.Contains(t, got, "TWO")
	})

	t.Run("a mount is read-only", func(ctx context.Context, t *testctx.T) {
		ws := source.AsWorkspace().WithMountedDirectory("/sub", c.Directory().WithNewFile("mod.txt", "one\ntwo\nthree\n"))
		_, err := ws.WithPatchFile(patch).File("/keep.txt").Contents(ctx)
		require.ErrorContains(t, err, "is a read-only mount")
	})
}

// TestWorkspaceWithPatchFileGit patches a workspace snapshotted from a git
// checkout, whose root is a git tree.
func (WorkspaceSuite) TestWorkspaceWithPatchFileGit(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	for name, contents := range patchTestFiles {
		writeTestFile(t, checkout, name, contents)
	}
	git("add", "-A")
	git("commit", "-m", "patch fixture")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	patch := c.Directory().WithNewFile("change.patch", gitPatch(ctx, t, patchTestFiles, patchTestEdit(t))).File("change.patch")
	ws := snapshotWorkspace(ctx, t, c, c.CurrentWorkspace()).WithPatchFile(patch)
	requireWorkspaceFiles(ctx, t, ws, patchTestWant, patchTestRemoved)
}

// TestWorkspaceWithPatchFileHost patches a host-backed workspace and exports
// it: the patch applies to the overlay with the host's content at its paths,
// and the export writes just its changes, keeping the files beside the ones it
// removes (dagger/dagger#14057).
func (WorkspaceSuite) TestWorkspaceWithPatchFileHost(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	for name, contents := range patchTestFiles {
		writeTestFile(t, checkout, name, contents)
	}
	// A directory the patch empties: on the host it stays, empty.
	writeTestFile(t, checkout, "lonely/only.txt", "only\n")
	git("add", "-A")
	git("commit", "-m", "patch fixture")

	files := map[string]string{"lonely/only.txt": "only\n"}
	for name, contents := range patchTestFiles {
		files[name] = contents
	}
	edit := patchTestEdit(t)
	patchText := gitPatch(ctx, t, files, func(dir string) {
		edit(dir)
		require.NoError(t, os.Remove(filepath.Join(dir, "lonely/only.txt")))
	})
	require.Contains(t, patchText, "rename from ren/from.txt")
	require.Contains(t, patchText, "GIT binary patch")

	c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(checkout, "sub")))
	patch := c.Directory().WithNewFile("change.patch", patchText).File("change.patch")
	ws := c.CurrentWorkspace().WithPatchFile(patch)
	removed := append([]string{"lonely/only.txt"}, patchTestRemoved...)
	requireWorkspaceFiles(ctx, t, ws, patchTestWant, removed)

	require.NoError(t, ws.Export(ctx))
	for name, contents := range patchTestWant {
		got, err := os.ReadFile(filepath.Join(checkout, name))
		require.NoError(t, err, name)
		require.Equal(t, contents, string(got), name)
	}
	for _, name := range removed {
		_, err := os.Stat(filepath.Join(checkout, name))
		require.ErrorIs(t, err, os.ErrNotExist, name)
	}
	info, err := os.Stat(filepath.Join(checkout, "lonely"))
	require.NoError(t, err, "the directory the patch empties is kept")
	require.True(t, info.IsDir())
	var status []string
	for line := range strings.SplitSeq(git("status", "--porcelain"), "\n") {
		status = append(status, strings.Join(strings.Fields(line), " "))
	}
	require.ElementsMatch(t, []string{
		"M bin.dat",
		"D gone/del.txt",
		"D lonely/only.txt",
		"D ren/from.txt",
		"M sub/mod.txt",
		"?? new/",
		"?? ren/to.txt",
	}, status)
}

// TestWorkspaceWithPatchFileHostMount refuses a patch under a mount of a
// host-backed workspace too.
func (WorkspaceSuite) TestWorkspaceWithPatchFileHostMount(ctx context.Context, t *testctx.T) {
	checkout, _ := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	patchText := gitPatch(ctx, t, map[string]string{"vendor/lib.txt": "lib\n"}, func(dir string) {
		writeTestFile(t, dir, "vendor/lib.txt", "hacked\n")
	})
	patch := c.Directory().WithNewFile("change.patch", patchText).File("change.patch")
	ws := c.CurrentWorkspace().
		WithMountedDirectory("/vendor", c.Directory().WithNewFile("lib.txt", "lib\n")).
		WithPatchFile(patch)
	_, err := ws.File("/vendor/lib.txt").Contents(ctx)
	require.ErrorContains(t, err, "is a read-only mount")
}
