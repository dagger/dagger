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

// directoryDigestTestRead is dir.digest.
func directoryDigestTestRead(t *testing.T, e *transferTestEngine, ctx context.Context, dir dagql.ObjectResult[*core.Directory]) dagql.AnyResult {
	t.Helper()
	var res dagql.AnyResult
	require.NoError(t, e.dag.Select(ctx, dir, &res, dagql.Selector{Field: "digest"}))
	return res
}

func directoryDigestTestValue(t *testing.T, res dagql.AnyResult) string {
	t.Helper()
	value, ok := res.Unwrap().(dagql.String)
	require.True(t, ok, "digest is a String, got %T", res.Unwrap())
	return value.String()
}

// Directory.digest persists its answer, so a later session on the engine hits
// it, and another engine that merges it hits it without evaluating the
// directory: no chain of the directory's is read, even when its part is
// offered. That is a module's implementation digest over its source's context
// directory on a fresh engine, which otherwise puts the whole tree on disk
// only to checksum it.
func TestDirectoryDigestPersists(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a := newTransferTestEngine(t, salt)
	contents := "[module]\n"

	// A session that holds the directory, not its digest, open throughout, so
	// whether the digest is kept rests on its own retention.
	holder := a.session(t, "holder")
	directoryExistsTestDir(t, a, holder, contents)

	a1 := a.session(t, "a1")
	first := directoryDigestTestRead(t, a, a1, directoryExistsTestDir(t, a, a1, contents))
	require.False(t, first.HitCache(), "the first digest runs")
	want := directoryDigestTestValue(t, first)
	require.NotEmpty(t, want)

	// The session ends, as a check's does. The digest is still there for the
	// next session.
	require.NoError(t, a.cache.ReleaseSession(a.base, "a1-session"))
	require.NoError(t, a.cache.WaitSessionRelease(a.base, "a1-session"))
	a2 := a.session(t, "a2")
	dirA := directoryExistsTestDir(t, a, a2, contents)
	second := directoryDigestTestRead(t, a, a2, dirA)
	require.True(t, second.HitCache(), "the digest is kept after its session ends")
	require.Equal(t, want, directoryDigestTestValue(t, second))

	// Another engine merges the digest with the directory's own part offered,
	// and asks for it.
	require.NoError(t, a.cache.Evaluate(a2, dirA))
	b := newTransferTestEngine(t, salt)
	b1 := b.session(t, "b1")
	selection := dagql.ValueSelection{
		Roots:   []dagql.AnyResult{second},
		Outputs: []dagql.SelectedValueOutput{{Result: dirA, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
	}
	require.NoError(t, a.cache.WithExportedValues(a2, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1, "the directory's chain is offered")
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
		b.cache.SetPartContentSource(directoryExistsTestSource{provider})
		reply, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		require.Len(t, reply.Imported(), 1, "the digest lands on the other engine")

		dirB := directoryExistsTestDir(t, b, b1, contents)
		onB := directoryDigestTestRead(t, b, b1, dirB)
		require.True(t, onB.HitCache(), "the other engine hits the merged digest")
		require.Equal(t, want, directoryDigestTestValue(t, onB))
		require.Zero(t, provider.Reads.Load(), "no chain of the directory's is read")
		require.True(t, dagql.HasPendingLazyComputation(dirB), "the directory is never evaluated")
		return nil
	}))
}
