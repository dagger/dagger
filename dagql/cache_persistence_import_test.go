package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
)

type persistConcurrentDecodeObj struct {
	Name string
}

type persistedPersistConcurrentDecodeObj struct {
	Name string `json:"name"`
}

type persistConcurrentDecodeHook struct {
	active        atomic.Int32
	firstEntered  chan struct{}
	allowFirst    chan struct{}
	secondEntered chan struct{}
}

var persistConcurrentDecodeHooks sync.Map

func (*persistConcurrentDecodeObj) Type() *ast.Type {
	return &ast.Type{
		NamedType: "PersistConcurrentDecodeObj",
		NonNull:   true,
	}
}

func (obj *persistConcurrentDecodeObj) EncodePersistedObject(ctx context.Context, cache PersistedObjectCache) (PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	payload, err := json.Marshal(persistedPersistConcurrentDecodeObj{Name: obj.Name})
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	return PersistedObjectEncoding{JSON: payload}, nil
}

func (*persistConcurrentDecodeObj) DecodePersistedObject(ctx context.Context, dag *Server, resultID uint64, _ *ResultCall, payload json.RawMessage) (Typed, error) {
	_ = dag
	var persisted persistedPersistConcurrentDecodeObj
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, err
	}

	if hookAny, ok := persistConcurrentDecodeHooks.Load(resultID); ok {
		hook := hookAny.(*persistConcurrentDecodeHook)
		switch hook.active.Add(1) {
		case 1:
			close(hook.firstEntered)
			select {
			case <-hook.allowFirst:
			case <-ctx.Done():
				hook.active.Add(-1)
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
				hook.active.Add(-1)
				return nil, fmt.Errorf("first decode was never released for result %d", resultID)
			}
		case 2:
			close(hook.secondEntered)
			hook.active.Add(-1)
			return nil, fmt.Errorf("concurrent decode for result %d", resultID)
		default:
			hook.active.Add(-1)
			return nil, fmt.Errorf("unexpected concurrent decode count for result %d", resultID)
		}
		hook.active.Add(-1)
	}

	return &persistConcurrentDecodeObj{Name: persisted.Name}, nil
}

func newPersistCodecImportTestServer() *Server {
	srv, err := NewServer(context.Background(), &persistCodecRoot{})
	if err != nil {
		panic(err)
	}
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	Fields[*persistCodecObj]{
		Func("name", func(ctx context.Context, self *persistCodecObj, _ struct{}) (String, error) {
			return String(self.Name), nil
		}),
	}.Install(srv)
	Fields[*persistCodecRoot]{
		NodeFunc("obj", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			return newPersistCodecImportTestResult(ctx, srv)
		}).IsPersistable(),
		NodeFunc("objCanonical", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			return newPersistCodecImportTestResult(ctx, srv)
		}).IsPersistable(),
		NodeFunc("objInner", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			return newPersistCodecImportTestResult(ctx, srv)
		}),
		NodeFunc("objAlias", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			var obj ObjectResult[*persistCodecObj]
			err := srv.Select(ctx, srv.root, &obj, Selector{Field: "objInner"})
			return obj, err
		}),
	}.Install(srv)
	return srv
}

func newPersistConcurrentDecodeTestServer() *Server {
	srv, err := NewServer(context.Background(), &persistCodecRoot{})
	if err != nil {
		panic(err)
	}
	srv.InstallObject(NewClass(srv, ClassOpts[*persistConcurrentDecodeObj]{}))
	Fields[*persistCodecRoot]{
		NodeFunc("objConcurrentDecode", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistConcurrentDecodeObj], error) {
			obj, err := NewObjectResultForCurrentCall(ctx, srv, &persistConcurrentDecodeObj{Name: "x"})
			if err != nil {
				return ObjectResult[*persistConcurrentDecodeObj]{}, err
			}
			return obj, nil
		}).IsPersistable(),
	}.Install(srv)
	return srv
}

