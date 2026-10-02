package core

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
)

// applyPatchOnto applies a rendered patch to dir the way Workspace.__withPatch
// does: git apply, then the directories a patch cannot express.
func applyPatchOnto(t *testing.T, dir string, p *PatchOnto) {
	t.Helper()
	stdio := telemetry.SpanStreams{
		Stdout: nopWriteCloser{io.Discard},
		Stderr: nopWriteCloser{io.Discard},
	}
	require.NoError(t, applyGitPatch(t.Context(), dir, bytes.NewReader(p.Patch), stdio, PatchConflictFail))
	for _, d := range p.NewDirectories {
		target := filepath.Join(dir, d.Path)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		require.NoError(t, os.MkdirAll(target, 0o755))
		require.NoError(t, os.Chmod(target, fs.FileMode(d.Permissions)))
	}
	for _, d := range p.RemovedDirectories {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, d)))
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return err
		}
		if d.IsDir() {
			tree[filepath.ToSlash(rel)+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(rel)] = string(data)
		return nil
	}))
	return tree
}

func TestRenderPatchOnto(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()

	// render diffs before and after, and renders the result onto base at
	// prefix.
	render := func(t *testing.T, base, before, after, prefix string) *PatchOnto {
		t.Helper()
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, false)
		require.NoError(t, err)
		out, err := renderPatchOntoDirs(ctx, base, after, prefix, paths, 1<<20)
		require.NoError(t, err)
		return out
	}

	t.Run("an add base already has becomes a modification", func(t *testing.T) {
		// A generator copies the workspace with gitignore, so its Before
		// never has the ignored file it generates; the workspace does after
		// the first run. A patch from Before would create the file and fail.
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, base, "keep.txt", "keep\n")
		writeDeltaTestFile(t, base, "gen.txt", "first\n")
		writeDeltaTestFile(t, before, "keep.txt", "keep\n")
		writeDeltaTestFile(t, after, "keep.txt", "keep\n")
		writeDeltaTestFile(t, after, "gen.txt", "second\n")

		p := render(t, base, before, after, ".")
		require.Contains(t, string(p.Patch), "-first\n+second\n")
		require.NotContains(t, string(p.Patch), "new file")
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"keep.txt": "keep\n", "gen.txt": "second\n"}, readTree(t, base))
	})

	t.Run("matching content renders nothing", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, base, "gen.txt", "same\n")
		writeDeltaTestFile(t, after, "gen.txt", "same\n")
		p := render(t, base, before, after, ".")
		require.True(t, p.IsEmpty())
	})

	t.Run("a removal base never had drops out", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, base, "keep.txt", "keep\n")
		writeDeltaTestFile(t, before, "stale.txt", "stale\n")
		writeDeltaTestFile(t, after, "new.txt", "new\n")
		p := render(t, base, before, after, ".")
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"keep.txt": "keep\n", "new.txt": "new\n"}, readTree(t, base))
	})

	t.Run("a removed directory takes base's extra content", func(t *testing.T) {
		// Directory.withChanges removes the whole directory, including what
		// Before never saw, e.g. ignored files.
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, base, "gone/a.txt", "a\n")
		writeDeltaTestFile(t, base, "gone/ignored.log", "log\n")
		require.NoError(t, os.MkdirAll(filepath.Join(base, "gone/empty"), 0o755))
		writeDeltaTestFile(t, base, "keep.txt", "keep\n")
		writeDeltaTestFile(t, before, "gone/a.txt", "a\n")
		writeDeltaTestFile(t, before, "keep.txt", "keep\n")
		writeDeltaTestFile(t, after, "keep.txt", "keep\n")

		p := render(t, base, before, after, ".")
		require.Equal(t, []string{"gone"}, p.RemovedDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"keep.txt": "keep\n"}, readTree(t, base))
	})

	t.Run("empty directories are added and kept", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "emptied/only.txt", "only\n")
			require.NoError(t, os.MkdirAll(filepath.Join(root, "removed"), 0o755))
		}
		require.NoError(t, os.MkdirAll(filepath.Join(after, "emptied"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(after, "added/empty"), 0o755))
		require.NoError(t, os.Chmod(filepath.Join(after, "added/empty"), 0o700))
		writeDeltaTestFile(t, after, "added/file.txt", "file\n")
		// Files deep in new directories: the patch creates all of them.
		writeDeltaTestFile(t, after, "vendor/a/b/lib.txt", "lib\n")
		// A new directory holding only an empty one: no file creates it.
		require.NoError(t, os.MkdirAll(filepath.Join(after, "shell/inner"), 0o755))

		p := render(t, base, before, after, ".")
		require.Equal(t, []PatchOntoDirectory{
			{Path: "added/empty", Permissions: 0o700},
			{Path: "emptied", Permissions: 0o755},
			{Path: "shell", Permissions: 0o755},
			{Path: "shell/inner", Permissions: 0o755},
		}, p.NewDirectories, "a new directory with files in it is left to the patch")
		require.Equal(t, []string{"removed"}, p.RemovedDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{
			"added/":             "",
			"added/empty/":       "",
			"added/file.txt":     "file\n",
			"emptied/":           "",
			"shell/":             "",
			"shell/inner/":       "",
			"vendor/":            "",
			"vendor/a/":          "",
			"vendor/a/b/":        "",
			"vendor/a/b/lib.txt": "lib\n",
		}, readTree(t, base))
		fi, err := os.Stat(filepath.Join(base, "added/empty"))
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(0o700), fi.Mode().Perm())
	})

	t.Run("a directory keeping other files is not listed", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "dir/remove.txt", "bye\n")
		}
		writeDeltaTestFile(t, base, "dir/ignored.log", "log\n")
		require.NoError(t, os.MkdirAll(filepath.Join(after, "dir"), 0o755))
		p := render(t, base, before, after, ".")
		require.Empty(t, p.NewDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"dir/": "", "dir/ignored.log": "log\n"}, readTree(t, base))
	})

	t.Run("a changeset measured from a subdirectory applies there", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, base, "a.txt", "root\n")
		writeDeltaTestFile(t, base, "sub/a.txt", "old\n")
		writeDeltaTestFile(t, base, "sub/gone.txt", "bye\n")
		writeDeltaTestFile(t, before, "a.txt", "old\n")
		writeDeltaTestFile(t, before, "gone.txt", "bye\n")
		writeDeltaTestFile(t, after, "a.txt", "new\n")
		require.NoError(t, os.MkdirAll(filepath.Join(after, "made"), 0o755))

		p := render(t, base, before, after, "sub")
		require.Equal(t, []PatchOntoDirectory{{Path: "sub/made", Permissions: 0o755}}, p.NewDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{
			"a.txt":     "root\n",
			"sub/":      "",
			"sub/a.txt": "new\n",
			"sub/made/": "",
		}, readTree(t, base))
	})

	t.Run("base content behind a symlink is not read", func(t *testing.T) {
		outside := t.TempDir()
		writeDeltaTestFile(t, outside, "secret.txt", "secret\n")
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(base, "link")))
		writeDeltaTestFile(t, after, "link/secret.txt", "mine\n")
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, false)
		require.NoError(t, err)
		p, err := renderPatchOntoDirs(ctx, base, after, ".", paths, 1<<20)
		require.NoError(t, err)
		require.NotContains(t, string(p.Patch), "secret\n")
	})

	t.Run("an oversized patch fails", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, after, "big.txt", string(bytes.Repeat([]byte("x\n"), 4096)))
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, false)
		require.NoError(t, err)
		_, err = renderPatchOntoDirs(ctx, base, after, ".", paths, 1024)
		require.ErrorIs(t, err, ErrPatchTooLarge)
	})
}
