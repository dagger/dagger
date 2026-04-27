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

func (obj *persistConcurrentDecodeObj) EncodePersistedObject(ctx context.Context, cache PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	return json.Marshal(persistedPersistConcurrentDecodeObj{Name: obj.Name})
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

	reqCall, err := resA.ResultCall()
	assert.NilError(t, err)

	cacheTestReleaseSession(t, cacheA, rootCtxA)
	assert.NilError(t, cA.persistCurrentState(ctx))
	assert.NilError(t, cA.Close(context.Background()))

	cacheB, err := NewCache(ctx, dbPath, nil, nil)
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
