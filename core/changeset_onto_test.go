package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
)

// applyPatchOnto applies a rendered patch to dir the way MCP applies it to a
// workspace: Workspace.withoutFiles, Workspace.withPatchFile, then the
// directories a patch cannot express.
func applyPatchOnto(t *testing.T, dir string, p *PatchOnto) {
	t.Helper()
	stdio := telemetry.SpanStreams{
		Stdout: nopWriteCloser{io.Discard},
		Stderr: nopWriteCloser{io.Discard},
	}
	for _, f := range p.RemovedFiles {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, f)))
	}
	if len(p.Patch) > 0 {
		require.NoError(t, applyGitPatch(t.Context(), dir, bytes.NewReader(p.Patch), stdio, PatchConflictFail))
	}
	for _, d := range p.RemovedDirectories {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, d)))
	}
	for _, d := range p.NewDirectories {
		target := filepath.Join(dir, d.Path)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		require.NoError(t, os.MkdirAll(target, 0o755))
		require.NoError(t, os.Chmod(target, fs.FileMode(d.Permissions)))
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
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, nil, false)
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

	t.Run("a removed directory is removed whole", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "gone/sub/a.txt", "a\n")
			writeDeltaTestFile(t, root, "keep.txt", "keep\n")
		}
		writeDeltaTestFile(t, after, "keep.txt", "keep\n")

		p := render(t, base, before, after, ".")
		require.Empty(t, p.Patch)
		require.Empty(t, p.RemovedFiles)
		require.Equal(t, []string{"gone"}, p.RemovedDirectories)
		require.Empty(t, p.NewDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"keep.txt": "keep\n"}, readTree(t, base))
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
		require.Empty(t, p.Patch)
		require.Empty(t, p.RemovedFiles, "the directory goes whole: its files need no listing")
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
			{Path: "shell", Permissions: 0o755},
			{Path: "shell/inner", Permissions: 0o755},
		}, p.NewDirectories, "a new directory with files in it is left to the patch, and one losing its files stays")
		require.Equal(t, []string{"emptied/only.txt"}, p.RemovedFiles)
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
		require.Equal(t, []string{"dir/remove.txt"}, p.RemovedFiles)
		require.Empty(t, p.Patch)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"dir/": "", "dir/ignored.log": "log\n"}, readTree(t, base))
	})

	t.Run("a deleted file's content stays out of the patch", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		big := string(bytes.Repeat([]byte("deleted line\n"), 1<<14))
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "big.txt", big)
			writeDeltaTestFile(t, root, "edit.txt", "old\n")
		}
		writeDeltaTestFile(t, after, "edit.txt", "new\n")

		p := render(t, base, before, after, ".")
		require.Equal(t, []string{"big.txt"}, p.RemovedFiles)
		require.NotContains(t, string(p.Patch), "deleted line")
		require.NotContains(t, string(p.Patch), "big.txt")
		require.Less(t, len(p.Patch), 512)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"edit.txt": "new\n"}, readTree(t, base))
	})

	t.Run("a rename removes the old path and creates the new one", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		content := string(bytes.Repeat([]byte("moved line\n"), 20))
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "dir/old.txt", content)
		}
		writeDeltaTestFile(t, after, "dir/new.txt", content)

		p := render(t, base, before, after, ".")
		require.Equal(t, []string{"dir/old.txt"}, p.RemovedFiles)
		require.Contains(t, string(p.Patch), "new file")
		require.NotContains(t, string(p.Patch), "deleted file")
		require.Empty(t, p.RemovedDirectories)
		require.Empty(t, p.NewDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"dir/": "", "dir/new.txt": content}, readTree(t, base))
	})

	t.Run("a directory replaced by a file goes before the patch", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "node/a.txt", "a\n")
			writeDeltaTestFile(t, root, "node/sub/b.txt", "b\n")
		}
		// What Before never had goes too, nested empty directories included,
		// which `git apply` could not replace.
		writeDeltaTestFile(t, base, "node/ignored.log", "log\n")
		require.NoError(t, os.MkdirAll(filepath.Join(base, "node/empty/deeper"), 0o755))
		writeDeltaTestFile(t, after, "node", "now a file\n")

		p := render(t, base, before, after, ".")
		require.Equal(t, []string{"node"}, p.RemovedFiles)
		require.Empty(t, p.RemovedDirectories, "removing the path after the patch would take the file")
		require.NotContains(t, string(p.Patch), "deleted file")
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"node": "now a file\n"}, readTree(t, base))
	})

	t.Run("a file replaced by a directory goes before the patch", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		for _, root := range []string{base, before} {
			writeDeltaTestFile(t, root, "node", "a file\n")
			writeDeltaTestFile(t, root, "leaf", "a file\n")
		}
		writeDeltaTestFile(t, after, "node/a.txt", "a\n")
		require.NoError(t, os.MkdirAll(filepath.Join(after, "leaf"), 0o700))

		p := render(t, base, before, after, ".")
		require.Equal(t, []string{"leaf", "node"}, p.RemovedFiles)
		require.Equal(t, []PatchOntoDirectory{{Path: "leaf", Permissions: 0o700}}, p.NewDirectories)
		require.NotContains(t, string(p.Patch), "deleted file")
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"leaf/": "", "node/": "", "node/a.txt": "a\n"}, readTree(t, base))
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
		require.Equal(t, []string{"sub/gone.txt"}, p.RemovedFiles)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{
			"a.txt":     "root\n",
			"sub/":      "",
			"sub/a.txt": "new\n",
			"sub/made/": "",
		}, readTree(t, base))
	})

	t.Run("a subdirectory emptied of its files stays", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, base, "sub/only.txt", "only\n")
		writeDeltaTestFile(t, before, "only.txt", "only\n")

		p := render(t, base, before, after, "sub")
		require.Equal(t, []string{"sub/only.txt"}, p.RemovedFiles)
		require.Empty(t, p.Patch)
		require.Empty(t, p.NewDirectories)
		require.Empty(t, p.RemovedDirectories)
		applyPatchOnto(t, base, p)
		require.Equal(t, map[string]string{"sub/": ""}, readTree(t, base))
	})

	t.Run("base content behind a symlink is not read", func(t *testing.T) {
		outside := t.TempDir()
		writeDeltaTestFile(t, outside, "secret.txt", "secret\n")
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(base, "link")))
		writeDeltaTestFile(t, after, "link/secret.txt", "mine\n")
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, nil, false)
		require.NoError(t, err)
		p, err := renderPatchOntoDirs(ctx, base, after, ".", paths, 1<<20)
		require.NoError(t, err)
		require.NotContains(t, string(p.Patch), "secret\n")
	})

	t.Run("an oversized patch fails", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, after, "big.txt", string(bytes.Repeat([]byte("x\n"), 4096)))
		paths, _, err := computeChangesetPathsDelta(ctx, before, after, nil, false)
		require.NoError(t, err)
		_, err = renderPatchOntoDirs(ctx, base, after, ".", paths, 1024)
		require.ErrorIs(t, err, ErrPatchTooLarge)
	})

	t.Run("a binary file", func(t *testing.T) {
		bin := map[string]string{"bin": "\x00old"}
		for _, tc := range []struct {
			name                string
			base, before, after map[string]string
			want                map[string]string
		}{
			{name: "added fails", after: map[string]string{"text.txt": "text\n", "bin": "\x00elf"}},
			{name: "modified fails", base: bin, before: bin, after: map[string]string{"bin": "\x00new"}},
			// --binary would carry a deleted file's content, to be
			// reversible; a removal needs none.
			{
				name: "deleted is a removal",
				base: map[string]string{"bin": "\x00old", "text.txt": "old\n"}, before: bin,
				after: map[string]string{"text.txt": "new\n"},
				want:  map[string]string{"text.txt": "new\n"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
				for dir, files := range map[string]map[string]string{base: tc.base, before: tc.before, after: tc.after} {
					for name, content := range files {
						writeDeltaTestFile(t, dir, name, content)
					}
				}
				paths, _, err := computeChangesetPathsDelta(ctx, before, after, nil, false)
				require.NoError(t, err)
				p, err := renderPatchOntoDirs(ctx, base, after, ".", paths, 1<<20)
				if tc.want == nil {
					require.ErrorIs(t, err, ErrPatchBinary)
					return
				}
				require.NoError(t, err)
				require.Equal(t, []string{"bin"}, p.RemovedFiles)
				require.NotContains(t, string(p.Patch), "GIT binary patch")
				applyPatchOnto(t, base, p)
				require.Equal(t, tc.want, readTree(t, base))
			})
		}
	})

	t.Run("text naming the binary marker is not binary", func(t *testing.T) {
		base, before, after := t.TempDir(), t.TempDir(), t.TempDir()
		writeDeltaTestFile(t, after, "notes.txt", "GIT binary patch\n")
		p := render(t, base, before, after, ".")
		require.Contains(t, string(p.Patch), "+GIT binary patch\n")
		require.NoError(t, CheckEmbeddablePatch(p.Patch))
	})
}

