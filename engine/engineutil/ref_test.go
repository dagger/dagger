//go:build !windows

package engineutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/stretchr/testify/require"
)

// dirMount is a writable bind mount of a directory, which a local mounter
// uses in place, without mounting anything.
type dirMount string

func (dir dirMount) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{Type: "bind", Source: string(dir), Options: []string{"rbind"}}}, func() error { return nil }, nil
}

// failedMount is a mount that fails.
type failedMount struct{}

func (failedMount) Mount() ([]mount.Mount, func() error, error) {
	return nil, nil, errors.New("mount failed")
}

// TestReadSnapshotSmallFile covers what a process can leave at the path,
// none of which but a small regular file is read, and the engine's own
// failures to look, which are errors.
func TestReadSnapshotSmallFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small"), []byte("report"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big"), []byte(strings.Repeat("x", 11)), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "dir"), 0o700))
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600))
	require.NoError(t, os.Symlink("small", filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink("loop", filepath.Join(dir, "loop")))
	require.NoError(t, os.Symlink("small/x", filepath.Join(dir, "through-file")))

	for _, tc := range []struct {
		name, path string
		want       []byte
	}{
		{"a small regular file", "small", []byte("report")},
		{"no file", "missing", nil},
		{"in no directory", "missing/file", nil},
		{"over the limit", "big", nil},
		{"a directory", "dir", nil},
		{"a named pipe", "fifo", nil},
		{"a symlink to a regular file", "link", nil},
		{"a symlink loop", "loop", nil},
		{"a symlink through a regular file", "through-file", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadSnapshotSmallFile(t.Context(), dirMount(dir), tc.path, 10)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("a stat that fails otherwise", func(t *testing.T) {
		// A path beneath a regular file: ENOTDIR, not ENOENT. The directories
		// above the file are the engine's, not the process's.
		_, err := ReadSnapshotSmallFile(t.Context(), dirMount(dir), "small/file", 10)
		require.ErrorIs(t, err, syscall.ENOTDIR)
	})

	t.Run("a mount that fails", func(t *testing.T) {
		_, err := ReadSnapshotSmallFile(t.Context(), failedMount{}, "small", 10)
		require.ErrorContains(t, err, "mount failed")
	})
}