func newPersistCodecImportTestResult(ctx context.Context, srv *Server) (ObjectResult[*persistCodecObj], error) {
	obj, err := NewObjectResultForCurrentCall(ctx, srv, &persistCodecObj{Name: "x"})
	if err != nil {
		return ObjectResult[*persistCodecObj]{}, err
	}
	return obj.WithContentDigest(ctx, digest.FromString("persist-codec-shared-object"))
}

func TestCachePersistenceImportRoundTripAcrossRestart(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
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

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetNone, cB.PersistenceResetReason())
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

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cA := cacheA
	srvA := newPersistCodecImportTestServer()

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

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetNone, cB.PersistenceResetReason())
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistCodecImportTestServer()

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

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cA := cacheA
	srvA := newPersistCodecImportTestServer()

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
	resultID := uint64(resA.cacheSharedResult().id)
	assert.Assert(t, resultID != 0)

	reqCall, err := resA.ResultCall()
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetNone, cB.PersistenceResetReason())
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	loadServer := cacheTestServer(t)
	routes := []struct {
		name      string
		sessionID string
		load      func(string) error
	}{
		{
			name:      "request lookup",
			sessionID: "rollback-request",
			load: func(sessionID string) error {
				_, err := cB.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
					return nil, errors.New("unexpected initializer call")
				})
				return err
			},
		},
		{
			name:      "digest lookup",
			sessionID: "rollback-digest",
			load: func(sessionID string) error {
				_, _, err := cB.lookupCacheForDigests(ctx, sessionID, noopTypeResolver{}, cacheTestCallDigest(reqCall), nil)
				return err
			},
		},
		{
			name:      "result ID lookup",
			sessionID: "rollback-result-id",
			load: func(sessionID string) error {
				_, err := cB.LoadResultByResultID(ctx, sessionID, loadServer, resultID)
				return err
			},
		},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			for range 2 {
				err := route.load(route.sessionID)
				assert.Assert(t, err != nil)
				assert.Assert(t, strings.Contains(err.Error(), "decode persisted hit payload"))
			}
			cB.sessionMu.Lock()
			_, recorded := cB.sessionResultIDsBySession[route.sessionID][sharedResultID(resultID)]
			cB.sessionMu.Unlock()
			assert.Assert(t, recorded, "failed read removed its session edge")
			assertCacheOwnershipExact(t, cB)
			assert.NilError(t, cB.ReleaseSession(ctx, route.sessionID))
			assertCacheOwnershipExact(t, cB)
		})
	}
	assertCacheDerivedIndexesConsistent(t, cB)
}

func TestCachePersistenceImportedObjectAliasSupportsChainedSelect(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	srvA := newPersistCodecImportTestServer()

	rootCtxA := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-object-alias-root",
	})
	rootCtxA = ContextWithCache(rootCtxA, cacheA)
	rootCtxA = srvToContext(rootCtxA, srvA)

	var seed ObjectResult[*persistCodecObj]
	err = srvA.Select(rootCtxA, srvA.root, &seed, Selector{Field: "objCanonical"})
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cacheA.persistCurrentState(ctx))
	assert.NilError(t, cacheA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, cacheB.Close(context.Background()))
	}()
	srvB := newPersistCodecImportTestServer()

	rootCtxB := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-object-alias-root",
	})
	rootCtxB = ContextWithCache(rootCtxB, cacheB)
	rootCtxB = srvToContext(rootCtxB, srvB)

	var name String
	err = srvB.Select(rootCtxB, srvB.root, &name,
		Selector{Field: "objAlias"},
		Selector{Field: "name"},
	)
	assert.NilError(t, err)
	assert.Equal(t, String("x"), name)

	cacheTestReleaseSession(t, cacheB, rootCtxB)
}

