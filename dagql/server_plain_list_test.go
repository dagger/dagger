package dagql

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type plainHolderTestObject struct {
	Name  string                                               `field:"true"`
	Held  ObjectResult[*reflectedAccessorTestObject]           `field:"true"`
	Maybe Nullable[ObjectResult[*reflectedAccessorTestObject]] `field:"true"`
}

func (*plainHolderTestObject) Type() *ast.Type {
	return &ast.Type{NamedType: "PlainHolderTestObject", NonNull: true}
}

// A client selecting only Trivial fields of a list of plain objects gets
// them resolved on the list's values: each field's handler runs on the
// value, with no result per element and no call per field. Fields whose
// handler sees the receiver's result, and id, fall back to the elements'
// results, with the same values.
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
	trivial := func(field Field[*reflectedAccessorTestObject]) Field[*reflectedAccessorTestObject] {
		field.Spec.Trivial = true
		return field
	}
	Fields[*reflectedAccessorTestObject]{
		// computed getters
		trivial(Func("doubled", func(_ context.Context, self *reflectedAccessorTestObject, _ struct{}) (Int, error) {
			return Int(self.Value * 2), nil
		})),
		trivial(Func("mirror", func(_ context.Context, self *reflectedAccessorTestObject, _ struct{}) (*reflectedAccessorTestObject, error) {
			return &reflectedAccessorTestObject{Value: self.Value * 10, Name: strings.ToUpper(self.Name)}, nil
		})),
		trivial(Func("shout", func(_ context.Context, self *reflectedAccessorTestObject, _ struct{}) (String, error) {
			return String(self.Name + "!"), nil
		})),
		trivial(Func("whisper", func(_ context.Context, self *reflectedAccessorTestObject, _ struct{}) (String, error) {
			return String(self.Name), nil
		})),
		// sees the receiver's result, so it needs one
		trivial(NodeFunc("nodeDoubled", func(_ context.Context, self ObjectResult[*reflectedAccessorTestObject], _ struct{}) (Int, error) {
			return Int(self.Self().Value * 2), nil
		})),
	}.Install(srv)
	objType, ok := srv.ObjectType("ReflectedAccessorTestObject")
	require.True(t, ok)
	class := objType.(Class[*reflectedAccessorTestObject])
	// an override installed with Func brings its own handler
	class.Install(Func("shout", func(_ context.Context, self *reflectedAccessorTestObject, _ struct{}) (String, error) {
		return String(strings.ToUpper(self.Name) + "!"), nil
	}).Extend())
	// a raw FieldFunc has no entry for plain values, even with a Trivial spec
	whisperSpec, ok := class.FieldSpec("whisper", "")
	require.True(t, ok)
	require.True(t, whisperSpec.Trivial)
	class.Extend(whisperSpec, func(ctx context.Context, self AnyResult, _ map[string]Input) (AnyResult, error) {
		obj := self.Unwrap().(*reflectedAccessorTestObject)
		return NewResultForCurrentCall(ctx, String(strings.ToLower(obj.Name)+"..."))
	})
	Fields[*plainHolderTestObject]{}.Install(srv)
	opt := "set"
	var holders Array[*plainHolderTestObject]
	Fields[cacheTestQuery]{
		Func("held", func(context.Context, cacheTestQuery, struct{}) (*reflectedAccessorTestObject, error) {
			return &reflectedAccessorTestObject{Value: 42}, nil
		}),
		Func("holders", func(context.Context, cacheTestQuery, struct{}) (Array[*plainHolderTestObject], error) {
			return holders, nil
		}),
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
	// resolve resolves sub on items, reporting how many calls it made and
	// cache entries it added
	resolve := func(sub ...Selection) ([]any, int32, int) {
		t.Helper()
		calls.Store(0)
		entries := c.EntryStats().CompletedCalls
		res, err := srv.Resolve(ctx, srv.Root(), Selection{Selector: Selector{Field: "items"}, Subselections: sub})
		require.NoError(t, err)
		items, ok := res["items"].([]any)
		require.True(t, ok, "%T", res["items"])
		return items, calls.Load(), c.EntryStats().CompletedCalls - entries
	}

	plain, n, added := resolve(fields...)
	require.Equal(t, int32(1), n, "only items is called")
	require.Equal(t, 1, added, "only items is cached")
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

	// a computed Trivial getter runs on the value too
	computed, n, added := resolve(Selection{Selector: Selector{Field: "value"}}, Selection{Selector: Selector{Field: "doubled"}})
	require.Equal(t, int32(1), n, "only items is called")
	require.Zero(t, added, "items is a cache hit")
	require.Equal(t, []any{
		map[string]any{"value": Int(1), "doubled": Int(2)},
		map[string]any{"value": Int(2), "doubled": Int(4)},
	}, computed)

	// so does one giving a plain object, and its selected fields
	mirrored, n, added := resolve(Selection{Selector: Selector{Field: "mirror"}, Subselections: []Selection{
		{Selector: Selector{Field: "value"}},
		{Selector: Selector{Field: "name"}},
		{Selector: Selector{Field: "doubled"}},
		{Selector: Selector{Field: "__typename"}},
	}})
	require.Equal(t, int32(1), n, "only items is called")
	require.Zero(t, added)
	require.Equal(t, []any{
		map[string]any{"mirror": map[string]any{"value": Int(10), "name": String("ONE"), "doubled": Int(20), "__typename": "ReflectedAccessorTestObject"}},
		map[string]any{"mirror": map[string]any{"value": Int(20), "name": String("TWO"), "doubled": Int(40), "__typename": "ReflectedAccessorTestObject"}},
	}, mirrored)

	// an override installed with Func runs the override's handler
	shouted, n, _ := resolve(Selection{Selector: Selector{Field: "shout"}})
	require.Equal(t, int32(1), n, "only items is called")
	require.Equal(t, []any{
		map[string]any{"shout": String("ONE!")},
		map[string]any{"shout": String("TWO!")},
	}, shouted)

	// a NodeFunc, or an override extended with a raw FieldFunc, is a call on
	// each element's result
	for _, tc := range []struct {
		field string
		want  []any
	}{
		{"nodeDoubled", []any{Int(2), Int(4)}},
		{"whisper", []any{String("one..."), String("two...")}},
	} {
		got, n, _ := resolve(Selection{Selector: Selector{Field: tc.field}})
		require.Greater(t, n, int32(1), tc.field)
		require.Equal(t, []any{
			map[string]any{tc.field: tc.want[0]},
			map[string]any{tc.field: tc.want[1]},
		}, got, tc.field)
	}

	// an object without a selection of subfields is still an error
	_, err = srv.Resolve(ctx, srv.Root(), Selection{Selector: Selector{Field: "items"}, Subselections: []Selection{
		{Selector: Selector{Field: "value"}},
		{Selector: Selector{Field: "child"}},
	}})
	require.ErrorContains(t, err, "must have a selection of subfields")

	// a struct field holding a result resolves its selections on the result
	var held ObjectResult[*reflectedAccessorTestObject]
	require.NoError(t, srv.Select(ctx, srv.Root(), &held, Selector{Field: "held"}))
	holders = Array[*plainHolderTestObject]{
		{Name: "holder", Held: held, Maybe: NonNull(held)},
		{Name: "unset"},
	}
	res, err := srv.Resolve(ctx, srv.Root(), Selection{Selector: Selector{Field: "holders"}, Subselections: []Selection{
		{Selector: Selector{Field: "name"}},
		{Selector: Selector{Field: "held"}, Subselections: []Selection{{Selector: Selector{Field: "value"}}}},
		{Selector: Selector{Field: "maybe"}, Subselections: []Selection{{Selector: Selector{Field: "value"}}}},
	}})
	require.NoError(t, err)
	require.Equal(t, []any{
		map[string]any{"name": String("holder"), "held": map[string]any{"value": Int(42)}, "maybe": map[string]any{"value": Int(42)}},
		map[string]any{"name": String("unset"), "held": nil, "maybe": nil},
	}, res["holders"])

	// id needs each element's own result: the usual path, same values
	withID, n, _ := resolve(append([]Selection{{Selector: Selector{Field: "id"}}}, fields...)...)
	require.Greater(t, n, int32(2))
	require.Len(t, withID, 2)
	for i, item := range withID {
		obj := item.(map[string]any)
		require.NotNil(t, obj["id"])
		delete(obj, "id")
		require.Equal(t, plain[i], obj)
	}
}
