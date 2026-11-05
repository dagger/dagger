package core

import (
	"context"
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

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

func TestListTypeNestedItemsKeepTheirRow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	sc, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, sc)
	root := &Query{}
	testSrv := &moduleObjectTestServer{mockServer: &mockServer{}, cache: sc, root: root}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "nested-list-client",
		SessionID: "nested-list-session",
	})
	installTypeDefTestClasses(dag)

	intDef := newTypeDefAttachedResult(t, ctx, sc, dag, "nestedListInt", (&TypeDef{}).WithKind(TypeDefKindInteger))
	rowDef := newTypeDefAttachedResult(t, ctx, sc, dag, "nestedListRow", (&TypeDef{}).WithListOf(
		newTypeDefAttachedResult(t, ctx, sc, dag, "nestedListRowDef", &ListTypeDef{ElementTypeDef: intDef}),
	))
	grid := &ListType{Elem: rowDef, Underlying: &ListType{Elem: intDef, Underlying: &PrimitiveType{Def: intDef.Self()}}}
	frame := &dagql.ResultCall{
		Kind:  dagql.ResultCallKindField,
		Field: "grid",
		Type:  dagql.NewResultCallType(dagql.DynamicArrayOutput{Elem: dagql.DynamicArrayOutput{Elem: dagql.Int(0)}}.Type()),
	}
	returned := []any{
		[]any{json.Number("1"), json.Number("2")},
		[]any{json.Number("3"), json.Number("4")},
	}
	res, err := sc.GetOrInitCall(ctx, "nested-list-session", dag, &dagql.CallRequest{ResultCall: frame}, func(ctx context.Context) (dagql.AnyResult, error) {
		return grid.ConvertFromSDKResult(dagql.ContextWithCall(ctx, frame), returned)
	})
	assert.NilError(t, err)

	ctx = dagql.ContextWithServer(ctx, dag)
	for i, want := range [][]int{{1, 2}, {3, 4}} {
		row, err := res.NthValue(ctx, i+1)
		assert.NilError(t, err)
		for j, w := range want {
			item, err := row.NthValue(ctx, j+1)
			assert.NilError(t, err)
			assert.Equal(t, dagql.Int(w), item.Unwrap(), "row %d, item %d", i+1, j+1)
		}
	}
}
