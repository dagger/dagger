package schema

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

func TestSingleChangesetPreservesSnapshotsAfterReopen(t *testing.T) {
	store := testutil.NewStore(t)
	path := filepath.Join(t.TempDir(), "cache.db")
	open := func() (context.Context, *dagql.Cache, *dagql.Server) {
		ctx, cache, srv := scratchTestCache(t, store, path, "single-snapshot")
		(&directorySchema{}).Install(srv)
		return ctx, cache, srv
	}
	ctx, cache, srv := open()
	var parent dagql.ObjectResult[*core.Changeset]
	require.NoError(t, srv.Select(ctx, srv.Root(), &parent, dagql.Selector{Field: "changeset"}))
	// Persist an evaluated snapshot, not just an unevaluated scratch recipe.
	require.NoError(t, cache.Evaluate(ctx, parent.Self().Before))
	// Retain it through a real persistable API result.
	var retained dagql.ObjectResult[*core.Changeset]
	require.NoError(t, srv.Select(ctx, parent, &retained, dagql.Selector{Field: "withChangesets", Args: []dagql.NamedInput{
		{Name: "changes", Value: dagql.ArrayInput[dagql.ID[*core.Changeset]]{}},
	}}))
	record, err := cache.CapturePersistedRecord(ctx, retained)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "single-snapshot"))
	require.NoError(t, cache.Close(ctx))
	store.Reload(t)
	ctx, cache, srv = open()
	loaded, err := cache.LoadResultByResultID(ctx, "single-snapshot", srv, record.ResultID)
	require.NoError(t, err)
	parent = loaded.(dagql.ObjectResult[*core.Changeset])
	require.IsType(t, &core.DirectoryRestoreLazy{}, parent.Self().Before.Self().Lazy)
	requireSingleChangesetMode(t, ctx, srv, parent)
}

func TestSingleChangesetPreservesSnapshotsAfterTransfer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		acquire bool
	}{
		{name: "pending"},
		{name: "acquired", acquire: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			aCtx, a, aSrv := scratchTestCache(t, aStore, "", "a")
			(&directorySchema{}).Install(aSrv)
			var original dagql.ObjectResult[*core.Changeset]
			require.NoError(t, aSrv.Select(aCtx, aSrv.Root(), &original, dagql.Selector{Field: "changeset"}))
			require.NoError(t, a.Evaluate(aCtx, original.Self().Before))
			var bundle dagql.ValueBundle
			require.NoError(t, a.WithExportedValues(aCtx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				raw, err := json.Marshal(exported.Bundle)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, &bundle)
			}))
			ctx, cache, srv := scratchTestCache(t, bStore, "", "b")
			(&directorySchema{}).Install(srv)
			// Keep new local directories distinct from the imported arm64
			// scratch. Building the input must not acquire the receiver first.
			srv.Root().(dagql.ObjectResult[*core.Query]).Self().Server.(*scratchTestServer).platform = core.Platform{OS: "linux", Architecture: "amd64"}
			reply, err := cache.MergeValues(ctx, dagql.CloudCacheID, bundle)
			require.NoError(t, err)
			imported := reply.Imported()
			require.Len(t, imported, 1)
			loaded, err := cache.LoadResultByResultID(ctx, "b", srv, imported[0].ResultID)
			require.NoError(t, err)
			parent := loaded.(dagql.ObjectResult[*core.Changeset])
			require.True(t, dagql.HasPendingLazyEvaluation(parent.Self().Before))
			if tc.acquire {
				require.NoError(t, cache.Evaluate(ctx, parent.Self().Before))
			}
			_, installed := parent.Self().Before.Self().Snapshot.Peek()
			require.Equal(t, tc.acquire, installed)
			requireSingleChangesetMode(t, ctx, srv, parent)
			_, installed = parent.Self().Before.Self().Snapshot.Peek()
			require.Equal(t, tc.acquire, installed)
		})
	}
}

func requireSingleChangesetMode(t *testing.T, ctx context.Context, srv *dagql.Server, parent dagql.ObjectResult[*core.Changeset]) {
	t.Helper()
	before := scratchSelect(t, ctx, srv)
	var after dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, before, &after, dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString("app")},
		{Name: "contents", Value: dagql.NewString("binary\x00payload")},
		{Name: "permissions", Value: dagql.Int(0o750)},
	}}))
	beforeID, err := before.ID()
	require.NoError(t, err)
	var change dagql.ObjectResult[*core.Changeset]
	require.NoError(t, srv.Select(ctx, after, &change, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{
		{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)},
	}}))
	changeID, err := change.ID()
	require.NoError(t, err)
	var merged dagql.ObjectResult[*core.Changeset]
	// A mount "operation not permitted" error here means the snapshot shortcut
	// was not taken: the fallback Git merge cannot mount in this package test.
	require.NoError(t, srv.Select(ctx, parent, &merged, dagql.Selector{Field: "withChangesets", Args: []dagql.NamedInput{
		{Name: "changes", Value: dagql.ArrayInput[dagql.ID[*core.Changeset]]{dagql.NewID[*core.Changeset](changeID)}},
	}}))
	stat, err := merged.Self().After.Self().Stat(ctx, merged.Self().After, srv, "app", false)
	require.NoError(t, err)
	require.Equal(t, 0o750, stat.Permissions)
}
