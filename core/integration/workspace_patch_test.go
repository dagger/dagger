package core

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger/core"

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

func requireWorkspaceFiles(ctx context.Context, t *testctx.T, ws *core.Workspace, want map[string]string, removed []string) {
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
	source := core.NewQuery(c).Directory()
	for name, contents := range patchTestFiles {
		source = source.WithNewFile(name, contents)
	}
	patch := core.NewQuery(c).Directory().WithNewFile("change.patch", gitPatch(ctx, t, patchTestFiles, patchTestEdit(t))).File("change.patch")

	t.Run("value workspace", func(ctx context.Context, t *testctx.T) {
		requireWorkspaceFiles(ctx, t, source.AsWorkspace().WithPatchFile(patch), patchTestWant, patchTestRemoved)
	})

	t.Run("paths are relative to the root, whatever the cwd", func(ctx context.Context, t *testctx.T) {
		ws := source.AsWorkspace(core.DirectoryAsWorkspaceOpts{Cwd: "sub"}).WithPatchFile(patch)
		requireWorkspaceFiles(ctx, t, ws, patchTestWant, patchTestRemoved)
		// The cwd is kept: relative reads resolve from it.
		got, err := ws.File("mod.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "one\nTWO\nthree\n", got)
	})

	t.Run("an empty patch changes nothing", func(ctx context.Context, t *testctx.T) {
		empty := core.NewQuery(c).Directory().WithNewFile("empty.patch", "").File("empty.patch")
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

		got, err := drifted.WithPatchFile(patch, core.WorkspaceWithPatchFileOpts{
			OnConflict: core.PatchConflictLeaveConflictMarkers,
		}).File("/sub/mod.txt").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, got, "<<<<<<< workspace")
		require.Contains(t, got, "drifted")
		require.Contains(t, got, "TWO")
	})

	t.Run("a patch spanning a mount", func(ctx context.Context, t *testctx.T) {
		base := source.AsWorkspace()
		mounted := base.WithMountedDirectory("/sub", core.NewQuery(c).Directory().WithNewFile("mod.txt", "one\ntwo\nthree\n"))
		ws := mounted.WithPatchFile(patch)
		// The mount's part lands in the mount; the rest in the workspace.
		want := maps.Clone(patchTestWant)
		delete(want, "sub/sibling.txt") // shadowed by the mount
		requireWorkspaceFiles(ctx, t, ws, want, patchTestRemoved)

		// Only the workspace's part is a pending change.
		diff, err := ws.Changes(core.WorkspaceChangesOpts{From: mounted}).AsPatch().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, diff, "new/added.txt")
		require.NotContains(t, diff, "sub/mod.txt")

		// Unmounting reveals the untouched source.
		got, err := ws.WithoutMount("/sub").File("/sub/mod.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "one\ntwo\nthree\n", got)
	})

	t.Run("a patch cannot move across a mount boundary", func(ctx context.Context, t *testctx.T) {
		ws := source.AsWorkspace().WithMountedDirectory("/ren", core.NewQuery(c).Directory().WithNewFile("from.txt", patchTestFiles["ren/from.txt"]))
		across := core.NewQuery(c).Directory().WithNewFile("across.patch", gitPatch(ctx, t, patchTestFiles, func(dir string) {
			require.NoError(t, os.Rename(filepath.Join(dir, "ren/from.txt"), filepath.Join(dir, "moved.txt")))
		})).File("across.patch")
		_, err := ws.WithPatchFile(across).File("/keep.txt").Contents(ctx)
		require.ErrorContains(t, err, "across a workspace mount boundary")
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
	patch := core.NewQuery(c).Directory().WithNewFile("change.patch", gitPatch(ctx, t, patchTestFiles, patchTestEdit(t))).File("change.patch")
	ws := snapshotWorkspace(ctx, t, c, core.NewQuery(c).CurrentWorkspace()).WithPatchFile(patch)
	requireWorkspaceFiles(ctx, t, ws, patchTestWant, patchTestRemoved)
}

// TestWorkspaceWithPatchFileHost patches a host-backed workspace and exports
// it: the patch applies to the overlay with the host's content at its paths,
// and the export writes just its changes, keeping the files beside the ones it
// removes (dagger/dagger#14057), while a directory the patch empties goes, as
// with `git apply` on the checkout.
func (WorkspaceSuite) TestWorkspaceWithPatchFileHost(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	for name, contents := range patchTestFiles {
		writeTestFile(t, checkout, name, contents)
	}
	// Directories the patch empties: of a file, and of a directory holding
	// only that file's directory.
	writeTestFile(t, checkout, "lonely/only.txt", "only\n")
	writeTestFile(t, checkout, "nest/deep/only.txt", "only\n")
	git("add", "-A")
	git("commit", "-m", "patch fixture")

	files := map[string]string{"lonely/only.txt": "only\n", "nest/deep/only.txt": "only\n"}
	for name, contents := range patchTestFiles {
		files[name] = contents
	}
	edit := patchTestEdit(t)
	patchText := gitPatch(ctx, t, files, func(dir string) {
		edit(dir)
		require.NoError(t, os.Remove(filepath.Join(dir, "lonely/only.txt")))
		require.NoError(t, os.Remove(filepath.Join(dir, "nest/deep/only.txt")))
	})
	require.Contains(t, patchText, "rename from ren/from.txt")
	require.Contains(t, patchText, "GIT binary patch")

	c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(checkout, "sub")))
	patch := core.NewQuery(c).Directory().WithNewFile("change.patch", patchText).File("change.patch")
	ws := core.NewQuery(c).CurrentWorkspace().WithPatchFile(patch)
	removed := append([]string{"lonely/only.txt", "nest/deep/only.txt"}, patchTestRemoved...)
	requireWorkspaceFiles(ctx, t, ws, patchTestWant, removed)

	require.NoError(t, ws.Export(ctx))
	for name, contents := range patchTestWant {
		got, err := os.ReadFile(filepath.Join(checkout, name))
		require.NoError(t, err, name)
		require.Equal(t, contents, string(got), name)
	}
	for _, name := range append([]string{"lonely", "nest"}, removed...) {
		_, err := os.Stat(filepath.Join(checkout, name))
		require.ErrorIs(t, err, os.ErrNotExist, name)
	}
	// The directories losing one of two files stay, with the other.
	for _, dir := range []string{"gone", "ren"} {
		info, err := os.Stat(filepath.Join(checkout, dir))
		require.NoError(t, err, dir)
		require.True(t, info.IsDir(), dir)
	}
	var status []string
	for line := range strings.SplitSeq(git("status", "--porcelain"), "\n") {
		status = append(status, strings.Join(strings.Fields(line), " "))
	}
	require.ElementsMatch(t, []string{
		"M bin.dat",
		"D gone/del.txt",
		"D lonely/only.txt",
		"D nest/deep/only.txt",
		"D ren/from.txt",
		"M sub/mod.txt",
		"?? new/",
		"?? ren/to.txt",
	}, status)
}

