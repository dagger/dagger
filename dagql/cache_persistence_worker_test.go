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
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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

func TestCachePersistenceSnapshotOmitsSessionOnlyResult(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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
	assert.Equal(t, 0, len(snapshot.results))

	cacheTestReleaseSession(t, cacheIface, ctx)
	assert.Equal(t, 0, c.Size())

	assert.NilError(t, c.applyPersistStateSnapshot(ctx, snapshot))

	rows, err := c.pdb.ListMirrorResults(ctx)
	assert.NilError(t, err)
	assert.Equal(t, 0, len(rows))
}

func TestCachePersistenceSnapshotKeepsOnlyCompletePersistedRootClosures(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	c := cacheIface

	create := func(field string, receiver *sharedResult, persistable bool, value int) AnyResult {
		t.Helper()
		frame := cacheTestIntCall(field)
		if receiver != nil {
			frame.Receiver = &ResultCallRef{ResultID: uint64(receiver.id)}
		}
		res, err := c.GetOrInitCall(ctx, "root-closure", noopTypeResolver{}, &CallRequest{
			ResultCall:     frame,
			ConcurrencyKey: field,
			IsPersistable:  persistable,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, value), nil
		})
		assert.NilError(t, err)
		return res
	}

	keptDep := create("root-closure-kept-dep", nil, false, 1)
	dirtyDep := create("root-closure-dirty-dep", nil, false, 2)
	keptRoot := create("root-closure-kept-root", keptDep.cacheSharedResult(), true, 3)
	droppedRoot := create("root-closure-dropped-root", dirtyDep.cacheSharedResult(), true, 4)
	sessionOnly := create("root-closure-session-only", nil, false, 5)
	assert.NilError(t, c.AddExplicitDependency(ctx, droppedRoot, keptDep, "shared-clean-dependency"))

	dirtyShared := dirtyDep.cacheSharedResult()
	dirtyShared.attachDepsMu.Lock()
	dirtyShared.attachDepsWaitCh = make(chan struct{})
	dirtyShared.attachDepsErr = context.Canceled
	close(dirtyShared.attachDepsWaitCh)
	dirtyShared.attachDepsMu.Unlock()

	snapshot, err := c.snapshotPersistState(ctx)
	assert.NilError(t, err)
	selected := make(map[sharedResultID]struct{}, len(snapshot.results))
	for _, result := range snapshot.results {
		selected[result.resultID] = struct{}{}
	}
	_, keptDepSelected := selected[keptDep.cacheSharedResult().id]
	_, keptRootSelected := selected[keptRoot.cacheSharedResult().id]
	_, dirtyDepSelected := selected[dirtyDep.cacheSharedResult().id]
	_, droppedRootSelected := selected[droppedRoot.cacheSharedResult().id]
	_, sessionOnlySelected := selected[sessionOnly.cacheSharedResult().id]
	assert.Assert(t, keptDepSelected)
	assert.Assert(t, keptRootSelected)
	assert.Assert(t, !dirtyDepSelected)
	assert.Assert(t, !droppedRootSelected)
	assert.Assert(t, !sessionOnlySelected)
	assert.Equal(t, 1, len(snapshot.persistedEdges))
	assert.Equal(t, int64(keptRoot.cacheSharedResult().id), snapshot.persistedEdges[0].ResultID)

	assert.NilError(t, c.applyPersistStateSnapshot(ctx, snapshot))
	rows, err := c.sqlDB.QueryContext(ctx, `PRAGMA foreign_key_check`)
	assert.NilError(t, err)
	assert.Assert(t, !rows.Next(), "persisted mirror has a dangling foreign key")
	assert.NilError(t, rows.Err())
	assert.NilError(t, rows.Close())

	for _, droppedID := range []sharedResultID{
		dirtyDep.cacheSharedResult().id,
		droppedRoot.cacheSharedResult().id,
		sessionOnly.cacheSharedResult().id,
	} {
		for _, table := range []struct {
			name   string
			column string
		}{
			{name: "results", column: "id"},
			{name: "persisted_edges", column: "result_id"},
			{name: "result_output_eq_classes", column: "result_id"},
			{name: "result_snapshot_links", column: "result_id"},
			{name: "result_deps", column: "parent_result_id"},
			{name: "result_deps", column: "dep_result_id"},
		} {
			var count int
			err := c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table.name+` WHERE `+table.column+` = ?`, droppedID).Scan(&count)
			assert.NilError(t, err)
			assert.Equal(t, 0, count, table.name+"."+table.column)
		}
	}

	for table, want := range map[string]int{
		"eq_classes":               2,
		"eq_class_digests":         2,
		"terms":                    2,
		"term_inputs":              1,
		"results":                  2,
		"persisted_edges":          1,
		"result_output_eq_classes": 2,
		"result_deps":              1,
		"result_snapshot_links":    0,
	} {
		var count int
		err := c.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count)
		assert.NilError(t, err)
		assert.Equal(t, want, count, table)
	}

	assert.NilError(t, c.Close(context.Background()))
	restarted, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, restarted.Close(context.Background()))
	}()
	restarted.egraphMu.RLock()
	assert.Equal(t, 2, len(restarted.resultsByID))
	assert.Assert(t, restarted.resultsByID[keptRoot.cacheSharedResult().id] != nil)
	assert.Assert(t, restarted.resultsByID[keptDep.cacheSharedResult().id] != nil)
	assert.Assert(t, restarted.resultsByID[droppedRoot.cacheSharedResult().id] == nil)
	assert.Assert(t, restarted.resultsByID[dirtyDep.cacheSharedResult().id] == nil)
	assert.Assert(t, restarted.resultsByID[sessionOnly.cacheSharedResult().id] == nil)
	restarted.egraphMu.RUnlock()
	assertCacheOwnershipExact(t, restarted)
}

func TestCachePersistenceCleanShutdownToggleOnClose(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheIface, err := NewCache(ctx, dbPath, nil, nil)
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