func TestCachePersistenceImportedObjectLoadSerializesPersistedDecode(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cA := cacheA
	srvA := newPersistConcurrentDecodeTestServer()

	rootCtxA := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-concurrent-decode-root",
	})
	rootCtxA = ContextWithCache(rootCtxA, cacheA)
	rootCtxA = srvToContext(rootCtxA, srvA)

	var seed ObjectResult[*persistConcurrentDecodeObj]
	err = srvA.Select(rootCtxA, srvA.root, &seed, Selector{Field: "objConcurrentDecode"})
	assert.NilError(t, err)
	assert.Assert(t, seed.cacheSharedResult() != nil)
	resultID := uint64(seed.cacheSharedResult().id)
	assert.Assert(t, resultID != 0)

	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetNone, cB.PersistenceResetReason())
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistConcurrentDecodeTestServer()

	hook := &persistConcurrentDecodeHook{
		firstEntered:  make(chan struct{}),
		allowFirst:    make(chan struct{}),
		secondEntered: make(chan struct{}),
	}
	persistConcurrentDecodeHooks.Store(resultID, hook)
	defer persistConcurrentDecodeHooks.Delete(resultID)

	loadCtx := func(sessionID string) context.Context {
		loadCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  sessionID + "-client",
			SessionID: sessionID,
		})
		loadCtx = ContextWithCache(loadCtx, cB)
		return srvToContext(loadCtx, srvB)
	}

	type loadResult struct {
		ctx context.Context
		err error
	}
	firstResultCh := make(chan loadResult, 1)
	secondResultCh := make(chan loadResult, 1)

	const firstSessionID = "persist-concurrent-decode-session-a"
	const secondSessionID = "persist-concurrent-decode-session-b"

	firstCtx := loadCtx(firstSessionID)
	go func() {
		_, err := cB.LoadResultByResultID(firstCtx, firstSessionID, srvB, resultID)
		firstResultCh <- loadResult{ctx: firstCtx, err: err}
	}()

	select {
	case <-hook.firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first persisted decode entry")
	}

	secondCtx := loadCtx(secondSessionID)
	go func() {
		_, err := cB.LoadResultByResultID(secondCtx, secondSessionID, srvB, resultID)
		secondResultCh <- loadResult{ctx: secondCtx, err: err}
	}()

	select {
	case <-hook.secondEntered:
	case <-time.After(50 * time.Millisecond):
	}
	close(hook.allowFirst)

	firstResult := <-firstResultCh
	secondResult := <-secondResultCh

	assert.NilError(t, cB.ReleaseSession(firstResult.ctx, firstSessionID))
	assert.NilError(t, cB.ReleaseSession(secondResult.ctx, secondSessionID))

	assert.NilError(t, firstResult.err)
	assert.NilError(t, secondResult.err)
}

func TestCachePersistenceUncleanMarkerWipesStore(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
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

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetUncleanShutdown, cB.PersistenceResetReason())
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

func TestCachePersistenceSchemaMismatchWipesStore(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cA := cacheA

	key := cacheTestIntCall("persist-import-schema-mismatch-wipe")
	_, err = cA.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 70), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, cA, ctx)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	db, q, err := prepareCacheDBs(ctx, dbPath)
	assert.NilError(t, err)
	assert.NilError(t, q.UpsertMeta(ctx, persistdb.MetaKeySchemaVersion, "old-schema"))
	assert.NilError(t, q.UpsertMeta(ctx, persistdb.MetaKeyCleanShutdown, "1"))
	assert.NilError(t, closeCacheDBs(db, q))

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetSchemaMismatch, cB.PersistenceResetReason())
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()

	resB, err := cB.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    key,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 71), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 71, cacheTestUnwrapInt(t, resB))
	cacheTestReleaseSession(t, cB, ctx)
}

