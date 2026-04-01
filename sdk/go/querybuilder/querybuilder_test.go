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
	require.Equal(t, `{core{image(ref:"alpine"){file(path:"/etc/alpine-release")}}}`, q)
}

func TestAlias(t *testing.T) {
	root := Query().
		Select("core").
		Select("image").Arg("ref", "alpine").
		SelectWithAlias("foo", "file").Arg("path", "/etc/alpine-release")

	q, err := root.Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{core{image(ref:"alpine"){foo:file(path:"/etc/alpine-release")}}}`, q)
}

func TestArgsCollision(t *testing.T) {
	q, err := Query().
		Select("a").Arg("arg", "one").
		Select("b").Arg("arg", "two").
		Build(context.Background())

	require.NoError(t, err)
	require.Equal(t, `{a(arg:"one"){b(arg:"two")}}`, q)
}

func TestNullableArgs(t *testing.T) {
	str := "value"

	tests := []struct {
		arg    any
		expect string
	}{
		{
			expect: `{a(arg:"value")}`,
			arg:    str,
		},
		{
			expect: `{a(arg:"value")}`,
			arg:    &str,
		},
		{
			expect: `{a(arg:["value"])}`,
			arg:    []string{str},
		},
		{
			expect: `{a(arg:["value"])}`,
			arg:    []*string{&str},
		},
		{
			expect: `{a(arg:["value"])}`,
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
	require.Equal(t, `{test{a}}`, a)

	// Make sure this is not `test{a,b}` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Select("b").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{test{b}}`, b)
}

func TestArgImmutability(t *testing.T) {
	root := Query().
		Select("test")

	a, err := root.Arg("foo", "bar").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{test(foo:"bar")}`, a)

	// Make sure this does not contain `hello` (e.g. the previous select didn't modify `root` in-place)
	b, err := root.Arg("hello", "world").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{test(hello:"world")}`, b)
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
		SelectMultiple("one", "two", "three").
		Build(context.Background())

	require.NoError(t, err)
	require.Equal(t, `{foo{bar{one two three}}}`, q)
}

func TestSiblingsLeaf(t *testing.T) {
	_, err := Query().
		Select("foo").
		SelectMultiple("one", "two", "three").
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
		SelectMultiple("one", "two", "three").
		Bind(&contents)

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

func TestSelectFields(t *testing.T) {
	q, err := Query().
		Select("user").
		SelectFields("id", "name", "email").
		Build(context.Background())

	require.NoError(t, err)
	require.Equal(t, `{user{id name email}}`, q)
}

func TestSelectFieldsNested(t *testing.T) {
	profileSelection := Query().SelectFields("bio", "avatar")

	q, err := Query().
		Select("user").
		SelectNested("profile", profileSelection).
		Build(context.Background())

	require.NoError(t, err)
	require.Equal(t, `{user{profile{bio avatar}}}`, q)
}

func TestSelectMixed(t *testing.T) {
	postsSelection := Query().SelectFields("title", "content")
	pageInfoSelection := Query().SelectFields("hasNextPage", "endCursor")

	q, err := Query().
		Select("user").Arg("id", "1").
		Select("posts").Arg("first", 2).
		SelectMixed(
			nil,
			map[string]*QueryBuilder{
				"posts":    postsSelection,
				"pageInfo": pageInfoSelection,
			},
		).
		Build(context.Background())

	require.NoError(t, err)
	require.Contains(t, q, `user(id:"1")`)
	require.Contains(t, q, `posts(first:2)`)
	require.Contains(t, q, `posts{title content}`)
	require.Contains(t, q, `pageInfo{hasNextPage endCursor}`)
}

func TestUnpackMixedFieldsAndSubselections(t *testing.T) {
	type Post struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}

	type PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	}

	type PostsConnection struct {
		Posts    []Post   `json:"posts"`
		PageInfo PageInfo `json:"pageInfo"`
	}

	var result PostsConnection

	postsSelection := Query().SelectFields("title", "content")
	pageInfoSelection := Query().SelectFields("hasNextPage", "endCursor")

	root := Query().
		Select("user").Arg("id", "1").
		Select("posts").Arg("first", 2).
		SelectMixed(
			nil,
			map[string]*QueryBuilder{
				"posts":    postsSelection,
				"pageInfo": pageInfoSelection,
			},
		).
		Bind(&result)

	var response any
	err := json.Unmarshal([]byte(`
		{
			"user": {
				"posts": {
					"posts": [
						{"title": "First Post", "content": "Hello World!"},
						{"title": "Third Post", "content": "Learning GraphQL pagination"}
					],
					"pageInfo": {
						"hasNextPage": true,
						"endCursor": "3"
					}
				}
			}
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.unpack(response))

	require.Len(t, result.Posts, 2)
	require.Equal(t, "First Post", result.Posts[0].Title)
	require.Equal(t, "Hello World!", result.Posts[0].Content)
	require.Equal(t, "Third Post", result.Posts[1].Title)
	require.Equal(t, "Learning GraphQL pagination", result.Posts[1].Content)
	require.True(t, result.PageInfo.HasNextPage)
	require.Equal(t, "3", result.PageInfo.EndCursor)
}

func TestUnpackSubselectionsOnly(t *testing.T) {
	type User struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	type Profile struct {
		Bio  string `json:"bio"`
		User User   `json:"user"`
	}

	var result Profile

	userSelection := Query().SelectFields("name", "age")

	root := Query().
		Select("userProfile").Arg("userId", "1").
		SelectMixed(
			[]string{"bio"},
			map[string]*QueryBuilder{
				"user": userSelection,
			},
		).
		Bind(&result)

	var response any
	err := json.Unmarshal([]byte(`
		{
			"userProfile": {
				"bio": "A passionate user sharing thoughts and ideas.",
				"user": {
					"name": "John Doe",
					"age": 30
				}
			}
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.unpack(response))

	require.Equal(t, "A passionate user sharing thoughts and ideas.", result.Bio)
	require.Equal(t, "John Doe", result.User.Name)
	require.Equal(t, 30, result.User.Age)
}

func TestUnpackEmptySelectionWithSubselections(t *testing.T) {
	type PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	}

	type Result struct {
		PageInfo PageInfo `json:"pageInfo"`
	}

	var result Result

	pageInfoSelection := Query().SelectFields("hasNextPage", "endCursor")

	root := Query().
		Select("data").
		SelectMixed(
			nil,
			map[string]*QueryBuilder{
				"pageInfo": pageInfoSelection,
			},
		).
		Bind(&result)

	var response any
	err := json.Unmarshal([]byte(`
		{
			"data": {
				"pageInfo": {
					"hasNextPage": true,
					"endCursor": "cursor123"
				}
			}
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.unpack(response))

	require.True(t, result.PageInfo.HasNextPage)
	require.Equal(t, "cursor123", result.PageInfo.EndCursor)
}

func TestUnpackNestedSubselectionsOnly(t *testing.T) {
	type User struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	type Result struct {
		User User `json:"user"`
	}

	var result Result

	userSelection := Query().SelectFields("name", "age")

	root := Query().
		Select("userProfile").Arg("userId", "1").
		SelectMixed(
			nil,
			map[string]*QueryBuilder{
				"user": userSelection,
			},
		).
		Bind(&result)

	var response any
	err := json.Unmarshal([]byte(`
		{
			"userProfile": {
				"user": {
					"name": "John Doe",
					"age": 30
				}
			}
		}
	`), &response)
	require.NoError(t, err)
	require.NoError(t, root.unpack(response))

	require.Equal(t, "John Doe", result.User.Name)
	require.Equal(t, 30, result.User.Age)
}
