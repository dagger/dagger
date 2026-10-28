package snapshots_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

const (
	exportFilePasswd = "auser:x:1234:4321::/home/auser:/bin/sh\n"
	exportFileGroup  = "agroup:x:4321:\n"
)

// writeExportFile writes name under root as testutil.Build does, so
// testutil.CheckFile can check its mode and time.
func writeExportFile(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(value), 0640))
	require.NoError(t, os.Chtimes(path, testutil.FileTime, testutil.FileTime))
}

// A file's chain is one layer holding the file, under parents with the
// source's metadata, and the account databases a named chown of it reads:
// none of the rest of the filesystem it came from.
func TestExportFileChainHoldsTheFileAlone(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	base, _ := producer.BuildWith(t, nil, func(root string) {
		writeExportFile(t, root, "etc/passwd", exportFilePasswd)
		writeExportFile(t, root, "etc/group", exportFileGroup)
		writeExportFile(t, root, "src/output/keep.txt", "sibling bytes")
	})
	tree, _ := producer.BuildWith(t, base, func(root string) {
		writeExportFile(t, root, "src/big.bin", strings.Repeat("x", 1<<20))
		writeExportFile(t, root, "src/go.mod", "module example\n")
		require.NoError(t, os.Chmod(filepath.Join(root, "src"), 0750))
		require.NoError(t, os.Chtimes(filepath.Join(root, "src"), testutil.FileTime, testutil.FileTime))
	})
	require.Len(t, exportChain(t, tree).Layers, 2)

	chain, err := tree.ExportFileChain(context.Background(), transferConfig, "/src/go.mod")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	require.Len(t, chain.Layers, 1)

	imported := importChain(t, consumer, supplied(chain, chain.Provider))
	testutil.CheckFile(t, imported, "src/go.mod", "module example\n")
	testutil.CheckFile(t, imported, "etc/passwd", exportFilePasswd)
	testutil.CheckFile(t, imported, "etc/group", exportFileGroup)
	root := testutil.Root(t, imported)
	src, err := os.Stat(filepath.Join(root, "src"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0750), src.Mode().Perm())
	require.Equal(t, testutil.FileTime, src.ModTime().UTC())
	require.NoFileExists(t, filepath.Join(root, "src/big.bin"))
	require.NoDirExists(t, filepath.Join(root, "src/output"))
}

// A file reached through a symlink keeps its whole chain.
func TestExportFileChainThroughASymlinkIsWhole(t *testing.T) {
	store := testutil.NewStore(t)
	base, _ := store.BuildWith(t, nil, func(root string) {
		writeExportFile(t, root, "real/go.mod", "module example\n")
		require.NoError(t, os.Symlink("real", filepath.Join(root, "src")))
	})
	tree, _ := store.Build(t, base, "other.txt", "other bytes")

	chain, err := tree.ExportFileChain(context.Background(), transferConfig, "/src/go.mod")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
	require.Len(t, chain.Layers, 2)
}

// A file that travels as its own content reports its own size. One reached
// through a symlink travels as its whole chain, and reports none.
func TestOwnFileSize(t *testing.T) {
	store := testutil.NewStore(t)
	sizer, ok := store.Manager.(interface {
		OwnFileSize(ctx context.Context, snapshotID, filePath string) (int64, bool, error)
	})
	require.True(t, ok, "the snapshot manager reports own file sizes")
	tree, _ := store.BuildWith(t, nil, func(root string) {
		writeExportFile(t, root, "src/go.mod", "module example\n")
		writeExportFile(t, root, "real/big.bin", strings.Repeat("x", 1<<20))
		require.NoError(t, os.Symlink("real", filepath.Join(root, "link")))
	})
	size, own, err := sizer.OwnFileSize(context.Background(), tree.SnapshotID(), "/src/go.mod")
	require.NoError(t, err)
	require.True(t, own)
	require.EqualValues(t, len("module example\n"), size)
	_, own, err = sizer.OwnFileSize(context.Background(), tree.SnapshotID(), "/link/big.bin")
	require.NoError(t, err)
	require.False(t, own, "a file reached through a symlink travels as its chain")
}
