//go:build linux

package layercopy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeBoundaryTestTree lays out root with a directory app/ whose links lead
// out of it, and an outside.txt next to it that copies within app/ must not
// reach.
func writeBoundaryTestTree(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app", "inner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "app", "inner", "file.txt"), []byte("inside"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.txt"), []byte("outside"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "outside"), 0o755))
	require.NoError(t, os.Symlink("../outside.txt", filepath.Join(root, "app", "up")))
	require.NoError(t, os.Symlink("/outside.txt", filepath.Join(root, "app", "abs")))
	require.NoError(t, os.Symlink("../outside", filepath.Join(root, "app", "updir")))
	require.NoError(t, os.Symlink("inner", filepath.Join(root, "app", "within")))
}

func TestBoundedRootPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeBoundaryTestTree(t, root)
	app := filepath.Join(root, "app")

	for _, tc := range []struct {
		p      string
		follow bool
		want   string
	}{
		{"/app/up", true, filepath.Join(app, "outside.txt")},
		{"/app/abs", true, filepath.Join(app, "outside.txt")},
		{"/app/updir/x", true, filepath.Join(app, "outside", "x")},
		{"/app/within/file.txt", true, filepath.Join(app, "inner", "file.txt")},
		{"/app/up", false, filepath.Join(app, "up")},
		{"/app", true, app},
		{"/", true, root},
	} {
		got, err := BoundedRootPath(root, "/app", tc.p, tc.follow)
		require.NoError(t, err, tc.p)
		require.Equal(t, tc.want, got, tc.p)
	}

	_, err := BoundedRootPath(root, "/app", "/outside.txt", true)
	require.Error(t, err)

	got, err := BoundedRootPath(root, "", "/app/up", true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "outside.txt"), got, "without a boundary, links resolve against the whole root")
}

func TestCopySourceBoundary(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	writeBoundaryTestTree(t, srcRoot)
	dstRoot := t.TempDir()

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	src := Mount{Root: srcRoot, Boundary: "/app"}
	for _, link := range []string{"/app/up", "/app/abs"} {
		err := copier.Copy(context.Background(), src, link, "/out", CopyOptions{CopyDirContents: true, ReplaceExisting: true})
		require.Error(t, err, "%s leads out of the source boundary", link)
	}
	_, err = os.Lstat(filepath.Join(dstRoot, "out"))
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, copier.Copy(context.Background(), src, "/app/within", "/in", CopyOptions{CopyDirContents: true, ReplaceExisting: true}))
	got, err := os.ReadFile(filepath.Join(dstRoot, "in", "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "inside", string(got))
}

func TestCopyDestBoundary(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "new.txt"), []byte("new"), 0o644))
	dstRoot := t.TempDir()
	writeBoundaryTestTree(t, dstRoot)

	copier, err := NewCopier(Mount{Root: dstRoot, Boundary: "/app"})
	require.NoError(t, err)

	// A destination through a link that climbs out of the boundary, to a
	// directory missing inside it, replaces the link with that directory, as
	// an overlay destination does.
	require.NoError(t, copier.CopyFile(context.Background(), Mount{Root: srcRoot}, "/new.txt", "/app/updir/new.txt", CopyOptions{ReplaceExisting: true}))
	// A destination that is itself a link is replaced, not written through.
	require.NoError(t, copier.CopyFile(context.Background(), Mount{Root: srcRoot}, "/new.txt", "/app/up", CopyOptions{ReplaceExisting: true}))
	require.NoError(t, copier.Close())

	got, err := os.ReadFile(filepath.Join(dstRoot, "app", "updir", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
	info, err := os.Lstat(filepath.Join(dstRoot, "app", "updir"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	_, err = os.Lstat(filepath.Join(dstRoot, "outside", "new.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)

	info, err = os.Lstat(filepath.Join(dstRoot, "app", "up"))
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	got, err = os.ReadFile(filepath.Join(dstRoot, "outside.txt"))
	require.NoError(t, err)
	require.Equal(t, "outside", string(got))
}
