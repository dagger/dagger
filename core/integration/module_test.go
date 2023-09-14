package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

/* TODO: add coverage for
* dagger env extend
* dagger env sync
* that the codegen of the testdata envs are up to date (or incorporate that into a cli command)
* if a dependency changes, then checks should re-run
 */

func TestEnvCmd(t *testing.T) {
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
	} {
		tc := tc
		for _, testGitEnv := range []bool{false, true} {
			testGitEnv := testGitEnv
			testName := "local environment"
			if testGitEnv {
				testName = "git environment"
			}
			testName += "/" + tc.environmentPath
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				stderr, err := CLITestContainer(ctx, t, c).
					WithLoadedEnv(tc.environmentPath, testGitEnv).
					CallEnv().
					Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, fmt.Sprintf(`"root": %q`, tc.expectedRoot))
				require.Contains(t, stderr, fmt.Sprintf(`"name": %q`, tc.expectedName))
				require.Contains(t, stderr, fmt.Sprintf(`"sdk": %q`, tc.expectedSDK))
			})
		}
	}
}

func TestEnvCmdHelps(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	baseCtr := CLITestContainer(ctx, t, c).WithHelpArg(true)

	// test with no env specified
	noEnvCtr := baseCtr
	// test with a valid local env
	validLocalEnvCtr := baseCtr.WithLoadedEnv("core/integration/testdata/environments/go/basic", false)
	// test with a broken local env (this helps ensure that we aren't actually running the entrypoints, if we did we'd get an error)
	brokenLocalEnvCtr := baseCtr.WithLoadedEnv("core/integration/testdata/environments/go/broken", false)

	for _, ctr := range []*DaggerCLIContainer{noEnvCtr, validLocalEnvCtr, brokenLocalEnvCtr} {
		type testCase struct {
			testName       string
			cmdCtr         *DaggerCLIContainer
			expectedOutput string
		}
		for _, tc := range []testCase{
			{
				testName:       "dagger env/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnv(),
				expectedOutput: "Usage:\n  dagger environment [flags]\n\nAliases:\n  environment, env",
			},
			{
				testName:       "dagger env init/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnvInit(),
				expectedOutput: "Usage:\n  dagger environment init",
			},
			{
				testName:       "dagger env sync/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnvSync(),
				expectedOutput: "Usage:\n  dagger environment sync",
			},
			{
				testName:       "dagger env extend/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnvExtend("./fake/dep"),
				expectedOutput: "Usage:\n  dagger environment extend",
			},
			{
				testName:       "dagger checks/" + ctr.EnvArg,
				cmdCtr:         ctr.CallChecks(),
				expectedOutput: "Usage:\n  dagger checks",
			},
		} {
			tc := tc
			t.Run(tc.testName, func(t *testing.T) {
				t.Parallel()
				stdout, err := tc.cmdCtr.Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, stdout, tc.expectedOutput)
			})
		}
	}
}

func TestEnvCmdInit(t *testing.T) {
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
			ctr := CLITestContainer(ctx, t, c).
				WithEnvArg(tc.environmentPath).
				WithSDKArg(tc.sdk).
				WithNameArg(tc.name).
				CallEnvInit()

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

			// TODO: test rest of SDKs once custom codegen is supported
			if tc.sdk == "go" {
				codegenFile := filepath.Join(filepath.Dir(expectedConfigPath), "dagger.gen.go")
				_, err := ctr.File(codegenFile).Contents(ctx)
				require.NoError(t, err)
			}

			stderr, err := ctr.CallEnv().Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stderr, fmt.Sprintf(`"name": %q`, tc.name))
			require.Contains(t, stderr, fmt.Sprintf(`"sdk": %q`, tc.sdk))
		})
	}

	t.Run("error on existing environment", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		_, err := CLITestContainer(ctx, t, c).
			WithLoadedEnv("core/integration/testdata/environments/go/basic", false).
			WithSDKArg("go").
			WithNameArg("foo").
			CallEnvInit().
			Sync(ctx)
		require.ErrorContains(t, err, "environment init config path already exists")
	})
}