func TestCacheCloseDiscardingPersistenceDoesNotMarkClean(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	assert.NilError(t, cacheA.CloseDiscardingPersistence())

	db, q, err := prepareCacheDBs(ctx, dbPath)
	assert.NilError(t, err)
	cleanShutdownVal, found, err := q.SelectMetaValue(ctx, persistdb.MetaKeyCleanShutdown)
	assert.NilError(t, err)
	assert.Assert(t, found)
	assert.Equal(t, "0", cleanShutdownVal)
	assert.NilError(t, closeCacheDBs(db, q))
}

func TestCachePersistenceImportFailureWipesStore(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheA, err := NewCache(ctx, dbPath, nil, nil)
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

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	cB := cacheB
	assert.Equal(t, CachePersistenceResetImportFailure, cB.PersistenceResetReason())
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

// persistRetryDecodeObj is a persistable object whose decode and owner-lease
// links are test-controlled: persistRetryDecodeHooks may block or fail a
// specific result's decode, and persistRetryDecodeSnapshotID names the
// snapshot ref the object reports, so flipping it between persist and reload
// forces the decode-time lease sync to do real remove/attach work.
type persistRetryDecodeObj struct {
	Name string
}

type persistedPersistRetryDecodeObj struct {
	Name string `json:"name"`
}

// map[uint64]func(context.Context) error, keyed by result ID; a non-nil
// error fails the decode.
var persistRetryDecodeHooks sync.Map

var persistRetryDecodeSnapshotID atomic.Value // string; "" means no link

func (*persistRetryDecodeObj) Type() *ast.Type {
	return &ast.Type{
		NamedType: "PersistRetryDecodeObj",
		NonNull:   true,
	}
}

func (obj *persistRetryDecodeObj) EncodePersistedObject(ctx context.Context, cache PersistedObjectCache) (PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	payload, err := json.Marshal(persistedPersistRetryDecodeObj{Name: obj.Name})
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	return PersistedObjectEncoding{
		JSON:          payload,
		SnapshotLinks: obj.PersistedSnapshotRefLinks(),
	}, nil
}

func (*persistRetryDecodeObj) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	snapshotID, _ := persistRetryDecodeSnapshotID.Load().(string)
	if snapshotID == "" {
		return nil
	}
	return []PersistedSnapshotRefLink{{
		RefKey: snapshotID,
		Role:   "snapshot",
	}}
}

func (*persistRetryDecodeObj) DecodePersistedObject(ctx context.Context, dag *Server, resultID uint64, _ *ResultCall, payload json.RawMessage) (Typed, error) {
	_ = dag
	var persisted persistedPersistRetryDecodeObj
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, err
	}
	if hookAny, ok := persistRetryDecodeHooks.Load(resultID); ok {
		if err := hookAny.(func(context.Context) error)(ctx); err != nil {
			return nil, err
		}
	}
	return &persistRetryDecodeObj{Name: persisted.Name}, nil
}

func newPersistRetryDecodeTestServer() *Server {
	srv, err := NewServer(context.Background(), &persistCodecRoot{})
	if err != nil {
		panic(err)
	}
	srv.InstallObject(NewClass(srv, ClassOpts[*persistRetryDecodeObj]{}))
	Fields[*persistCodecRoot]{
		NodeFunc("objRetryDecode", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistRetryDecodeObj], error) {
			obj, err := NewObjectResultForCurrentCall(ctx, srv, &persistRetryDecodeObj{Name: "x"})
			if err != nil {
				return ObjectResult[*persistRetryDecodeObj]{}, err
			}
			return obj, nil
		}).IsPersistable(),
	}.Install(srv)
	return srv
}

