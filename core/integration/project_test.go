package core

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

// Project dir needs to be the root of this repo so we can pick up the go.mod there and thus
// the local go sdk code, which should be used rather than a previously released one
const testProjectDir = "../.."

func TestProjectCmd(t *testing.T) {
	t.Parallel()

	type testCase struct {
		configDir    string
		expectedSDK  string
		expectedName string
	}
	for _, tc := range []testCase{
		{
			configDir:    "core/integration/testdata/projects/go/basic",
			expectedSDK:  "go",
			expectedName: "basic",
		},
		{
			configDir:    "core/integration/testdata/projects/go/codetoschema",
			expectedSDK:  "go",
			expectedName: "codetoschema",
		},
		// TODO: add python+ts projects once those are under testdata too
	} {
		tc := tc
		for _, testGitProject := range []bool{false, true} {
			testGitProject := testGitProject
			testName := "local project"
			if testGitProject {
				testName = "git project"
			}
			testName += "/" + tc.configDir
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()
				output, err := CLITestContainer(ctx, t, c).
					WithLoadedProject(testProjectDir, testGitProject).
					WithConfigArg(tc.configDir).
					CallProject().
					Stderr(ctx)
				require.NoError(t, err)
				cfg := core.ProjectConfig{}
				require.NoError(t, json.Unmarshal([]byte(lastNLines(output, 4)), &cfg))
				require.Equal(t, tc.expectedSDK, cfg.SDK)
				require.Equal(t, tc.expectedName, cfg.Name)
			})
		}
	}
}

func TestProjectCmdInit(t *testing.T) {
	t.Parallel()

	type testCase struct {
		testName             string
		projectPath          string
		configPath           string
		sdk                  string
		name                 string
		expectedErrorMessage string
	}
	for _, tc := range []testCase{
		{
			testName:    "explicit project+config/go",
			projectPath: "/var/testproject",
			configPath:  "subdir",
			sdk:         "go",
			name:        identity.NewID(),
		},
		{
			testName:    "explicit project+config/python",
			projectPath: "/var/testproject",
			configPath:  "subdir",
			sdk:         "python",
			name:        identity.NewID(),
		},
		{
			testName: "implicit project+config",
			sdk:      "go",
			name:     identity.NewID(),
		},
		{
			testName:   "implicit project",
			configPath: "subdir",
			sdk:        "python",
			name:       identity.NewID(),
		},
		{
			testName:    "implicit config",
			projectPath: "/var/testproject",
			sdk:         "python",
			name:        identity.NewID(),
		},
		{
			testName:             "invalid sdk",
			projectPath:          "/var/testproject",
			sdk:                  "c++--",
			name:                 identity.NewID(),
			expectedErrorMessage: "unsupported project SDK",
		},
		{
			testName:             "error on git",
			projectPath:          "git://github.com/dagger/dagger.git",
			sdk:                  "go",
			name:                 identity.NewID(),
			expectedErrorMessage: "project init is not supported for git projects",
		},
	} {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			defer c.Close()
			ctr := CLITestContainer(ctx, t, c).
				WithProjectArg(tc.projectPath).
				WithConfigArg(tc.configPath).
				WithSDKArg(tc.sdk).
				WithNameArg(tc.name).
				CallProjectInit()

			if tc.expectedErrorMessage != "" {
				_, err := ctr.Sync(ctx)
				require.ErrorContains(t, err, tc.expectedErrorMessage)
				return
			}

			output, err := ctr.CallProject().Stderr(ctx)
			require.NoError(t, err)
			cfg := core.ProjectConfig{}
			require.NoError(t, json.Unmarshal([]byte(lastNLines(output, 4)), &cfg))
			require.Equal(t, tc.sdk, cfg.SDK)
			require.Equal(t, tc.name, cfg.Name)
		})
	}

	t.Run("error on existing project", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		defer c.Close()
		_, err := CLITestContainer(ctx, t, c).
			WithLoadedProject(testProjectDir, false).
			WithConfigArg("core/integration/testdata/projects/go/basic").
			WithSDKArg("go").
			WithNameArg("foo").
			CallProjectInit().
			Sync(ctx)
		require.ErrorContains(t, err, "project init config path already exists")
	})
}

