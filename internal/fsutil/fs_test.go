package fsutil

import (
	"context"
	gofs "io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil/types"
)

func TestWalk(t *testing.T) {
	tmpDir := t.TempDir()

	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateFile("dir/foo", []byte("contents"), 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))

	f, err := NewFS(tmpDir)
	require.NoError(t, err)
	paths := []string{}
	files := []gofs.DirEntry{}
	err = f.Walk(context.TODO(), "", func(path string, entry gofs.DirEntry, err error) error {
		require.NoError(t, err)
		paths = append(paths, path)
		files = append(files, entry)
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{"dir", filepath.FromSlash("dir/foo")}, paths)
	require.Len(t, files, 2)
	require.Equal(t, "dir", files[0].Name())
	require.Equal(t, "foo", files[1].Name())

	fis := []gofs.FileInfo{}
	for _, f := range files {
		fi, err := f.Info()
		require.NoError(t, err)
		fis = append(fis, fi)
	}
	require.Equal(t, "dir", fis[0].Name())
	require.Equal(t, "foo", fis[1].Name())

	require.Equal(t, len("contents"), int(fis[1].Size()))

	require.Equal(t, "dir", fis[0].(*StatInfo).Path)
	require.Equal(t, filepath.FromSlash("dir/foo"), fis[1].(*StatInfo).Path)
}

func TestWalkDir(t *testing.T) {
	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateFile("dir/foo", []byte("contents"), 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	tmpDir2 := t.TempDir()
	apply = fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateFile("dir/bar", []byte("contents2"), 0600),
	)
	require.NoError(t, apply.Apply(tmpDir2))
	tmpfs2, err := NewFS(tmpDir2)
	require.NoError(t, err)

	f, err := SubDirFS([]Dir{
		{
			Stat: types.Stat{
				Mode: uint32(os.ModeDir | 0755),
				Path: "1",
			},
			FS: tmpfs,
		},
		{
			Stat: types.Stat{
				Mode: uint32(os.ModeDir | 0755),
				Path: "2",
			},
			FS: tmpfs2,
		},
	})
	require.NoError(t, err)
	paths := []string{}
	files := []gofs.DirEntry{}
	err = f.Walk(context.TODO(), "", func(path string, entry gofs.DirEntry, err error) error {
		require.NoError(t, err)
		paths = append(paths, path)
		files = append(files, entry)
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{"1", filepath.FromSlash("1/dir"), filepath.FromSlash("1/dir/foo"), "2", filepath.FromSlash("2/dir"), filepath.FromSlash("2/dir/bar")}, paths)
	require.Equal(t, "1", files[0].Name())
	require.Equal(t, "dir", files[1].Name())
	require.Equal(t, "foo", files[2].Name())
}
