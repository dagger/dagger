package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// directorySyncTestFile is dir.file(path).
func directorySyncTestFile(t *testing.T, e *transferTestEngine, ctx context.Context, dir dagql.ObjectResult[*core.Directory], path string) dagql.ObjectResult[*core.File] {
	t.Helper()
	var file dagql.ObjectResult[*core.File]
	require.NoError(t, e.dag.Select(ctx, dir, &file, dagql.Selector{Field: "file", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString(path)},
	}}))
	return file
}

// directorySyncTestSync is dir.sync.
func directorySyncTestSync(t *testing.T, e *transferTestEngine, ctx context.Context, dir dagql.ObjectResult[*core.Directory]) {
	t.Helper()
	var synced dagql.AnyResult
	require.NoError(t, e.dag.Select(ctx, dir, &synced, dagql.Selector{Field: "sync"}))
}

// A sync of a directory that another engine completed, with no content
// stored, computes nothing: the operation completed there, as a local hit's
// did here. A file then taken from the directory, whose own record didn't
// come, computes the directory and is keyed by its content as on the engine
// that ran it, so calls over the file match that engine's. That is dagger/go's
// package walk on a fresh engine: it syncs each package's output directory,
// then takes the package's archive from it.
func TestDirectorySyncOfACompletedRecordComputesNothing(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	contents := "[workspace]\n"

	a1 := a.session(t, "a1")
	dirA := directoryExistsTestDir(t, a, a1, contents)
	require.NoError(t, a.cache.Evaluate(a1, dirA))
	fileA := directorySyncTestFile(t, a, a1, dirA, "dagger.toml")
	idA, err := fileA.RecipeID(a1)
	require.NoError(t, err)
	require.NotEmpty(t, idA.ContentDigest(), "a file from an evaluated directory is keyed by its content")
	recordsOnly := a.export(t, a1, dirA)

	b1 := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	_, err = b.cache.MergeValues(b1, dagql.CloudCacheID, recordsOnly)
	require.NoError(t, err)
	dirB := directoryExistsTestDir(t, b, b1, contents)
	require.True(t, dirB.HitCache(), "the other engine hits the merged directory")
	directorySyncTestSync(t, b, b1, dirB)
	require.True(t, dagql.HasPendingLazyComputation(dirB), "the sync computed the directory")

	fileB := directorySyncTestFile(t, b, b1, dirB, "dagger.toml")
	require.False(t, fileB.HitCache(), "the file's record didn't come")
	idB, err := fileB.RecipeID(b1)
	require.NoError(t, err)
	require.Equal(t, idA.ContentDigest(), idB.ContentDigest(), "the file is keyed by its content, as on the engine that ran the directory")
	require.False(t, dagql.HasPendingLazyComputation(dirB), "reading the file computed the directory")
}

// A sync of a directory that another engine completed, with its snapshot
// offered, reads none of its content: the snapshot stays in the Cloud until
// something reads it. A file then taken from the directory, whose own record
// didn't come, is read from the downloaded directory and keyed by its content
// as on the engine that stored it.
func TestDirectorySyncLeavesOfferedSnapshotAndFileKeepsContentKey(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a := newTransferTestEngine(t, salt)
	contents := "[workspace]\n"

	a1 := a.session(t, "a1")
	dirA := directoryExistsTestDir(t, a, a1, contents)
	require.NoError(t, a.cache.Evaluate(a1, dirA))
	fileA := directorySyncTestFile(t, a, a1, dirA, "dagger.toml")
	idA, err := fileA.RecipeID(a1)
	require.NoError(t, err)
	require.NotEmpty(t, idA.ContentDigest(), "a file from an evaluated directory is keyed by its content")

	// Another engine merges the directory alone, with its part offered: the
	// file's record stays behind.
	b := newTransferTestEngine(t, salt)
	b1 := b.session(t, "b1")
	selection := dagql.ValueSelection{
		Roots:   []dagql.AnyResult{dirA},
		Outputs: []dagql.SelectedValueOutput{{Result: dirA, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}
	require.NoError(t, a.cache.WithExportedValues(a1, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1, "the directory's chain is offered")
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
		b.cache.SetPartContentSource(directoryExistsTestSource{provider})
		_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)

		dirB := directoryExistsTestDir(t, b, b1, contents)
		require.True(t, dirB.HitCache(), "the other engine hits the merged directory")
		directorySyncTestSync(t, b, b1, dirB)
		require.Zero(t, provider.Reads.Load(), "the sync read none of the directory's chain")

		fileB := directorySyncTestFile(t, b, b1, dirB, "dagger.toml")
		require.False(t, fileB.HitCache(), "the file's record didn't come")
		idB, err := fileB.RecipeID(b1)
		require.NoError(t, err)
		require.Equal(t, idA.ContentDigest(), idB.ContentDigest(), "the file is keyed by its content, as on the engine that stored the directory")
		require.Positive(t, provider.Reads.Load(), "reading the file downloaded the directory")
		return nil
	}))
}

// A sync of a directory whose record another engine sent before running it
// computes the directory, as a sync of a local lazy directory does: nothing
// has shown the operation succeeds.
func TestDirectorySyncOfAPendingRecordComputesIt(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	contents := "[workspace]\n"

	a1 := a.session(t, "a1")
	dirA := directoryExistsTestDir(t, a, a1, contents)
	require.True(t, dagql.HasPendingLazyComputation(dirA), "the directory is still lazy where it's exported")
	pending := a.export(t, a1, dirA)

	b1 := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, pending)
	require.NoError(t, err)
	dirB := directoryExistsTestDir(t, b, b1, contents)
	require.True(t, dirB.HitCache(), "the other engine hits the merged directory")
	directorySyncTestSync(t, b, b1, dirB)
	require.False(t, dagql.HasPendingLazyComputation(dirB), "the sync computed the directory")
}
