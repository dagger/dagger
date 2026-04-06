package dagql

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
	"gotest.tools/v3/assert"
)

func newPersistCodecImportTestServer(cache *Cache) *Server {
	srv, err := NewServer(context.Background(), &persistCodecRoot{})
	if err != nil {
		panic(err)
	}
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	Fields[*persistCodecRoot]{
		NodeFunc("obj", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			return NewObjectResultForCurrentCall(ctx, srv, &persistCodecObj{Name: "x"})
		}).IsPersistable(),
	}.Install(srv)
	return srv
}

func TestCachePersistenceImportRoundTripAcrossRestart(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cA := cacheA

	key := cacheTestIntCall("persist-import-roundtrip")
	resA, err := cA.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 123), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	cacheTestReleaseSession(t, cA, ctx)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cB := cacheB
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	resB, err := cB.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return nil, errors.New("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 123, cacheTestUnwrapInt(t, resB))
	cacheTestReleaseSession(t, cB, ctx)
}

func TestCachePersistenceImportRoundTripObjectResult(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cA := cacheA
	srvA := newPersistCodecImportTestServer(cacheA)

	rootCtxA := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-object-root",
	})
	rootCtxA = ContextWithCache(rootCtxA, cacheA)
	rootCtxA = srvToContext(rootCtxA, srvA)

	resA, err := srvA.root.Select(rootCtxA, srvA, Selector{Field: "obj"})
	assert.NilError(t, err)
	assert.Assert(t, resA != nil)
	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cB := cacheB
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistCodecImportTestServer(cacheB)

	rootCtxB := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-object-root",
	})
	rootCtxB = ContextWithCache(rootCtxB, cacheB)
	rootCtxB = srvToContext(rootCtxB, srvB)

	resB, err := srvB.root.Select(rootCtxB, srvB, Selector{Field: "obj"})
	assert.NilError(t, err)
	assert.Assert(t, resB != nil)
	assert.Assert(t, resB.HitCache())
	obj, ok := UnwrapAs[*persistCodecObj](resB.Unwrap())
	assert.Assert(t, ok)
	assert.Equal(t, "x", obj.Name)
	cacheTestReleaseSession(t, cacheB, rootCtxB)
}

func TestCachePersistenceImportedObjectHitWithoutServerErrors(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cA := cacheA
	srvA := newPersistCodecImportTestServer(cacheA)

	rootCtxA := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-object-root",
	})
	rootCtxA = ContextWithCache(rootCtxA, cacheA)
	rootCtxA = srvToContext(rootCtxA, srvA)

	resA, err := srvA.root.Select(rootCtxA, srvA, Selector{Field: "obj"})
	assert.NilError(t, err)
	assert.Assert(t, resA != nil)

	reqCall, err := resA.ResultCall()
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cB := cacheB
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	initCalls := 0
	_, err = cB.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, errors.New("unexpected initializer call")
	})
	assert.Assert(t, err != nil)
	assert.Equal(t, 0, initCalls)
	assert.Assert(t, strings.Contains(err.Error(), "decode persisted hit payload"))
}

func TestCachePersistenceUncleanMarkerWipesStore(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cA := cacheA

	key := cacheTestIntCall("persist-import-unclean-wipe")
	_, err = cA.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 7), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, cA, ctx)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	db, q, err := prepareCacheDBs(ctx, dbPath)
	assert.NilError(t, err)
	assert.NilError(t, q.UpsertMeta(ctx, persistdb.MetaKeyCleanShutdown, "0"))
	assert.NilError(t, closeCacheDBs(db, q))

	cacheB, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cB := cacheB
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	resB, err := cB.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 8), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 8, cacheTestUnwrapInt(t, resB))
	cacheTestReleaseSession(t, cB, ctx)
}

func TestCachePersistenceImportFailureWipesStore(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cA := cacheA

	key := cacheTestIntCall("persist-import-corrupt-wipe")
	_, err = cA.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 50), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, cA, ctx)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	db, q, err := prepareCacheDBs(ctx, dbPath)
	assert.NilError(t, err)
	_, err = db.Exec(`UPDATE results SET self_payload = x'7B6E6F742D6A736F6E'`)
	assert.NilError(t, err)
	assert.NilError(t, q.UpsertMeta(ctx, persistdb.MetaKeyCleanShutdown, "1"))
	assert.NilError(t, closeCacheDBs(db, q))

	cacheB, err := NewCache(ctx, dbPath, nil)
	assert.NilError(t, err)
	cB := cacheB
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	resB, err := cB.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 51), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 51, cacheTestUnwrapInt(t, resB))
	cacheTestReleaseSession(t, cB, ctx)
}