// persistRetryDecodeSeed publishes one persistRetryDecodeObj result on a
// fresh cache at dbPath, persists it, closes the cache, and returns the
// result ID for a reload on a second cache.
func persistRetryDecodeSeed(t *testing.T, ctx context.Context, dbPath string, snapshotManager bkcache.SnapshotManager) uint64 {
	t.Helper()
	cacheA, err := NewCache(ctx, dbPath, snapshotManager, nil)
	assert.NilError(t, err)
	srvA := newPersistRetryDecodeTestServer()

	rootCtxA := ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-import-retry-decode-root",
	})
	rootCtxA = ContextWithCache(rootCtxA, cacheA)
	rootCtxA = srvToContext(rootCtxA, srvA)

	var seed ObjectResult[*persistRetryDecodeObj]
	err = srvA.Select(rootCtxA, srvA.root, &seed, Selector{Field: "objRetryDecode"})
	assert.NilError(t, err)
	assert.Assert(t, seed.cacheSharedResult() != nil)
	resultID := uint64(seed.cacheSharedResult().id)
	assert.Assert(t, resultID != 0)

	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cacheA.persistCurrentState(ctx))
	assert.NilError(t, cacheA.Close(context.Background()))
	return resultID
}

// A decode leader whose own context is canceled mid-decode must not fail a
// parked joiner: the joiner classifies the latched error as the departed
// leader's own cancellation and retries, leading a fresh decode.
func TestCachePersistenceImportedDecodeLeaderCancelRetriesJoiner(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	persistRetryDecodeSnapshotID.Store("")
	resultID := persistRetryDecodeSeed(t, ctx, dbPath, nil)

	cB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistRetryDecodeTestServer()

	var decodeEntries atomic.Int32
	firstEntered := make(chan struct{})
	persistRetryDecodeHooks.Store(resultID, func(hookCtx context.Context) error {
		if decodeEntries.Add(1) == 1 {
			close(firstEntered)
			select {
			case <-hookCtx.Done():
				return hookCtx.Err()
			case <-time.After(10 * time.Second):
				return fmt.Errorf("first decode was never canceled for result %d", resultID)
			}
		}
		return nil
	})
	defer persistRetryDecodeHooks.Delete(resultID)

	loadCtx := func(sessionID string) context.Context {
		loadCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  sessionID + "-client",
			SessionID: sessionID,
		})
		loadCtx = ContextWithCache(loadCtx, cB)
		return srvToContext(loadCtx, srvB)
	}

	const leaderSessionID = "persist-decode-cancel-session-a"
	const joinerSessionID = "persist-decode-cancel-session-b"

	leaderCtx, cancelLeader := context.WithCancel(loadCtx(leaderSessionID))
	defer cancelLeader()
	leaderCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(leaderCtx, leaderSessionID, srvB, resultID)
		leaderCh <- err
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the leader to enter the persisted decode")
	}

	joinerCtx := loadCtx(joinerSessionID)
	joinerCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(joinerCtx, joinerSessionID, srvB, resultID)
		joinerCh <- err
	}()
	time.Sleep(100 * time.Millisecond) // let the joiner park on the decode channel
	cancelLeader()

	leaderErr := <-leaderCh
	joinerErr := <-joinerCh
	assert.ErrorContains(t, leaderErr, "context canceled")
	assert.NilError(t, joinerErr)
	assert.Equal(t, decodeEntries.Load(), int32(2))

	assert.NilError(t, cB.ReleaseSession(joinerCtx, joinerSessionID))
	assert.NilError(t, cB.ReleaseSession(loadCtx(leaderSessionID), leaderSessionID))
}

