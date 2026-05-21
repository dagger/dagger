package core

// These tests cover current module-shaped `dagger.json` config for a single
// module. They verify validation, normalization, dependencies, SDK config, and
// source/context rules.
//
// See also:
// - module_config_compat_test.go: old module config shapes still accepted.
// - workspace_compat_test.go: legacy `dagger.json` workspace inference.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
)

func (ModuleConfigSuite) TestConfigs(ctx context.Context, t *testctx.T) {
	// Test dagger.json source configs that are part of the current supported
	// module config surface and aren't inherently covered in other tests.
	t.Run("malicious config", func(ctx context.Context, t *testctx.T) {
		// verify a maliciously/incorrectly constructed dagger.json is still handled correctly

		baseCtr := func(t *testctx.T, c *dagger.Client) *dagger.Container {
			return goGitBase(t, c).
				With(withModuleFixture(t, c, "/tmp/foo", "go/config-malicious-dep")).
				With(withModuleFixture(t, c, "/work/dep", "go/config-malicious-dep")).
				With(withModuleFixture(t, c, "/work", "go/config-malicious")).
				WithWorkdir("/work")
		}

		t.Run("source points out of root", func(ctx context.Context, t *testctx.T) {
			t.Run("local", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				base := baseCtr(t, c).
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK: &modules.SDK{
							Source: "go",
						},
						Source: "..",
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				requireErrOut(t, err, `source path ".." escapes context from source root "."`)
			})

			t.Run("local with absolute path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				base := baseCtr(t, c).
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK: &modules.SDK{
							Source: "go",
						},
						Source: "/tmp",
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				requireErrOut(t, err, `source path "/tmp" is absolute`)
			})

			testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
				t.Run("git", func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)
					privateSetup, cleanup := privateRepoSetup(c, t, tc)
					defer cleanup()

					_, err := baseCtr(t, c).With(privateSetup).With(daggerCallAt(testGitModuleRef(tc, "invalid/bad-source"), "container-echo", "--string-arg", "plz fail")).Sync(ctx)
					requireErrOut(t, err, `source path "../../../" escapes context`)
				})
			})
		})

		t.Run("dep points out of root", func(ctx context.Context, t *testctx.T) {
			t.Run("local", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				base := baseCtr(t, c).
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK: &modules.SDK{
							Source: "go",
						},
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "..",
						}},
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				requireErrOut(t, err, `local module dep source path ".." escapes context "/work"`)

				base = base.
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK: &modules.SDK{
							Source: "go",
						},
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "../tmp/foo",
						}},
					}))

				_, err = base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				requireErrOut(t, err, `local module dep source path "../tmp/foo" escapes context "/work"`)
			})

			t.Run("local with absolute path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				base := baseCtr(t, c).
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK: &modules.SDK{
							Source: "go",
						},
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "/tmp/foo",
						}},
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				requireErrOut(t, err, `local module dep source path "/tmp/foo" is absolute`)

				base = base.
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK: &modules.SDK{
							Source: "go",
						},
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "/./dep",
						}},
					}))

				_, err = base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				requireErrOut(t, err, `local module dep source path "/./dep" is absolute`)
			})

			testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
				t.Run("git", func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)
					privateSetup, cleanup := privateRepoSetup(c, t, tc)
					defer cleanup()

					_, err := baseCtr(t, c).With(privateSetup).With(daggerCallAt(testGitModuleRef(tc, "invalid/bad-dep"), "container-echo", "--string-arg", "plz fail")).Sync(ctx)
					requireErrRegexp(t, err, `git module source ".*" does not contain a dagger config file`)
				})
			})
		})
	})

}

