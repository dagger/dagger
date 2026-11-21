package dagql

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

// Selecting a list of plain values into a slice of plain values reads them
// from the list: the list is cached, its elements get no results of their
// own. A destination that holds results still gets each element's result.
func TestSelectScalarListIntoSlice(t *testing.T) {
	t.Parallel()
	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	baseCtx = ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)

	Fields[*cacheTestObject]{
		Func("keys", func(_ context.Context, _ *cacheTestObject, _ struct{}) (Array[String], error) {
			return NewStringArray("a", "b", "c"), nil
		}),
		Func("maybeKeys", func(_ context.Context, _ *cacheTestObject, _ struct{}) (Array[Nullable[String]], error) {
			return Array[Nullable[String]]{NonNull(String("a")), Null[String](), NonNull(String("c"))}, nil
		}),
	}.Install(srv)

	parent := cacheTestDetachedObjectResult(&ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "parent",
	}, srv, &cacheTestObject{Value: 1})
	ctx := srvToContext(cacheTestSessionContext(baseCtx, "session-1"), srv)

	completed := func() int { return c.EntryStats().CompletedCalls }

	before := completed()
	var keys Array[String]
	assert.NilError(t, srv.Select(ctx, parent, &keys, Selector{Field: "keys"}))
	assert.DeepEqual(t, NewStringArray("a", "b", "c"), keys)
	assert.Equal(t, before+1, completed(), "only the list is cached")

	var names []string
	assert.NilError(t, srv.Select(ctx, parent, &names, Selector{Field: "keys"}))
	assert.DeepEqual(t, []string{"a", "b", "c"}, names)
	assert.Equal(t, before+1, completed(), "the cached list is reused")

	before = completed()
	var maybe []string
	assert.NilError(t, srv.Select(ctx, parent, &maybe, Selector{Field: "maybeKeys"}))
	assert.DeepEqual(t, []string{"a", "", "c"}, maybe)
	assert.Equal(t, before+1, completed())

	// a destination of results gets the elements' results
	var items []AnyResult
	assert.NilError(t, srv.Select(ctx, parent, &items, Selector{Field: "keys"}))
	assert.Equal(t, 3, len(items))
	for i, item := range items {
		assert.Equal(t, String([]string{"a", "b", "c"}[i]), item.Unwrap())
		assert.Assert(t, item.cacheSharedResult().id != 0)
	}
	assert.Equal(t, before+1+3, completed())
}