// TestCheckEmbeddablePatch covers the guard every engine-embedded patch blob
// goes through (EmbedPatch), for patches RenderPatchOnto did not bound, e.g.
// Changeset.asPatch output.
func TestCheckEmbeddablePatch(t *testing.T) {
	const text = "diff --git a/a.txt b/a.txt\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/a.txt\n" +
		"@@ -0,0 +1 @@\n" +
		"+GIT binary patch\n"
	require.NoError(t, CheckEmbeddablePatch(nil))
	require.NoError(t, CheckEmbeddablePatch([]byte(text)))

	t.Run("binary hunk", func(t *testing.T) {
		patch := text + "diff --git a/bin b/bin\n" +
			"new file mode 100644\n" +
			"index 0000000000000000000000000000000000000000..0123456789abcdef0123456789abcdef01234567\n" +
			"GIT binary patch\n" +
			"literal 4\n" +
			"LcmZQzWMT#Y01f~L\n\n" +
			"literal 0\n" +
			"HcmV?d00001\n\n"
		require.ErrorIs(t, CheckEmbeddablePatch([]byte(patch)), ErrPatchBinary)
		// First line too.
		require.ErrorIs(t, CheckEmbeddablePatch([]byte("GIT binary patch\nliteral 0\n")), ErrPatchBinary)
	})

	t.Run("binary file left out", func(t *testing.T) {
		patch := text + "diff --git a/bin b/bin\n" +
			"index 1111111..2222222 100644\n" +
			"Binary files a/bin and b/bin differ\n"
		require.ErrorIs(t, CheckEmbeddablePatch([]byte(patch)), ErrPatchBinary)
	})

	t.Run("oversized", func(t *testing.T) {
		patch := bytes.Repeat([]byte("+x\n"), EmbeddedPatchMaxBytes/3+1)
		require.ErrorIs(t, CheckEmbeddablePatch(patch), ErrPatchTooLarge)
		require.NoError(t, CheckEmbeddablePatch(patch[:EmbeddedPatchMaxBytes-EmbeddedPatchMaxBytes%3]))
	})

	require.True(t, PatchNotEmbeddable(fmt.Errorf("wrapped: %w", ErrPatchBinary)))
	require.True(t, PatchNotEmbeddable(fmt.Errorf("wrapped: %w", ErrPatchTooLarge)))
	require.False(t, PatchNotEmbeddable(nil))
	require.False(t, PatchNotEmbeddable(errors.New("other")))
}
