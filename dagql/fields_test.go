package dagql

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

type reflectedAccessorTestObject struct {
	Value    int                          `field:"true"`
	Name     string                       `field:"true"`
	Ratio    float64                      `field:"true"`
	Enabled  bool                         `field:"true"`
	Optional *string                      `field:"true"`
	Child    *reflectedAccessorTestObject `field:"true"`
	Names    []string                     `field:"true"`
}

func (*reflectedAccessorTestObject) Type() *ast.Type {
	return &ast.Type{NamedType: "ReflectedAccessorTestObject", NonNull: true}
}

func TestFieldsInstallMarksReflectedAccessorsTrivial(t *testing.T) {
	srv := newDagqlServerForTest(t, &reflectedAccessorTestObject{})
	Fields[*reflectedAccessorTestObject]{}.Install(srv)

	obj, ok := srv.ObjectType("ReflectedAccessorTestObject")
	require.True(t, ok)
	for _, name := range []string{"value", "name", "ratio", "enabled", "optional", "child", "names"} {
		spec, ok := obj.FieldSpec(name, call.View(""))
		require.True(t, ok, name)
		require.True(t, spec.Trivial, name)
		require.False(t, spec.NoTelemetry, name)
		if name == "value" || name == "name" || name == "ratio" || name == "enabled" {
			require.NotEmpty(t, spec.DoNotCache, name)
		} else {
			require.Empty(t, spec.DoNotCache, name)
		}
	}
}

func TestReflectedScalarGettersKeepValuesAndTracing(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	defer func() {
		cacheTestReleaseSession(t, c, ctx)
		require.NoError(t, c.Close(ctx))
	}()
	srv := newDagqlServerForTest(t, cacheTestQuery{})
	Fields[*reflectedAccessorTestObject]{}.Install(srv)
	Fields[cacheTestQuery]{
		Func("item", func(context.Context, cacheTestQuery, struct{}) (*reflectedAccessorTestObject, error) {
			return &reflectedAccessorTestObject{Value: 42, Name: "example", Ratio: 1.5, Enabled: true}, nil
		}),
	}.Install(srv)
	ctx = srvToContext(ctx, srv)
	var item ObjectResult[*reflectedAccessorTestObject]
	require.NoError(t, srv.Select(ctx, srv.Root(), &item, Selector{Field: "item"}))
	var calls, hits atomic.Int32
	srv.Around(func(ctx context.Context, _ *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		return ctx, func(_ AnyResult, cached bool, _ *error) {
			calls.Add(1)
			if cached {
				hits.Add(1)
			}
		}
	})
	for range 2 {
		for name, want := range map[string]Typed{
			"value": Int(42), "name": String("example"), "ratio": Float(1.5), "enabled": Boolean(true),
		} {
			result, err := item.Select(ctx, srv, Selector{Field: name})
			require.NoError(t, err)
			require.Equal(t, want, result.Unwrap())
		}
	}
	require.Equal(t, int32(8), calls.Load())
	require.Zero(t, hits.Load())
}
