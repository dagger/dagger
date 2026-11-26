package dagql

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/persistdb"
	"gotest.tools/v3/assert"
)

// importStreamTestStore saves n persisted int results to a new store at
// dbPath, then pads every row's saved envelope with padBytes of JSON
// whitespace, so its raw row is large and decodes to what it did before.
// It returns the results' ids in the table's order.
func importStreamTestStore(t *testing.T, ctx context.Context, dbPath string, n, padBytes int) []int64 {
	t.Helper()
	c, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	for i := range n {
		key := cacheTestIntCall(fmt.Sprintf("import-stream-%d", i))
		_, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:    key,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key, i), nil
		})
		assert.NilError(t, err)
	}
	cacheTestReleaseSession(t, c, ctx)
	assert.NilError(t, c.persistCurrentState(ctx))
	assert.NilError(t, c.Close(context.Background()))

	db, q, err := prepareCacheDBs(ctx, dbPath, false)
	assert.NilError(t, err)
	if padBytes > 0 {
		// hex() of a zeroblob is padBytes '0' characters, each made a space.
		_, err = db.ExecContext(ctx, `UPDATE results SET self_payload = CAST(self_payload || replace(hex(zeroblob(?)), '0', ' ') AS BLOB)`, padBytes/2)
		assert.NilError(t, err)
	}
	var ids []int64
	err = q.EachMirrorResult(ctx, func(row persistdb.MirrorResult) error {
		ids = append(ids, row.ID)
		return nil
	})
	assert.NilError(t, err)
	assert.NilError(t, closeCacheDBs(db, q))
	assert.Equal(t, n, len(ids))
	return ids
}

// liveHeap collects and returns the bytes of live heap.
func liveHeap() uint64 {
	runtime.GC()
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// A restore holds a row's raw bytes only until its batch is decoded, not
// the whole results table beside the decoded rows: what it holds beyond the
// restored cache stays far below the table's raw bytes.
//
// It measures the process's heap, so it runs alone (not parallel), on two
// cores, which bounds the batches read and not yet decoded.
func TestCachePersistenceImportHoldsRawRowsOnlyUntilDecoded(t *testing.T) {
	const (
		rows     = 3000
		padBytes = 32 << 10
		// The table's raw rows are about 96 MiB. Two cores hold at most five
		// batches of 64 rows, about 10 MiB of them, at once.
		transitBound = 32 << 20
	)
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(2))

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	importStreamTestStore(t, ctx, dbPath, rows, padBytes)

	var (
		peak uint64
		done = make(chan struct{})
		wg   sync.WaitGroup
	)
	liveHeap()
	wg.Go(func() {
		for {
			if live := liveHeap(); live > peak {
				peak = live
			}
			select {
			case <-done:
				return
			default:
			}
		}
	})
	c, err := NewCache(ctx, dbPath, nil, nil)
	close(done)
	wg.Wait()
	assert.NilError(t, err)
	assert.Equal(t, CachePersistenceResetNone, c.PersistenceResetReason())
	assert.Equal(t, rows, len(c.resultsByID))
	restored := liveHeap()
	runtime.KeepAlive(c)
	assert.NilError(t, c.Close(context.Background()))

	transit := int64(peak) - int64(restored)
	t.Logf("live heap: peak %d during the restore, %d restored, %d in transit", peak, restored, transit)
	assert.Assert(t, transit < transitBound, "the restore held %d bytes beyond the restored cache, over %d", transit, transitBound)
}

// A restore reports the first invalid row in the table's order, wherever
// the rows' batches fall and in whatever order they are decoded.
//
// It reads the process's default logger, so it runs alone (not parallel).
func TestCachePersistenceImportReportsFirstInvalidRow(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	// Four batches of 64 rows (importResultBatchRows), with one invalid row
	// in the middle of the second, and one in the last.
	ids := importStreamTestStore(t, ctx, dbPath, 256, 0)
	first, later := ids[96], ids[253]

	db, q, err := prepareCacheDBs(ctx, dbPath, false)
	assert.NilError(t, err)
	for _, id := range []int64{first, later} {
		_, err = db.ExecContext(ctx, `UPDATE results SET self_payload = x'7B6E6F742D6A736F6E' WHERE id = ?`, id)
		assert.NilError(t, err)
	}
	assert.NilError(t, q.UpsertMeta(ctx, persistdb.MetaKeyCleanShutdown, "1"))
	assert.NilError(t, closeCacheDBs(db, q))

	var (
		mu     sync.Mutex
		failed []string
	)
	prev := slog.Default()
	slog.SetDefault(slog.New(importErrorHandler{record: func(msg, err string) {
		if msg == "dagql persistence import failed; wiping and cold-starting" {
			mu.Lock()
			failed = append(failed, err)
			mu.Unlock()
		}
	}}))
	c, err := NewCache(ctx, dbPath, nil, nil)
	slog.SetDefault(prev)
	assert.NilError(t, err)
	assert.Equal(t, CachePersistenceResetImportFailure, c.PersistenceResetReason())
	assert.NilError(t, c.Close(context.Background()))

	assert.Equal(t, 1, len(failed))
	want := fmt.Sprintf("import result %d self payload: ", first)
	assert.Assert(t, strings.HasPrefix(failed[0], want), "import failed with %q, want the error of result %d", failed[0], first)
}

// importErrorHandler is a slog handler that passes each record's message and
// its "err" attribute to record.
type importErrorHandler struct {
	record func(msg, err string)
}

func (importErrorHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h importErrorHandler) Handle(_ context.Context, r slog.Record) error {
	var errText string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "err" {
			errText = a.Value.String()
		}
		return true
	})
	h.record(r.Message, errText)
	return nil
}

func (h importErrorHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h importErrorHandler) WithGroup(string) slog.Handler      { return h }
