package schema

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// A Directory made without a recipe of its own, whose record another engine
// sent without its content and with no offer of it, has no source on the
// engine that receives it. Reading it there runs its call once, as reading
// it on an engine that never had it does, and gets the call's content; the
// row then has the part, and reading it again runs nothing.
func TestUnbackedDirectoryRunsItsCall(t *testing.T) {
	for _, reached := range []string{"by-lookup", "by-result-id"} {
		t.Run(reached, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			actx, a, asrv := scratchTestCache(t, aStore, "", "unbacked-a")
			bctx, b, bsrv := scratchTestCache(t, bStore, "", "unbacked-b")
			// Each engine computes the call the same way, eagerly, from its
			// own store.
			made := func(store *testutil.Store, calls *atomic.Int64) func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
				return func(context.Context, *core.Query, struct{}) (*core.Directory, error) {
					calls.Add(1)
					ref, _ := store.Build(t, nil, "data", "made here\n")
					dir := &core.Directory{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
					dir.SetPath("/")
					dir.SetSnapshot(ref)
					return dir, nil
				}
			}
			var aCalls, bCalls atomic.Int64
			dagql.Fields[*core.Query]{dagql.Func("eagerDirectory", made(aStore, &aCalls))}.Install(asrv)
			dagql.Fields[*core.Query]{dagql.Func("eagerDirectory", made(bStore, &bCalls))}.Install(bsrv)
			// A stored child of the directory is what A sends: the directory
			// goes with it as a record only.
			dagql.Fields[*core.Directory]{dagql.NodeFunc("savedEntries", func(context.Context, dagql.ObjectResult[*core.Directory], struct{}) (dagql.Array[dagql.String], error) {
				return dagql.Array[dagql.String]{"data"}, nil
			})}.Install(asrv)

			var original dagql.ObjectResult[*core.Directory]
			require.NoError(t, asrv.Select(actx, asrv.Root(), &original, dagql.Selector{Field: "eagerDirectory"}))
			saved, err := original.Select(actx, asrv, dagql.Selector{Field: "savedEntries"})
			require.NoError(t, err)
			var dirRow uint64
			require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{saved}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				require.Empty(t, exported.Chains.Entries, "the directory's content is not sent")
				require.Empty(t, exported.Bundle.Outputs, "nor offered")
				reply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
				require.NoError(t, err)
				imported := reply.Imported()
				require.Len(t, imported, 1)
				child, err := b.LoadResultByResultID(bctx, "unbacked-b", bsrv, imported[0].ResultID)
				require.NoError(t, err)
				frame, err := child.ResultCall()
				require.NoError(t, err)
				require.NotNil(t, frame.Receiver)
				dirRow = frame.Receiver.ResultID
				return nil
			}))
			require.EqualValues(t, 1, aCalls.Load())

			reach := func() dagql.ObjectResult[*core.Directory] {
				var got dagql.ObjectResult[*core.Directory]
				switch reached {
				case "by-lookup":
					require.NoError(t, bsrv.Select(bctx, bsrv.Root(), &got, dagql.Selector{Field: "eagerDirectory"}))
				case "by-result-id":
					// As a stored child's receiver is reached, with no lookup
					// of its call.
					loaded, err := b.LoadResultByResultID(bctx, "unbacked-b", bsrv, dirRow)
					require.NoError(t, err)
					got = loaded.(dagql.ObjectResult[*core.Directory])
				}
				return got
			}
			read := func(got dagql.ObjectResult[*core.Directory]) {
				require.Equal(t, []string{"data"}, demandedDirectoryEntries(t, bctx, got))
				snapshot, err := got.Self().Snapshot.GetOrEval(bctx, got.Result)
				require.NoError(t, err)
				content, err := os.ReadFile(filepath.Join(testutil.Root(t, snapshot), "data"))
				require.NoError(t, err)
				require.Equal(t, "made here\n", string(content))
			}
			// The snapshots the merged row owns.
			rowSnapshots := func() []string {
				return recordSnapshots(t, bctx, b, bsrv, "unbacked-b", dirRow)
			}
			require.Empty(t, rowSnapshots(), "the merged row has no content")

			got := reach()
			if reached == "by-lookup" {
				id, err := b.PersistedResultID(got)
				require.NoError(t, err)
				require.Equal(t, dirRow, id, "the lookup hits the merged row")
			}
			read(got)
			require.EqualValues(t, 1, bCalls.Load(), "B runs the call once, as an engine without the record does")
			owned := rowSnapshots()
			require.Len(t, owned, 1, "the row takes the part of the value its call made")

			// A second read of the row, and of the call reached the same way,
			// is served from the part the row has now: no second run, and
			// nothing installed again.
			read(got)
			read(reach())
			require.EqualValues(t, 1, bCalls.Load(), "the second read runs nothing")
			require.Equal(t, owned, rowSnapshots(), "the second read installs nothing")
		})
	}
}

