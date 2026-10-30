package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeStubs creates, under root, each mount's destination the way the
// executor does before it mounts: a directory with its parents, or for a
// file an empty file.
func makeStubs(t *testing.T, root string, dirs []string, files []string) {
	t.Helper()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	for _, f := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, f), nil, 0o755))
	}
}

func TestMountStubsCleaner(t *testing.T) {
	ctx := context.Background()

	t.Run("removes stubs and restores parent times", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "go/bin"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "etc/hosts"), []byte("x"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "etc/resolv.conf"), []byte("x"), 0o644))

		mounts := []Mount{
			{Dest: "/go/pkg/mod/example.com/a@v1.0.0"},
			{Dest: "/go/pkg/mod/example.com/b@v1.0.0"},
			{Dest: "/work/lib"},
			{Dest: "/gobuild/gohdr"},
		}
		cleanup := MountStubsCleaner(ctx, root, mounts, true)
		makeStubs(t, root,
			[]string{"go/pkg/mod/example.com/a@v1.0.0", "go/pkg/mod/example.com/b@v1.0.0", "work/lib"},
			[]string{"gobuild/gohdr"})
		then := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		require.NoError(t, os.Chtimes(filepath.Join(root, "go"), then, then))
		require.NoError(t, os.Chtimes(root, then, then))
		cleanup()

		for _, p := range []string{"go/pkg", "work", "gobuild"} {
			_, err := os.Lstat(filepath.Join(root, p))
			require.ErrorIs(t, err, os.ErrNotExist, p)
		}
		_, err := os.Lstat(filepath.Join(root, "go/bin"))
		require.NoError(t, err)
		for _, p := range []string{root, filepath.Join(root, "go")} {
			st, err := os.Stat(p)
			require.NoError(t, err)
			require.Equal(t, then, st.ModTime().UTC(), p)
		}
	})

	t.Run("keeps what the exec wrote", func(t *testing.T) {
		root := t.TempDir()
		mounts := []Mount{{Dest: "/go/pkg/mod/example.com/a@v1.0.0"}, {Dest: "/out"}}
		cleanup := MountStubsCleaner(ctx, root, mounts, true)
		makeStubs(t, root, []string{"go/pkg/mod/example.com/a@v1.0.0"}, []string{"out"})
		require.NoError(t, os.WriteFile(filepath.Join(root, "go/pkg/built"), []byte("x"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "out"), []byte("x"), 0o644))
		cleanup()

		_, err := os.Lstat(filepath.Join(root, "go/pkg/mod"))
		require.ErrorIs(t, err, os.ErrNotExist)
		for _, p := range []string{"go/pkg/built", "out"} {
			_, err := os.Lstat(filepath.Join(root, p))
			require.NoError(t, err, p)
		}
	})

	t.Run("removes nothing through a symlink", func(t *testing.T) {
		root := t.TempDir()
		mounts := []Mount{{Dest: "/work/lib"}, {Dest: "/stub"}}
		cleanup := MountStubsCleaner(ctx, root, mounts, true)
		makeStubs(t, root, []string{"work/lib", "stub"}, nil)
		// The exec replaces the stubs with symlinks to empty directories of
		// its own.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "elsewhere/lib"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
		require.NoError(t, os.Rename(filepath.Join(root, "work"), filepath.Join(root, "work.old")))
		require.NoError(t, os.Symlink("/elsewhere", filepath.Join(root, "work")))
		require.NoError(t, os.Remove(filepath.Join(root, "stub")))
		require.NoError(t, os.Symlink("/empty", filepath.Join(root, "stub")))
		cleanup()

		for _, p := range []string{"elsewhere/lib", "empty", "work", "stub"} {
			_, err := os.Lstat(filepath.Join(root, p))
			require.NoError(t, err, p)
		}
	})
}