// TestWorkspaceWithPatchFileHostEmptiedDir pins which directories a patch
// emptying them removes from a host-backed workspace: only those the host
// holds nothing else in, and for good, through later edits.
func (WorkspaceSuite) TestWorkspaceWithPatchFileHostEmptiedDir(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	files := map[string]string{
		"lonely/only.txt":    "only\n",
		"pair/del.txt":       "bye\n",
		"pair/sibling.txt":   "stays\n",
		"shared/sub/del.txt": "bye\n",
	}
	for name, contents := range files {
		writeTestFile(t, checkout, name, contents)
	}
	git("add", "-A")
	git("commit", "-m", "patch fixture")
	// Neither in the patch nor tracked: it keeps its directory, as for git.
	writeTestFile(t, checkout, "shared/untracked.txt", "mine\n")

	patchText := gitPatch(ctx, t, files, func(dir string) {
		for _, name := range []string{"lonely/only.txt", "pair/del.txt", "shared/sub/del.txt"} {
			require.NoError(t, os.Remove(filepath.Join(dir, name)))
		}
	})
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	patch := core.NewQuery(c).Directory().WithNewFile("change.patch", patchText).File("change.patch")
	// A later edit must not bring the emptied directory back.
	ws := core.NewQuery(c).CurrentWorkspace().WithPatchFile(patch).WithNewFile("later.txt", "later\n")
	require.NoError(t, ws.Export(ctx))

	for _, name := range []string{"lonely", "pair/del.txt", "shared/sub"} {
		_, err := os.Stat(filepath.Join(checkout, name))
		require.ErrorIs(t, err, os.ErrNotExist, name)
	}
	for name, contents := range map[string]string{
		"pair/sibling.txt":     "stays\n",
		"shared/untracked.txt": "mine\n",
		"later.txt":            "later\n",
	} {
		got, err := os.ReadFile(filepath.Join(checkout, name))
		require.NoError(t, err, name)
		require.Equal(t, contents, string(got), name)
	}
}

