package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeTrashFixtureTree(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "snapshots", "1", "fs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "content", "blobs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "snapshots", "1", "fs", "data"), []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "content", "blobs", "blob"), []byte("blob"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "metadata.db"), []byte("db"), 0o644))
	require.NoError(t, os.Symlink("snapshots", filepath.Join(dir, "link")))
}

func TestMoveLocalCacheStateToTrash(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	workerDir := filepath.Join(rootDir, "worker")
	writeTrashFixtureTree(t, workerDir)

	trashDir, err := moveLocalCacheStateToTrash(workerDir)
	require.NoError(t, err)
	require.NotEmpty(t, trashDir)
	require.Equal(t, rootDir, filepath.Dir(trashDir))
	require.True(t, len(filepath.Base(trashDir)) > len(workerTrashPrefix))
	require.Contains(t, filepath.Base(trashDir), workerTrashPrefix)

	// the worker dir is gone, its content moved under the trash dir
	require.NoDirExists(t, workerDir)
	require.FileExists(t, filepath.Join(trashDir, "snapshots", "1", "fs", "data"))
	require.FileExists(t, filepath.Join(trashDir, "metadata.db"))

	// a missing worker dir is a no-op
	trashDir, err = moveLocalCacheStateToTrash(workerDir)
	require.NoError(t, err)
	require.Empty(t, trashDir)
}

func TestFindLocalCacheTrashDirs(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootDir, "worker"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(rootDir, workerTrashPrefix+"1"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(rootDir, workerTrashPrefix+"2"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "dagql-cache.db"), []byte("db"), 0o644))

	trashDirs, err := findLocalCacheTrashDirs(rootDir)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		filepath.Join(rootDir, workerTrashPrefix+"1"),
		filepath.Join(rootDir, workerTrashPrefix+"2"),
	}, trashDirs)

	// a missing root dir is a no-op, not an error
	trashDirs, err = findLocalCacheTrashDirs(filepath.Join(rootDir, "does-not-exist"))
	require.NoError(t, err)
	require.Empty(t, trashDirs)
}

func TestRemoveAllPaced(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	trashDir := filepath.Join(rootDir, workerTrashPrefix+"1")
	writeTrashFixtureTree(t, trashDir)

	// a tiny work slice forces the pacer through its rest path as well
	pacer := &trashPacer{workSlice: time.Nanosecond, restSlice: time.Nanosecond}
	require.NoError(t, removeAllPaced(context.Background(), trashDir, pacer))
	require.NoDirExists(t, trashDir)

	// removing an already-removed tree is a no-op
	require.NoError(t, removeAllPaced(context.Background(), trashDir, pacer))
}

func TestRemoveAllPacedManyEntries(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	trashDir := filepath.Join(rootDir, workerTrashPrefix+"1")
	// more entries than one readDirnamesBatch pass covers
	require.NoError(t, os.MkdirAll(trashDir, 0o755))
	for i := range trashSweepBatch + 100 {
		require.NoError(t, os.WriteFile(filepath.Join(trashDir, "f"+strconv.Itoa(i)), nil, 0o644))
	}

	require.NoError(t, removeAllPaced(context.Background(), trashDir, &trashPacer{}))
	require.NoDirExists(t, trashDir)
}

func TestRemoveAllPacedCanceled(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	trashDir := filepath.Join(rootDir, workerTrashPrefix+"1")
	writeTrashFixtureTree(t, trashDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := removeAllPaced(ctx, trashDir, &trashPacer{})
	require.ErrorIs(t, err, context.Canceled)
	// the tree survives (partially or fully) for the next startup's sweep
	require.DirExists(t, trashDir)
}

func TestSweepLocalCacheTrash(t *testing.T) {
	t.Parallel()
	rootDir := t.TempDir()
	srv := &Server{rootDir: rootDir}

	trashA := filepath.Join(rootDir, workerTrashPrefix+"a")
	trashB := filepath.Join(rootDir, workerTrashPrefix+"b")
	writeTrashFixtureTree(t, trashA)
	writeTrashFixtureTree(t, trashB)
	keep := filepath.Join(rootDir, "worker")
	writeTrashFixtureTree(t, keep)

	trashDirs, err := findLocalCacheTrashDirs(rootDir)
	require.NoError(t, err)
	srv.sweepLocalCacheTrash(context.Background(), trashDirs)

	require.NoDirExists(t, trashA)
	require.NoDirExists(t, trashB)
	require.DirExists(t, keep)
}
