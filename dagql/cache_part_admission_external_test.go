package dagql_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func TestPartSessionlessRestoredDirectory(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "test", SessionID: "test"})
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	open := func(store *testutil.Store, path string) (*dagql.Cache, *dagql.Server) {
		c, err := dagql.NewCache(ctx, path, store.Manager, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
		srv := newExternalDagqlServerForTest(t, &core.Query{})
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Directory]{}))
		return c, srv
	}
	attach := func(c *dagql.Cache, srv *dagql.Server, store *testutil.Store) dagql.AnyResult {
		ref, _ := store.Build(t, nil, "payload", "directory transformation")
		newDir := func(ref snapshots.ImmutableRef) *core.Directory {
			d := &core.Directory{Dir: new(core.LazyAccessor[string, *core.Directory]), Snapshot: new(core.LazyAccessor[snapshots.ImmutableRef, *core.Directory]), Platform: core.Platform{OS: "linux", Architecture: "amd64"}}
			d.SetPath("/")
			d.SetSnapshot(ref)
			return d
		}
		attach := func(frame *dagql.ResultCall, d *core.Directory) dagql.AnyResult {
			result, err := c.GetOrInitCall(dagql.ContextWithCache(ctx, c), "test", srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewObjectResultForCall(d, srv, frame) })
			require.NoError(t, err)
			return result
		}
		baseRef, err := store.Manager.GetBySnapshotID(ctx, ref.SnapshotID())
		require.NoError(t, err)
		base := attach(&dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "directory", Type: dagql.NewResultCallType((&core.Directory{}).Type())}, newDir(baseRef))
		id, err := c.PersistedResultID(base)
		require.NoError(t, err)
		return attach(&dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "withNewFile", Receiver: &dagql.ResultCallRef{ResultID: id}, Type: dagql.NewResultCallType((&core.Directory{}).Type())}, newDir(ref))
	}
	a, asrv := open(aStore, "")
	original := attach(a, asrv, aStore)
	path := filepath.Join(t.TempDir(), "cache.db")
	b, bsrv := open(bStore, path)
	donor := attach(b, bsrv, bStore)
	donorID, err := b.PersistedResultID(donor)
	require.NoError(t, err)
	var receiverID uint64
	require.NoError(t, a.WithExportedValues(dagql.ContextWithCache(ctx, a), dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{}, func(_ context.Context, exported *dagql.ExportedValues) error {
		imported, err := b.ImportValues(ctx, exported.Bundle)
		if err == nil {
			receiverID = imported[0].ResultID
		}
		return err
	}))
	require.NotEqual(t, donorID, receiverID)
	require.NoError(t, b.ReleaseSession(ctx, "test"))
	require.NoError(t, b.Close(ctx))
	b, _ = open(bStore, path)
	dagql.CheckSessionlessRestoredDirectoryForTest(t, ctx, b, receiverID, donorID)
}
