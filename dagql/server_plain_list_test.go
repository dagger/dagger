package dagql

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// A client selecting only struct fields of a list of plain objects gets them
// read from the list's values: no result per element, and no call per field.
// Selecting id falls back to the elements' results, with the same values.
func TestResolvePlainObjectList(t *testing.T) {
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
	opt := "set"
	Fields[cacheTestQuery]{
		Func("items", func(context.Context, cacheTestQuery, struct{}) (Array[*reflectedAccessorTestObject], error) {
			return Array[*reflectedAccessorTestObject]{
				{Value: 1, Name: "one", Names: []string{"a", "b"}, Child: &reflectedAccessorTestObject{Value: 10}},
				{Value: 2, Name: "two", Optional: &opt, Child: &reflectedAccessorTestObject{Value: 20, Names: []string{"c"}}},
			}, nil
		}),
	}.Install(srv)
	ctx = srvToContext(ctx, srv)

	var calls atomic.Int32
	srv.Around(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		return ctx, func(AnyResult, bool, *error) { calls.Add(1) }
	})

	fields := []Selection{
		{Selector: Selector{Field: "value"}},
		{Alias: "label", Selector: Selector{Field: "name"}},
		{Selector: Selector{Field: "optional"}},
		{Selector: Selector{Field: "names"}},
		{Selector: Selector{Field: "__typename"}},
		{Selector: Selector{Field: "child"}, Subselections: []Selection{
			{Selector: Selector{Field: "value"}},
			{Selector: Selector{Field: "names"}},
		}},
	}
	resolve := func(sub []Selection) []any {
		t.Helper()
		res, err := srv.Resolve(ctx, srv.Root(), Selection{Selector: Selector{Field: "items"}, Subselections: sub})
		require.NoError(t, err)
		items, ok := res["items"].([]any)
		require.True(t, ok, "%T", res["items"])
		return items
	}

	entries := c.EntryStats().CompletedCalls
	plain := resolve(fields)
	require.Equal(t, int32(1), calls.Load(), "only items is called")
	require.Equal(t, entries+1, c.EntryStats().CompletedCalls, "only items is cached")
	require.Equal(t, []any{
		map[string]any{
			"value": Int(1), "label": String("one"), "optional": nil,
			"names": []any{String("a"), String("b")}, "__typename": "ReflectedAccessorTestObject",
			"child": map[string]any{"value": Int(10), "names": []any{}},
		},
		map[string]any{
			"value": Int(2), "label": String("two"), "optional": String("set"),
			"names": []any{}, "__typename": "ReflectedAccessorTestObject",
			"child": map[string]any{"value": Int(20), "names": []any{String("c")}},
		},
	}, plain)

	// id needs each element's own result: the usual path, same values
	withID := resolve(append([]Selection{{Selector: Selector{Field: "id"}}}, fields...))
	require.Greater(t, calls.Load(), int32(2))
	require.Len(t, withID, 2)
	for i, item := range withID {
		obj := item.(map[string]any)
		require.NotNil(t, obj["id"])
		delete(obj, "id")
		require.Equal(t, plain[i], obj)
	}
}
