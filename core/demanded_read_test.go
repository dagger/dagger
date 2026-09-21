package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// demandedFileContents demands a File's snapshot and path the way
// File.Contents does, then reads the bytes in the test store's snapshot
// directory. File.Contents itself reads through a read-only mount, which a
// unit test must not need.
func demandedFileContents(t *testing.T, ctx context.Context, result dagql.ObjectResult[*File]) []byte {
	t.Helper()
	snapshot, err := result.Self().Snapshot.GetOrEval(ctx, result.Result)
	require.NoError(t, err)
	name, err := result.Self().File.GetOrEval(ctx, result.Result)
	require.NoError(t, err)
	path, err := RootPathWithoutFinalSymlink(testutil.Root(t, snapshot), name)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

// demandedDirectoryEntries is the Directory.Entries counterpart.
func demandedDirectoryEntries(t *testing.T, ctx context.Context, result dagql.ObjectResult[*Directory], sub string) []string {
	t.Helper()
	snapshot, err := result.Self().Snapshot.GetOrEval(ctx, result.Result)
	require.NoError(t, err)
	dir, err := result.Self().Dir.GetOrEval(ctx, result.Result)
	require.NoError(t, err)
	if snapshot == nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(testutil.Root(t, snapshot), dir, sub))
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
