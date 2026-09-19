package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine"
)

// persistDecodeOwnerObj models a module object: its persisted payload carries
// only its own fields, while the class template that decodes it supplies a
// result (the module) that the decoded payload keeps using afterwards.
type persistDecodeOwnerObj struct {
	Name   string
	module AnyResult
}

type persistedDecodeOwnerObj struct {
	Name string `json:"name"`
}

func (*persistDecodeOwnerObj) Type() *ast.Type {
	return &ast.Type{NamedType: "PersistDecodeOwnerObj", NonNull: true}
}

func (obj *persistDecodeOwnerObj) EncodePersistedObject(context.Context, PersistedObjectCache) (PersistedObjectEncoding, error) {
	payload, err := json.Marshal(persistedDecodeOwnerObj{Name: obj.Name})
	if err != nil {
		return PersistedObjectEncoding{}, err
	}
	return PersistedObjectEncoding{JSON: payload}, nil
}

// DecodePersistedObject runs on the decoding class's template, so the module
// comes from that template rather than from the persisted bytes.
func (obj *persistDecodeOwnerObj) DecodePersistedObject(_ context.Context, _ *Server, _ uint64, _ *ResultCall, payload json.RawMessage) (Typed, error) {
	var persisted persistedDecodeOwnerObj
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, err
	}
	return &persistDecodeOwnerObj{Name: persisted.Name, module: obj.module}, nil
}

func (obj *persistDecodeOwnerObj) DecodedDependencyResults() []AnyResult {
	if obj == nil || obj.module == nil {
		return nil
	}
	return []AnyResult{obj.module}
}

func newPersistDecodeOwnerTestServer(module AnyResult) *Server {
	srv, err := NewServer(context.Background(), &persistCodecRoot{})
	if err != nil {
		panic(err)
	}
	srv.InstallObject(NewClass(srv, ClassOpts[*persistDecodeOwnerObj]{Typed: &persistDecodeOwnerObj{module: module}}))
	Fields[*persistCodecRoot]{
		Func("owned", func(context.Context, *persistCodecRoot, struct{}) (*persistDecodeOwnerObj, error) {
			return &persistDecodeOwnerObj{Name: "single", module: module}, nil
		}).IsPersistable(),
		Func("ownedList", func(context.Context, *persistCodecRoot, struct{}) ([]*persistDecodeOwnerObj, error) {
			return []*persistDecodeOwnerObj{
				{Name: "first", module: module},
				{Name: "second", module: module},
			}, nil
		}).IsPersistable(),
	}.Install(srv)
	return srv
}

func persistDecodeOwnerContext(t *testing.T, cache *Cache, srv *Server, session string) context.Context {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  session,
		SessionID: session,
	})
	ctx = ContextWithCall(ctx, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-decode-owner-root",
	})
	ctx = ContextWithCache(ctx, cache)
	if srv != nil {
		ctx = srvToContext(ctx, srv)
	}
	t.Cleanup(func() { _ = cache.ReleaseSession(context.Background(), session) })
	return ctx
}

func persistDecodeOwnerModule(t *testing.T, ctx context.Context, cache *Cache, session, op string) AnyResult {
	t.Helper()
	frame := cacheTestIntCall(op)
	module, err := cache.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame}, ValueFunc(cacheTestIntResult(frame, 7)))
	assert.NilError(t, err)
	return module
}

