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

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestID("persist-worker-retained")
	res, err := c.GetOrInitCall(ctx, CacheKey{
		ID:            key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)

	shared := res.cacheSharedResult()
	assert.Assert(t, shared != nil)
	sharedID := shared.id

	assert.NilError(t, res.Release(ctx))
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

func TestCachePersistenceDoesNotWriteDuringRuntime(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestID("persist-runtime-no-write")
	res, err := c.GetOrInitCall(ctx, CacheKey{
		ID:            key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 42).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, res.Release(ctx))

	var rowCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&rowCount)
	assert.NilError(t, err)
	assert.Equal(t, 0, rowCount)
}

func TestCachePersistenceWorkerMirrorsPrunedStateAfterRelease(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	key := cacheTestID("persist-worker-pruned")
	res, err := c.GetOrInitCall(ctx, CacheKey{ID: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 99), nil
	})
	assert.NilError(t, err)

	assert.NilError(t, res.Release(ctx))
	assert.NilError(t, c.persistCurrentState(ctx))

	var rowCount int
	err = c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM results`).Scan(&rowCount)
	assert.NilError(t, err)
	assert.Equal(t, 0, rowCount)
}

func TestCachePersistenceWorkerMirrorsAuthoritativeEgraphState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)
	defer func() {
		assert.NilError(t, c.Close(context.Background()))
	}()

	sourceKey := cacheTestID("persist-worker-source")
	sourceRes, err := c.GetOrInitCall(ctx, CacheKey{
		ID:            sourceKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(sourceKey, 11).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)

	rootKey := sourceKey.Append(Int(0).Type(), "persist-worker-root")
	rootRes, err := c.GetOrInitCall(ctx, CacheKey{
		ID:            rootKey,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(rootKey, 22).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)

	assert.NilError(t, sourceRes.Release(ctx))
	assert.NilError(t, rootRes.Release(ctx))
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

func TestCachePersistenceCleanShutdownToggleOnClose(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)

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
