package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

func TestValueTransferPersistenceFinalOfferRestart(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	source, _ := aStore.Build(t, nil, "value.txt", "durable bytes")
	aCtx, a, aServer := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
	file := &File{Platform: Platform{OS: "linux", Architecture: "amd64"}, File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File])}
	file.SetPath("/value.txt")
	file.SetSnapshot(source)
	root := attachTransferObject(t, aCtx, a, aServer, "a", "value", file)
	var bundle dagql.ValueBundle
	var acquired bkcache.ImmutableRef
	require.NoError(t, a.WithExportedValues(aCtx, dagql.ValueSelection{Roots: []dagql.AnyResult{root}, Outputs: []dagql.SelectedValueOutput{{Result: root, Address: dagql.PersistedPartAddress{Part: "snapshot"}}}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
		bundle = values.Bundle
		chain := values.Chains.Entries[0]
		var err error
		acquired, err = bStore.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: chain.Provider})
		return err
	}))
	path := filepath.Join(t.TempDir(), "b.db")
	bCtx, b, bServer := transferCache(t, bStore, path, "b")
	values, err := b.ImportValues(bCtx, bundle)
	require.NoError(t, err)
	loaded, err := b.LoadResultByResultID(bCtx, "b", bServer, values[0].ResultID)
	require.NoError(t, err)
	installed := loaded.(dagql.ObjectResult[*File])
	// Model the locally completed checkpoint boundary before its redundant
	// offer is retired. This writes a test value; it adds no acquisition route.
	installed.Self().outputMu.Lock()
	installed.Self().transferPending = nil
	installed.Self().OutputRev++
	installed.Self().outputMu.Unlock()
	installed.Self().SetPath("/value.txt")
	installed.Self().SetSnapshot(acquired)
	require.NoError(t, b.SyncResultSnapshotOwnerLeases(bCtx, installed))
	record, err := b.CapturePersistedRecord(bCtx, installed)
	require.NoError(t, err)
	require.Len(t, record.Envelope.PendingOffers, 1)
	require.Len(t, record.SnapshotLinks, 1)
	assertTypedSnapshotOwners(t, bStore, acquired.SnapshotID(), 1)
	require.NoError(t, b.ReleaseSession(bCtx, "b"))
	require.NoError(t, b.Close(bCtx))
	bStore.Reload(t)
	// The new cache restores durable snapshot owners before ending the offer.
	bCtx, b, bServer = transferCache(t, bStore, path, "restarted")
	require.Equal(t, dagql.CachePersistenceResetNone, b.PersistenceResetReason())
	report, err := b.TransferFixtureSnapshot(bCtx, "restarted", nil)
	require.NoError(t, err)
	require.Empty(t, report.Owners)
	for _, row := range report.Rows {
		require.Empty(t, row.Offers)
	}
	assertTypedSnapshotOwners(t, bStore, acquired.SnapshotID(), 1)
	require.NoError(t, bkcache.ReleaseTransferLeasesAfterRestart(bCtx, bStore.Leases))
	bStore.GC(t)
	loaded, err = b.LoadResultByResultID(bCtx, "restarted", bServer, values[0].ResultID)
	require.NoError(t, err)
	restored := loaded.(dagql.ObjectResult[*File])
	contents := demandedFileContents(t, bCtx, restored)
	require.Equal(t, "durable bytes", string(contents))
}
