package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/persistdb"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// The stored body tests' pool: records the size of a module's type
// definitions, each its own root, as an engine exports them.
const (
	storedBodyTestRecords = 400
	storedBodyTestFields  = 64
)

// storedBodyTestExport exports the pool from an engine's cache.
func storedBodyTestExport(t *testing.T) ValueBundle {
	t.Helper()
	actx := cacheTestContext(context.Background())
	a, err := NewCache(actx, filepath.Join(t.TempDir(), "a.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*storedBenchValue]{}))
	actx = srvToContext(ContextWithCache(actx, a), srv)
	var roots []AnyResult
	for i := range storedBodyTestRecords {
		field := fmt.Sprintf("body-%d", i)
		value := &storedBenchValue{Text: field, Fields: mergeBenchBody(field, storedBodyTestFields)}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := a.GetOrInitCall(actx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(t, err)
		roots = append(roots, res)
	}
	var bundle ValueBundle
	require.NoError(t, a.WithExportedValues(actx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, storedBodyTestRecords)
	return bundle
}

// storedBodyTestHeap is the live heap after collecting, with the body codec
// made: its buffers are the process's, not a record's.
func storedBodyTestHeap() int64 {
	if enc, _, err := storedBodyCodec(); err == nil {
		enc.EncodeAll([]byte("{}"), nil)
	}
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return int64(stats.HeapAlloc)
}

// storedBodyTestRecordBytes is the mean size of the Cloud's records as
// persistence writes them.
func storedBodyTestRecordBytes(cloud *Cache) int64 {
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	var total int64
	for _, res := range cloud.resultsByID {
		total += res.storedRecordBytes
	}
	return total / int64(len(cloud.resultsByID))
}

// storedBodyTestUse has the Cloud price the roots and build their bundle, as
// a live service does before it places them, and returns the bundle.
func storedBodyTestUse(t *testing.T, ctx context.Context, cloud *Cache, roots []uint64) ValueBundle {
	t.Helper()
	_, err := cloud.StoredSizes(ctx, roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err := cloud.StoredBundle(ctx, roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Values, len(roots))
	return got.Bundle
}

// storedTestRecordLocked is the cache's record of res whole: decoded from its
// body, on a blob-backed cache that holds it compressed, or as the entry
// holds it. Requires egraphMu.
func storedTestRecordLocked(t *testing.T, res *sharedResult) PersistedRecord {
	t.Helper()
	frame, err := res.entryFrameLocked().decode()
	require.NoError(t, err)
	if res.stored != nil {
		rec, err := res.stored.record(uint64(res.id))
		require.NoError(t, err)
		rec.Call = frame
		return rec
	}
	return PersistedRecord{ResultID: uint64(res.id), Envelope: *res.persistedEnvelope, Call: frame}
}

// storedTestHoldWhole stops the Cloud's body worker, so that its records
// stay whole: a test of the path a record takes until it is compressed.
func storedTestHoldWhole(c *Cache) {
	c.closeStoredBodies()
}

// storedTestCompress stops the Cloud's body worker and compresses every
// stored record now: a test of the path a compressed record takes.
func storedTestCompress(c *Cache) {
	c.closeStoredBodies()
	c.egraphMu.RLock()
	var entries []*sharedResult
	for _, res := range c.resultsByID {
		entries = append(entries, res)
	}
	c.egraphMu.RUnlock()
	c.storeBodies(entries)
}

// storedTestSize is the entry's kept measured size: its compressed body's,
// or its kept transfer record's. Requires egraphMu.
func storedTestSize(res *sharedResult) *storedValueSize {
	if res.stored != nil {
		return res.stored.size.Load()
	}
	if kept := res.storedTransfer.Load(); kept != nil {
		return kept.size.Load()
	}
	return nil
}

// requireHeldCompressed checks that the Cloud holds its stored records in
// less memory than three quarters of their encoded size: a record's body,
// held as its encoding or decoded, would take at least that alone.
func requireHeldCompressed(t *testing.T, cloud *Cache, heap int64) {
	t.Helper()
	cloud.egraphMu.RLock()
	records := int64(len(cloud.resultsByID))
	cloud.egraphMu.RUnlock()
	require.Equal(t, int64(storedBodyTestRecords), records)
	perRecord := heap / records
	encoded := storedBodyTestRecordBytes(cloud)
	t.Logf("live heap %d bytes a stored record, of %d encoded", perRecord, encoded)
	require.Less(t, perRecord, encoded*3/4, "the Cloud holds a record in less than three quarters of its encoded size")
}

// The Cloud holds each record it stores compressed: once it has priced and
// sent them, as a live service has, its records take less memory than their
// encodings would, within a few seconds of the merge that stored them.
//
// It measures the heap, so it runs alone.
func TestCloudHoldsStoredRecordsCompressed(t *testing.T) {
	bundle := storedBodyTestExport(t)
	ctx := cacheTestContext(context.Background())
	before := storedBodyTestHeap()
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	roots := storedPersistenceTestMerge(t, ctx, cloud, bundle)
	require.Len(t, roots, storedBodyTestRecords)
	storedBodyTestUse(t, ctx, cloud, roots)
	encoded := storedBodyTestRecordBytes(cloud)
	var heap int64
	require.Eventually(t, func() bool {
		heap = storedBodyTestHeap() - before
		return heap/storedBodyTestRecords < encoded*3/4
	}, 10*time.Second, 100*time.Millisecond, "the Cloud's records come to take less than three quarters of their encoded size")
	runtime.KeepAlive(bundle)
	requireHeldCompressed(t, cloud, heap)
}

// A Cloud restored from its save holds its records compressed too, without
// decoding them, and sends exactly the records it sent before it was saved.
//
// It measures the heap, so it runs alone.
func TestCloudRestoresStoredRecordsCompressed(t *testing.T) {
	bundle := storedBodyTestExport(t)
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	roots := storedPersistenceTestMerge(t, ctx, cloud, bundle)
	require.Len(t, roots, storedBodyTestRecords)
	want := mustTransferJSON(t, storedBodyTestUse(t, ctx, cloud, roots).Values)
	require.NoError(t, cloud.Close(ctx))

	before := storedBodyTestHeap()
	ctx, restored := storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, restored.Close(ctx)) }()
	require.Equal(t, CachePersistenceResetNone, restored.PersistenceResetReason())
	heap := storedBodyTestHeap() - before
	runtime.KeepAlive(bundle)
	requireHeldCompressed(t, restored, heap)

	got := storedBodyTestUse(t, ctx, restored, roots)
	require.JSONEq(t, string(want), string(mustTransferJSON(t, got.Values)), "the restored Cloud sends the records it sent before")
}

// A Cloud's store saved in another format, here an engine's, is wiped once
// when the Cloud opens it, as a store of another schema version is. An
// engine's store in its own format is restored (TestCloudSaveAndRestoreKeepThePool
// and the engine persistence tests).
func TestCloudWipesAStoreOfAnotherFormat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	bundle, _ := mergeTestSource(t, "other-format", "value")
	storedPersistenceTestMerge(t, ctx, cloud, bundle)
	require.NoError(t, cloud.Close(ctx))

	setCacheTestMeta(t, ctx, path, persistdb.MetaKeySchemaVersion, cachePersistenceSchemaVersion)
	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	require.Equal(t, CachePersistenceResetSchemaMismatch, cloud.PersistenceResetReason())
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Empty(t, cloud.resultsByID)
}

// A bundle's check of itself finds a compressed record's part at a nested
// output path, an inline list item's, by its whole address.
func TestStoredBundleChecksANestedPartOfACompressedRecord(t *testing.T) {
	t.Parallel()
	address := PersistedPartAddress{OutputPath: PersistedRefPath{}.Field("items").Index(0), Part: "snapshot"}
	key, err := partAddressKey(address)
	require.NoError(t, err)
	value := SnapshotValue{Kind: "directory", Path: "/"}
	frame := &ResultCall{Kind: ResultCallKindField, Field: "nested", Type: NewResultCallType((&transferTestValue{}).Type())}
	bundle := ValueBundle{
		Version: ValueBundleVersion,
		Roots:   []TransferredRoot{{Ordinal: 1}},
		Values:  []TransferredValue{{Ordinal: 1, SenderNumber: 7, Record: PersistedRecord{ResultID: 7, Call: frame}}},
		Outputs: []TransferredOutput{{Ordinal: 1, Address: address, State: "completed", Value: &value, Chain: &OfferedChain{}, Owner: &PersistedOfferOwner{}}},
	}
	stored := map[uint64][]declaredPart{1: {{key: key, address: address, state: "pending", kind: "directory"}}}
	require.NoError(t, validateStoredBundle(bundle, stored))
}

// A value the Cloud sends compressed encodes exactly as the same value sent
// whole, byte for byte, so a record's encoding doesn't depend on whether it
// was compressed yet.
func TestStoredValueEncodesAsTheWholeValue(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloudIn(t, "root", true)
	whole, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	for _, value := range whole.Bundle.Values {
		require.Nil(t, value.storedEnvelope, "the record goes whole")
	}
	storedTestCompress(cloud)
	compressed, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.NotNil(t, compressed.Bundle.Values[0].storedEnvelope, "the record goes compressed")
	require.Equal(t, string(mustTransferJSON(t, whole.Bundle.Values)), string(mustTransferJSON(t, compressed.Bundle.Values)))
}
