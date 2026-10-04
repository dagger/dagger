package dagql

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

type reflectedAccessorTestObject struct {
	Value int `field:"true"`
}

func (*reflectedAccessorTestObject) Type() *ast.Type {
	return &ast.Type{NamedType: "ReflectedAccessorTestObject", NonNull: true}
}

func TestFieldsInstallMarksReflectedAccessorsTrivial(t *testing.T) {
	srv := newDagqlServerForTest(t, &reflectedAccessorTestObject{})
	Fields[*reflectedAccessorTestObject]{}.Install(srv)

	obj, ok := srv.ObjectType("ReflectedAccessorTestObject")
	require.True(t, ok)
	spec, ok := obj.FieldSpec("value", call.View(""))
	require.True(t, ok)
	require.True(t, spec.Trivial)
	require.False(t, spec.NoTelemetry)
}

type trivialScopeTestQuery struct{}

func (trivialScopeTestQuery) Type() *ast.Type {
	return &ast.Type{NamedType: "Query", NonNull: true}
}

// A trivial field's resolver can run real work (e.g. a module object field
// loading an ID); those nested calls must see their own trivial state.
func TestTrivialFieldMarkIsScopedToItsCall(t *testing.T) {
	srv := newDagqlServerForTest(t, trivialScopeTestQuery{})
	var mu sync.Mutex
	atTelemetry := map[string]bool{}
	inResolver := map[string]bool{}
	record := func(m map[string]bool, field string, ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()
		m[field] = CurrentFieldIsTrivial(ctx)
	}
	srv.Around(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		record(atTelemetry, req.Field, ctx)
		return ctx, func(AnyResult, bool, *error) {}
	})
	Fields[trivialScopeTestQuery]{
		{
			Spec: &FieldSpec{Name: "getter", Type: String(""), Trivial: true},
			Func: func(ctx context.Context, self ObjectResult[trivialScopeTestQuery], _ map[string]Input, _ call.View) (AnyResult, error) {
				record(inResolver, "getter", ctx)
				var out String
				if err := srv.Select(ctx, self, &out, Selector{Field: "work"}); err != nil {
					return nil, err
				}
				return NewResultForCurrentCall(ctx, out)
			},
		},
		{
			Spec: &FieldSpec{Name: "work", Type: String("")},
			Func: func(ctx context.Context, _ ObjectResult[trivialScopeTestQuery], _ map[string]Input, _ call.View) (AnyResult, error) {
				record(inResolver, "work", ctx)
				return NewResultForCurrentCall(ctx, NewString("done"))
			},
		},
	}.Install(srv)

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	ctx = srvToContext(ctx, srv)
	var out String
	require.NoError(t, srv.Select(ctx, srv.Root(), &out, Selector{Field: "getter"}))
	require.Equal(t, "done", out.String())

	require.Equal(t, map[string]bool{"getter": true, "work": false}, atTelemetry)
	require.Equal(t, map[string]bool{"getter": true, "work": false}, inResolver)
}
