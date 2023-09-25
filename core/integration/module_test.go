package core

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

/* TODO: add coverage for
* dagger mod use
* dagger mod sync
* that the codegen of the testdata envs are up to date (or incorporate that into a cli command)
* if a dependency changes, then checks should re-run
 */

func TestModuleGoInit(t *testing.T) {
	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=bare", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{bare{myFunction(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"myFunction":{"stdout":"hello\n"}}}`, out)

		t.Run("configures .gitignore", func(t *testing.T) {
			ignore, err := modGen.File(".gitignore").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, ignore, "/dagger.gen.go\n")
			require.Contains(t, ignore, "/internal/\n")
		})

		t.Run("configures .gitattributes", func(t *testing.T) {
			attributes, err := modGen.File(".gitattributes").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, attributes, "/dagger.gen.go linguist-generated=true\n")
		})
	})

	t.Run("kebab-cases Go module name, camel-cases Dagger module name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=My-Module", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{myModule{myFunction(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"myFunction":{"stdout":"hello\n"}}}`, out)

		generated, err := modGen.File("go.mod").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, generated, "module my-module")
	})

	t.Run("creates go.mod beneath an existing go.mod", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/go.mod", dagger.ContainerWithNewFileOpts{
				Contents: "module example.com/test\n",
			}).
			WithNewFile("/work/foo.go", dagger.ContainerWithNewFileOpts{
				Contents: "package foo\n",
			}).
			WithWorkdir("/work/ci").
			With(daggerExec("mod", "init", "--name=beneathGoMod", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{beneathGoMod{myFunction(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"beneathGoMod":{"myFunction":{"stdout":"hello\n"}}}`, out)

		t.Run("names Go module after Dagger module", func(t *testing.T) {
			generated, err := modGen.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module beneath-go-mod")
		})
	})

	t.Run("respects existing go.mod", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			With(daggerExec("mod", "init", "--name=hasGoMod", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasGoMod{myFunction(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"myFunction":{"stdout":"hello\n"}}}`, out)

		t.Run("preserves module name", func(t *testing.T) {
			generated, err := modGen.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module example.com/test")
		})
	})

	t.Run("respects existing main.go", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type HasMainGo struct {}

func (m *HasMainGo) Hello() string { return "Hello, world!" }
`,
			}).
			With(daggerExec("mod", "init", "--name=hasMainGo", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasMainGo":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("respects existing package without creating main.go", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/notmain.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type HasNotMainGo struct {}

func (m *HasNotMainGo) Hello() string { return "Hello, world!" }
`,
			}).
			With(daggerExec("mod", "init", "--name=hasNotMainGo", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasNotMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasNotMainGo":{"hello":"Hello, world!"}}`, out)
	})
}

func TestModuleGoSyncRemovesIgnored(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	committedModGen := c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"}).
		With(daggerExec("mod", "init", "--name=bare", "--sdk=go")).
		WithExec([]string{"rm", ".gitignore"}).
		WithExec([]string{"cp", "-a", "./internal/querybuilder", "./querybuilder"}).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init with generated files"})

	changedAfterSync, err := committedModGen.
		With(daggerExec("mod", "sync")).
		WithExec([]string{"git", "diff"}). // for debugging
		WithExec([]string{"git", "status", "--short"}).
		Stdout(ctx)
	require.NoError(t, err)
	t.Logf("changed after sync:\n%s", changedAfterSync)
	require.Contains(t, changedAfterSync, "D  dagger.gen.go\n")
	require.Contains(t, changedAfterSync, "D  querybuilder/marshal.go\n")
	require.Contains(t, changedAfterSync, "D  querybuilder/querybuilder.go\n")
	require.Contains(t, changedAfterSync, "D  internal/querybuilder/marshal.go\n")
	require.Contains(t, changedAfterSync, "D  internal/querybuilder/querybuilder.go\n")
}