func (ModuleConfigSuite) TestCustomDepNames(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			With(withModuleFixture(t, c, "/work", "go/config-custom-dep-names-basic")).
			WithWorkdir("/work")

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("get-obj")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo from dep", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("get-other-obj")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey from dep", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("get-conflict-name-obj", "str")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "it worked?", strings.TrimSpace(out))
	})

	t.Run("same mod name as dep", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			With(withModuleFixture(t, c, "/work", "go/config-custom-dep-names-same-name")).
			WithWorkdir("/work")

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))
	})

	t.Run("two deps with same name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			With(withModuleFixture(t, c, "/work", "go/config-custom-dep-names-two-deps")).
			WithWorkdir("/work")

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep1 hi from dep2", strings.TrimSpace(out))
	})
}

func (ModuleConfigSuite) TestSDKConfig(ctx context.Context, t *testctx.T) {
	t.Run("go sdk", func(ctx context.Context, t *testctx.T) {
		testcases := []struct {
			name          string
			daggerjson    string
			expectedValue string
			expectedError string
		}{
			{
				name: "go sdk supports goprivate",
				daggerjson: `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "go",
		"config": {
			"goprivate": "github.com/foobar"
		}
	}
}`,
				expectedValue: "github.com/foobar",
			},
			{
				name: "go sdk errors if invalid value for goprivate is configured",
				daggerjson: `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "go",
		"config": {
			"goprivate": 1234
		}
	}
}`,
				expectedError: "'GoPrivate' expected type 'string', got unconvertible type 'float64', value: '1234'",
			},
			{
				name: "unknown sdk config keys returns error",
				daggerjson: `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "go",
		"config": {
			"foobar": 1234
		}
	}
}`,
				expectedError: `unknown sdk config keys found [foobar]`,
			},
		}

		for _, tc := range testcases {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				ctr := goGitBase(t, c).
					With(withModuleFixture(t, c, "/work", "go/config-sdk-go")).
					WithWorkdir("/work").
					WithNewFile("dagger.json", tc.daggerjson)

				output, err := ctr.With(daggerCall("check-env")).Stdout(ctx)
				if tc.expectedError != "" {
					require.NotNil(t, err)
					execerror := err.(*dagger.ExecError)
					require.Contains(t, execerror.Stderr, tc.expectedError)
				} else {
					require.Nil(t, err)
					require.Equal(t, tc.expectedValue, output)
				}
			})
		}
	})

	t.Run("module sdk", func(ctx context.Context, t *testctx.T) {
		daggerjson := `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "coolsdk"
	}
}
`

		daggerjsonWithValidSDKConfig := `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "coolsdk",
		"config": {
			"barConfig": "override-value"
		}
	}
}`

		daggerjsonWithInvalidValueForSDKConfig := `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "coolsdk",
		"config": {
			"barConfig": 1234
		}
	}
}`

		daggerjsonWithUnknownConfigKey := `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "coolsdk",
		"config": {
			"foobar": 1234
		}
	}
}`

		for _, tc := range []struct {
			name             string
			fixture          string
			expectedCoolName string
			daggerjson       string
			expectedError    string
		}{
			{
				name:             "withConfig function is optional if no sdk config specified in dagger.json",
				fixture:          "go/config-sdk-module-no-config-support",
				expectedCoolName: "class-default",
				daggerjson:       daggerjson,
			},
			{
				name:             "withConfig function is required if dagger.json has sdk config specified",
				fixture:          "go/config-sdk-module-no-config-support",
				expectedCoolName: "class-default",
				daggerjson:       daggerjsonWithValidSDKConfig,
				expectedError:    "sdk does not currently support specifying config",
			},
			{
				name:             "withConfig function is called if it exists with sdk config from dagger json",
				fixture:          "go/config-sdk-module-with-config-support",
				expectedCoolName: "override-value",
				daggerjson:       daggerjsonWithValidSDKConfig,
			},
			{
				name:             "if sdk config not provided, use the default arg value in withConfig function",
				fixture:          "go/config-sdk-module-with-config-support",
				expectedCoolName: "func-default",
				daggerjson:       daggerjson,
			},
			{
				name:          "invalid format for sdk config in dagger json",
				fixture:       "go/config-sdk-module-with-config-support",
				daggerjson:    daggerjsonWithInvalidValueForSDKConfig,
				expectedError: `parsing value for arg "barConfig": cannot create String from float64`,
			},
			{
				name:          "unknown config key returns error",
				fixture:       "go/config-sdk-module-with-config-support",
				daggerjson:    daggerjsonWithUnknownConfigKey,
				expectedError: `unknown sdk config keys found [foobar]`,
			},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				ctr := goGitBase(t, c).
					With(withModuleFixture(t, c, "/work", tc.fixture)).
					WithWorkdir("/work").
					WithNewFile("dagger.json", tc.daggerjson)

				output, err := ctr.With(daggerCall("get-cool-name")).Stdout(ctx)
				if tc.expectedError != "" {
					require.NotNil(t, err)
					execerror := err.(*dagger.ExecError)
					require.Contains(t, execerror.Stderr, tc.expectedError)
				} else {
					require.Nil(t, err)
					require.Equal(t, tc.expectedCoolName, output)
				}
			})
		}
	})
}

