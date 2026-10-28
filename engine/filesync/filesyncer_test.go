package filesync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/client"
)

// The parent sync recreates a target directory's parents before the target
// is synced. It must read the client's filesystem along that parent chain and
// nowhere else, whatever characters the parents' names hold: it used to pass
// the target path as a glob pattern, and a pattern character in it sent the
// client walking its whole filesystem from /, where an entry it couldn't read
// (a /proc entry, in CI) failed the upload.
func TestSyncParentDirsReadsOnlyTheParentChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	root := t.TempDir()
	target := filepath.Join(root, "a^b", "[x]", "target")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "file.txt"), []byte("content"), 0o600))
	// Directories the client can't walk: one beside the parent chain, and one
	// in the target, whose contents the parent sync must not read either.
	makeUnwalkableDir(t, filepath.Join(root, "a^b", "beside"))
	makeUnwalkableDir(t, filepath.Join(target, "inside"))

	mirror := t.TempDir()
	syncer := &FileSyncer{}
	conn := serveFilesync(t, client.FilesyncSource{})
	require.NoError(t, syncer.syncParentDirs(ctx, NewMirrorSharedState(mirror), conn, target, "", SnapshotOpts{}))

	for dir := target; dir != "/"; dir = filepath.Dir(dir) {
		info, err := os.Lstat(filepath.Join(mirror, dir))
		require.NoError(t, err)
		require.True(t, info.IsDir(), dir)
	}
	entries, err := os.ReadDir(filepath.Join(mirror, target))
	require.NoError(t, err)
	require.Empty(t, entries, "the parent sync copied the target's contents")
}

// makeUnwalkableDir creates dir with a chain of subdirectories beneath it
// whose path is longer than PATH_MAX, so that walking it fails with
// ENAMETOOLONG: not a permission error, which a walk would skip.
func makeUnwalkableDir(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.Mkdir(dir, 0o755))
	fd, err := unix.Open(dir, unix.O_DIRECTORY|unix.O_RDONLY, 0)
	require.NoError(t, err)
	name := strings.Repeat("d", 255)
	for depth := len(dir); depth <= unix.PathMax; depth += len(name) + 1 {
		require.NoError(t, unix.Mkdirat(fd, name, 0o755))
		next, err := unix.Openat(fd, name, unix.O_DIRECTORY|unix.O_RDONLY, 0)
		require.NoError(t, err)
		require.NoError(t, unix.Close(fd))
		fd = next
	}
	require.NoError(t, unix.Close(fd))
}
