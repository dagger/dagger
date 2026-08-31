package core

import (
	"os"
	"path/filepath"
	"testing"

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
