package querybuilder

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuery(t *testing.T) {
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		Select("file").Arg("path", "/etc/alpine-release")

	q, err := root.Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `query{core{image(ref:"alpine"){file(path:"/etc/alpine-release")}}}`, q)
}

func TestAlias(t *testing.T) {
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		SelectWithAlias("foo", "file").Arg("path", "/etc/alpine-release")

	q, err := root.Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `query{core{image(ref:"alpine"){foo:file(path:"/etc/alpine-release")}}}`, q)
}

func TestArgsCollision(t *testing.T) {
	q, err := Query().
		Select("a").Arg("arg", "one").
		Select("b").Arg("arg", "two").
		Build(context.Background())

	require.NoError(t, err)
	require.Equal(t, `query{a(arg:"one"){b(arg:"two")}}`, q)
}

func TestNullableArgs(t *testing.T) {
	str := "value"

	tests := []struct {
		arg    any
		expect string
	}{
		{
			expect: `query{a(arg:"value")}`,
			arg:    str,
		},
		{
			expect: `query{a(arg:"value")}`,
			arg:    &str,
		},
		{
			expect: `query{a(arg:["value"])}`,
			arg:    []string{str},
		},
		{
			expect: `query{a(arg:["value"])}`,
			arg:    []*string{&str},
		},
		{
			expect: `query{a(arg:["value"])}`,
			arg:    &([]*string{&str}),
		},
	}

	for _, test := range tests {
		q, err := Query().Select("a").Arg("arg", test.arg).Build(context.Background())
		require.NoError(t, err)
		require.Equal(t, test.expect, q)
	}
}

func TestFieldImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, err := root.Select("a").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `query{test{a}}`, a)

	// Make sure this is not `test{a,b}` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Select("b").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `query{test{b}}`, b)
}

func TestArgImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, err := root.Arg("foo", "bar").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `query{test(foo:"bar")}`, a)

	// Make sure this does not contain `hello` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Arg("hello", "world").Build(context.Background())
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
	require.NoError(t, root.unpack(response))
	require.Equal(t, "TEST", contents)
}

func TestUnpackList(t *testing.T) {
	var contents []string
	root := Query().
		Select("foo").
		Select("bar").
		Bind(&contents)

	var response any
	err := json.Unmarshal([]byte(`
        {
            "foo": {
                "bar": [
                    "one",
                    "two",
                    "three"
                ]
            }
        }
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.unpack(response))
	require.EqualValues(t, []string{"one", "two", "three"}, contents)
}

func TestSiblings(t *testing.T) {
	q, err := Query().
		Select("foo").
		Select("bar").
		Select("one", "two", "three").
		Build(context.Background())

	require.NoError(t, err)
	require.Equal(t, `query{foo{bar{one two three}}}`, q)
}

func TestSiblingsLeaf(t *testing.T) {
	_, err := Query().
		Select("foo").
		Select("one", "two", "three").
		Select("bar").
		Build(context.Background())

	require.ErrorContains(t, err, "sibling selections not end of chain")
}

func TestUnpackSiblings(t *testing.T) {
	type data struct {
		One   string
		Two   int
		Three bool
	}
	var contents data
	root := Query().
		Select("foo").
		Select("bar").
		Bind(&contents).
		Select("one", "two", "three")

	var response any
	err := json.Unmarshal([]byte(`
        {
            "foo": {
                "bar": {
                    "one": "TEST",
                    "two": 12,
                    "three": true
                }
            }
        }
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.unpack(response))
	require.EqualValues(t, data{"TEST", 12, true}, contents)
}
