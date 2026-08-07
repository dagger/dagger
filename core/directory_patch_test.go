package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
)

func TestConvertRejectToMarkers(t *testing.T) {
	t.Run("rejected hunk becomes a marker block at its old position", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "greeting.txt")
		require.NoError(t, os.WriteFile(target, []byte("intro\ndrifted line\noutro\n"), 0o644))
		rej := filepath.Join(dir, "greeting.txt.rej")
		require.NoError(t, os.WriteFile(rej, []byte(`--- greeting.txt
+++ greeting.txt
@@ -2,1 +2,1 @@
-original line
+patched line
`), 0o644))

		require.NoError(t, convertRejectToMarkers(target, rej))

		got, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, `intro
<<<<<<< workspace
=======
patched line
>>>>>>> patch (rejected hunk at line 2)
drifted line
outro
`, string(got))
	})

	t.Run("multiple hunks insert bottom-up without shifting", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "f.txt")
		require.NoError(t, os.WriteFile(target, []byte("a\nb\nc\nd\ne\n"), 0o644))
		rej := filepath.Join(dir, "f.txt.rej")
		require.NoError(t, os.WriteFile(rej, []byte(`--- f.txt
+++ f.txt
@@ -1,1 +1,1 @@
-x
+X
@@ -5,1 +5,1 @@
-y
+Y
`), 0o644))

		require.NoError(t, convertRejectToMarkers(target, rej))

		got, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, `<<<<<<< workspace
=======
X
>>>>>>> patch (rejected hunk at line 1)
a
b
c
d
<<<<<<< workspace
=======
Y
>>>>>>> patch (rejected hunk at line 5)
e
`, string(got))
	})

	t.Run("hunk position past end of file clamps to end", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "short.txt")
		require.NoError(t, os.WriteFile(target, []byte("only\n"), 0o644))
		rej := filepath.Join(dir, "short.txt.rej")
		require.NoError(t, os.WriteFile(rej, []byte(`--- short.txt
+++ short.txt
@@ -10,1 +10,1 @@
-gone
+new content
`), 0o644))

		require.NoError(t, convertRejectToMarkers(target, rej))

		got, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, `only
<<<<<<< workspace
=======
new content
>>>>>>> patch (rejected hunk at line 10)
`, string(got))
	})

	t.Run("missing target file gets created with the marker block", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "removed.txt")
		rej := filepath.Join(dir, "removed.txt.rej")
		require.NoError(t, os.WriteFile(rej, []byte(`--- removed.txt
+++ removed.txt
@@ -1,1 +1,1 @@
-old
+new
`), 0o644))

		require.NoError(t, convertRejectToMarkers(target, rej))

		got, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Contains(t, string(got), "<<<<<<< workspace")
		require.Contains(t, string(got), "new")
	})
}

func TestFindRejectFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.rej"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.rej"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "code.go"), nil, 0o644))

	rejects, err := findRejectFiles(dir)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{
		"a.rej":                       true,
		filepath.Join("sub", "b.rej"): true,
	}, rejects)
}

func TestGitDiagnosticsWrap(t *testing.T) {
	t.Run("no output", func(t *testing.T) {
		var d gitDiagnostics
		err := d.wrap("git apply", io.ErrUnexpectedEOF)
		require.ErrorContains(t, err, "git apply")
		require.ErrorContains(t, err, "(no output)")
	})

	t.Run("includes subprocess output", func(t *testing.T) {
		var d gitDiagnostics
		_, err := d.Write([]byte("error: patch failed: a.txt:1\n"))
		require.NoError(t, err)
		require.ErrorContains(t, d.wrap("git apply", io.ErrUnexpectedEOF), "error: patch failed: a.txt:1")
	})

	t.Run("bounds output", func(t *testing.T) {
		var d gitDiagnostics
		n, err := d.Write([]byte(strings.Repeat("x", gitDiagnosticsMaxLen*2)))
		require.NoError(t, err)
		// Reports a full write (it's a tee, not a sink) but retains a bounded head.
		require.Equal(t, gitDiagnosticsMaxLen*2, n)
		require.Len(t, d.buf, gitDiagnosticsMaxLen)
	})

	t.Run("quotes the patch line git named", func(t *testing.T) {
		patch := writeTempPatch(t, "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-hello\n+bye")
		d := gitDiagnostics{patchPath: patch}
		_, err := d.Write([]byte("error: corrupt patch at <stdin>:6\n"))
		require.NoError(t, err)
		msg := d.wrap("git apply", io.ErrUnexpectedEOF).Error()
		require.Contains(t, msg, "patch around <stdin>:6:")
		// Quoted, so trailing-whitespace-sensitive lines stay legible.
		require.Contains(t, msg, `> 6: "+bye"`)
		require.Contains(t, msg, `4: "@@ -1 +1 @@"`)
	})
}

// writeTempPatch writes contents to a temp file and returns its path.
func writeTempPatch(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "patch")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestApplyGitPatchIgnoresEmbeddedRepo(t *testing.T) {
	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-hello\n+bye\n"

	// A checkout made with `git worktree` has a .git FILE naming a gitdir
	// elsewhere on the developer's machine. Inside the engine that path
	// doesn't exist, and git used to die during repository discovery —
	// "fatal: not a git repository" — before it even read the patch, so
	// EVERY patch against such a tree failed. Patching a working tree needs
	// no repository at all.
	for _, tc := range []struct {
		name     string
		writeGit func(t *testing.T, dir string)
	}{
		{"no repo", func(*testing.T, string) {}},
		{"dangling worktree pointer", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"),
				[]byte("gitdir: /nonexistent/host/path/.bare/worktrees/wip\n"), 0o644))
		}},
		{"garbage .git directory", func(t *testing.T, dir string) {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, onConflict := range []PatchConflict{PatchConflictFail, PatchConflictLeaveMarkers} {
				t.Run(string(onConflict), func(t *testing.T) {
					dir := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
					tc.writeGit(t, dir)

					err := applyGitPatch(t.Context(), dir, strings.NewReader(patch), "",
						telemetry.SpanStdio(t.Context(), InstrumentationLibrary), onConflict)
					require.NoError(t, err)

					got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
					require.NoError(t, err)
					require.Equal(t, "bye\n", string(got))
				})
			}
		})
	}
}
