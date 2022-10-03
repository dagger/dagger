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

	q, err := root.Build()
	require.NoError(t, err)
	require.Equal(t, q, `query{core{image(ref:"alpine"){file(path:"/etc/alpine-release")}}}`)
}

func TestAlias(t *testing.T) {
	var contents string
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		SelectWithAlias("foo", "file").Arg("path", "/etc/alpine-release").Bind(&contents)

	q, err := root.Build()
	require.NoError(t, err)
	require.Equal(t, q, `query{core{image(ref:"alpine"){foo:file(path:"/etc/alpine-release")}}}`)
}

func TestFieldImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, err := root.Select("a").Build()
	require.NoError(t, err)
	require.Equal(t, `query{test{a}}`, a)

	// Make sure this is not `test{a,b}` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Select("b").Build()
	require.NoError(t, err)
	require.Equal(t, `query{test{b}}`, b)
}

func TestArgImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, err := root.Arg("foo", "bar").Build()
	require.NoError(t, err)
	require.Equal(t, `query{test(foo:"bar")}`, a)

	// Make sure this does not contain `hello` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Arg("hello", "world").Build()
	require.NoError(t, err)
	require.Equal(t, `query{test(hello:"world")}`, b)
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