func (ModuleConfigSuite) TestIncludeExclude(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk                    string
		fixture                string
		mainSource             string
		customSDKSource        string
		customSDKUnderlyingSDK string
	}{
		{
			sdk:     "go",
			fixture: "go/config-include-exclude",
			mainSource: `package main
import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn() *dagger.Directory {
	return dag.CurrentModule().Source()
}
			`,
		},
		{
			sdk:     "python",
			fixture: "python/config-include-exclude",
			mainSource: `import dagger
from dagger import dag, function, object_type

@object_type
class Test:
    @function
    def fn(self) -> dagger.Directory:
        return dag.current_module().source()
`,
		},
		{
			sdk:     "typescript",
			fixture: "typescript/config-include-exclude",
			mainSource: `
import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  fn(): Directory {
    return dag.currentModule().source()
  }
}`,
		},
		{
			sdk:     "coolsdk",
			fixture: "go/config-include-exclude-coolsdk",
			mainSource: `package main
import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn() *dagger.Directory {
	return dag.CurrentModule().Source()
}
`,
			customSDKUnderlyingSDK: "go",
			customSDKSource: `package main

import (
	"context"
	"encoding/json"

	"dagger/coolsdk/internal/dagger"
)

type Coolsdk struct {}

func (m *Coolsdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	mod := modSource.WithSDK("go").AsModule()
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(modID)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine").
		WithNewFile(outputFilePath, string(b)).
		WithEntrypoint([]string{
			"sh", "-c", "",
		}), nil
}

func (m *Coolsdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *Coolsdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	modSource = modSource.WithSDK("go")
	return dag.GeneratedCode(
		// apply generated diff over context directory
		modSource.ContextDirectory().WithDirectory("/", modSource.GeneratedContextDirectory()),
	)
}
`,
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := goGitBase(t, c).
				With(withModuleFixture(t, c, "/work", tc.fixture)).
				WithWorkdir("/work")

			// TODO: use cli to configure include/exclude once supported
			ctr = ctr.
				With(configFile(".", &modules.ModuleConfig{
					Name: "test",
					SDK: &modules.SDK{
						Source: tc.sdk,
					},
					Include: []string{"dagger/subdir/keepdir", "!dagger/subdir/keepdir/rmdir"},
					Source:  "dagger",
				})).
				WithDirectory("dagger/subdir/keepdir/rmdir", c.Directory())

			// call should work even though dagger.json and main source files weren't
			// explicitly included
			out, err := ctr.
				With(daggerCall("fn", "directory", "--path", "subdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "keepdir/", strings.TrimSpace(out))

			out, err = ctr.
				With(daggerCall("fn", "directory", "--path", "subdir/keepdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", strings.TrimSpace(out))

			// call should also work from other directories
			out, err = ctr.
				WithWorkdir("/mnt").
				With(daggerCallAt("../work", "fn", "directory", "--path", "subdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "keepdir/", strings.TrimSpace(out))

		})
	}

	t.Run("dependency", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			With(withModuleFixture(t, c, "/work", "go/config-include-exclude-dependency")).
			WithWorkdir("/work")

		t.Run("dependency filtered", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.
				With(daggerCallAt("dep", "context-directory")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "/src/dep/foo")
			require.NotContains(t, out, "/src/dep/.dagger/bar")
		})

		t.Run("main module not affected", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.
				With(daggerCall("context-directory")).
				Stdout(ctx)

			require.NoError(t, err)
			require.NotContains(t, out, "/src/foo")
			require.Contains(t, out, "/src/.dagger/bar")
		})
	})
}

