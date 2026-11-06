package dangv2

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"

	"github.com/dagger/dagger/dagql/call"
)

// A collection whose items each hold the same module, as dagger/go's package
// entries do.
const receivedCollectionJSON = `{
	"module": {"name": "m"},
	"entries": [
		{"name": "a", "module": {"name": "m"}},
		{"name": "b", "module": {"name": "m"}},
		{"name": "c", "module": {"name": "other"}}
	]
}`

func TestDecodeReceivedMergesEqualObjects(t *testing.T) {
	t.Parallel()

	val, err := decodeReceivedJSON([]byte(receivedCollectionJSON))
	require.NoError(t, err)
	top := val.(*receivedObject)
	entries := top.fields["entries"].([]any)
	module := func(i int) *receivedObject {
		return entries[i].(*receivedObject).fields["module"].(*receivedObject)
	}
	require.Same(t, top.fields["module"], module(0))
	require.Same(t, module(0), module(1))
	require.NotSame(t, module(0), module(2))
	require.Equal(t, receivedJSON(t, []byte(receivedCollectionJSON)), plainReceived(val))
}

func TestDecodeReceivedKeepsDifferentObjectsApart(t *testing.T) {
	t.Parallel()

	// Pairs of objects that differ only in a way a careless comparison or hash
	// could miss.
	data := []byte(`[
		{"v": "1"}, {"v": 1},
		{"v": 1}, {"v": 1.0},
		{"a": null}, {},
		{"a": true}, {"a": false},
		{"l": ["x", "y"]}, {"l": ["y", "x"]},
		{"l": [["x"], []]}, {"l": [[], ["x"]]},
		{"a": "b"}, {"ab": ""},
		{"a": {"b": "c"}}, {"a": {"b": "d"}}
	]`)
	val, err := decodeReceivedJSON(data)
	require.NoError(t, err)
	objs := val.([]any)
	for i := 0; i < len(objs); i += 2 {
		require.NotSame(t, objs[i], objs[i+1], "objects %d and %d", i, i+1)
	}
	require.Equal(t, receivedJSON(t, data), plainReceived(val))
}

func TestConvertRebuildsSharedObjectOnce(t *testing.T) {
	t.Parallel()

	stringType := hm.NonNullType{Type: dang.StringType}
	moduleType := dang.NewType("Module", dang.ObjectKind)
	moduleType.Add("name", hm.NewScheme(nil, stringType))
	entryType := dang.NewType("Entry", dang.ObjectKind)
	entryType.Add("name", hm.NewScheme(nil, stringType))
	entryType.Add("module", hm.NewScheme(nil, hm.NonNullType{Type: moduleType}))
	collType := dang.NewType("Coll", dang.ObjectKind)
	collType.Add("module", hm.NewScheme(nil, hm.NonNullType{Type: moduleType}))
	collType.Add("entries", hm.NewScheme(nil, hm.NonNullType{Type: dang.ListType{Type: hm.NonNullType{Type: entryType}}}))

	val, err := decodeReceivedJSON([]byte(receivedCollectionJSON))
	require.NoError(t, err)
	conv := dangConverter{
		env:     dang.NewValueScope(dang.NewType("Test", dang.ObjectKind)),
		objects: map[receivedObjectKey]dang.Value{},
	}
	coll, err := conv.convert(t.Context(), val, hm.NonNullType{Type: collType})
	require.NoError(t, err)

	field := func(v dang.Value, name string) dang.Value {
		got, found, err := v.(*dang.Object).Lookup(t.Context(), name)
		require.NoError(t, err)
		require.True(t, found, name)
		return got
	}
	entries := field(coll, "entries").(dang.ListValue).Elements
	require.Same(t, field(coll, "module"), field(entries[0], "module"))
	require.Same(t, field(entries[0], "module"), field(entries[1], "module"))
	require.NotSame(t, field(entries[0], "module"), field(entries[2], "module"))

	got, err := json.Marshal(coll)
	require.NoError(t, err)
	require.JSONEq(t, receivedCollectionJSON, string(got))
}

