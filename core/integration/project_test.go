package core

import (
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

/* TODO:
* Fix namespacing of projects, very easy to overlap with core api (e.g. command named File)
 */
func TestProjectHostExport(t *testing.T) {
	t.Parallel()
	// Project dir needs to be the root of this repo so we can pick up the go.mod there and thus
	// the local go sdk code, which should be used rather than a previously released one
	projectDir := "../../"
	configDir := "core/integration/testdata/projects/go/basic"

	prefix := identity.NewID()

	for _, testGitProject := range []bool{false, true} {
		testGitProject := testGitProject
		testName := "local project"
		if testGitProject {
			testName = "git project"
		}
		t.Run(testName, func(t *testing.T) {
			t.Parallel()

			t.Run("file export implicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()
				result, err := DaggerDoCmd{
					ProjectLocalPath: projectDir,
					TestGitProject:   testGitProject,
					Config:           configDir,
					Target:           "testFile",
					Flags: map[string]string{
						"prefix": prefix,
					},
				}.Run(ctx, t, c)
				if testGitProject {
					require.Error(t, err)
				} else {
					_, err := result.File(prefix + "foo.txt").Contents(ctx)
					require.NoError(t, err)
				}
			})

			t.Run("dir export implicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				result, err := DaggerDoCmd{
					ProjectLocalPath: projectDir,
					TestGitProject:   testGitProject,
					Config:           configDir,
					Target:           "testDir",
					Flags: map[string]string{
						"prefix": prefix,
					},
				}.Run(ctx, t, c)
				if testGitProject {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					_, err = result.File(prefix + "subdir/subbar1.txt").Contents(ctx)
					require.NoError(t, err)
					_, err = result.File(prefix + "subdir/subbar2.txt").Contents(ctx)
					require.NoError(t, err)
					_, err = result.File(prefix + "bar1.txt").Contents(ctx)
					require.NoError(t, err)
					_, err = result.File(prefix + "bar2.txt").Contents(ctx)
					require.NoError(t, err)
				}
			})

			t.Run("file export explicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				outputPath := "/var/blahblah.txt"
				result, err := DaggerDoCmd{
					ProjectLocalPath: projectDir,
					TestGitProject:   testGitProject,
					Config:           configDir,
					OutputPath:       outputPath,
					Target:           "testFile",
				}.Run(ctx, t, c)
				require.NoError(t, err)

				_, err = result.File(outputPath).Contents(ctx)
				require.NoError(t, err)
			})

			t.Run("file export explicit output to parent dir", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				outputDir := "/var"
				result, err := DaggerDoCmd{
					ProjectLocalPath: projectDir,
					TestGitProject:   testGitProject,
					Config:           configDir,
					OutputPath:       outputDir,
					Target:           "testFile",
				}.Run(ctx, t, c)
				require.NoError(t, err)
				_, err = result.File(filepath.Join(outputDir, "foo.txt")).Contents(ctx)
				require.NoError(t, err)
			})

			t.Run("dir export explicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				outputDir := "/var"
				result, err := DaggerDoCmd{
					ProjectLocalPath: projectDir,
					TestGitProject:   testGitProject,
					Config:           configDir,
					OutputPath:       outputDir,
					Target:           "testDir",
				}.Run(ctx, t, c)
				require.NoError(t, err)

				_, err = result.File(filepath.Join(outputDir, "/subdir/subbar1.txt")).Contents(ctx)
				require.NoError(t, err)
				_, err = result.File(filepath.Join(outputDir, "/subdir/subbar2.txt")).Contents(ctx)
				require.NoError(t, err)
				_, err = result.File(filepath.Join(outputDir, "/bar1.txt")).Contents(ctx)
				require.NoError(t, err)
				_, err = result.File(filepath.Join(outputDir, "/bar2.txt")).Contents(ctx)
				require.NoError(t, err)
			})
		})
	}
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
func TestProjectGoCodeToSchema(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	// manually load project TODO: maybe this test should just use `dagger do`
	dirWithGoMod, err := filepath.Abs("../../")
	require.NoError(t, err)
	configAbsPath, err := filepath.Abs("testdata/projects/go/codetoschema/dagger.json")
	require.NoError(t, err)
	configRelPath, err := filepath.Rel(dirWithGoMod, configAbsPath)
	require.NoError(t, err)
	// TODO: have to force lazy execution of project load with Name...
	_, err = c.Project().Load(
		c.Host().Directory(dirWithGoMod),
		configRelPath,
	).Name(ctx)
	require.NoError(t, err)

	res := struct {
		Test struct {
			RequiredTypes  string
			ParentResolver struct {
				SubField string
			}
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestCodeToSchema {
					test {
						requiredTypes(
							str: "foo",
						)
						parentResolver(str: "parent") {
							subField(str: "child")
						}
					}
				}`,
		},
		&dagger.Response{Data: &res},
	)
	require.NoError(t, err)
	require.Equal(t, `foo`, res.Test.RequiredTypes)
	require.Equal(t, "parent-child", res.Test.ParentResolver.SubField)
}
