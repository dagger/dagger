package schema

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// localRerunContainer is
// container().withRootfs(directory()).withUser("u").withLabel("l", "v").withDefaultArgs(["x"]).
func localRerunContainer(t *testing.T, e *transferTestEngine, ctx context.Context) dagql.ObjectResult[*core.Container] {
	t.Helper()
	dirID := rebuildTestID[*core.Directory](t, e, ctx, dagql.Selector{Field: "directory"})
	var ctr dagql.ObjectResult[*core.Container]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &ctr,
		dagql.Selector{Field: "container"},
		dagql.Selector{Field: "withRootfs", Args: []dagql.NamedInput{rebuildTestArg("directory", dirID)}},
		dagql.Selector{Field: "withUser", Args: []dagql.NamedInput{rebuildTestArg("name", dagql.NewString("u"))}},
		dagql.Selector{Field: "withLabel", Args: []dagql.NamedInput{rebuildTestArg("name", dagql.NewString("l")), rebuildTestArg("value", dagql.NewString("v"))}},
		dagql.Selector{Field: "withDefaultArgs", Args: []dagql.NamedInput{rebuildTestArg("args", dagql.ArrayInput[dagql.String](dagql.NewStringArray("x")))}},
	))
	return ctr
}

// noSourceDecisions counts the part demands, recorded under the wcprof op
// root, that found no source and computed their part themselves.
func noSourceDecisions(t *testing.T, root uint64) int {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	parents := map[uint64]uint64{}
	for _, ev := range events {
		if ev.Type == "op" {
			parents[ev.OpID] = ev.ParentID
		}
	}
	under := func(id uint64) bool {
		for ; id != 0; id = parents[id] {
			if id == root {
				return true
			}
		}
		return false
	}
	n := 0
	for _, ev := range events {
		if ev.Type == "link" && ev.LinkKind == "fact" && header.Strings[ev.IdentID] == "part.no_source" && under(ev.ParentID) {
			n++
		}
	}
	return n
}

// A record from another engine that came with no download address for any of
// its parts is computed again as a result restored from local disk is: by
// its recipe, reading its inputs' values in memory. Asking a hit's content
// then makes no part demand that finds no source, at any step of the chain.
func TestTransferredRecordsWithoutAddressesRerunLocally(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		build func(*testing.T, *transferTestEngine, context.Context) dagql.AnyResult
		check func(*testing.T, dagql.AnyResult)
	}{
		{
			name: "a container's metadata steps over a scratch rootfs",
			build: func(t *testing.T, e *transferTestEngine, ctx context.Context) dagql.AnyResult {
				return localRerunContainer(t, e, ctx)
			},
			check: func(t *testing.T, res dagql.AnyResult) {
				ctr := res.(dagql.ObjectResult[*core.Container]).Self()
				require.Equal(t, "u", ctr.Config.User)
				require.Equal(t, "v", ctr.Config.Labels["l"])
				require.Equal(t, []string{"x"}, ctr.Config.Cmd)
			},
		},
		{
			name: "a file",
			build: func(t *testing.T, e *transferTestEngine, ctx context.Context) dagql.AnyResult {
				return fileContentsTestFile(t, e, ctx, "contents")
			},
			check: func(t *testing.T, res dagql.AnyResult) {
				require.False(t, dagql.HasPendingLazyComputation(res), "the file is computed")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			salt := transferTestSalt(t)
			a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
			actx := a.session(t, "a1")
			seed := tc.build(t, a, actx)
			require.NoError(t, a.cache.Evaluate(actx, seed))
			bundle := a.export(t, actx, seed)

			bctx := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
			_, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, bundle)
			require.NoError(t, err)
			wcprof.EnsureRecorder()
			pctx, root := wcprof.BeginOp(wcprof.ContextWithProfiling(bctx), wcprof.OpKindSessionPhase, "test.localRerun", wcprof.OpOpts{})
			hit := tc.build(t, b, pctx)
			require.True(t, hit.HitCache(), "the other engine hits the merged record")
			require.NoError(t, b.cache.Evaluate(pctx, hit))
			root.End(wcprof.OutcomeOK)
			require.Zero(t, noSourceDecisions(t, root.ID()), "no step of the chain searches for a source it doesn't have")
			tc.check(t, hit)
		})
	}
}

// A record decoded with no download address can get one later, as when the
// Cloud stores the part after the record was merged. The part, not yet
// computed, then downloads from it rather than being computed.
func TestTransferredRecordDownloadsAnAddressThatCameAfterIt(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a, b := newTransferTestEngine(t, salt), newTransferTestEngine(t, salt)
	actx := a.session(t, "a1")
	seed := fileContentsTestFile(t, a, actx, "contents")
	require.NoError(t, a.cache.Evaluate(actx, seed))
	recordsOnly := a.export(t, actx, seed)

	bctx := dagql.ContextWithServer(b.session(t, "b1"), b.dag)
	_, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, recordsOnly)
	require.NoError(t, err)
	hit := fileContentsTestFile(t, b, bctx, "contents")
	require.True(t, hit.HitCache(), "the other engine hits the merged record")

	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{seed}, Outputs: []dagql.SelectedValueOutput{{Result: seed, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}
	require.NoError(t, a.cache.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Chains.Entries, 1, "the file's snapshot is offered")
		provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
		b.cache.SetPartContentSource(fileContentsTestSource{provider})
		_, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		require.NoError(t, b.cache.Evaluate(bctx, hit))
		require.Positive(t, provider.Reads.Load(), "the file downloads from the address that came after its record")
		return nil
	}))
}