func TestProjectHostExport(t *testing.T) {
	t.Parallel()
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
				ctr, err := CLITestContainer(ctx, t, c).
					WithLoadedProject(testProjectDir, testGitProject).
					WithConfigArg(configDir).
					WithTarget("testFile").
					WithUserArg("prefix", prefix).
					CallDo().
					Sync(ctx)
				if testGitProject {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					_, err := ctr.File(prefix + "foo.txt").Contents(ctx)
					require.NoError(t, err)
				}
			})

			t.Run("dir export implicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()
				ctr, err := CLITestContainer(ctx, t, c).
					WithLoadedProject(testProjectDir, testGitProject).
					WithConfigArg(configDir).
					WithTarget("testDir").
					WithUserArg("prefix", prefix).
					CallDo().
					Sync(ctx)
				if testGitProject {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					_, err = ctr.File(prefix + "subdir/subbar1.txt").Contents(ctx)
					require.NoError(t, err)
					_, err = ctr.File(prefix + "subdir/subbar2.txt").Contents(ctx)
					require.NoError(t, err)
					_, err = ctr.File(prefix + "bar1.txt").Contents(ctx)
					require.NoError(t, err)
					_, err = ctr.File(prefix + "bar2.txt").Contents(ctx)
					require.NoError(t, err)
				}
			})

			t.Run("file export explicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				outputPath := "/var/blahblah.txt"
				ctr, err := CLITestContainer(ctx, t, c).
					WithLoadedProject(testProjectDir, testGitProject).
					WithConfigArg(configDir).
					WithTarget("testFile").
					WithOutputArg(outputPath).
					CallDo().
					Sync(ctx)
				require.NoError(t, err)
				_, err = ctr.File(outputPath).Contents(ctx)
				require.NoError(t, err)
			})

			t.Run("file export explicit output to parent dir", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				outputDir := "/var"
				ctr, err := CLITestContainer(ctx, t, c).
					WithLoadedProject(testProjectDir, testGitProject).
					WithConfigArg(configDir).
					WithTarget("testFile").
					WithOutputArg(outputDir).
					CallDo().
					Sync(ctx)
				require.NoError(t, err)
				_, err = ctr.File(filepath.Join(outputDir, "foo.txt")).Contents(ctx)
				require.NoError(t, err)
			})

			t.Run("dir export explicit output", func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()

				outputDir := "/var"
				ctr, err := CLITestContainer(ctx, t, c).
					WithLoadedProject(testProjectDir, testGitProject).
					WithConfigArg(configDir).
					WithTarget("testDir").
					WithOutputArg(outputDir).
					CallDo().
					Sync(ctx)
				require.NoError(t, err)

				_, err = ctr.File(filepath.Join(outputDir, "/subdir/subbar1.txt")).Contents(ctx)
				require.NoError(t, err)
				_, err = ctr.File(filepath.Join(outputDir, "/subdir/subbar2.txt")).Contents(ctx)
				require.NoError(t, err)
				_, err = ctr.File(filepath.Join(outputDir, "/bar1.txt")).Contents(ctx)
				require.NoError(t, err)
				_, err = ctr.File(filepath.Join(outputDir, "/bar2.txt")).Contents(ctx)
				require.NoError(t, err)
			})
		})
	}
}

func TestProjectDirImported(t *testing.T) {
	t.Parallel()
	projectDir := "../../"
	configDir := "core/integration/testdata/projects/go/basic"
	for _, testGitProject := range []bool{false, true} {
		testGitProject := testGitProject
		testName := "local project"
		if testGitProject {
			testName = "git project"
		}
		t.Run(testName, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			defer c.Close()
			output, err := CLITestContainer(ctx, t, c).
				WithLoadedProject(projectDir, testGitProject).
				WithConfigArg(configDir).
				WithTarget("testImportedProjectDir").
				CallDo().
				Stderr(ctx)
			require.NoError(t, err)
			outputLines := strings.Split(output, "\n")
			require.Contains(t, outputLines, "README.md")
			require.Contains(t, outputLines, configDir)
			require.Contains(t, outputLines, configDir+"/dagger.json")
			require.Contains(t, outputLines, configDir+"/main.go")
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