// verify that if there is no local .git in parent dirs then the context defaults to the source root
func (ModuleConfigSuite) TestContextDefaultsToSourceRoot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		With(withModuleFixture(t, c, "/work", "go/config-context-defaults-source-root")).
		WithWorkdir("/work").
		WithNewFile("random-file", "")

	out, err := ctr.
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "random-file")
}

// Git hosting providers to test behavior against
type vcsTestCase struct {
	name              string
	gitTestRepoRef    string
	gitTestRepoCommit string
	// host component of repoURL
	expectedHost string
	// base HTML URL might differ from ref (e.g. not contain .git ; vanity URLs )
	expectedBaseHTMLURL string
	// path separator to access `tree` view of src at commit, per provider
	expectedURLPathComponent string
	// Azure needs a path prefix
	expectedPathPrefix string
	isPrivateRepo      bool
	skipProxyTest      bool

	// encodedToken is a based64 encoded read-only PAT
	encodedToken string
	// encodedToken2 is an optional second token to test cases of using different tokens for the same repo
	encodedToken2 string
	// sshKey determines whether to propagate the host's ssh-key
	sshKey bool
}

func (tc vcsTestCase) token() string {
	return decodedGitToken(tc.encodedToken)
}

func decodedGitToken(encodedToken string) string {
	decodedToken, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		return ""
	}
	decodedToken = bytes.TrimSpace(decodedToken)
	return string(decodedToken)
}

const vcsTestCaseCommit = "d730fb3af8757e1ca293e01aa4fcfd510a6e40e5"