// A leader canceled inside the post-install owner-lease sync must not leave
// the lease set silently unsynchronized: the next demand retries just the
// sync (the payload is decoded exactly once) and the desired lease is
// attached, instead of serving the value on the fast path.
func TestCachePersistenceImportedDecodeLeaseSyncCancelRetriesSync(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	persistRetryDecodeSnapshotID.Store("persist-decode-sync-snap-old")
	resultID := persistRetryDecodeSeed(t, ctx, dbPath, &fakeSnapshotManager{})
	// The reloaded object reports a different snapshot ref than the stored
	// link rows, so the decode-time sync must remove the old lease and
	// attach the new one.
	persistRetryDecodeSnapshotID.Store("persist-decode-sync-snap-new")

	managerB := &fakeSnapshotManager{}
	cB, err := NewCache(ctx, dbPath, managerB, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistRetryDecodeTestServer()
	// Arm the attach block only after NewCache: the import itself attaches
	// the stored snap-old owner lease, and that call must not park.
	managerB.attachStarted = make(chan struct{})
	managerB.allowAttach = make(chan struct{})

	var decodeEntries atomic.Int32
	persistRetryDecodeHooks.Store(resultID, func(context.Context) error {
		decodeEntries.Add(1)
		return nil
	})
	defer persistRetryDecodeHooks.Delete(resultID)

	loadCtx := func(sessionID string) context.Context {
		loadCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  sessionID + "-client",
			SessionID: sessionID,
		})
		loadCtx = ContextWithCache(loadCtx, cB)
		return srvToContext(loadCtx, srvB)
	}

	const leaderSessionID = "persist-decode-sync-session-a"
	const readerSessionID = "persist-decode-sync-session-b"

	leaderCtx, cancelLeader := context.WithCancel(loadCtx(leaderSessionID))
	defer cancelLeader()
	leaderCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(leaderCtx, leaderSessionID, srvB, resultID)
		leaderCh <- err
	}()
	select {
	case <-managerB.attachStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the leader to block in AttachLease")
	}
	cancelLeader()

	// The leader failed inside the sync after installing the payload, so
	// the pending-sync flag is latched by the time its error returns.
	leaderErr := <-leaderCh
	assert.ErrorContains(t, leaderErr, "sync persisted hit owner leases")
	assert.ErrorContains(t, leaderErr, "context canceled")

	// A later demand must not serve the value on the fast path with the
	// lease set unsynchronized: it leads a sync-only attempt first.
	readerCtx := loadCtx(readerSessionID)
	_, readerErr := cB.LoadResultByResultID(readerCtx, readerSessionID, srvB, resultID)
	assert.NilError(t, readerErr)
	// The payload was decoded once; the retry ran only the lease sync.
	assert.Equal(t, decodeEntries.Load(), int32(1))
	// Attaches: the import attached the stored snap-old lease; the leader's
	// canceled attach recorded nothing; the retry attached snap-new. Removes:
	// the failed attempt and the retry each removed the stale snap-old lease
	// (the reconciled link set is stored only on full success).
	assert.Equal(t, len(managerB.attachCalls), 2)
	assert.Equal(t, managerB.attachCalls[0].SnapshotID, "persist-decode-sync-snap-old")
	assert.Equal(t, managerB.attachCalls[1].SnapshotID, "persist-decode-sync-snap-new")
	assert.Equal(t, len(managerB.removeCalls), 2)

	assert.NilError(t, cB.ReleaseSession(readerCtx, readerSessionID))
	assert.NilError(t, cB.ReleaseSession(loadCtx(leaderSessionID), leaderSessionID))
}

