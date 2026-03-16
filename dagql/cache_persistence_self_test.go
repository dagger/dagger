package dagql

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql/call"
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
	baseCacheIface, err := NewCache(t.Context(), "")
	assert.NilError(t, err)
	baseCache := baseCacheIface.(*cache)
	srv := NewServer(&persistCodecRoot{}, NewSessionCache(baseCache))
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	rootObjType, ok := srv.ObjectType("Query")
	assert.Assert(t, ok)
	_, ok = rootObjType.(Class[*persistCodecRoot])
	assert.Assert(t, ok)
	Fields[*persistCodecRoot]{
		NodeFunc("obj", func(ctx context.Context, _ ObjectResult[*persistCodecRoot], _ struct{}) (ObjectResult[*persistCodecObj], error) {
			return NewObjectResultForCurrentID(ctx, srv, &persistCodecObj{Name: "x"})
		}),
	}.Install(srv)

	ctx := ContextWithID(t.Context(), cacheTestID("persist-codec-root"))
	ctx = srvToContext(ctx, srv)
	return ctx
}

func TestPersistedSelfCodecScalarRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)

	original, err := NewResultForID(String("hello"), cacheTestID("persist-scalar"))
	assert.NilError(t, err)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, original)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindScalar))

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, CurrentDagqlServer(ctx), 0, original.ID(), env)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(decoded.Unwrap(), Typed(String("hello"))))
	assert.Check(t, is.Equal(decoded.ID().Digest().String(), original.ID().Digest().String()))
}

func TestPersistedSelfCodecObjectIDRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)
	srv := CurrentDagqlServer(ctx)
	assert.Assert(t, srv != nil)

	original, err := srv.root.Select(ctx, srv, Selector{Field: "obj"})
	assert.NilError(t, err)
	assert.Assert(t, original != nil)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, original)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindObject))
	assert.Check(t, is.Equal(string(env.ObjectJSON), `{"name":"x"}`))

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, original.ID(), env)
	assert.NilError(t, err)
	assert.Assert(t, decoded != nil)
	assert.Check(t, is.Equal(decoded.ID().Digest().String(), original.ID().Digest().String()))
}

func TestPersistedSelfCodecNestedListRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)

	intA, err := NewResultForID(Int(1), cacheTestID("persist-list-int-a"))
	assert.NilError(t, err)
	intB, err := NewResultForID(Int(2), cacheTestID("persist-list-int-b"))
	assert.NilError(t, err)

	innerAVal := DynamicResultArrayOutput{
		Elem:   Int(0),
		Values: []AnyResult{intA},
	}
	innerBVal := DynamicResultArrayOutput{
		Elem:   Int(0),
		Values: []AnyResult{intB},
	}

	innerA, err := NewResultForID(innerAVal, cacheTestID("persist-list-inner-a"))
	assert.NilError(t, err)
	innerB, err := NewResultForID(innerBVal, cacheTestID("persist-list-inner-b"))
	assert.NilError(t, err)

	outerVal := DynamicResultArrayOutput{
		Elem:   innerAVal,
		Values: []AnyResult{innerA, innerB},
	}
	outer, err := NewResultForID(outerVal, call.New().Append(outerVal.Type(), "persist-list-outer"))
	assert.NilError(t, err)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, outer)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindList))
	assert.Check(t, is.Equal(len(env.Items), 2))

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, CurrentDagqlServer(ctx), 0, outer.ID(), env)
	assert.NilError(t, err)
	assert.Assert(t, decoded != nil)
	assert.Check(t, is.Equal(decoded.ID().Digest().String(), outer.ID().Digest().String()))

	list, ok := decoded.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Check(t, is.Equal(list.Len(), 2))
}

func TestPersistedSelfCodecIgnoresEnvelopeIDsDuringDecode(t *testing.T) {
	t.Parallel()
	ctx := setupPersistCodecTest(t)

	intA, err := NewResultForID(Int(1), cacheTestID("persist-ignore-env-int-a"))
	assert.NilError(t, err)
	intB, err := NewResultForID(Int(2), cacheTestID("persist-ignore-env-int-b"))
	assert.NilError(t, err)

	outerVal := DynamicResultArrayOutput{
		Elem:   Int(0),
		Values: []AnyResult{intA, intB},
	}
	outer, err := NewResultForID(outerVal, call.New().Append(outerVal.Type(), "persist-ignore-env-outer"))
	assert.NilError(t, err)

	env, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, outer)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(env.Kind, persistedResultKindList))
	assert.Check(t, is.Equal(len(env.Items), 2))

	wrongOuterID, err := cacheTestID("persist-ignore-env-wrong-outer").Encode()
	assert.NilError(t, err)
	wrongItemAID, err := cacheTestID("persist-ignore-env-wrong-item-a").Encode()
	assert.NilError(t, err)
	wrongItemBID, err := cacheTestID("persist-ignore-env-wrong-item-b").Encode()
	assert.NilError(t, err)

	env.ID = wrongOuterID
	env.Items[0].ID = wrongItemAID
	env.Items[1].ID = wrongItemBID

	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, CurrentDagqlServer(ctx), 0, outer.ID(), env)
	assert.NilError(t, err)
	assert.Assert(t, decoded != nil)
	assert.Check(t, is.Equal(decoded.ID().Digest().String(), outer.ID().Digest().String()))

	first, err := decoded.NthValue(1)
	assert.NilError(t, err)
	assert.Assert(t, first != nil)
	assert.Check(t, is.Equal(first.ID().Digest().String(), outer.ID().SelectNth(1).Digest().String()))

	second, err := decoded.NthValue(2)
	assert.NilError(t, err)
	assert.Assert(t, second != nil)
	assert.Check(t, is.Equal(second.ID().Digest().String(), outer.ID().SelectNth(2).Digest().String()))
}
