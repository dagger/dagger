package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime/metrics"
	"testing"

	"gotest.tools/v3/assert"
)

// TestCachePersistenceSaveStartsNoGoroutinePerRow checks that a save under
// a context that can be cancelled starts a bounded number of goroutines,
// however many rows it writes. The SQLite driver starts a goroutine to
// interrupt each statement it runs under such a context; a save that runs a
// statement for each row under one started thousands at once, which on a
// service held to 2 CPUs queued its other work behind them.
//
// It counts goroutines created process-wide, so it doesn't call t.Parallel:
// top-level tests that do wait until it is done.
func TestCachePersistenceSaveStartsNoGoroutinePerRow(t *testing.T) {
	ctx, cancel := context.WithCancel(cacheTestContext(t.Context()))
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	const entries = 500
	for i := range entries {
		key := cacheTestIntCall(fmt.Sprintf("save-goroutines-%d", i))
		_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:    key,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key, i), nil
		})
		assert.NilError(t, err)
	}
	cacheTestReleaseSession(t, c, ctx)

	created := []metrics.Sample{{Name: "/sched/goroutines-created:goroutines"}}
	metrics.Read(created)
	assert.Equal(t, created[0].Value.Kind(), metrics.KindUint64, "the runtime counts goroutines created")
	before := created[0].Value.Uint64()
	assert.NilError(t, c.persistCurrentState(ctx))
	metrics.Read(created)
	started := created[0].Value.Uint64() - before

	var rows int
	assert.NilError(t, c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&rows))
	assert.Equal(t, rows, entries)
	// Every entry is at least its results row, its class and its digest.
	assert.Assert(t, started < 50, "a save of %d entries started %d goroutines", entries, started)
}
