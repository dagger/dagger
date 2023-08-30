package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentCmd(t *testing.T) {
	t.Skip("TODO FIX TESTDATA ENVS TO USE NEW API")

	t.Parallel()

	type testCase struct {
		environmentPath string
		expectedSDK     string
		expectedName    string
		expectedRoot    string
	}
	for _, tc := range []testCase{
		{
			environmentPath: "core/integration/testdata/environments/go/basic",
			expectedSDK:     "go",
			expectedName:    "basic",
			expectedRoot:    "../../../../../../",
		},
		{
			environmentPath: "core/integration/testdata/environments/go/codetoschema",
			expectedSDK:     "go",
			expectedName:    "codetoschema",
			expectedRoot:    "../../../../../../",
		},
		{
			environmentPath: "core/integration/testdata/environments/python/basic",
			expectedSDK:     "python",
			expectedName:    "basic",
			expectedRoot:    "../../../../../../",
		},
		// TODO: add ts environments once those are under testdata too
	} {
		tc := tc
		for _, testGitEnvironment := range []bool{false, true} {
			testGitEnvironment := testGitEnvironment
			testName := "local environment"
			if testGitEnvironment {
				testName = "git environment"
			}
			testName += "/" + tc.environmentPath
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()
				stderr, err := CLITestContainer(ctx, t, c).
					WithLoadedEnvironment(tc.environmentPath, testGitEnvironment).
					CallEnvironment().
					Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, fmt.Sprintf(`"root": %q`, tc.expectedRoot))
				require.Contains(t, stderr, fmt.Sprintf(`"name": %q`, tc.expectedName))
				require.Contains(t, stderr, fmt.Sprintf(`"sdk": %q`, tc.expectedSDK))
			})
		}
	}
}

func TestEnvironmentCmdInit(t *testing.T) {
	t.Parallel()

	type testCase struct {
		testName             string
		environmentPath      string
		sdk                  string
		name                 string
		root                 string
		expectedErrorMessage string
	}
	for _, tc := range []testCase{
		{
			testName:        "explicit environment dir/go",
			environmentPath: "/var/testenvironment/subdir",
			sdk:             "go",
			name:            identity.NewID(),
			root:            "../",
		},
		{
			testName:        "explicit environment dir/python",
			environmentPath: "/var/testenvironment/subdir",
			sdk:             "python",
			name:            identity.NewID(),
			root:            "../..",
		},
		{
			testName:        "explicit environment file",
			environmentPath: "/var/testenvironment/subdir/dagger.json",
			sdk:             "python",
			name:            identity.NewID(),
		},
		{
			testName: "implicit environment",
			sdk:      "go",
			name:     identity.NewID(),
		},
		{
			testName:        "implicit environment with root",
			environmentPath: "/var/testenvironment",
			sdk:             "python",
			name:            identity.NewID(),
			root:            "..",
		},
		{
			testName:             "invalid sdk",
			environmentPath:      "/var/testenvironment",
			sdk:                  "c++--",
			name:                 identity.NewID(),
			expectedErrorMessage: "unsupported environment SDK",
		},
		{
			testName:             "error on git",
			environmentPath:      "git://github.com/dagger/dagger.git",
			sdk:                  "go",
			name:                 identity.NewID(),
			expectedErrorMessage: "environment init is not supported for git environments",
		},
	} {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			defer c.Close()
			ctr := CLITestContainer(ctx, t, c).
				WithEnvironmentArg(tc.environmentPath).
				WithSDKArg(tc.sdk).
				WithNameArg(tc.name).
				CallEnvironmentInit()

			if tc.expectedErrorMessage != "" {
				_, err := ctr.Sync(ctx)
				require.ErrorContains(t, err, tc.expectedErrorMessage)
				return
			}

			expectedConfigPath := tc.environmentPath
			if !strings.HasSuffix(expectedConfigPath, "dagger.json") {
				expectedConfigPath = filepath.Join(expectedConfigPath, "dagger.json")
			}
			_, err := ctr.File(expectedConfigPath).Contents(ctx)
			require.NoError(t, err)

			stderr, err := ctr.CallEnvironment().Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stderr, fmt.Sprintf(`"name": %q`, tc.name))
			require.Contains(t, stderr, fmt.Sprintf(`"sdk": %q`, tc.sdk))
		})
	}

	t.Run("error on existing environment", func(t *testing.T) {
		t.Skip("TODO FIX TESTDATA ENVS TO USE NEW API")

		t.Parallel()
		c, ctx := connect(t)
		defer c.Close()
		_, err := CLITestContainer(ctx, t, c).
			WithLoadedEnvironment("core/integration/testdata/environments/go/basic", false).
			WithSDKArg("go").
			WithNameArg("foo").
			CallEnvironmentInit().
			Sync(ctx)
		require.ErrorContains(t, err, "environment init config path already exists")
	})
}

func TestEnvironmentCommandHierarchy(t *testing.T) {
	t.Skip("TODO FIX TESTDATA ENVS TO USE NEW API")
	t.Parallel()

	for _, sdk := range []string{"go", "python"} {
		environmentDir := fmt.Sprintf("core/integration/testdata/environments/%s/basic", sdk)

		t.Run(environmentDir, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			defer c.Close()

			stderr, err := CLITestContainer(ctx, t, c).
				WithLoadedEnvironment(environmentDir, false).
				WithTarget("level-1:level-2:level-3:foo").
				CallDo().
				Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stderr, "hello from foo")

			stderr, err = CLITestContainer(ctx, t, c).
				WithLoadedEnvironment(environmentDir, false).
				WithTarget("level-1:level-2:level-3:bar").
				CallDo().
				Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stderr, "hello from bar")
		})
	}
}