//go:embed testdata/modules/go/minimal/main.go
var goSignatures string

func TestModuleGoSignatures(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goSignatures,
		})

	logGen(ctx, t, modGen.Directory("."))

	t.Run("func Hello() string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"hello":"hello"}}`, out)
	})

	t.Run("func Echo(string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echo(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echo":"hello...hello...hello..."}}`, out)
	})

	t.Run("func HelloContext(context.Context) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloContext}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloContext":"hello context"}}`, out)
	})

	t.Run("func EchoContext(context.Context, string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoContext(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoContext":"ctx.hello...ctx.hello...ctx.hello..."}}`, out)
	})

	t.Run("func HelloStringError() (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloStringError}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloStringError":"hello i worked"}}`, out)
	})

	t.Run("func HelloVoid()", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloVoid}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoid":null}}`, out)
	})

	t.Run("func HelloVoidError() error", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloVoidError}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoidError":null}}`, out)
	})

	t.Run("func EchoOpts(string, Opts) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi...hi...hi..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInline(string, struct{Suffix string, Times int}) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInline(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInline":"hi...hi...hi..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInline(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInline":"hi!hi!"}}`, out)
	})
}

//go:embed testdata/modules/go/extend/main.go
var goExtend string

func TestModuleGoExtendCore(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goExtend,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{container{from(address:"` + alpineImage + `"){echo(msg:"hi!")}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"container":{"from":{"echo":"hi!\n"}}}`, out)
}

//go:embed testdata/modules/go/custom-types/main.go
var customTypes string

func TestModuleGoCustomTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: customTypes,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{test{repeater(msg:"echo!", times: 3){render}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"repeater":{"render":"echo!echo!echo!"}}}`, out)
}

//go:embed testdata/modules/go/use/dep/main.go
var useInner string

//go:embed testdata/modules/go/use/main.go
var useOuter string

func TestModuleGoUseLocal(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=use", "--sdk=go")).
		WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
			Contents: useInner,
		}).
		WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
			Contents: useOuter,
		}).
		With(daggerExec("mod", "use", "./dep")).
		WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

	// cannot use transitive dependency directly
	_, err = modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, `Cannot query field "dep" on type "Query".`)
}

//go:embed testdata/modules/go/wrapper/main.go
var wrapper string

func TestModuleGoWrapping(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=wrapper", "--sdk=go")).
		WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
			Contents: wrapper,
		})

	logGen(ctx, t, modGen.Directory("."))

	id := identity.NewID()
	out, err := modGen.With(daggerQuery(
		fmt.Sprintf(`{wrapper{container{echo(msg:%q){unwrap{stdout}}}}}`, id),
	)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t,
		fmt.Sprintf(`{"wrapper":{"container":{"echo":{"unwrap":{"stdout":%q}}}}}`, id),
		out)
}

func TestEnvCmd(t *testing.T) {
	t.Skip("pending conversion to modules")

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
	t.Skip("pending conversion to modules")

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
	t.Skip("pending conversion to modules")

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
	t.Skip("pending conversion to modules")

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

func daggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--debug"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerQuery(query string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "--debug", "query"}, dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func logGen(ctx context.Context, t *testing.T, modSrc *dagger.Directory) {
	generated, err := modSrc.File("dagger.gen.go").Contents(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		t.Name()
		fileName := filepath.Join(
			os.TempDir(),
			t.Name(),
			fmt.Sprintf("dagger.gen.%d.go", time.Now().Unix()),
		)

		if err := os.MkdirAll(filepath.Dir(fileName), 0o755); err != nil {
			t.Logf("failed to create temp dir for generated code: %v", err)
			return
		}

		if err := os.WriteFile(fileName, []byte(generated), 0644); err != nil {
			t.Logf("failed to write generated code to %s: %v", fileName, err)
		} else {
			t.Logf("wrote generated code to %s", fileName)
		}
	})
}
