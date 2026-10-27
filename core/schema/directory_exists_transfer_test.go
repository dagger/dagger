package schema

import (
	"context"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// directoryExistsTestSource serves every offered chain from one provider.
type directoryExistsTestSource struct{ provider content.InfoReaderProvider }

func (directoryExistsTestSource) Available(dagql.PersistedPartOffer, time.Time) bool { return true }

func (s directoryExistsTestSource) Provider(context.Context, dagql.PersistedPartOffer, *dagql.PartDemandState) content.InfoReaderProvider {
	return s.provider
}

// directoryExistsTestDir is directory().withNewFile("dagger.toml", contents).
func directoryExistsTestDir(t *testing.T, e *transferTestEngine, ctx context.Context, contents string) dagql.ObjectResult[*core.Directory] {
	t.Helper()
	var dir dagql.ObjectResult[*core.Directory]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &dir,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("dagger.toml")},
			{Name: "contents", Value: dagql.NewString(contents)},
		}}))
	return dir
}

// directoryExistsTestCheck is dir.exists(path).
func directoryExistsTestCheck(t *testing.T, e *transferTestEngine, ctx context.Context, dir dagql.ObjectResult[*core.Directory], path string) dagql.AnyResult {
	t.Helper()
	var res dagql.AnyResult
	require.NoError(t, e.dag.Select(ctx, dir, &res, dagql.Selector{Field: "exists", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString(path)},
	}}))
	return res
}

func directoryExistsTestValue(t *testing.T, res dagql.AnyResult) bool {
	t.Helper()
	value, ok := res.Unwrap().(dagql.Boolean)
	require.True(t, ok, "exists is a Boolean, got %T", res.Unwrap())
	return bool(value)
}

// Directory.exists persists its answer, so a later session on the engine hits
// it, and another engine that merges it hits it without evaluating the
// directory: no chain of the directory's is read, even when its part is
// offered. That is workspace detection asking whether dagger.toml exists in a
// workspace's git tree on a fresh engine, which otherwise checks the whole
// tree out to answer. An answer that the path is missing persists the same
// way.
func TestDirectoryExistsPersists(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{name: "a path that exists", path: "dagger.toml", want: true},
		{name: "a missing path", path: "dagger.lock", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			salt := transferTestSalt(t)
			a := newTransferTestEngine(t, salt)
			contents := "[workspace]\n"

			// A session that holds the directory, not the answer, open
			// throughout, so whether the answer is kept rests on its own
			// retention.
			holder := a.session(t, "holder")
			directoryExistsTestDir(t, a, holder, contents)

			a1 := a.session(t, "a1")
			first := directoryExistsTestCheck(t, a, a1, directoryExistsTestDir(t, a, a1, contents), tc.path)
			require.False(t, first.HitCache(), "the first check runs")
			require.Equal(t, tc.want, directoryExistsTestValue(t, first))

			// The session ends, as a check's does. The answer is still there
			// for the next session.
			require.NoError(t, a.cache.ReleaseSession(a.base, "a1-session"))
			require.NoError(t, a.cache.WaitSessionRelease(a.base, "a1-session"))
			a2 := a.session(t, "a2")
			dirA := directoryExistsTestDir(t, a, a2, contents)
			second := directoryExistsTestCheck(t, a, a2, dirA, tc.path)
			require.True(t, second.HitCache(), "the answer is kept after its session ends")
			require.Equal(t, tc.want, directoryExistsTestValue(t, second))

			// Another engine merges the answer with the directory's own part
			// offered, and asks the same question.
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
				require.Len(t, reply.Imported(), 1, "the answer lands on the other engine")

				dirB := directoryExistsTestDir(t, b, b1, contents)
				onB := directoryExistsTestCheck(t, b, b1, dirB, tc.path)
				require.True(t, onB.HitCache(), "the other engine hits the merged answer")
				require.Equal(t, tc.want, directoryExistsTestValue(t, onB))
				require.Zero(t, provider.Reads.Load(), "no chain of the directory's is read")
				require.True(t, dagql.HasPendingLazyComputation(dirB), "the directory is never evaluated")
				return nil
			}))
		})
	}
}
