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

// containerSyncTestCtr is container().withRootfs(directory().withNewFile("f",
// contents)): a container with an operation of its own, which this test
// engine can compute without running a process.
func containerSyncTestCtr(t *testing.T, e *transferTestEngine, ctx context.Context, contents string) dagql.ObjectResult[*core.Container] {
	t.Helper()
	dirID := rebuildTestID[*core.Directory](t, e, ctx,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("f")},
			{Name: "contents", Value: dagql.NewString(contents)},
		}})
	var ctr dagql.ObjectResult[*core.Container]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &ctr,
		dagql.Selector{Field: "container"},
		dagql.Selector{Field: "withRootfs", Args: []dagql.NamedInput{{Name: "directory", Value: dirID}}}))
	return ctr
}

// containerSyncTestSync is ctr.sync.
func containerSyncTestSync(t *testing.T, e *transferTestEngine, ctx context.Context, ctr dagql.ObjectResult[*core.Container]) {
	t.Helper()
	var synced dagql.AnyResult
	require.NoError(t, e.dag.Select(ctx, ctr, &synced, dagql.Selector{Field: "sync"}))
}

// containerSyncTestRead is ctr.rootfs.file("f").contents.
func containerSyncTestRead(t *testing.T, e *transferTestEngine, ctx context.Context, ctr dagql.ObjectResult[*core.Container]) string {
	t.Helper()
	var contents dagql.String
	require.NoError(t, e.dag.Select(ctx, ctr, &contents,
		dagql.Selector{Field: "rootfs"},
		dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("f")}}},
		dagql.Selector{Field: "contents"}))
	return string(contents)
}

// A sync of a container that another engine completed, with no content
// stored, computes nothing: the operation completed there, as a local hit's
// did here. That is a module syncing an exec to check that it succeeded,
// whose outputs its pipeline then reads through results of their own: a
// fresh engine ran the exec again only to answer the sync. A read of the
// container afterwards computes what it reads. A record of the container sent
// on from there still says its operation completed, so a third engine's sync
// computes nothing either.
func TestContainerSyncOfACompletedRecordComputesNothing(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	contents := "built\n"

	a1 := a.session(t, "a1")
	ctrA := containerSyncTestCtr(t, a, a1, contents)
	require.NoError(t, a.cache.Evaluate(a1, ctrA))
	require.False(t, dagql.HasPendingLazyComputation(ctrA), "the container completed where it's exported")
	recordsOnly := a.export(t, a1, ctrA)

	b1 := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, recordsOnly)
	require.NoError(t, err)
	ctrB := containerSyncTestCtr(t, b, b1, contents)
	require.True(t, ctrB.HitCache(), "the other engine hits the merged container")
	containerSyncTestSync(t, b, b1, ctrB)
	require.True(t, dagql.HasPendingLazyComputation(ctrB), "the sync computed the container")

	// The second engine's own record of the container, which it never
	// computed, still says the operation completed.
	c := newTransferTestEngine(t, salt)
	c1 := dagql.ContextWithServer(c.session(t, "c1"), c.dag)
	_, err = c.cache.MergeValues(c1, dagql.CloudCacheID, b.export(t, b1, ctrB))
	require.NoError(t, err)
	ctrC := containerSyncTestCtr(t, c, c1, contents)
	require.True(t, ctrC.HitCache(), "the third engine hits the second engine's record")
	containerSyncTestSync(t, c, c1, ctrC)
	require.True(t, dagql.HasPendingLazyComputation(ctrC), "the third engine's sync computed the container")

	require.Equal(t, contents, containerSyncTestRead(t, b, b1, ctrB), "the read computes what it reads")
	require.Equal(t, contents, containerSyncTestRead(t, c, c1, ctrC), "the third engine's read computes what it reads")
}

// A sync of a container that another engine completed, with its filesystem
// offered, reads none of it: the content stays in the Cloud until something
// reads it. A read of a file from it afterwards downloads it. A record of the
// container sent on from there still says its operation completed.
func TestContainerSyncLeavesOfferedPartsToTheirReader(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a := newTransferTestEngine(t, salt)
	contents := "offered\n"

	a1 := a.session(t, "a1")
	ctrA := containerSyncTestCtr(t, a, a1, contents)
	require.NoError(t, a.cache.Evaluate(a1, ctrA))

	b := newTransferTestEngine(t, salt)
	b1 := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	selection := dagql.ValueSelection{
		Roots:   []dagql.AnyResult{ctrA},
		Outputs: []dagql.SelectedValueOutput{{Result: ctrA, Address: dagql.PersistedPartAddress{Part: core.ContainerPartFS}}},
	}
	require.NoError(t, a.cache.WithExportedValues(a1, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.NotEmpty(t, exported.Chains.Entries, "the container's filesystem is offered")
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
		b.cache.SetPartContentSource(directoryExistsTestSource{provider})
		_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)

		ctrB := containerSyncTestCtr(t, b, b1, contents)
		require.True(t, ctrB.HitCache(), "the other engine hits the merged container")
		containerSyncTestSync(t, b, b1, ctrB)
		require.Zero(t, provider.Reads.Load(), "the sync read none of the container's filesystem")
		require.True(t, dagql.HasPendingLazyComputation(ctrB), "the sync installed the container")

		c := newTransferTestEngine(t, salt)
		c1 := dagql.ContextWithServer(c.session(t, "c1"), c.dag)
		_, err = c.cache.MergeValues(c1, dagql.CloudCacheID, b.export(t, b1, ctrB))
		require.NoError(t, err)
		ctrC := containerSyncTestCtr(t, c, c1, contents)
		require.True(t, ctrC.HitCache(), "the third engine hits the second engine's record")
		containerSyncTestSync(t, c, c1, ctrC)
		require.True(t, dagql.HasPendingLazyComputation(ctrC), "the third engine's sync computed the container")

		require.Equal(t, contents, containerSyncTestRead(t, b, b1, ctrB), "the read is the container's file")
		require.Positive(t, provider.Reads.Load(), "reading the file downloaded the container's filesystem")
		return nil
	}))
}

// A sync of a container whose record another engine sent before running it
// computes the container, as a sync of a local lazy container does: nothing
// has shown the operation succeeds.
func TestContainerSyncOfAPendingRecordComputesIt(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	contents := "pending\n"

	a1 := a.session(t, "a1")
	ctrA := containerSyncTestCtr(t, a, a1, contents)
	require.True(t, dagql.HasPendingLazyComputation(ctrA), "the container is still lazy where it's exported")
	pending := a.export(t, a1, ctrA)

	b1 := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, pending)
	require.NoError(t, err)
	ctrB := containerSyncTestCtr(t, b, b1, contents)
	require.True(t, ctrB.HitCache(), "the other engine hits the merged container")
	containerSyncTestSync(t, b, b1, ctrB)
	require.False(t, dagql.HasPendingLazyComputation(ctrB), "the sync computed the container")
}
