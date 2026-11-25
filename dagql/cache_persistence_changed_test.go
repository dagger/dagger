package dagql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
