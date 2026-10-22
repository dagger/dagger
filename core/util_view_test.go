package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLeavesDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.MkdirAll(filepath.Join(app, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(app, "main.txt"), []byte("main"), 0o644))
	require.NoError(t, os.Symlink("../main.txt", filepath.Join(app, "nested", "inner")))
	require.NoError(t, os.Symlink("nested", filepath.Join(app, "alias")))
	require.NoError(t, os.Symlink("../other.txt", filepath.Join(app, "up")))
	require.NoError(t, os.Symlink("/main.txt", filepath.Join(app, "abs")))
	require.NoError(t, os.Symlink("nested/../../x", filepath.Join(app, "climb")))
	require.NoError(t, os.Symlink("loop", filepath.Join(app, "loop")))

	for _, tc := range []struct {
		p      string
		leaves bool
	}{
		{"main.txt", false},
		{"nested/inner", false},
		{"alias/inner", false},
		{"missing/x", false},
		{"../main.txt", false}, // ".." in the path itself stops at the directory, as written
		{"up", true},
		{"abs", true},
		{"climb", true},
		{"alias/../up", true},
	} {
		leaves, err := leavesDir(app, tc.p)
		require.NoError(t, err, tc.p)
		require.Equal(t, tc.leaves, leaves, tc.p)
	}

	_, err := leavesDir(app, "loop")
	require.Error(t, err)
}

func TestResolveInDirStaysInDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0o755))
	require.NoError(t, os.Symlink("../other.txt", filepath.Join(app, "up")))

	for _, p := range []string{"..", "../", "../..", ".", "/"} {
		got, err := ResolveInDir(root, "/app", p)
		require.NoError(t, err, p)
		require.Equal(t, app, got, p)
		got, err = ResolveInDirWithoutFinalSymlink(root, "/app", p)
		require.NoError(t, err, p)
		require.Equal(t, app, got, p)
	}

	got, err := ResolveInDir(root, "/app", "up")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(app, "other.txt"), got)
	got, err = ResolveInDirWithoutFinalSymlink(root, "/app", "up")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(app, "up"), got)
}

func TestWithLinksConfined(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("inside"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "inside.txt"), []byte("outside"), 0o644))
	require.NoError(t, os.Symlink("inside.txt", filepath.Join(dir, "within")))

	// No link leaves the directory: the callback gets it as it is.
	require.NoError(t, withLinksConfined(dir, func(p string) error {
		require.Equal(t, dir, p)
		return nil
	}))

	require.NoError(t, os.Symlink("/inside.txt", filepath.Join(dir, "sub", "abs")))
	require.NoError(t, os.Symlink("../../inside.txt", filepath.Join(dir, "sub", "up")))
	require.NoError(t, os.Symlink("abs", filepath.Join(dir, "sub", "chain")))
	require.NoError(t, withLinksConfined(dir, func(p string) error {
		require.NotEqual(t, dir, p)
		for _, name := range []string{"within", "sub/abs", "sub/up", "sub/chain"} {
			got, err := os.ReadFile(filepath.Join(p, name))
			require.NoError(t, err, name)
			require.Equal(t, "inside", string(got), name)
		}
		target, err := os.Readlink(filepath.Join(p, "within"))
		require.NoError(t, err)
		require.Equal(t, "inside.txt", target, "links within the directory are kept as they are")
		return nil
	}))
}
