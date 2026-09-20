package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixDiffGitHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "added file (both sides b/)",
			in:   "diff --git b/add.txt b/add.txt",
			want: "diff --git a/add.txt b/add.txt",
		},
		{
			name: "deleted file (both sides a/)",
			in:   "diff --git a/gone.txt a/gone.txt",
			want: "diff --git a/gone.txt b/gone.txt",
		},
		{
			name: "modified file is left alone",
			in:   "diff --git a/mod.txt b/mod.txt",
			want: "diff --git a/mod.txt b/mod.txt",
		},
		{
			name: "nested path",
			in:   "diff --git b/sub/dir/new.txt b/sub/dir/new.txt",
			want: "diff --git a/sub/dir/new.txt b/sub/dir/new.txt",
		},
		{
			name: "path containing spaces",
			in:   "diff --git b/with space.txt b/with space.txt",
			want: "diff --git a/with space.txt b/with space.txt",
		},
		{
			name: "quoted path",
			in:   `diff --git "b/caf\303\251.txt" "b/caf\303\251.txt"`,
			want: `diff --git "a/caf\303\251.txt" "b/caf\303\251.txt"`,
		},
		{
			name: "rename header keeps distinct paths",
			in:   "diff --git a/old-name.txt b/renamed.txt",
			want: "diff --git a/old-name.txt b/renamed.txt",
		},
		{
			name: "non-header line untouched",
			in:   "+diff --git b/x b/x",
			want: "+diff --git b/x b/x",
		},
		{
			name: "unparseable header untouched",
			in:   "diff --git nonsense",
			want: "diff --git nonsense",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, fixDiffGitHeader(tc.in))
		})
	}
}

func TestDiffGitHeaderRewriter(t *testing.T) {
	in := "diff --git b/add.txt b/add.txt\nnew file mode 100644\n--- /dev/null\n+++ b/add.txt\n@@ -0,0 +1 @@\n+hi\ntrailing-no-newline"
	var out bytes.Buffer
	rw := &diffGitHeaderRewriter{w: &out}
	// Write in awkward chunks to exercise the line buffering.
	for i := 0; i < len(in); i += 7 {
		end := min(i+7, len(in))
		n, err := rw.Write([]byte(in[i:end]))
		require.NoError(t, err)
		require.Equal(t, end-i, n)
	}
	require.NoError(t, rw.Flush())
	require.Equal(t,
		"diff --git a/add.txt b/add.txt\nnew file mode 100644\n--- /dev/null\n+++ b/add.txt\n@@ -0,0 +1 @@\n+hi\ntrailing-no-newline",
		out.String())
}

func TestTruncatingWriter(t *testing.T) {
	t.Run("under limit passes everything through", func(t *testing.T) {
		var out bytes.Buffer
		tw := &truncatingWriter{w: &out, limit: 100}
		n, err := tw.Write([]byte("hello\n"))
		require.NoError(t, err)
		require.Equal(t, 6, n)
		require.Equal(t, "hello\n", out.String())
		require.Zero(t, tw.Dropped())
		require.True(t, tw.atLineStart())
	})

	t.Run("cuts mid-write and counts the rest", func(t *testing.T) {
		var out bytes.Buffer
		tw := &truncatingWriter{w: &out, limit: 8}
		// Writes always claim full success so a MultiWriter sibling
		// (the patch file) keeps receiving the whole stream.
		n, err := tw.Write([]byte("0123456789"))
		require.NoError(t, err)
		require.Equal(t, 10, n)
		require.Equal(t, "01234567", out.String())
		require.EqualValues(t, 2, tw.Dropped())
		require.False(t, tw.atLineStart())

		n, err = tw.Write([]byte("abcdef\n"))
		require.NoError(t, err)
		require.Equal(t, 7, n)
		require.Equal(t, "01234567", out.String(), "nothing more forwarded once the budget is spent")
		require.EqualValues(t, 9, tw.Dropped())
	})

	t.Run("cut exactly at limit across chunks", func(t *testing.T) {
		var out bytes.Buffer
		tw := &truncatingWriter{w: &out, limit: 6}
		for _, chunk := range []string{"ab", "cd", "ef", "gh"} {
			n, err := tw.Write([]byte(chunk))
			require.NoError(t, err)
			require.Equal(t, 2, n)
		}
		require.Equal(t, "abcdef", out.String())
		require.EqualValues(t, 2, tw.Dropped())
	})

	t.Run("nothing written is at line start", func(t *testing.T) {
		tw := &truncatingWriter{w: io.Discard, limit: 0}
		n, err := tw.Write([]byte("dropped"))
		require.NoError(t, err)
		require.Equal(t, 7, n)
		require.EqualValues(t, 7, tw.Dropped())
		require.True(t, tw.atLineStart())
	})

	t.Run("propagates underlying errors", func(t *testing.T) {
		tw := &truncatingWriter{w: failingWriter{}, limit: 100}
		_, err := tw.Write([]byte("x"))
		require.ErrorIs(t, err, errFailingWriter)
	})

	t.Run("sits inside a MultiWriter without starving siblings", func(t *testing.T) {
		var full, capped bytes.Buffer
		tw := &truncatingWriter{w: &capped, limit: 4}
		w := io.MultiWriter(&full, tw)
		_, err := io.WriteString(w, "line one\n")
		require.NoError(t, err)
		_, err = io.WriteString(w, "line two\n")
		require.NoError(t, err)
		require.Equal(t, "line one\nline two\n", full.String())
		require.Equal(t, "line", capped.String())
		require.EqualValues(t, len("line one\nline two\n")-4, tw.Dropped())
	})
}

