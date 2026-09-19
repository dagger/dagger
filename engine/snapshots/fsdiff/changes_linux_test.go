//go:build linux
// +build linux

package fsdiff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	continuityfs "github.com/containerd/continuity/fs"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestWalkChangesComparisonModes(t *testing.T) {
	lower := t.TempDir()
	upper := t.TempDir()

	writeFile(t, lower, "level1/file.txt", "level1 original")
	writeFile(t, upper, "level1/file.txt", "level1 modified")
	mt := time.Unix(1775443663, 799549393)
	require.NoError(t, os.Chtimes(filepath.Join(lower, "level1/file.txt"), mt, mt))
	require.NoError(t, os.Chtimes(filepath.Join(upper, "level1/file.txt"), mt, mt))

	var compat []string
	require.NoError(t, WalkChanges(context.Background(), lower, upper, CompareCompat, collectPaths(&compat)))
	require.NotContains(t, compat, "/level1/file.txt")

	var contentAware []string
	require.NoError(t, WalkChanges(context.Background(), lower, upper, CompareContentOnMetadataMatch, collectPaths(&contentAware)))
	require.Contains(t, contentAware, "/level1/file.txt")
}

func TestWalkChangesContentModeSymlinkTarget(t *testing.T) {
	lower := t.TempDir()
	upper := t.TempDir()

	writeSymlink(t, lower, "link", "one")
	writeSymlink(t, upper, "link", "two")
	mt := time.Unix(1775443663, 799549393)
	setSymlinkTimes(t, filepath.Join(lower, "link"), mt)
	setSymlinkTimes(t, filepath.Join(upper, "link"), mt)

	var contentAware []string
	require.NoError(t, WalkChanges(context.Background(), lower, upper, CompareContentOnMetadataMatch, collectPaths(&contentAware)))
	require.Contains(t, contentAware, "/link")
}

func TestWalkChangesContentModeZeroLengthFile(t *testing.T) {
	lower := t.TempDir()
	upper := t.TempDir()

	writeFile(t, lower, "zero", "")
	writeFile(t, upper, "zero", "")
	mt := time.Unix(1775443663, 799549393)
	require.NoError(t, os.Chtimes(filepath.Join(lower, "zero"), mt, mt))
	require.NoError(t, os.Chtimes(filepath.Join(upper, "zero"), mt, mt))

	var contentAware []string
	require.NoError(t, WalkChanges(context.Background(), lower, upper, CompareContentOnMetadataMatch, collectPaths(&contentAware)))
	require.NotContains(t, contentAware, "/zero")
}

func TestInodeOnlyComparisonDoesNotReadContent(t *testing.T) {
	lower := filepath.Join(t.TempDir(), "file")
	upper := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(lower, []byte("same"), 0o644))
	require.NoError(t, os.WriteFile(upper, []byte("same"), 0o644))
	mt := time.Unix(1775443663, 799549393)
	require.NoError(t, os.Chtimes(lower, mt, mt))
	require.NoError(t, os.Chtimes(upper, mt, mt))
	f1, err := os.Stat(lower)
	require.NoError(t, err)
	f2, err := os.Stat(upper)
	require.NoError(t, err)
	// Keep only stat data: attempting to inspect either path now fails.
	require.NoError(t, os.Remove(lower))
	require.NoError(t, os.Remove(upper))
	same, err := samePathInfo(f1, f2, lower, upper, CompareInodeOnly)
	require.NoError(t, err)
	require.False(t, same, "distinct inodes count conservatively even with identical metadata")
	same, err = samePathInfo(f1, f1, lower, lower, CompareInodeOnly)
	require.NoError(t, err)
	require.True(t, same, "shared backing files need no content inspection")
}

func collectPaths(paths *[]string) continuityfs.ChangeFunc {
	return func(kind continuityfs.ChangeKind, path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if kind != continuityfs.ChangeKindUnmodified {
			*paths = append(*paths, path)
		}
		return nil
	}
}

func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	full := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
}

func writeSymlink(t *testing.T, root, rel, target string) {
	t.Helper()
	full := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.Symlink(target, full))
}

func setSymlinkTimes(t *testing.T, path string, tm time.Time) {
	t.Helper()
	ts := unix.NsecToTimespec(tm.UnixNano())
	require.NoError(t, unix.UtimesNanoAt(unix.AT_FDCWD, path, []unix.Timespec{ts, ts}, unix.AT_SYMLINK_NOFOLLOW))
}
