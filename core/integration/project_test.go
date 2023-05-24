package core

import (
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

/* TODO:
* Test git projects
* Fix namespacing of projects, very easy to overlap with core api (e.g. command named File)
 */
func TestProjectHostExport(t *testing.T) {
	t.Parallel()

	projectDir := "../../" // needed so we pick up the go.mod in the root of our repo
	prefix := ".testtmp" + identity.NewID()
	projectDirPlusPrefix := filepath.Join(projectDir, prefix)
	t.Cleanup(func() {
		tmps, err := filepath.Glob(projectDirPlusPrefix + "*")
		if err == nil {
			for _, tmp := range tmps {
				os.RemoveAll(tmp)
			}
		}
	})
	configDir := "./testdata/projects/go/basic"

	t.Run("file export implicit output", func(t *testing.T) {
		t.Parallel()
		DaggerDoCmd{
			Project: projectDir,
			Config:  configDir,
			Target:  "testFile",
			Flags: map[string]string{
				"prefix": prefix,
			},
		}.Run(t)
		require.FileExists(t, projectDirPlusPrefix+"foo.txt")
	})

	t.Run("dir export implicit output", func(t *testing.T) {
		t.Parallel()
		DaggerDoCmd{
			Project: projectDir,
			Config:  configDir,
			Target:  "testDir",
			Flags: map[string]string{
				"prefix": prefix,
			},
		}.Run(t)
		require.FileExists(t, projectDirPlusPrefix+"subdir/subbar1.txt")
		require.FileExists(t, projectDirPlusPrefix+"subdir/subbar2.txt")
		require.FileExists(t, projectDirPlusPrefix+"bar1.txt")
		require.FileExists(t, projectDirPlusPrefix+"bar2.txt")
	})

	t.Run("file export explicit output", func(t *testing.T) {
		t.Parallel()
		tmpdir := t.TempDir()
		outputPath := filepath.Join(tmpdir, "blahblah.txt")
		DaggerDoCmd{
			Project:    projectDir,
			Config:     configDir,
			OutputPath: outputPath,
			Target:     "testFile",
		}.Run(t)
		require.FileExists(t, outputPath)
	})

	t.Run("file export explicit output to parent dir", func(t *testing.T) {
		t.Parallel()
		tmpdir := t.TempDir()
		DaggerDoCmd{
			Project:    projectDir,
			Config:     configDir,
			OutputPath: tmpdir,
			Target:     "testFile",
		}.Run(t)
		require.FileExists(t, filepath.Join(tmpdir, "foo.txt"))
	})

	t.Run("dir export explicit output", func(t *testing.T) {
		t.Parallel()
		tmpdir := t.TempDir()
		DaggerDoCmd{
			Project:    projectDir,
			Config:     configDir,
			OutputPath: tmpdir,
			Target:     "testDir",
		}.Run(t)
		require.FileExists(t, filepath.Join(tmpdir, "subdir/subbar1.txt"))
		require.FileExists(t, filepath.Join(tmpdir, "subdir/subbar2.txt"))
		require.FileExists(t, filepath.Join(tmpdir, "bar1.txt"))
		require.FileExists(t, filepath.Join(tmpdir, "bar2.txt"))
	})
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