var errFailingWriter = errors.New("failing writer")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errFailingWriter }

func TestWriteGitDiffPatchBinaryDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "a"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b", "binary"), []byte("\x00binary payload"), 0o644))
	var patch, diagnostics bytes.Buffer
	require.NoError(t, writeGitDiffPatch(t.Context(), root, nil, &patch, &diagnostics, &diagnostics))
	require.Contains(t, patch.String(), "GIT binary patch")
	require.Equal(t, "running git diff (0 pathspecs)\n", diagnostics.String())
}

// TestWriteGitDiffPatch_Integration generates a real patch the way AsPatch
// does, asserts the `diff --git` header of every change kind (added, deleted,
// modified, renamed) uses git's a/ ... b/ convention, and - most importantly -
// asserts the patch actually applies (and reverse-applies) with `git apply`.
func TestWriteGitDiffPatch_Integration(t *testing.T) {
	ctx := context.Background()

	root := t.TempDir()
	before := filepath.Join(root, "a")
	after := filepath.Join(root, "b")

	writeFile := func(dir, path, contents string) {
		full := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
	}

	// before/
	writeFile(before, "keep.txt", "unchanged\n")
	writeFile(before, "mod.txt", "original\n")
	writeFile(before, "gone.txt", "will be removed\n")
	writeFile(before, "old-name.txt", "same content across the rename\n")

	// after/
	writeFile(after, "keep.txt", "unchanged\n")
	writeFile(after, "mod.txt", "modified\n")
	writeFile(after, "add.txt", "newly added\n")
	writeFile(after, "new-name.txt", "same content across the rename\n")

	var patch, diagnostics bytes.Buffer
	require.NoError(t, writeGitDiffPatch(ctx, root, nil, &patch, &diagnostics, &diagnostics))
	require.Equal(t, "running git diff (0 pathspecs)\n", diagnostics.String())

	patchText := patch.String()
	require.NotEmpty(t, patchText)

	var headers []string
	for _, line := range strings.Split(patchText, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			headers = append(headers, line)
		}
	}

	// Renames are emitted as delete+add (--no-renames), but every kind must
	// still use a/ on the left and b/ on the right.
	require.ElementsMatch(t, []string{
		"diff --git a/add.txt b/add.txt",           // added
		"diff --git a/gone.txt b/gone.txt",         // deleted
		"diff --git a/mod.txt b/mod.txt",           // modified
		"diff --git a/new-name.txt b/new-name.txt", // renamed (add side)
		"diff --git a/old-name.txt b/old-name.txt", // renamed (delete side)
	}, headers)

	// Now prove the patch is actually consumable by git apply.
	repo := t.TempDir()
	writeFile(repo, "keep.txt", "unchanged\n")
	writeFile(repo, "mod.txt", "original\n")
	writeFile(repo, "gone.txt", "will be removed\n")
	writeFile(repo, "old-name.txt", "same content across the rename\n")

	git := func(t *testing.T, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}

	git(t, "init", "-q")
	git(t, "add", ".")
	git(t, "commit", "-qm", "base")

	patchPath := filepath.Join(t.TempDir(), "changes.patch")
	require.NoError(t, os.WriteFile(patchPath, patch.Bytes(), 0o644))

	git(t, "apply", "--check", patchPath)
	git(t, "apply", patchPath)

	readRepoFile := func(path string) string {
		contents, err := os.ReadFile(filepath.Join(repo, path))
		require.NoError(t, err)
		return string(contents)
	}
	require.Equal(t, "unchanged\n", readRepoFile("keep.txt"))
	require.Equal(t, "modified\n", readRepoFile("mod.txt"))
	require.Equal(t, "newly added\n", readRepoFile("add.txt"))
	require.Equal(t, "same content across the rename\n", readRepoFile("new-name.txt"))
	require.NoFileExists(t, filepath.Join(repo, "gone.txt"))
	require.NoFileExists(t, filepath.Join(repo, "old-name.txt"))

	// And that it reverse-applies cleanly, which the undo/redo use case needs.
	git(t, "apply", "-R", "--check", patchPath)
	git(t, "apply", "-R", patchPath)
	require.Equal(t, "original\n", readRepoFile("mod.txt"))
	require.Equal(t, "will be removed\n", readRepoFile("gone.txt"))
	require.Equal(t, "same content across the rename\n", readRepoFile("old-name.txt"))
	require.NoFileExists(t, filepath.Join(repo, "add.txt"))
	require.NoFileExists(t, filepath.Join(repo, "new-name.txt"))
}