var vcsTestCases = []vcsTestCase{
	// Test cases for public repositories using Go-style references, without '.git' suffix (optional)
	// These cases verify correct handling of repository URLs across different Git hosting providers

	// GitHub public repository
	{
		name:                     "GitHub public",
		gitTestRepoRef:           "github.com/dagger/dagger-test-modules",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "github.com",
		expectedBaseHTMLURL:      "github.com/dagger/dagger-test-modules",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
	},
	{
		name:                     "GitLab public",
		gitTestRepoRef:           "gitlab.com/dagger-modules/test/more/dagger-test-modules-public",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "gitlab.com",
		expectedBaseHTMLURL:      "gitlab.com/dagger-modules/test/more/dagger-test-modules-public",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
	},
	// {
	// 	name:                     "BitBucket public",
	// 	gitTestRepoRef:           "bitbucket.org/dagger-modules/dagger-test-modules-public",
	// 	gitTestRepoCommit:        vcsTestCaseCommit,
	// 	expectedHost:             "bitbucket.org",
	// 	expectedBaseHTMLURL:      "bitbucket.org/dagger-modules/dagger-test-modules-public",
	// 	expectedURLPathComponent: "src",
	// 	expectedPathPrefix:       "",
	// },
	{
		name:                     "Azure DevOps public",
		gitTestRepoRef:           "dev.azure.com/daggere2e/public/_git/dagger-test-modules",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "dev.azure.com",
		expectedBaseHTMLURL:      "dev.azure.com/daggere2e/public/_git/dagger-test-modules",
		expectedURLPathComponent: "commit",
		expectedPathPrefix:       "?path=",
	},

	// SSH references support both private and public repositories across various Git hosting providers.
	// The following test cases demonstrate the handling of SSH references for different scenarios.

	// GitLab private repository using explicit SSH reference format
	{
		name:                     "SSH Private GitLab",
		gitTestRepoRef:           "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "gitlab.com",
		expectedBaseHTMLURL:      "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
		isPrivateRepo:            true,
		skipProxyTest:            true,
		sshKey:                   true,
	},
	// GitLab private repository using PAT
	{
		name:                     "Private GitLab",
		gitTestRepoRef:           "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "gitlab.com",
		expectedBaseHTMLURL:      "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
		isPrivateRepo:            true,
		// NOTE: this is not a security vulnerability, these tokens are read-only and scoped to a test repository
		// with no actual private code
		encodedToken:  "Z2xwYXQtMGF2bWZBbHBxWENwOXpuazZfZ2JmbTg2TVFwMU9tTjRhV3BqQ3cuMDEuMTIxbWF0b2Rx",
		encodedToken2: "Z2xwYXQtcFVIWDVmZmVCUmdjZ2FYTHdndjNPVzg2TVFwMU9tTjRhV3BqQ3cuMDEuMTIxa2oyMHJi",
	},
	// BitBucket private repository using SCP-like SSH reference format
	{
		name:                     "SSH Private BitBucket",
		gitTestRepoRef:           "git@bitbucket.org:dagger-modules/private-modules-test.git",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "bitbucket.org",
		expectedBaseHTMLURL:      "bitbucket.org/dagger-modules/private-modules-test",
		expectedURLPathComponent: "src",
		expectedPathPrefix:       "",
		isPrivateRepo:            true,
		skipProxyTest:            true,
		sshKey:                   true,
	},
	// GitHub public repository using SSH reference
	// Note: This format is also valid for private GitHub repositories
	{
		name:                     "SSH Public GitHub",
		gitTestRepoRef:           "git@github.com:dagger/dagger-test-modules.git",
		gitTestRepoCommit:        vcsTestCaseCommit,
		expectedHost:             "github.com",
		expectedBaseHTMLURL:      "github.com/dagger/dagger-test-modules",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
		skipProxyTest:            true,
		sshKey:                   true,
	},
	// Azure DevOps private repository using SSH reference
	// Note: Currently commented out due to Azure DevOps limitations on scoped SSH keys at the repository level
	//
	//	{
	//		name:                     "SSH Private Azure",
	//		gitTestRepoRef:           "git@ssh.dev.azure.com:v3/daggere2e/private/dagger-test-modules",
	//		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
	//		expectedHost:             "dev.azure.com",
	//		expectedBaseHTMLURL:      "dev.azure.com/daggere2e/private/_git/dagger-test-modules",
	//		expectedURLPathComponent: "commit",
	//		expectedPathPrefix:       "?path=",
	//		isPrivateRepo:              true,
	//	},
}

func testOnMultipleVCS(t *testctx.T, testFunc func(ctx context.Context, t *testctx.T, tc vcsTestCase)) {
	for _, tc := range vcsTestCases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			testFunc(ctx, t, tc)
		})
	}
}

func getVCSTestCase(t *testctx.T, url string) vcsTestCase {
	for _, tc := range vcsTestCases {
		if tc.gitTestRepoRef == url {
			return tc
		}
	}
	require.Fail(t, "no test case found", url)
	return vcsTestCase{}
}

func testGitModuleRef(tc vcsTestCase, subpath string) string {
	url := tc.gitTestRepoRef
	if subpath != "" {
		if !strings.HasPrefix(subpath, "/") {
			subpath = "/" + subpath
		}
		url += subpath
	}
	return fmt.Sprintf("%s@%s", url, tc.gitTestRepoCommit)
}

