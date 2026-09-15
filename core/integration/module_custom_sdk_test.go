package core

// These tests cover modules whose `sdk` points to another Dagger module instead
// of a built-in SDK. They verify local and Git-backed SDK modules, runtime and
// codegen hooks, and SDK modules that implement only part of the provider API.
//
// See also:
// - module_go_test.go: built-in Go SDK behavior.
// - module_python_test.go: built-in Python SDK behavior.
// - module_typescript_test.go: built-in TypeScript SDK behavior.

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func customSDKRuntimeFixture(t *testctx.T, c *dagger.Client, sdkDir string) *dagger.Container {
	t.Helper()

	return workspaceFixture(t, c, "custom-sdk/runtime").
		With(withTestdataFixture(t, c, "sdk", "sdks", sdkDir))
}

func customSDKLocalFixture(t *testctx.T, c *dagger.Client) *dagger.Container {
	t.Helper()

	return goGitBase(t, c).
		With(withModuleFixture(t, c, ".", "go/custom-sdk-test-local")).
		With(withModuleFixture(t, c, "coolsdk", "go/custom-sdk-cool-sdk"))
}

func customSDKInitFixture(t *testctx.T, c *dagger.Client) *dagger.Container {
	t.Helper()

	return goGitBase(t, c).
		With(withModuleFixture(t, c, ".", "go/custom-sdk-init-test")).
		With(withModuleFixture(t, c, "coolsdk", "go/custom-sdk-init-sdk"))
}

func customSDKGitFixture(t *testctx.T, c *dagger.Client, sdkSource string) *dagger.Container {
	t.Helper()

	cfg := fmt.Sprintf(`{
  "name": "test",
  "engineVersion": "latest",
  "sdk": {
    "source": %q
  },
  "source": "."
}`, sdkSource)

	return goGitBase(t, c).
		With(withTestdataFile(t, c, "main.go", "modules", "go", "custom-sdk-test-git", "main.go")).
		WithNewFile("dagger.json", cfg)
}

func (ModuleSuite) TestCustomSDKRuntime(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := customSDKRuntimeFixture(t, c, "only-runtime").
		With(daggerCall("hello-world")).
		CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "Hello world")
}

func (ModuleSuite) TestPartialCustomSDKRuntime(ctx context.Context, t *testctx.T) {
	t.Run("only codegen rejects runtime calls", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := customSDKRuntimeFixture(t, c, "only-codegen").
			With(daggerExec("call", "foo")).
			Sync(ctx)
		requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
	})

	t.Run("only runtime exposes functions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := customSDKRuntimeFixture(t, c, "only-runtime")
		out, err := ctr.With(daggerFunctions()).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "hello-world")

		out, err = ctr.With(daggerCall("hello-world")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Hello world")
	})
}

func (ModuleSuite) TestCustomSDK(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := customSDKLocalFixture(t, c).
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

			out, err := customSDKGitFixture(t, c, testGitModuleRef(tc, "cool-sdk")).
				With(privateSetup).
				With(daggerCall("fn")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
	})

	t.Run("module types", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Verify that SDKs can create an exec and call CurrentModule().Source
		// while producing module type definitions.
		out, err := customSDKInitFixture(t, c).
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

		ctr := customSDKRuntimeFixture(t, c, "only-codegen")

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

		ctr := customSDKRuntimeFixture(t, c, "only-runtime")

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
