package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/testutil"
	"go.dagger.io/dagger/sdk/go/dagger"
)

func TestExtensionMount(t *testing.T) {
	startOpts := &engine.Config{
		Workdir:    "../../",
		ConfigPath: "core/integration/testdata/extension/cloak.yaml",
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		res := struct {
			Core struct {
				Filesystem struct {
					WriteFile struct {
						ID string `json:"id"`
					}
				}
			}
		}{}
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `{
					core {
						filesystem(id: "scratch") {
							writeFile(path: "/foo", contents: "bar") {
								id
							}
						}
					}
				}`,
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)

		res2 := struct {
			Test struct {
				TestMount string
			}
		}{}
		err = ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestMount($in: FSID!) {
					test {
						testMount(in: $in)
					}
				}`,
				Variables: map[string]any{
					"in": res.Core.Filesystem.WriteFile.ID,
				},
			},
			&graphql.Response{Data: &res2},
		)
		require.NoError(t, err)
		require.Equal(t, res2.Test.TestMount, "bar")

		return nil
	})
	require.NoError(t, err)
}

func TestGoGenerate(t *testing.T) {
	tmpdir := t.TempDir()

	yamlPath := filepath.Join(tmpdir, "cloak.yaml")
	err := os.WriteFile(yamlPath, []byte(`
name: testgogenerate
scripts:
  - path: .
    sdk: go
`), 0644) // #nosec G306
	require.NoError(t, err)

	goModPath := filepath.Join(tmpdir, "go.mod")
	err = os.WriteFile(goModPath, []byte(`
module testgogenerate
go 1.19
`), 0644) // #nosec G306
	require.NoError(t, err)

	startOpts := &engine.Config{
		LocalDirs: map[string]string{
			"testgogenerate": tmpdir,
		},
	}

	err = engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		data := struct {
			Core struct {
				Filesystem struct {
					LoadProject struct {
						GeneratedCode dagger.Filesystem
					}
				}
			}
		}{}
		resp := &graphql.Response{Data: &data}

		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `
			query GeneratedCode($fs: FSID!, $configPath: String!) {
				core {
					filesystem(id: $fs) {
						loadProject(configPath: $configPath) {
							generatedCode {
								id
							}
						}
					}
				}
			}`,
				Variables: map[string]any{
					"fs":         ctx.LocalDirs["testgogenerate"],
					"configPath": ctx.ConfigPath,
				},
			},
			resp,
		)
		require.NoError(t, err)

		generatedFSID := data.Core.Filesystem.LoadProject.GeneratedCode.ID

		_, err = testutil.ReadFile(ctx, ctx.Client, generatedFSID, "main.go")
		require.NoError(t, err)
		return nil
	})
	require.NoError(t, err)
}

/*  TODO:
* Lists of structs (probably works already but add test)
* Pointer and non-pointer receiver type for structs (probably works already)
* Spread across multiple files
* (Maybe) Spread across multiple packages?
* (Maybe) Extending types from other extensions (core, or otherwise)
* Embedded structs (as in go embedding)? At least need comprehensible errors if that's not allowed yet
* Go interfaces as input/output? At least need comprehensible errors if that's not allowed yet
* Generics? At least need comprehensible errors if that's not allowed yet
* Custom scalars?
* (Someday) enums
* (Someday) unions
* (Someday) Use (as input or return) of types from other non-core extensions
* Exported and unexported methods and struct fields
* Provide multiple structs that have overlapping "type trees"
* Unnamed (inlined) structs. e.g. `type Foo struct { Bar struct { Baz string } }`
* Actual names for all these tests (not a,b,c,etc.)
* Circular types (i.e. structs that have fields that reference themselves, etc.)
 */
func TestCodeToSchema(t *testing.T) {
	startOpts := &engine.Config{
		Workdir:    "../../",
		ConfigPath: "core/integration/testdata/codetoschema/cloak.yaml",
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
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
				A      string
				Bset   string
				Bunset string
				Cnil   *string
				Cnonil *string
				D      []int
				E      []*string
				F      allTheTypes
				G      struct {
					SubField string
				}
				H struct {
					File string
				}
			}
		}{}
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestCodeToSchema($unsetString: String, $unsetInt: Int, $unsetBool: Boolean) {
					test {
						a(
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
						bset: b(str: "foo", i: 42, b: true, strArray: ["foo", $unsetString], intArray: [1, $unsetInt], boolArray: [true, $unsetBool])
						bunset: b(strArray: [])
						cnil: c(returnNil: true)
						cnonil: c(returnNil: false)
						d(intArray: [1, 2, 3])
						e(strArray: ["foo", $unsetString])
						f(strukt: {
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
						g(str: "parent") {
							subField(str: "child")
						}
						h(ref: "alpine:3.16.2") {
							file(path: "/etc/alpine-release")
						}
					}
				}`,
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)
		require.Equal(t, `foo 42 true {Str:bar Int:43 Bool:false StrArray:[baz qux] IntArray:[3 4] BoolArray:[false true] SubStruct:{SubStr:subBar SubInt:44 SubBool:true SubStrArray:[subBaz subQux] SubIntArray:[5 6] SubBoolArray:[true false]}} [foo bar] [1 2] [true false]`, res.Test.A)
		require.Equal(t, "foo 42 true [foo <nil>] [1 <nil>] [true <nil>]", res.Test.Bset)
		require.Equal(t, "<nil> <nil> <nil> [] <nil> <nil>", res.Test.Bunset)
		require.Nil(t, res.Test.Cnil)
		require.NotNil(t, res.Test.Cnonil)
		require.Equal(t, []int{1, 2, 3}, res.Test.D)
		require.Equal(t, []*string{ptrTo("foo"), nil}, res.Test.E)
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
		}, res.Test.F)
		require.Equal(t, "parent-child", res.Test.G.SubField)
		require.Equal(t, "3.16.2\n", res.Test.H.File)

		return nil
	})
	require.NoError(t, err)
}

func ptrTo[T any](v T) *T {
	return &v
}