// The same, for a Directory held inline in a list another engine sent as a
// record only: reading it runs the list's call once, and the list's row
// takes the Directory's part from the list that call makes.
func TestUnbackedInlineDirectoryRunsItsCall(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := scratchTestCache(t, aStore, "", "inline-a")
	bctx, b, bsrv := scratchTestCache(t, bStore, "", "inline-b")
	made := func(store *testutil.Store, calls *atomic.Int64) func(context.Context, *core.Query, struct{}) (dagql.Array[*core.Directory], error) {
		return func(context.Context, *core.Query, struct{}) (dagql.Array[*core.Directory], error) {
			calls.Add(1)
			ref, _ := store.Build(t, nil, "data", "made here\n")
			dir := &core.Directory{Platform: core.Platform{OS: "linux", Architecture: "arm64"}, Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory])}
			dir.SetPath("/")
			dir.SetSnapshot(ref)
			return dagql.Array[*core.Directory]{dir}, nil
		}
	}
	var aCalls, bCalls atomic.Int64
	dagql.Fields[*core.Query]{dagql.Func("eagerDirectories", made(aStore, &aCalls))}.Install(asrv)
	dagql.Fields[*core.Query]{dagql.Func("eagerDirectories", made(bStore, &bCalls))}.Install(bsrv)

	original, err := asrv.Root().Select(actx, asrv, dagql.Selector{Field: "eagerDirectories"})
	require.NoError(t, err)
	var listRow uint64
	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		require.Len(t, exported.Bundle.Values, 1, "the list is one record, its Directory inline")
		require.Empty(t, exported.Chains.Entries, "the Directory's content is not sent")
		require.Empty(t, exported.Bundle.Outputs, "nor offered")
		reply, err := b.MergeValues(bctx, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		imported := reply.Imported()
		require.Len(t, imported, 1)
		listRow = imported[0].ResultID
		return nil
	}))
	require.EqualValues(t, 1, aCalls.Load())

	// read reads the list's Directory.
	read := func(list dagql.AnyResult) {
		item, err := list.Unwrap().(dagql.Enumerable).Nth(1)
		require.NoError(t, err)
		dir, ok := dagql.UnwrapAs[*core.Directory](item)
		require.True(t, ok)
		require.NoError(t, dir.LazyEvalFunc()(bctx))
		ref, set := dir.Snapshot.Peek()
		require.True(t, set)
		path, _ := dir.Dir.Peek()
		content, err := os.ReadFile(filepath.Join(testutil.Root(t, ref), path, "data"))
		require.NoError(t, err)
		require.Equal(t, "made here\n", string(content))
	}
	rowSnapshots := func() []string {
		return recordSnapshots(t, bctx, b, bsrv, "inline-b", listRow)
	}
	require.Empty(t, rowSnapshots(), "the merged list has no content")

	list, err := bsrv.Root().Select(bctx, bsrv, dagql.Selector{Field: "eagerDirectories"})
	require.NoError(t, err)
	id, err := b.PersistedResultID(list)
	require.NoError(t, err)
	require.Equal(t, listRow, id, "the lookup hits the merged list")
	read(list)
	require.EqualValues(t, 1, bCalls.Load(), "B runs the list's call once")
	owned := rowSnapshots()
	require.Len(t, owned, 1, "the list's row takes the part of the list its call made")

	// A second read of the merged list's row is served from the part it has
	// now: no second run, and nothing installed again.
	again, err := b.LoadResultByResultID(bctx, "inline-b", bsrv, listRow)
	require.NoError(t, err)
	read(again)
	require.EqualValues(t, 1, bCalls.Load(), "the second read runs nothing")
	require.Equal(t, owned, rowSnapshots(), "the second read installs nothing")
}

// recordSnapshots returns the snapshots the cache's record of row owns.
func recordSnapshots(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session string, row uint64) []string {
	t.Helper()
	loaded, err := cache.LoadResultByResultID(ctx, session, srv, row)
	require.NoError(t, err)
	record, err := cache.CapturePersistedRecord(ctx, loaded)
	require.NoError(t, err)
	var refs []string
	for _, link := range record.SnapshotLinks {
		refs = append(refs, link.RefKey)
	}
	return refs
}
