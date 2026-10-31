package dangv2

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
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
