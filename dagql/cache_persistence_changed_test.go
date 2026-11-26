package dagql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

// A save after one entry was added writes what changed, not the whole store
// again: the write-ahead log it leaves is a small part of the store. A save
// that rewrote the store would write every page of it into the log.
func TestCachePersistenceSaveWritesOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()
	create := func(session, field string) {
		t.Helper()
		key := cacheTestIntCall(field)
		_, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{
			ResultCall:    key,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key, len(field)), nil
		})
		assert.NilError(t, err)
	}

	const entries = 2000
	for i := range entries {
		create("test-session", fmt.Sprintf("save-what-changed-%d", i))
	}
	cacheTestReleaseSession(t, c, ctx)
	assert.NilError(t, c.persistCurrentState(ctx))
	_, err = c.sqlDB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	assert.NilError(t, err)
	store, err := os.Stat(dbPath)
	assert.NilError(t, err)

	create("later-session", "save-what-changed-later")
	assert.NilError(t, c.ReleaseSession(ctx, "later-session"))
	assert.NilError(t, c.persistCurrentState(ctx))
	log, err := os.Stat(dbPath + "-wal")
	assert.NilError(t, err)
	assert.Assert(t, log.Size() < store.Size()/4,
		"a save after one new entry wrote %d bytes to the log, of a store of %d", log.Size(), store.Size())

	var saved int
	assert.NilError(t, c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&saved))
	assert.Equal(t, saved, entries+1)
}

// A Cloud's saves write only what changed, across the changes a service
// makes: new entries, a changed last use, a stored part, a prune that empties
// the store, a compaction of its classes, a restart. TestMain checks every one
// of these saves against a full save at its hold. It runs on several stores,
// each with its own fingerprint seed.
func TestCloudSavesWriteOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	for i := range 6 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("cloud-%d.db", i))
		ctx, cloud := storedPersistenceTestCloud(t, path)
		cloud.closeStoredBodies()
		first, _ := storedPersistenceTestRootAndDep(t, ctx, cloud, fmt.Sprintf("first-%d", i))
		second, _ := storedPersistenceTestRootAndDep(t, ctx, cloud, fmt.Sprintf("second-%d", i))
		require.NoError(t, cloud.Checkpoint(ctx))
		require.Positive(t, cloud.PersistedResults())
		require.Equal(t, cloud.PersistedResults(), cloud.PersistedWritten(), "a first save writes every entry")

		require.NoError(t, cloud.Checkpoint(ctx))
		require.Zero(t, cloud.PersistedWritten(), "nothing changed")

		storedPruneTestUse(cloud, first, time.Now().Add(-time.Hour))
		require.NoError(t, cloud.Checkpoint(ctx))
		require.Equal(t, 1, cloud.PersistedWritten(), "only the entry whose last use moved")

		stored, _, err := cloud.SetStoredPart(ctx, second, storedPruneTestPart("snapshot", map[string]int64{fmt.Sprintf("layer-%d", i): 10}), 0)
		require.NoError(t, err)
		require.True(t, stored)
		require.NoError(t, cloud.Checkpoint(ctx))
		require.Positive(t, cloud.PersistedWritten(), "the entry that stores a part")
		require.Less(t, cloud.PersistedWritten(), cloud.PersistedResults())

		storedPersistenceTestRootAndDep(t, ctx, cloud, fmt.Sprintf("third-%d", i))
		require.NoError(t, cloud.Checkpoint(ctx))

		_, err = cloud.PruneMetadataEstimate(ctx, 2, 1)
		require.NoError(t, err)
		require.NoError(t, cloud.Checkpoint(ctx))
		// The engine's holdings kept the pruned entries; a store keeps no
		// holding, so they go with them.
		_, _, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-a").Candidates, nil)
		require.NoError(t, err)
		require.NoError(t, cloud.Checkpoint(ctx))
		_, _, err = cloud.CompactEqClasses(ctx)
		require.NoError(t, err)
		storedPersistenceTestRootAndDep(t, ctx, cloud, fmt.Sprintf("fourth-%d", i))
		require.NoError(t, cloud.Checkpoint(ctx))

		before := storedPersistenceTestState(cloud)
		require.NoError(t, cloud.Close(ctx))
		ctx, cloud = storedPersistenceTestCloud(t, path)
		cloud.closeStoredBodies()
		require.Equal(t, before, storedPersistenceTestState(cloud))
		require.NoError(t, cloud.Checkpoint(ctx))
		require.Zero(t, cloud.PersistedWritten(), "a save right after a restore writes nothing: the store holds what was restored")
		storedPersistenceTestRootAndDep(t, ctx, cloud, fmt.Sprintf("fifth-%d", i))
		require.NoError(t, cloud.Checkpoint(ctx))
		require.Equal(t, 2, cloud.PersistedWritten(), "the new root and its dependency")
		require.NoError(t, cloud.Close(ctx))
	}
}

// A restore that settles a saved record's offer, for a part the record says
// is complete, changes the record before anything else runs and while it is
// still undecoded. The next save writes the record without the offer, as a
// full save would: what the cache keeps of the database is the record as the
// database holds it, not as the restore left it.
func TestCachePersistenceSaveWritesWhatTheRestoreSettled(t *testing.T) {
	t.Parallel()
	chain := newChainFixture(t, "settled")
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	receiver := persistedListTestResult(t, ctx, c, srv, "settled-receiver", &transferTestValue{
		Text:  "settled",
		Parts: []transferTestPart{{Part: "snapshot", State: "pending"}},
	})
	id := receiver.cacheSharedResult().id
	out, err := c.testOfferParts(ctx, receiver, []PersistedPartOffer{exhaustionOffer(chain, "settled-key", false)})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	require.NoError(t, c.Close(ctx))

	record := func() string {
		t.Helper()
		db, q, err := prepareCacheDBs(context.Background(), path, false)
		require.NoError(t, err)
		defer closeCacheDBs(db, q)
		var payload string
		require.NoError(t, db.QueryRow(`SELECT CAST(self_payload AS TEXT) FROM result_records WHERE result_id = ?`, int64(id)).Scan(&payload))
		return payload
	}
	require.Contains(t, record(), `"pendingOffers"`)
	// The record says its part is complete, with the offer still pending: a
	// restore settles that offer.
	db, q, err := prepareCacheDBs(context.Background(), path, false)
	require.NoError(t, err)
	res, err := db.Exec(`UPDATE result_records SET self_payload = CAST(REPLACE(CAST(self_payload AS TEXT), '"state":"pending"', '"state":"completed"') AS BLOB) WHERE result_id = ?`, int64(id))
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, closeCacheDBs(db, q))

	ctx, c = reopenTransferTestCache(t, path)
	c.egraphMu.RLock()
	restored := c.resultsByID[id]
	offers := len(restored.testPartOffers())
	lazy := restored.loadPayloadState().persistedEnvelope != nil
	c.egraphMu.RUnlock()
	require.Zero(t, offers, "the restore settled the offer")
	require.True(t, lazy, "the record was never decoded")
	require.NoError(t, c.Close(ctx))
	require.NotContains(t, record(), `"pendingOffers"`, "the save wrote what the restore settled")
}