func (ModuleConfigSuite) TestDaggerGitRefs(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		c := connect(ctx, t)

		repoSetup, done := privateRepoSetup(c, t, tc)
		t.Cleanup(done)
		base := goGitBase(t, c).
			With(repoSetup)

		t.Run("root module", func(ctx context.Context, t *testctx.T) {
			htmlURL, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, ""), "html-url")).
				Stdout(ctx)
			require.NoError(t, err)
			expectedURL := fmt.Sprintf("https://%s/%s/%s", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit)
			require.Equal(t, expectedURL, htmlURL)
			// URL format matches public repo from same provider.
			// No need to test with auth on those refs
			if !tc.isPrivateRepo {
				resp, err := http.Get(htmlURL)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, fmt.Sprintf("https://%s/%s/%s", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit), htmlURL)
			}

			commit, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, ""), "commit")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.gitTestRepoCommit, commit)

			refStr, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, ""), "as-string")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, ""), refStr)
		})

		t.Run("top-level module", func(ctx context.Context, t *testctx.T) {
			htmlURL, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, "top-level"), "html-url")).
				Stdout(ctx)
			require.NoError(t, err)
			expectedURL := fmt.Sprintf("https://%s/%s/%s%s/top-level", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit, tc.expectedPathPrefix)
			require.Equal(t, expectedURL, htmlURL)

			// URL format matches public repo from same provider.
			// No need to test with auth on those refs
			if !tc.isPrivateRepo {
				resp, err := http.Get(htmlURL)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
			}

			commit, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, "top-level"), "commit")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.gitTestRepoCommit, commit)

			refStr, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, "top-level"), "as-string")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "top-level"), refStr)
		})

		t.Run("subdir dep2 module", func(ctx context.Context, t *testctx.T) {
			htmlURL, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, "subdir/dep2"), "html-url")).
				Stdout(ctx)
			require.NoError(t, err)
			expectedURL := fmt.Sprintf("https://%s/%s/%s%s/subdir/dep2", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit, tc.expectedPathPrefix)
			require.Equal(t, expectedURL, htmlURL)

			// URL format matches public repo from same provider.
			// No need to test with auth on those refs
			if !tc.isPrivateRepo {
				resp, err := http.Get(htmlURL)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
			}

			commit, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, "subdir/dep2"), "commit")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.gitTestRepoCommit, commit)

			refStr, err := base.
				With(daggerExec("core", "module-source", "--ref-string", testGitModuleRef(tc, "subdir/dep2"), "as-string")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "subdir/dep2"), refStr)
		})
	})
}

func (ModuleConfigSuite) TestDaggerGitWithSources(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		for _, modSubpath := range []string{"samedir", "subdir"} {
			t.Run(modSubpath, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				privateSetup, cleanup := privateRepoSetup(c, t, tc)
				defer cleanup()

				ctr := goGitBase(t, c).
					With(privateSetup).
					With(withModuleFixture(t, c, "/work", "go/config-dagger-git-with-sources")).
					WithWorkdir("/work").
					With(configFile(".", &modules.ModuleConfig{
						Name: "test",
						SDK: &modules.SDK{
							Source: "go",
						},
						Source: ".",
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "foo",
							Source: testGitModuleRef(tc, "various-source-values/"+modSubpath),
						}},
					}))

				out, err := ctr.With(daggerCallAt("foo", "container-echo", "--string-arg", "hi", "stdout")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi", strings.TrimSpace(out))

				out, err = ctr.With(daggerCallAt(testGitModuleRef(tc, "various-source-values/"+modSubpath), "container-echo", "--string-arg", "hi", "stdout")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi", strings.TrimSpace(out))
			})
		}
	})
}

func (ModuleConfigSuite) TestDepPins(ctx context.Context, t *testctx.T) {
	// check that pins are correctly followed and loaded

	c := connect(ctx, t)

	repo := "github.com/dagger/dagger-test-modules/versioned"
	branch := "main"
	commit := "82adc5f7997e43ab3027810347298405f32a44db"

	ctr := goGitBase(t, c).
		With(withModuleFixture(t, c, "/work", "go/config-dep-pins")).
		WithWorkdir("/work")

	modCfgContents, err := ctr.
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)

	var modCfg modules.ModuleConfig
	require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
	modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
		Name:   "versioned",
		Source: repo + "@" + branch,
		Pin:    commit,
	})
	rewrittenModCfg, err := json.Marshal(modCfg)
	require.NoError(t, err)
	ctr = ctr.WithNewFile("dagger.json", string(rewrittenModCfg))

	out, err := ctr.With(daggerExec("call", "hello")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "VERSION 2")
}