// Two received copies of an object holding the same handle are one object, so
// they compare equal, as the object the sender sent twice did. Before copies
// were merged, each copy got its own handle value, and Dang compares handles
// by identity, so they compared unequal.
func TestConvertSharedObjectsWithHandlesCompareEqual(t *testing.T) {
	t.Parallel()

	env := dang.NewValueScope(dang.NewType("Test", dang.ObjectKind))
	env.Bind("node", dang.GraphQLFunction{
		Name:   "node",
		Field:  &introspection.Field{Name: "node"},
		Client: failingClient{},
		Schema: contractTestSchema(),
	}, dang.PublicVisibility)
	dirType := dang.NewType("Directory", dang.ObjectKind)
	childType := dang.NewType("Child", dang.ObjectKind)
	childType.Add("dir", hm.NewScheme(nil, hm.NonNullType{Type: dirType}))

	handle, err := call.NewEngineResultID(7, call.NewType(&ast.Type{NamedType: "Directory", NonNull: true})).Encode()
	require.NoError(t, err)
	data, err := json.Marshal([]any{map[string]any{"dir": handle}, map[string]any{"dir": handle}})
	require.NoError(t, err)
	val, err := decodeReceivedJSON(data)
	require.NoError(t, err)
	conv := dangConverter{env: env, objects: map[receivedObjectKey]dang.Value{}}
	list, err := conv.convert(t.Context(), val, hm.NonNullType{Type: dang.ListType{Type: hm.NonNullType{Type: childType}}})
	require.NoError(t, err)

	children := list.(dang.ListValue).Elements
	equal := func(a, b dang.Value) bool {
		eq, err := (&dang.Equality{Left: &dang.ValueNode{Val: a}, Right: &dang.ValueNode{Val: b}}).Eval(t.Context(), env)
		require.NoError(t, err)
		return eq.(dang.BoolValue).Val
	}
	dir := func(child dang.Value) dang.Value {
		v, found, err := child.(*dang.Object).Lookup(t.Context(), "dir")
		require.NoError(t, err)
		require.True(t, found)
		return v
	}
	require.True(t, equal(children[0], children[1]))
	require.True(t, equal(dir(children[0]), dir(children[1])))
}

// Calls that receive the same JSON share its decoded value, but each builds its
// own Dang values against its own engine client: a call's client closes when
// the call ends, so a value built for one call must never reach another.
func TestConvertSharedDecodedValuePerCall(t *testing.T) {
	t.Parallel()

	dirType := dang.NewType("Directory", dang.ObjectKind)
	childType := dang.NewType("Child", dang.ObjectKind)
	childType.Add("dir", hm.NewScheme(nil, hm.NonNullType{Type: dirType}))
	listType := hm.NonNullType{Type: dang.ListType{Type: hm.NonNullType{Type: childType}}}

	handle, err := call.NewEngineResultID(7, call.NewType(&ast.Type{NamedType: "Directory", NonNull: true})).Encode()
	require.NoError(t, err)
	data, err := json.Marshal([]any{map[string]any{"dir": handle}, map[string]any{"dir": handle}})
	require.NoError(t, err)
	shared, err := decodeReceivedJSON(data)
	require.NoError(t, err)

	convert := func(client graphql.Client) []dang.Value {
		env := dang.NewValueScope(dang.NewType("Test", dang.ObjectKind))
		env.Bind("node", dang.GraphQLFunction{
			Name:   "node",
			Field:  &introspection.Field{Name: "node"},
			Client: client,
			Schema: contractTestSchema(),
		}, dang.PublicVisibility)
		conv := dangConverter{env: env, objects: map[receivedObjectKey]dang.Value{}}
		list, err := conv.convert(t.Context(), shared, listType)
		require.NoError(t, err)
		return list.(dang.ListValue).Elements
	}
	clientA, clientB := &namedClient{"a"}, &namedClient{"b"}
	childrenA, childrenB := convert(clientA), convert(clientB)

	for _, got := range []struct {
		children []dang.Value
		client   graphql.Client
	}{{childrenA, clientA}, {childrenB, clientB}} {
		for _, child := range got.children {
			dir, found, err := child.(*dang.Object).Lookup(t.Context(), "dir")
			require.NoError(t, err)
			require.True(t, found)
			require.Same(t, got.client, dir.(dang.GraphQLValue).Client)
		}
	}
	require.NotSame(t, childrenA[0], childrenB[0])
}

type namedClient struct{ name string }

func (c *namedClient) MakeRequest(context.Context, *graphql.Request, *graphql.Response) error {
	return fmt.Errorf("client %s: unexpected API request", c.name)
}

func receivedJSON(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(data, &v))
	return v
}

// plainReceived turns a decoded value back into what encoding/json decodes,
// so it can be compared with the JSON it came from.
func plainReceived(val any) any {
	switch v := val.(type) {
	case *receivedObject:
		m := make(map[string]any, len(v.fields))
		for k, f := range v.fields {
			m[k] = plainReceived(f)
		}
		return m
	case []any:
		l := make([]any, len(v))
		for i, e := range v {
			l[i] = plainReceived(e)
		}
		return l
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return v
	}
}
