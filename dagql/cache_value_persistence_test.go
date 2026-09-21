package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

func TestValueTransferPersistence(t *testing.T) {
	ctx, a, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, a, srv, "root", &transferTestValue{Text: "pending"})
	leaf := persistedListTestResult(t, ctx, a, srv, "leaf", String("owner input"))
	transferTestOffer(t, a, ctx, root, leaf)
	bundle := exportTestBundle(t, ctx, a, root)
	path := filepath.Join(t.TempDir(), "b.db")
	bctx, b, _ := persistedListTestCache(t, path)
	mapping, err := b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	require.NoError(t, b.Close(bctx))
	bctx, b, _ = persistedListTestCache(t, path)
	require.Equal(t, CachePersistenceResetNone, b.PersistenceResetReason())
	row := b.resultsByID[sharedResultID(mapping[0].ResultID)]
	require.NotNil(t, row)
	require.True(t, row.imported)
	require.Empty(t, row.deps)
	require.Len(t, row.partOffers, 1)
	require.Len(t, b.offerOwners, 1)
	var forwarded ValueBundle
	require.NoError(t, b.WithExportedValues(bctx, ValueSelection{Roots: []AnyResult{Result[Typed]{shared: row}}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		require.Empty(t, values.Chains.Entries)
		forwarded = values.Bundle
		return nil
	}))
	raw, err := json.Marshal(forwarded)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "owner_id")
	require.NotContains(t, string(raw), "origin_result_id")
	cctx, c, _ := transferTestCache(t)
	third, err := c.ImportValues(cctx, forwarded)
	require.NoError(t, err)
	require.Len(t, third, 1)
	require.Len(t, c.offerOwners, 1)
	require.Len(t, c.resultsByID, 2)
	require.Empty(t, c.resultsByID[sharedResultID(third[0].ResultID)].deps)
}