// A joiner's own cancellation returns its own cause, never the attempt's
// outcome, and leaves the running attempt untouched.
func TestCachePersistenceImportedDecodeJoinerOwnCancelReturnsOwnCause(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	persistRetryDecodeSnapshotID.Store("")
	resultID := persistRetryDecodeSeed(t, ctx, dbPath, nil)

	cB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistRetryDecodeTestServer()

	var decodeEntries atomic.Int32
	firstEntered := make(chan struct{})
	allowFirst := make(chan struct{})
	persistRetryDecodeHooks.Store(resultID, func(hookCtx context.Context) error {
		if decodeEntries.Add(1) == 1 {
			close(firstEntered)
			select {
			case <-allowFirst:
				return nil
			case <-hookCtx.Done():
				return hookCtx.Err()
			case <-time.After(10 * time.Second):
				return fmt.Errorf("first decode was never released for result %d", resultID)
			}
		}
		return nil
	})
	defer persistRetryDecodeHooks.Delete(resultID)

	loadCtx := func(sessionID string) context.Context {
		loadCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  sessionID + "-client",
			SessionID: sessionID,
		})
		loadCtx = ContextWithCache(loadCtx, cB)
		return srvToContext(loadCtx, srvB)
	}

	const leaderSessionID = "persist-decode-joiner-cancel-session-a"
	const joinerSessionID = "persist-decode-joiner-cancel-session-b"

	leaderCtx := loadCtx(leaderSessionID)
	leaderCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(leaderCtx, leaderSessionID, srvB, resultID)
		leaderCh <- err
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the leader to enter the persisted decode")
	}

	joinerCtx, cancelJoiner := context.WithCancel(loadCtx(joinerSessionID))
	defer cancelJoiner()
	joinerCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(joinerCtx, joinerSessionID, srvB, resultID)
		joinerCh <- err
	}()
	time.Sleep(100 * time.Millisecond) // let the joiner park on the decode channel
	cancelJoiner()

	joinerErr := <-joinerCh
	assert.Assert(t, errors.Is(joinerErr, context.Canceled), "joiner error: %v", joinerErr)

	close(allowFirst)
	leaderErr := <-leaderCh
	assert.NilError(t, leaderErr)
	assert.Equal(t, decodeEntries.Load(), int32(1))

	assert.NilError(t, cB.ReleaseSession(leaderCtx, leaderSessionID))
	assert.NilError(t, cB.ReleaseSession(loadCtx(joinerSessionID), joinerSessionID))
}

// A genuine decode failure still propagates to every caller: no one
// classifies it as retryable and no one loops.
func TestCachePersistenceImportedDecodeFailurePropagatesToJoiners(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	persistRetryDecodeSnapshotID.Store("")
	resultID := persistRetryDecodeSeed(t, ctx, dbPath, nil)

	cB, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	defer func() {
		assert.NilError(t, cB.Close(context.Background()))
	}()
	srvB := newPersistRetryDecodeTestServer()

	var decodeEntries atomic.Int32
	firstEntered := make(chan struct{})
	allowFirst := make(chan struct{})
	persistRetryDecodeHooks.Store(resultID, func(hookCtx context.Context) error {
		if decodeEntries.Add(1) == 1 {
			close(firstEntered)
			select {
			case <-allowFirst:
			case <-time.After(10 * time.Second):
			}
		}
		return fmt.Errorf("persist decode genuine failure for result %d", resultID)
	})
	defer persistRetryDecodeHooks.Delete(resultID)

	loadCtx := func(sessionID string) context.Context {
		loadCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			ClientID:  sessionID + "-client",
			SessionID: sessionID,
		})
		loadCtx = ContextWithCache(loadCtx, cB)
		return srvToContext(loadCtx, srvB)
	}

	const leaderSessionID = "persist-decode-fail-session-a"
	const joinerSessionID = "persist-decode-fail-session-b"

	leaderCtx := loadCtx(leaderSessionID)
	leaderCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(leaderCtx, leaderSessionID, srvB, resultID)
		leaderCh <- err
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the leader to enter the persisted decode")
	}

	joinerCtx := loadCtx(joinerSessionID)
	joinerCh := make(chan error, 1)
	go func() {
		_, err := cB.LoadResultByResultID(joinerCtx, joinerSessionID, srvB, resultID)
		joinerCh <- err
	}()
	time.Sleep(100 * time.Millisecond) // let the joiner park on the decode channel
	close(allowFirst)

	leaderErr := <-leaderCh
	joinerErr := <-joinerCh
	assert.ErrorContains(t, leaderErr, "persist decode genuine failure")
	assert.ErrorContains(t, joinerErr, "persist decode genuine failure")

	assert.NilError(t, cB.ReleaseSession(leaderCtx, leaderSessionID))
	assert.NilError(t, cB.ReleaseSession(joinerCtx, joinerSessionID))
}
