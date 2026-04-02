package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

type persistCodecRoot struct{}

func (*persistCodecRoot) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

type persistCodecObj struct {
	Name string
}

type persistedPersistCodecObj struct {
	Name string `json:"name"`
}

func (*persistCodecObj) Type() *ast.Type {
	return &ast.Type{
		NamedType: "PersistCodecObj",
		NonNull:   true,
	}
}

func (obj *persistCodecObj) EncodePersistedObject(ctx context.Context, cache PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = cache
	return json.Marshal(persistedPersistCodecObj{Name: obj.Name})
}

func (*persistCodecObj) DecodePersistedObject(ctx context.Context, dag *Server, _ uint64, _ *ResultCall, payload json.RawMessage) (Typed, error) {
	_ = ctx
	_ = dag
	var persisted persistedPersistCodecObj
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, err
	}
	return &persistCodecObj{Name: persisted.Name}, nil
}

func setupPersistCodecTest(t *testing.T) context.Context {
	t.Helper()
	baseCacheIface, err := NewCache(t.Context(), "", nil)
	assert.NilError(t, err)
	baseCache := baseCacheIface
	srv := NewServer(&persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	rootObjType, ok := srv.ObjectType("Query")
	assert.Assert(t, ok)
	_, ok = rootObjType.(Class[*persistCodecRoot])
	assert.Assert(t, ok)
	Fields[*persistCodecRoot]{
		NodeFunc("obj", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			return NewObjectResultForCurrentCall(ctx, srv, &persistCodecObj{Name: "x"})
		}),
	}.Install(srv)

	ctx := ContextWithCall(cacheTestContext(t.Context()), &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&persistCodecRoot{}).Type()),
		Field: "persist-codec-root",
	})
	ctx = ContextWithCache(ctx, baseCache)
	ctx = srvToContext(ctx, srv)
	return ctx
}

func TestPersistedSelfCodecScalarRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)

	originalCall := cacheTestIntCall("persist-scalar")
	original, err := NewResultForCall(String("hello"), originalCall)
	assert.NilError(t, err)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, original)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindScalar))

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, CurrentDagqlServer(ctx), 0, original.cacheSharedResult().resultCall.clone(), env)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(decoded.Unwrap(), Typed(String("hello"))))
	decodedRecipeEnc, err := cacheTestMustRecipeID(t, ctx, decoded).Encode()
	assert.NilError(t, err)
	originalRecipeEnc, err := cacheTestMustRecipeID(t, ctx, original).Encode()
	assert.NilError(t, err)
	assert.Check(t, is.Equal(decodedRecipeEnc, originalRecipeEnc))
}

func TestPersistedSelfCodecObjectIDRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)
	srv := CurrentDagqlServer(ctx)
	assert.Assert(t, srv != nil)

	var original AnyResult
	err := srv.Select(ctx, srv.root, &original, Selector{Field: "obj"})
	assert.NilError(t, err)
	assert.Assert(t, original != nil)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, original)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindObject))
	assert.Check(t, is.Equal(string(env.ObjectJSON), `{"name":"x"}`))

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, original.cacheSharedResult().resultCall.clone(), env)
	assert.NilError(t, err)
	assert.Assert(t, decoded != nil)
	decodedRecipeEnc, err := cacheTestMustRecipeID(t, ctx, decoded).Encode()
	assert.NilError(t, err)
	originalRecipeEnc, err := cacheTestMustRecipeID(t, ctx, original).Encode()
	assert.NilError(t, err)
	assert.Check(t, is.Equal(decodedRecipeEnc, originalRecipeEnc))
}

func TestObjectCacheHitPreservesObjectResultShape(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	cacheIface, err := NewCache(ctx, "", nil)
	assert.NilError(t, err)
	srv := NewServer(&persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))

	req := &CallRequest{
		ResultCall: &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&persistCodecObj{}).Type()),
			Field: "obj",
		},
	}

	first, err := cacheIface.GetOrInitCall(ctx, "test-session", srv, req, func(callCtx context.Context) (AnyResult, error) {
		return NewObjectResultForCurrentCall(callCtx, srv, &persistCodecObj{Name: "x"})
	})
	assert.NilError(t, err)
	assert.Assert(t, first != nil)
	_, ok := first.(ObjectResult[*persistCodecObj])
	assert.Assert(t, ok)
	assert.Assert(t, !first.HitCache())

	second, err := cacheIface.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		return nil, errors.New("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, second != nil)
	_, ok = second.(ObjectResult[*persistCodecObj])
	assert.Assert(t, ok)
	assert.Assert(t, second.HitCache())

	cacheTestReleaseSession(t, cacheIface, ctx)
}

func TestPersistedSelfCodecNestedListRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)

	intA, err := NewResultForCall(Int(1), cacheTestIntCall("persist-list-int-a"))
	assert.NilError(t, err)
	intB, err := NewResultForCall(Int(2), cacheTestIntCall("persist-list-int-b"))
	assert.NilError(t, err)

	innerAVal := DynamicResultArrayOutput{
		Elem:   Int(0),
		Values: []AnyResult{intA},
	}
	innerBVal := DynamicResultArrayOutput{
		Elem:   Int(0),
		Values: []AnyResult{intB},
	}

	innerA, err := NewResultForCall(innerAVal, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(innerAVal.Type()),
		Field: "persist-list-inner-a",
	})
	assert.NilError(t, err)
	innerB, err := NewResultForCall(innerBVal, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(innerBVal.Type()),
		Field: "persist-list-inner-b",
	})
	assert.NilError(t, err)

	outerVal := DynamicResultArrayOutput{
		Elem:   innerAVal,
		Values: []AnyResult{innerA, innerB},
	}
	outer, err := NewResultForCall(outerVal, &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(outerVal.Type()),
		Field: "persist-list-outer",
	})
	assert.NilError(t, err)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, outer)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindList))
	assert.Check(t, is.Equal(len(env.Items), 2))

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, CurrentDagqlServer(ctx), 0, outer.cacheSharedResult().resultCall.clone(), env)
	assert.NilError(t, err)
	assert.Assert(t, decoded != nil)
	decodedRecipeEnc, err := cacheTestMustRecipeID(t, ctx, decoded).Encode()
	assert.NilError(t, err)
	outerRecipeEnc, err := cacheTestMustRecipeID(t, ctx, outer).Encode()
	assert.NilError(t, err)
	assert.Check(t, is.Equal(decodedRecipeEnc, outerRecipeEnc))

	list, ok := decoded.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Check(t, is.Equal(list.Len(), 2))
}