func TestEnvChecks(t *testing.T) {
	t.Parallel()

	allChecks := []string{
		"cool-static-check",
		"sad-static-check",
		"cool-container-check",
		"sad-container-check",
		"cool-composite-check",
		"sad-composite-check",
		"another-cool-static-check",
		"another-sad-static-check",
		"cool-composite-check-from-explicit-dep",
		"sad-composite-check-from-explicit-dep",
		"cool-composite-check-from-dynamic-dep",
		"sad-composite-check-from-dynamic-dep",
		"cool-check-only-return",
		"cool-check-result-only-return",
		"cool-string-only-return",
		"cool-error-only-return",
		"sad-error-only-return",
		"cool-string-error-return",
		"sad-string-error-return",
	}
	compositeCheckToSubcheckNames := map[string][]string{
		"cool-composite-check": {
			"cool-subcheck-a",
			"cool-subcheck-b",
		},
		"sad-composite-check": {
			"sad-subcheck-a",
			"sad-subcheck-b",
		},
		"cool-composite-check-from-explicit-dep": {
			"another-cool-static-check",
			"another-cool-container-check",
			"another-cool-composite-check",
		},
		"sad-composite-check-from-explicit-dep": {
			"another-sad-static-check",
			"another-sad-container-check",
			"another-sad-composite-check",
		},
		"cool-composite-check-from-dynamic-dep": {
			"yet-another-cool-static-check",
			"yet-another-cool-container-check",
			"yet-another-cool-composite-check",
		},
		"sad-composite-check-from-dynamic-dep": {
			"yet-another-sad-static-check",
			"yet-another-sad-container-check",
			"yet-another-sad-composite-check",
		},
		"another-cool-composite-check": {
			"another-cool-subcheck-a",
			"another-cool-subcheck-b",
		},
		"another-sad-composite-check": {
			"another-sad-subcheck-a",
			"another-sad-subcheck-b",
		},
		"yet-another-cool-composite-check": {
			"yet-another-cool-subcheck-a",
			"yet-another-cool-subcheck-b",
		},
		"yet-another-sad-composite-check": {
			"yet-another-sad-subcheck-a",
			"yet-another-sad-subcheck-b",
		},
	}

	// should be aligned w/ `func checkOutput` in ./testdata/environments/go/basic/main.go
	checkOutput := func(name string) string {
		return "WE ARE RUNNING CHECK " + strcase.ToKebab(name)
	}

	type testCase struct {
		name            string
		environmentPath string
		selectedChecks  []string
		expectFailure   bool
	}
	for _, tc := range []testCase{
		{
			name:            "happy-path",
			environmentPath: "core/integration/testdata/environments/go/basic",
			selectedChecks: []string{
				"cool-static-check",
				"cool-container-check",
				"cool-composite-check",
				"another-cool-static-check",
				"cool-composite-check-from-explicit-dep",
				"cool-composite-check-from-dynamic-dep",
				"cool-check-only-return",
				"cool-check-result-only-return",
				"cool-string-only-return",
				"cool-error-only-return",
				"cool-string-error-return",
			},
		},
		{
			name:            "sad-path",
			expectFailure:   true,
			environmentPath: "core/integration/testdata/environments/go/basic",
			selectedChecks: []string{
				"sad-static-check",
				"sad-container-check",
				"sad-composite-check",
				"another-sad-static-check",
				"sad-composite-check-from-explicit-dep",
				"sad-composite-check-from-dynamic-dep",
				"sad-error-only-return",
				"sad-string-error-return",
			},
		},
		{
			name:            "mixed-path",
			expectFailure:   true,
			environmentPath: "core/integration/testdata/environments/go/basic",
			// run all checks, don't select any
		},
	} {
		tc := tc
		for _, testGitEnv := range []bool{false, true} {
			testGitEnv := testGitEnv
			testName := tc.name
			testName += "/gitenv=" + strconv.FormatBool(testGitEnv)
			testName += "/" + tc.environmentPath
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				stderr, err := CLITestContainer(ctx, t, c).
					WithLoadedEnv(tc.environmentPath, testGitEnv).
					CallChecks(tc.selectedChecks...).
					Stderr(ctx)
				if tc.expectFailure {
					require.Error(t, err)
					execErr := new(dagger.ExecError)
					require.True(t, errors.As(err, &execErr))
					stderr = execErr.Stderr
				} else {
					require.NoError(t, err)
				}

				selectedChecks := tc.selectedChecks
				if len(selectedChecks) == 0 {
					selectedChecks = allChecks
				}

				curChecks := selectedChecks
				for len(curChecks) > 0 {
					var nextChecks []string
					for _, checkName := range curChecks {
						subChecks, ok := compositeCheckToSubcheckNames[checkName]
						if ok {
							nextChecks = append(nextChecks, subChecks...)
						} else {
							// special case for successful error only check, doesn't have output
							if checkName == "cool-error-only-return" {
								continue
							}
							require.Contains(t, stderr, checkOutput(checkName))
						}
					}
					curChecks = nextChecks
				}
			})
		}
	}
}