// TestWorkspaceWithPatchFileCreatesExisting creates a file the workspace
// already has: as with `git apply`, it fails, under LEAVE_CONFLICT_MARKERS
// too, since conflict markers cannot express it — whether the workspace is a
// value or host-backed, where the overlay the patch applies to does not hold
// the host's file.
func (WorkspaceSuite) TestWorkspaceWithPatchFileCreatesExisting(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	writeTestFile(t, checkout, "exists.txt", "host\n")
	writeTestFile(t, checkout, "from.txt", strings.Repeat("renamed content\n", 20))
	writeTestFile(t, checkout, "onto.txt", "host\n")
	git("add", "-A")
	git("commit", "-m", "patch fixture")

	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	newFile := core.NewQuery(c).Directory().WithNewFile("change.patch", gitPatch(ctx, t, nil, func(dir string) {
		writeTestFile(t, dir, "exists.txt", "patch\n")
	})).File("change.patch")
	renamed := map[string]string{"from.txt": strings.Repeat("renamed content\n", 20)}
	rename := core.NewQuery(c).Directory().WithNewFile("change.patch", gitPatch(ctx, t, renamed, func(dir string) {
		require.NoError(t, os.Rename(filepath.Join(dir, "from.txt"), filepath.Join(dir, "onto.txt")))
	})).File("change.patch")
	markers := core.WorkspaceWithPatchFileOpts{OnConflict: core.PatchConflictLeaveConflictMarkers}

	value := core.NewQuery(c).Directory().
		WithNewFile("exists.txt", "host\n").
		WithNewFile("from.txt", renamed["from.txt"]).
		WithNewFile("onto.txt", "host\n").
		AsWorkspace()
	for name, ws := range map[string]*core.Workspace{
		"value workspace": value,
		"host workspace":  core.NewQuery(c).CurrentWorkspace(),
	} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			for _, patch := range []*core.File{newFile, rename} {
				_, err := ws.WithPatchFile(patch).File("/exists.txt").Contents(ctx)
				require.Error(t, err)

				_, err = ws.WithPatchFile(patch, markers).File("/exists.txt").Contents(ctx)
				require.ErrorContains(t, err, "already exists in working directory")
				require.ErrorContains(t, err, "could not be patched at all")
			}

			// A file an earlier edit removed is free to create.
			got, err := ws.WithoutFile("exists.txt").WithPatchFile(newFile).File("/exists.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "patch\n", got)
		})
	}

	err := core.NewQuery(c).CurrentWorkspace().WithPatchFile(newFile).Export(ctx)
	require.ErrorContains(t, err, "exists.txt: already exists in working directory")
	got, err := os.ReadFile(filepath.Join(checkout, "exists.txt"))
	require.NoError(t, err)
	require.Equal(t, "host\n", string(got), "the host file must not be overwritten")
}

// TestWorkspaceWithPatchFileHostMount patches a host-backed workspace across a
// mount: the mounted part lands in the mount, in the engine, and only the
// rest is exported to the host.
func (WorkspaceSuite) TestWorkspaceWithPatchFileHostMount(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	writeTestFile(t, checkout, "root.txt", "root\n")
	git("add", "-A")
	git("commit", "-m", "root fixture")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	patchText := gitPatch(ctx, t, map[string]string{"vendor/lib.txt": "lib\n", "root.txt": "root\n"}, func(dir string) {
		writeTestFile(t, dir, "vendor/lib.txt", "patched\n")
		writeTestFile(t, dir, "root.txt", "patched root\n")
	})
	patch := core.NewQuery(c).Directory().WithNewFile("change.patch", patchText).File("change.patch")
	ws := core.NewQuery(c).CurrentWorkspace().
		WithMountedDirectory("/vendor", core.NewQuery(c).Directory().WithNewFile("lib.txt", "lib\n")).
		WithPatchFile(patch)
	got, err := ws.File("/vendor/lib.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "patched\n", got)
	got, err = ws.File("/root.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "patched root\n", got)

	diff, err := ws.Changes().AsPatch().Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, diff, "root.txt")
	require.NotContains(t, diff, "vendor")

	require.NoError(t, ws.Export(ctx))
	gotHost, err := os.ReadFile(filepath.Join(checkout, "root.txt"))
	require.NoError(t, err)
	require.Equal(t, "patched root\n", string(gotHost))
	_, err = os.Stat(filepath.Join(checkout, "vendor"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
