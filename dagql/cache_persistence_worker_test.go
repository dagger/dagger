package dagql

import (
	"context"
	"path/filepath"
	"testing"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCachePersistenceWorkerMirrorsRetainedPersistableResult(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestIntCall("persist-worker-retained")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42), nil
	})
	assert.NilError(t, err)

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)
	sharedID := shared.id

	cacheTestReleaseSession(t, cacheIface, ctx)
	assert.NilError(t, c.persistCurrentState(ctx))

	var rowCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results WHERE id = ?`, sharedID).Scan(&rowCount)
	assert.NilError(t, err)
	assert.Equal(t, 1, rowCount)

	var storedCallFrameJSON string
	err = c.sqlDB.QueryRowContext(ctx, `SELECT call_frame_json FROM results WHERE id = ?`, sharedID).Scan(&storedCallFrameJSON)
	assert.NilError(t, err)
	assert.Check(t, cmp.Contains(storedCallFrameJSON, `"field":"persist-worker-retained"`))
}

func TestCachePersistenceWorkerMirrorsUnpruneablePersistedEdge(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestIntCall("persist-worker-unpruneable")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.MakeResultUnpruneable(ctx, res))
	cacheTestReleaseSession(t, cacheIface, ctx)
	assert.NilError(t, c.persistCurrentState(ctx))

	rows, err := c.pdb.ListMirrorPersistedEdges(ctx)
	assert.NilError(t, err)
	assert.Equal(t, 1, len(rows))
	assert.Assert(t, rows[0].Unpruneable)
	assert.Equal(t, int64(0), rows[0].ExpiresAtUnix)
}

func TestCachePersistenceDoesNotWriteDuringRuntime(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestIntCall("persist-runtime-no-write")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, cacheIface, ctx)

	var rowCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&rowCount)
	assert.NilError(t, err)
	assert.Equal(t, 0, rowCount)
}

func TestCachePersistenceWorkerMirrorsPrunedStateAfterRelease(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestIntCall("persist-worker-pruned")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cacheIface, ctx)
	assert.NilError(t, c.persistCurrentState(ctx))

	var rowCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&rowCount)
	assert.NilError(t, err)
	assert.Equal(t, 0, rowCount)
}

func TestCachePersistenceWorkerMirrorsAuthoritativeEgraphState(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	sourceKey := cacheTestIntCall("persist-worker-source")
	sourceRes, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    sourceKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(sourceKey, 11), nil
	})
	assert.NilError(t, err)

	rootKey := &ResultCall{
		Kind:     ResultCallKindField,
		Type:     NewResultCallType(Int(0).Type()),
		Field:    "persist-worker-root",
		Receiver: &ResultCallRef{ResultID: uint64(sourceRes.cacheSharedResult().id)},
	}
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    rootKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestPlainResult(NewInt(22)), nil
	})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cacheIface, ctx)
	assert.NilError(t, c.persistCurrentState(ctx))

	var resultsCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&resultsCount)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(resultsCount, 2))

	var termsCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM terms`).Scan(&termsCount)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(termsCount, 2))

	var resultOutputEqClassesCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM result_output_eq_classes`).Scan(&resultOutputEqClassesCount)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(resultOutputEqClassesCount, 2))

	var resultInputCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM term_inputs WHERE provenance_kind = ?`, string(egraphInputProvenanceKindResult)).Scan(&resultInputCount)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(resultInputCount, 1))
}

func TestCachePersistenceSnapshotRemainsValidAfterLiveResultRemoval(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestIntCall("persist-snapshot-self-contained")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall: key,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42), nil
	})
	assert.NilError(t, err)

	snapshot, err := c.snapshotPersistState(ctx)
	assert.NilError(t, err)
	assert.Equal(t, 1, len(snapshot.results))
	assert.Assert(t, snapshot.results[0].row.ID != 0)
	snapshotResultID := snapshot.results[0].row.ID

	cacheTestReleaseSession(t, cacheIface, ctx)
	assert.Equal(t, 0, c.Size())

	assert.NilError(t, c.applyPersistStateSnapshot(ctx, snapshot))

	rows, err := c.pdb.ListMirrorResults(ctx)
	assert.NilError(t, err)
	assert.Equal(t, 1, len(rows))
	assert.Equal(t, snapshotResultID, rows[0].ID)

	assert.Check(t, cmp.Contains(rows[0].CallFrameJSON, `"field":"persist-snapshot-self-contained"`))
}

func TestCachePersistenceCleanShutdownToggleOnClose(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	c := cacheIface

	val, found, err := c.pdb.SelectMetaValue(ctx, persistdb.MetaKeyCleanShutdown)
	assert.NilError(t, err)
	assert.Check(t, found)
	assert.Check(t, cmp.Equal(val, "0"))

	assert.NilError(t, c.Close(context.Background()))

	db, q, err := prepareCacheDBs(ctx, dbPath)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, closeCacheDBs(db, q))
	}()

	val, found, err = q.SelectMetaValue(ctx, persistdb.MetaKeyCleanShutdown)
	assert.NilError(t, err)
	assert.Check(t, found)
	assert.Check(t, cmp.Equal(val, "1"))
}
