package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestExtensionMount(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(
		ctx,
		dagger.WithWorkdir("../../"),
		dagger.WithConfigPath("testdata/extension/dagger.json"),
	)
	require.NoError(t, err)
	defer c.Close()

	res := struct {
		Directory struct {
			WithNewFile struct {
				ID string
			}
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `{
					directory {
						withNewFile(path: "foo", contents: "bar") {
							id
						}
					}
				}`,
		},
		&dagger.Response{Data: &res},
	)
	require.NoError(t, err)

	res2 := struct {
		Test struct {
			TestMount string
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestMount($in: DirectoryID!) {
					test {
						testMount(in: $in)
					}
				}`,
			Variables: map[string]any{
				"in": res.Directory.WithNewFile.ID,
			},
		},
		&dagger.Response{Data: &res2},
	)
	require.NoError(t, err)
	require.Equal(t, res2.Test.TestMount, "bar")
}

/*
	TODO:(sipsma) more test cases to add

* Lists of structs (probably works already but add test)
* Pointer and non-pointer receiver type for structs (probably works already)
* Spread across multiple files
* Go interfaces as input/output? At least need comprehensible errors if that's not allowed yet
* Generics? At least need comprehensible errors if that's not allowed yet
* Exported and unexported methods and struct fields
* Provide multiple structs that have overlapping "type trees"
* Unnamed (inlined) structs. e.g. `type Foo struct { Bar struct { Baz string } }`
* Circular types (i.e. structs that have fields that reference themselves, etc.)
*/
func TestCodeToSchema(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(
		ctx,
		dagger.WithWorkdir("../../"),
		dagger.WithConfigPath("testdata/codetoschema/dagger.json"),
	)
	require.NoError(t, err)
	defer c.Close()

	type allTheSubTypes struct {
		SubStr       string
		SubInt       int
		SubBool      bool
		SubStrArray  []string
		SubIntArray  []int
		SubBoolArray []bool
	}
	type allTheTypes struct {
		Str       string
		Int       int
		Bool      bool
		StrArray  []string
		IntArray  []int
		BoolArray []bool
		SubStruct allTheSubTypes
	}

	res := struct {
		Test struct {
			RequiredTypes       string
			OptionalTypesSet    string
			OptionalTypesUnset  string
			OptionalReturnNil   *string
			OptionalReturnNonil *string
			IntArrayReturn      []int
			StringArrayReturn   []*string
			StructReturn        allTheTypes
			ParentResolver      struct {
				SubField string
			}
			ReturnDirectory struct {
				File struct {
					Contents string
				}
			}
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestCodeToSchema($unsetString: String, $unsetInt: Int, $unsetBool: Boolean) {
					test {
						requiredTypes(
							str: "foo",
							i: 42,
							b: true,
							strArray: ["foo", "bar"],
							intArray: [1, 2],
							boolArray: [true, false],
							strukt: {
								str: "bar",
								int: 43,
								bool: false,
								strArray: ["baz", "qux"],
								intArray: [3, 4],
								boolArray: [false, true],
								subStruct: {
									subStr: "subBar",
									subInt: 44,
									subBool: true,
									subStrArray: ["subBaz", "subQux"],
									subIntArray: [5, 6],
									subBoolArray: [true, false],
								},
							},
						)
						optionalTypesSet: optionalTypes(str: "foo", i: 42, b: true, strArray: ["foo", $unsetString], intArray: [1, $unsetInt], boolArray: [true, $unsetBool])
						optionalTypesUnset: optionalTypes(strArray: [])
						optionalReturnNil: optionalReturn(returnNil: true)
						optionalReturnNonil: optionalReturn(returnNil: false)
						intArrayReturn(intArray: [1, 2, 3])
						stringArrayReturn(strArray: ["foo", $unsetString])
						structReturn(strukt: {
								str: "blah",
								int: 45,
								bool: false,
								strArray: ["baq", "quz"],
								intArray: [7, 8],
								boolArray: [false, true],
								subStruct: {
									subStr: "subBar",
									subInt: 48,
									subBool: true,
									subStrArray: ["subBaq", "subQuz"],
									subIntArray: [9, 10],
									subBoolArray: [true, false],
								},
							}
						) {
							str
							int
							bool
							strArray
							intArray
							boolArray
							subStruct {
								subStr
								subInt
								subBool
								subStrArray
								subIntArray
								subBoolArray
							}
						}
						parentResolver(str: "parent") {
							subField(str: "child")
						}
						returnDirectory(ref: "alpine:3.16.2") {
							file(path: "/etc/alpine-release") {
								contents
							}
						}
					}
				}`,
		},
		&dagger.Response{Data: &res},
	)
	require.NoError(t, err)
	require.Equal(t, `foo 42 true {Str:bar Int:43 Bool:false StrArray:[baz qux] IntArray:[3 4] BoolArray:[false true] SubStruct:{SubStr:subBar SubInt:44 SubBool:true SubStrArray:[subBaz subQux] SubIntArray:[5 6] SubBoolArray:[true false]}} [foo bar] [1 2] [true false]`, res.Test.RequiredTypes)
	require.Equal(t, "foo 42 true [foo <nil>] [1 <nil>] [true <nil>]", res.Test.OptionalTypesSet)
	require.Equal(t, "<nil> <nil> <nil> [] <nil> <nil>", res.Test.OptionalTypesUnset)
	require.Nil(t, res.Test.OptionalReturnNil)
	require.NotNil(t, res.Test.OptionalReturnNonil)
	require.Equal(t, []int{1, 2, 3}, res.Test.IntArrayReturn)
	require.Equal(t, []*string{ptrTo("foo"), nil}, res.Test.StringArrayReturn)
	require.Equal(t, allTheTypes{
		Str:       "blah",
		Int:       45,
		Bool:      false,
		StrArray:  []string{"baq", "quz"},
		IntArray:  []int{7, 8},
		BoolArray: []bool{false, true},
		SubStruct: allTheSubTypes{
			SubStr:       "subBar",
			SubInt:       48,
			SubBool:      true,
			SubStrArray:  []string{"subBaq", "subQuz"},
			SubIntArray:  []int{9, 10},
			SubBoolArray: []bool{true, false},
		},
	}, res.Test.StructReturn)
	require.Equal(t, "parent-child", res.Test.ParentResolver.SubField)
	require.Equal(t, "3.16.2\n", res.Test.ReturnDirectory.File.Contents)
}

func ptrTo[T any](v T) *T {
	return &v
}
