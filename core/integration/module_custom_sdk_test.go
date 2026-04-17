package core

// Workspace alignment: mostly aligned; coverage targets post-workspace custom SDK loading and capability behavior, though setup still relies on historical module helpers.
// Scope: Loading custom SDKs from local or git-backed sources, and validating partial SDK capability support.
// Intent: Keep custom SDK provider behavior separate from built-in SDK coverage and the remaining module runtime umbrella.

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestCustomSDK(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}

func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
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

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
			)

		out, err := ctr.
			With(daggerCall("fn")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			ctr := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(privateSetup).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk="+testGitModuleRef(tc, "cool-sdk"))).
				WithNewFile("main.go", `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
				)

			out, err := ctr.
				With(daggerCall("fn")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
	})

	t.Run("module initialization", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// verify that SDKs can successfully:
		// - create an exec during module initialization
		// - call CurrentModule().Source
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}


func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	// return hardcoded typedefs; this module will thus only work during init, but that's all we're testing here
	mod := dag.Module().WithObject(dag.TypeDef().
		WithObject("Test").
		WithFunction(dag.Function("CoolFn", dag.TypeDef().WithKind(dagger.TypeDefKindVoidKind).WithOptional(true))))
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

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", `package main

type Test struct {}
`,
			)

		out, err := ctr.
			With(daggerFunctions()).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `cool-fn`)
	})
}

// TestUnbundleSDK verifies that you can implement a SDK without
// having to implements the full interface but only the ones you want.
// cc: https://github.com/dagger/dagger/issues/7707
func (ModuleSuite) TestUnbundleSDK(ctx context.Context, t *testctx.T) {
	t.Run("only codegen", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/work/sdk", c.Host().Directory("./testdata/sdks/only-codegen")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=./sdk", "--source=."))

		t.Run("can run dagger develop", func(ctx context.Context, t *testctx.T) {
			generatedFile, err := ctr.With(daggerExec("develop")).File("/work/hello.txt").Contents(ctx)

			require.NoError(t, err)
			require.Equal(t, "Hello, world!", generatedFile)
		})

		t.Run("explicit error on dagger call", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerExec("call", "foo")).Sync(ctx)

			requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
		})

		t.Run("explicit error on dagger functions", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerFunctions()).Sync(ctx)

			requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
		})
	})

	t.Run("only runtime", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/work/sdk", c.Host().Directory("./testdata/sdks/only-runtime")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=./sdk", "--source=."))

		t.Run("can run dagger develop without failing", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerExec("develop")).Sync(ctx)

			require.NoError(t, err)
		})

		t.Run("can run dagger functions", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerFunctions()).Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "hello-world")
		})

		t.Run("can run dagger call", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("hello-world")).Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "Hello world")
		})
	})
}