// An imported row's decoded payload keeps the result its decoding class
// supplied, which only the decoding session acquired. The row must own that
// result from the moment the payload is served, and release it with the row.
func TestCachePersistenceDecodedPayloadOwnsDecoderSuppliedResults(t *testing.T) {
	for _, field := range []string{"owned", "ownedList"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			dbPath := filepath.Join(t.TempDir(), "cache.db")

			cacheA, err := NewCache(t.Context(), dbPath, nil, nil)
			assert.NilError(t, err)
			ctxA := persistDecodeOwnerContext(t, cacheA, nil, "producer")
			moduleA := persistDecodeOwnerModule(t, ctxA, cacheA, "producer", "decode-owner-module-a")
			srvA := newPersistDecodeOwnerTestServer(moduleA)
			ctxA = srvToContext(ctxA, srvA)
			resA, err := srvA.root.Select(ctxA, srvA, Selector{Field: field})
			assert.NilError(t, err)
			rowID := uint64(resA.cacheSharedResult().id)
			assert.Assert(t, rowID != 0)
			assert.NilError(t, cacheA.ReleaseSession(ctxA, "producer"))
			assert.NilError(t, cacheA.persistCurrentState(t.Context()))
			assert.NilError(t, cacheA.Close(context.Background()))

			cacheB, err := NewCache(t.Context(), dbPath, nil, nil)
			assert.NilError(t, err)
			t.Cleanup(func() { assert.NilError(t, cacheB.Close(context.Background())) })
			assert.Equal(t, CachePersistenceResetNone, cacheB.PersistenceResetReason())

			decoderCtx := persistDecodeOwnerContext(t, cacheB, nil, "decoder")
			moduleB := persistDecodeOwnerModule(t, decoderCtx, cacheB, "decoder", "decode-owner-module-b")
			moduleBID := uint64(moduleB.cacheSharedResult().id)
			srvB := newPersistDecodeOwnerTestServer(moduleB)
			decoderCtx = srvToContext(decoderCtx, srvB)

			// A template whose result is detached cannot be owned: the decode
			// must fail before installing the payload, so that a later demand
			// with a usable template decodes cleanly.
			detached := newPersistDecodeOwnerTestServer(cacheTestDetachedResult(cacheTestIntCall("decode-owner-detached"), NewInt(9)))
			_, err = cacheB.LoadResultByResultID(decoderCtx, "decoder", detached, rowID)
			assert.ErrorContains(t, err, "declares a detached")
			cacheB.egraphMu.RLock()
			row := cacheB.resultsByID[sharedResultID(rowID)]
			installed := row != nil && row.hasValue
			cacheB.egraphMu.RUnlock()
			assert.Assert(t, !installed, "a failed retention must not leave the payload installed")

			loaded, err := cacheB.LoadResultByResultID(decoderCtx, "decoder", srvB, rowID)
			assert.NilError(t, err)
			assertDecodeOwnerModules := func(t *testing.T, res AnyResult) {
				t.Helper()
				var objs []*persistDecodeOwnerObj
				if list, ok := UnwrapAs[DynamicResultArrayOutput](res); ok {
					for _, item := range list.Values {
						obj, ok := UnwrapAs[*persistDecodeOwnerObj](item)
						assert.Assert(t, ok)
						objs = append(objs, obj)
					}
				} else {
					obj, ok := UnwrapAs[*persistDecodeOwnerObj](res)
					assert.Assert(t, ok)
					objs = append(objs, obj)
				}
				assert.Assert(t, len(objs) > 0)
				for _, obj := range objs {
					assert.Equal(t, moduleBID, uint64(obj.module.cacheSharedResult().id), "the payload keeps the decoder's module")
				}
			}
			assertDecodeOwnerModules(t, loaded)
			cacheB.egraphMu.RLock()
			_, owns := cacheB.resultsByID[sharedResultID(rowID)].deps[sharedResultID(moduleBID)]
			cacheB.egraphMu.RUnlock()
			assert.Assert(t, owns, "the decoded row must own the decoder-supplied module")

			// The decoding session ends; the module outlives it through the row.
			assert.NilError(t, cacheB.ReleaseSession(decoderCtx, "decoder"))
			laterCtx := persistDecodeOwnerContext(t, cacheB, srvB, "later")
			_, err = cacheB.LoadResultByResultID(laterCtx, "later", srvB, moduleBID)
			assert.NilError(t, err, "the module must survive the decoding session")
			again, err := cacheB.LoadResultByResultID(laterCtx, "later", srvB, rowID)
			assert.NilError(t, err)
			assertDecodeOwnerModules(t, again)
			assert.NilError(t, cacheB.ReleaseSession(laterCtx, "later"))

			// Pruning the persisted row releases the module with it: the edge
			// is ordinary ownership, not a leak.
			_, err = cacheB.Prune(t.Context(), []CachePrunePolicy{{All: true}})
			assert.NilError(t, err)
			assert.Equal(t, 0, cacheB.Size())
		})
	}
}
