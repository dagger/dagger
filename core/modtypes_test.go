package core

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type nullableSDKInputRecorder struct {
	got dagql.Typed
}

func (r *nullableSDKInputRecorder) ConvertFromSDKResult(context.Context, any) (dagql.AnyResult, error) {
	return nil, nil
}

func (r *nullableSDKInputRecorder) ConvertToSDKInput(_ context.Context, value dagql.Typed) (any, error) {
	r.got = value
	return "converted", nil
}

func (r *nullableSDKInputRecorder) CollectContent(context.Context, dagql.AnyResult, *CollectedContent) error {
	return nil
}

func (r *nullableSDKInputRecorder) SourceMod() Mod {
	return nil
}

func (r *nullableSDKInputRecorder) TypeDef(context.Context) (dagql.ObjectResult[*TypeDef], error) {
	return dagql.ObjectResult[*TypeDef]{}, nil
}

func TestCollectedContentCollectUnknownAnyResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, cacheIface)
	sc := cacheIface
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "collect-unknown-client",
		SessionID: "test-session",
	})

	resCall := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "collectUnknown",
		Type:        dagql.NewResultCallType(dagql.String("").Type()),
	}
	detached, err := dagql.NewResultForCall(dagql.String("value"), resCall)
	assert.NilError(t, err)
	res, err := sc.AttachResult(ctx, "test-session", dag, detached)
	assert.NilError(t, err)
	content := NewCollectedContent()
	assert.NilError(t, content.CollectUnknown(ctx, res))
	assert.Assert(t, content.Digest() != "")
}

func TestNullableTypeConvertToSDKInputDereferencesNullableWrappedResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	inner := &nullableSDKInputRecorder{}
	nullable := &NullableType{Inner: inner}

	res, err := dagql.NewResultForCall(dagql.String("value"), moduleObjectTestSyntheticCall("nullableWrappedResult", dagql.String("")))
	assert.NilError(t, err)

	converted, err := nullable.ConvertToSDKInput(ctx, res.NullableWrapped())
	assert.NilError(t, err)
	assert.Equal(t, converted, "converted")

	got, ok := inner.got.(dagql.AnyResult)
	assert.Assert(t, ok)
	assert.Equal(t, got.Unwrap(), dagql.String("value"))
}

func TestNullableTypeConvertToSDKInputReturnsNilForNullResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	inner := &nullableSDKInputRecorder{}
	nullable := &NullableType{Inner: inner}
	null := dagql.DynamicNullable{
		Elem: dagql.String(""),
	}
	res, err := dagql.NewResultForCall(null, moduleObjectTestSyntheticCall("nullResult", null))
	assert.NilError(t, err)

	converted, err := nullable.ConvertToSDKInput(ctx, res)
	assert.NilError(t, err)
	assert.Equal(t, converted, nil)
	assert.Equal(t, inner.got, nil)
}