func TestEnvironmentHostExport(t *testing.T) {
	t.Skip("TODO FIX TESTDATA ENVS TO USE NEW API")

	t.Parallel()

	prefix := identity.NewID()

	type testCase struct {
		sdk              string
		expectedMainFile string
	}
	for _, tc := range []testCase{
		{
			sdk:              "go",
			expectedMainFile: "main.go",
		},
		{
			sdk:              "python",
			expectedMainFile: "main.py",
		},
	} {
		tc := tc
		environmentDir := fmt.Sprintf("core/integration/testdata/environments/%s/basic", tc.sdk)

		for _, testGitEnvironment := range []bool{false, true} {
			testGitEnvironment := testGitEnvironment
			testName := "local environment"
			if testGitEnvironment {
				testName = "git environment"
			}
			testName += "/" + environmentDir
			t.Run(testName, func(t *testing.T) {
				t.Parallel()

				t.Run("file export implicit output", func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)
					defer c.Close()
					ctr, err := CLITestContainer(ctx, t, c).
						WithLoadedEnvironment(environmentDir, testGitEnvironment).
						WithTarget("test-file").
						WithUserArg("file-prefix", prefix).
						CallDo().
						Sync(ctx)
					if testGitEnvironment {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						_, err := ctr.File(filepath.Join(cliContainerRepoMntPath, prefix+"foo.txt")).Contents(ctx)
						require.NoError(t, err)
					}
				})

				t.Run("dir export implicit output", func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)
					defer c.Close()
					ctr, err := CLITestContainer(ctx, t, c).
						WithLoadedEnvironment(environmentDir, testGitEnvironment).
						WithTarget("test-dir").
						WithUserArg("dir-prefix", prefix).
						CallDo().
						Sync(ctx)
					if testGitEnvironment {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						_, err = ctr.File(filepath.Join(cliContainerRepoMntPath, prefix+"subdir/subbar1.txt")).Contents(ctx)
						require.NoError(t, err)
						_, err = ctr.File(filepath.Join(cliContainerRepoMntPath, prefix+"subdir/subbar2.txt")).Contents(ctx)
						require.NoError(t, err)
						_, err = ctr.File(filepath.Join(cliContainerRepoMntPath, prefix+"bar1.txt")).Contents(ctx)
						require.NoError(t, err)
						_, err = ctr.File(filepath.Join(cliContainerRepoMntPath, prefix+"bar2.txt")).Contents(ctx)
						require.NoError(t, err)
					}
				})

				t.Run("file export explicit output", func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)
					defer c.Close()

					outputPath := "/var/blahblah.txt"
					ctr, err := CLITestContainer(ctx, t, c).
						WithLoadedEnvironment(environmentDir, testGitEnvironment).
						WithTarget("test-file").
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
						WithLoadedEnvironment(environmentDir, testGitEnvironment).
						WithTarget("test-file").
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
						WithLoadedEnvironment(environmentDir, testGitEnvironment).
						WithTarget("test-dir").
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

				t.Run("export from container host", func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)
					defer c.Close()
					outputDir := "/var"
					ctr, err := CLITestContainer(ctx, t, c).
						WithLoadedEnvironment(environmentDir, testGitEnvironment).
						WithTarget("test-export-local-dir").
						WithOutputArg(outputDir).
						CallDo().
						Sync(ctx)
					require.NoError(t, err)
					_, err = ctr.File(filepath.Join(outputDir, tc.expectedMainFile)).Contents(ctx)
					require.NoError(t, err)
					_, err = ctr.File(filepath.Join(outputDir, "dagger.json")).Contents(ctx)
					require.NoError(t, err)
				})
			})
		}
	}
}

func TestEnvironmentDirImported(t *testing.T) {
	t.Skip("TODO FIX TESTDATA ENVS TO USE NEW API")

	t.Parallel()

	type testCase struct {
		sdk              string
		expectedMainFile string
	}
	for _, tc := range []testCase{
		{
			sdk:              "go",
			expectedMainFile: "main.go",
		},
		{
			sdk:              "python",
			expectedMainFile: "main.py",
		},
	} {
		tc := tc
		environmentDir := fmt.Sprintf("core/integration/testdata/environments/%s/basic", tc.sdk)

		for _, testGitEnvironment := range []bool{false, true} {
			testGitEnvironment := testGitEnvironment
			testName := "local environment"
			if testGitEnvironment {
				testName = "git environment"
			}
			testName += "/" + environmentDir
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				defer c.Close()
				stderr, err := CLITestContainer(ctx, t, c).
					WithLoadedEnvironment(environmentDir, testGitEnvironment).
					WithTarget("test-imported-environment-dir").
					CallDo().
					Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, "README.md")
				require.Contains(t, stderr, environmentDir)
				require.Contains(t, stderr, environmentDir+"/dagger.json")
				require.Contains(t, stderr, environmentDir+"/"+tc.expectedMainFile)
			})
		}
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
func TestEnvironmentGoCodeToSchema(t *testing.T) {
	t.Skip("TODO FIX TESTDATA ENVS TO USE NEW API")

	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	// manually load environment TODO: maybe this test should just use `dagger do`
	dirWithGoMod, err := filepath.Abs("../../")
	require.NoError(t, err)
	configAbsPath, err := filepath.Abs("testdata/environments/go/codetoschema/dagger.json")
	require.NoError(t, err)
	configRelPath, err := filepath.Rel(dirWithGoMod, configAbsPath)
	require.NoError(t, err)
	// TODO: have to force lazy execution of environment load with Name...
	_, err = c.Environment().Load(
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
