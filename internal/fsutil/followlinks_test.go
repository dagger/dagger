package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/stretchr/testify/require"
)

func TestFollowLinks(t *testing.T) {
	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateFile("dir/foo", []byte("contents"), 0600),
		fstest.Symlink("foo", "dir/l1"),
		fstest.Symlink("dir/l1", "l2"),
		fstest.CreateFile("bar", nil, 0600),
		fstest.CreateFile("baz", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	out, err := FollowLinks(tmpfs, []string{"l2", "bar"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"bar", "dir/foo", "dir/l1", "l2"})
}

func TestFollowLinksLoop(t *testing.T) {
	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.Symlink("l1", "l1"),
		fstest.Symlink("l2", "l3"),
		fstest.Symlink("l3", "l2"),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	out, err := FollowLinks(tmpfs, []string{"l1", "l3"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"l1", "l2", "l3"})
}

func TestFollowLinksAbsolute(t *testing.T) {
	abslutePathForBaz := "/foo/bar/baz"
	if runtime.GOOS == "windows" {
		abslutePathForBaz = "C:/foo/bar/baz"
	}

	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.Symlink(abslutePathForBaz, "dir/l1"),
		fstest.CreateDir("foo", 0700),
		fstest.Symlink("../", "foo/bar"),
		fstest.CreateFile("baz", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	out, err := FollowLinks(tmpfs, []string{"dir/l1"})
	require.NoError(t, err)

	require.Equal(t, []string{"baz", "dir/l1", "foo/bar"}, out)

	// same but a link outside root
	tmpDir = t.TempDir()
	apply = fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.Symlink(abslutePathForBaz, "dir/l1"),
		fstest.CreateDir("foo", 0700),
		fstest.Symlink("../../../", "foo/bar"),
		fstest.CreateFile("baz", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err = NewFS(tmpDir)
	require.NoError(t, err)

	out, err = FollowLinks(tmpfs, []string{"dir/l1"})
	require.NoError(t, err)

	require.Equal(t, []string{"baz", "dir/l1", "foo/bar"}, out)
}

func TestFollowLinksNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	out, err := FollowLinks(tmpfs, []string{"foo/bar/baz", "bar/baz"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"bar/baz", "foo/bar/baz"})

	// root works fine with empty directory
	out, err = FollowLinks(tmpfs, []string{"."})
	require.NoError(t, err)

	require.Equal(t, out, []string(nil))

	out, err = FollowLinks(tmpfs, []string{"f*/foo/t*"})
	require.NoError(t, err)

	require.Equal(t, []string{"f*/foo/t*"}, out)
}

func TestFollowLinksNormalized(t *testing.T) {
	tmpDir := t.TempDir()
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	out, err := FollowLinks(tmpfs, []string{"foo/bar/baz", "foo/bar"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"foo/bar"})

	rootPath := "/"
	if runtime.GOOS == "windows" {
		rootPath = "C:/"
	}

	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.Symlink(filepath.Join(rootPath, "foo"), "dir/l1"),
		fstest.Symlink(rootPath, "dir/l2"),
		fstest.CreateDir("foo", 0700),
		fstest.CreateFile("foo/bar", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))

	out, err = FollowLinks(tmpfs, []string{"dir/l1", "foo/bar"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"dir/l1", "foo"})

	out, err = FollowLinks(tmpfs, []string{"dir/l2", "foo", "foo/bar"})
	require.NoError(t, err)

	require.Equal(t, out, []string(nil))
}

func TestFollowLinksWildcard(t *testing.T) {
	absolutePathForFoo := "/foo"
	if runtime.GOOS == "windows" {
		absolutePathForFoo = "C:/foo"
	}

	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateDir("foo", 0700),
		fstest.Symlink(filepath.Join(absolutePathForFoo, "bar1"), "dir/l1"),
		fstest.Symlink(filepath.Join(absolutePathForFoo, "bar2"), "dir/l2"),
		fstest.Symlink(filepath.Join(absolutePathForFoo, "bar3"), "dir/anotherlink"),
		fstest.Symlink("../baz", "foo/bar2"),
		fstest.CreateFile("foo/bar1", nil, 0600),
		fstest.CreateFile("foo/bar3", nil, 0600),
		fstest.CreateFile("baz", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	out, err := FollowLinks(tmpfs, []string{"dir/l*"})
	require.NoError(t, err)

	require.Equal(t, []string{"baz", "dir/l*", "foo/bar1", "foo/bar2"}, out)

	out, err = FollowLinks(tmpfs, []string{"dir"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"dir"})

	out, err = FollowLinks(tmpfs, []string{"dir", "dir/*link"})
	require.NoError(t, err)

	require.Equal(t, out, []string{"dir", "foo/bar3"})
}

func TestInternalReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateFile("dir/foo1", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	entry, err := statFile(tmpfs, "/")
	require.NoError(t, err)
	require.Nil(t, entry)
	entry, err = statFile(tmpfs, "")
	require.NoError(t, err)
	require.Nil(t, entry)

	entry, err = statFile(tmpfs, "dir")
	require.NoError(t, err)
	require.Equal(t, "dir", entry.Name())
	require.True(t, entry.Type().IsDir())
	entry, err = statFile(tmpfs, "dir/foo1")
	require.NoError(t, err)
	require.Equal(t, "foo1", entry.Name())
	require.False(t, entry.Type().IsDir())

	entry, err = statFile(tmpfs, "dir/foo2")
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Nil(t, entry)
	entry, err = statFile(tmpfs, "dir/x/y/z")
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Nil(t, entry)
}

func TestInternalReadDir(t *testing.T) {
	tmpDir := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("dir", 0700),
		fstest.CreateFile("dir/foo1", nil, 0600),
	)
	require.NoError(t, apply.Apply(tmpDir))
	tmpfs, err := NewFS(tmpDir)
	require.NoError(t, err)

	entries, err := readDir(tmpfs, "/")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "dir", entries[0].Name())
	require.True(t, entries[0].IsDir())

	entries, err = readDir(tmpfs, "dir")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "foo1", entries[0].Name())

	entries, err = readDir(tmpfs, "dir2")
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Empty(t, entries)

	entries, err = readDir(tmpfs, "dir/foo1")
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Empty(t, entries)
}
