package querybuilder

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuery(t *testing.T) {
	var contents string
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		Select("file").Arg("path", "/etc/alpine-release").Bind(&contents)

	q, v := root.Build()
	require.Equal(t, `query($ref: String!, $path: String!){core{image(ref:$ref){file(path:$path)}}}`, q)
	require.Equal(t, "alpine", v["ref"])
	require.Equal(t, "/etc/alpine-release", v["path"])
}

func TestAlias(t *testing.T) {
	var contents string
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		SelectWithAlias("foo", "file").Arg("path", "/etc/alpine-release").Bind(&contents)

	q, v := root.Build()
	require.Equal(t, `query($ref: String!, $path: String!){core{image(ref:$ref){foo:file(path:$path)}}}`, q)
	require.Equal(t, "alpine", v["ref"])
	require.Equal(t, "/etc/alpine-release", v["path"])
}

func TestArgsCollision(t *testing.T) {
	q, v := Query().
		Select("a").Arg("arg", "one").
		Select("b").Arg("arg", "two").Build()
	require.Equal(t, `query($arg: String!, $arg2: String!){a(arg:$arg){b(arg:$arg2)}}`, q)
	require.Equal(t, "one", v["arg"])
	require.Equal(t, "two", v["arg2"])
}

func TestNullableArgs(t *testing.T) {
	v := "value"

	tests := map[string]any{
		`query($arg: String!){a(arg:$arg)}`:    v,
		`query($arg: String){a(arg:$arg)}`:     &v,
		`query($arg: [String!]!){a(arg:$arg)}`: []string{v},
		`query($arg: [String]!){a(arg:$arg)}`:  []*string{&v},
		`query($arg: [String]){a(arg:$arg)}`:   &([]*string{&v}),
	}

	for expect, arg := range tests {
		q, _ := Query().Select("a").Arg("arg", arg).Build()
		require.Equal(t, expect, q)
	}
}

func TestFieldImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, _ := root.Select("a").Build()
	require.Equal(t, `query{test{a}}`, a)

	// Make sure this is not `test{a,b}` (e.g. the previous select didn't modify `root` in-place)
	b, _ := root.Select("b").Build()
	require.Equal(t, `query{test{b}}`, b)
}

func TestArgImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, v := root.Arg("foo", "bar").Build()
	require.Equal(t, `query($foo: String!){test(foo:$foo)}`, a)
	require.Equal(t, "bar", v["foo"])

	// Make sure this does not contain `hello` (e.g. the previous select didn't modify `root` in-place)
	b, v := root.Arg("hello", "world").Build()
	require.Equal(t, `query($hello: String!){test(hello:$hello)}`, b)
	require.Equal(t, "world", v["hello"])
}

func TestUnpack(t *testing.T) {
	var contents string
	root := Query().
		Select("foo").
		Select("bar").Arg("hello", "world").
		Select("field").Arg("test", "test").Bind(&contents)

	var response any
	err := json.Unmarshal([]byte(`
		{
			"foo": {
				"bar": {
					"field": "TEST"
				}
			}
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.Unpack(response))
	require.Equal(t, "TEST", contents)
}
