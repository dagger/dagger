package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type ToolchainSuite struct{}

func TestToolchain(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ToolchainSuite{})
}

func toolchainTestEnv(t *testctx.T, c *dagger.Client) *dagger.Container {
	return c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "init"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory(".", c.Host().Directory("./testdata/test-blueprint")).
		WithDirectory("app", c.Directory())
}

func (ToolchainSuite) TestToolchainConstructor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Test single toolchain installation
	t.Run("use toolchain constructor", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-constructor"))
		modGen = modGen.WithNewFile("app-config.txt", "this is the app configuration").
			WithNewFile("other-config.txt", "this is the other app configuration")
		appConfig, err := modGen.
			With(daggerExec("call", "hello", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
		appConfig, err = modGen.
			With(daggerExec("call", "hello", "--config", "other-config.txt", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the other app configuration")
		// Test multiple toolchain installations
		modGen = modGen.With(daggerExec("toolchain", "install", "../myblueprint-ts"))
		appConfig, err = modGen.
			With(daggerExec("call", "hello", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
		appConfig, err = modGen.
			With(daggerExec("call", "hello", "--config", "other-config.txt", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the other app configuration")
	})
}

func (ToolchainSuite) TestMultipleToolchains(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("install multiple toolchains", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello"))
		// Verify toolchain was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		// install another toolchain
		modGen = modGen.
			With(daggerExec("toolchain", "install", "../myblueprint-ts")).
			With(daggerExec("toolchain", "install", "../myblueprint-py"))
		out, err = modGen.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "toolchain installed")
		out, err = modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		out, err = modGen.
			With(daggerExec("call", "myblueprint-py", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		out, err = modGen.
			With(daggerExec("call", "myblueprint-ts", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		modGen = modGen.WithNewFile("app-config.txt", "this is the app configuration")
		appConfig, err := modGen.
			With(daggerExec("call", "hello", "app-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
	})
}

func (ToolchainSuite) TestToolchainsWithSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use blueprint with sdk", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--sdk=go")).
			With(daggerExec("toolchain", "install", "../hello"))
		// verify we can call function from our module code
		out, err := modGen.
			With(daggerExec("call", "container-echo", "--string-arg", "yoyo", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "yoyo")
		// verify we can call a function from our blueprint
		out, err = modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
	})
}

func (ToolchainSuite) TestToolchainsWithMultipleObjects(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use toolchain with multiple objects", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-objects"))
		// verify we can call a function from our blueprint
		out, err := modGen.
			With(daggerExec("call", "hello-with-objects", "say-greeting", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Hello!")
	})
}

func (ToolchainSuite) TestToolchainsWithBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("use toolchains with blueprint", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint=../hello")).
			With(daggerExec("toolchain", "install", "../myblueprint-py"))
		// verify we can call function from our module code
		out, err := modGen.
			With(daggerExec("call", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
		// verify we can call a function from our blueprint
		out, err = modGen.
			With(daggerExec("call", "myblueprint-py", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")
	})
}

func (ToolchainSuite) TestToolchainsWithConfiguration(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("override function default argument", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			WithNewFile("dagger.json", `
{
  "name": "app",
  "engineVersion": "v0.19.4",
  "toolchains": [
    {
      "name": "hello",
      "source": "../hello",
      "customizations": [
        {
          "function": ["configurableMessage"],
          "argument": "message",
          "default": "hola"
        }
      ]
    }
  ]
}
				`)
		// verify we can call a function from our toolchain with overridden argument
		out, err := modGen.
			With(daggerExec("call", "hello", "configurable-message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hola from blueprint")
	})

	t.Run("override constructor defaultPath argument", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			WithNewFile("dagger.json", `
{
  "name": "app",
  "engineVersion": "v0.19.4",
  "toolchains": [
    {
      "name": "hello",
      "source": "../hello-with-constructor",
      "customizations": [
        {
          "argument": "config",
          "defaultPath": "./custom-config.txt"
        }
      ]
    }
  ]
}
				`).
			WithNewFile("custom-config.txt", "this is custom configuration")
		// verify we can call a function from our toolchain and it uses the overridden defaultPath
		out, err := modGen.
			With(daggerExec("call", "hello", "field-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "this is custom configuration")
	})

	t.Run("override function default argument in chained function", func(ctx context.Context, t *testctx.T) {
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			WithNewFile("dagger.json", `
{
  "name": "app",
  "engineVersion": "v0.19.4",
  "toolchains": [
    {
      "name": "hello",
      "source": "../hello",
      "customizations": [
        {
          "function": ["greet", "planet"],
          "argument": "planet",
          "default": "Mars"
        }
      ]
    }
  ]
}
				`)
		// verify we can call a function from our toolchain with overridden argument
		out, err := modGen.
			With(daggerExec("call", "hello", "greet", "planet")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Greetings from Mars!")
	})
}

func (ToolchainSuite) TestToolchainIgnoreChecks(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("ignore checks from toolchain using ignoreChecks config", func(ctx context.Context, t *testctx.T) {
		// Set up test environment with checks test data
		modGen := c.Container().
			From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory(".", c.Host().Directory("./testdata/checks")).
			WithDirectory("app", c.Directory()).
			WithWorkdir("app").
			With(daggerExec("init"))

		// Install hello-with-checks as a toolchain
		modGen = modGen.With(daggerExec("toolchain", "install", "../hello-with-checks"))

		// Verify all checks are visible by default
		out, err := modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks:passing-check")
		require.Contains(t, out, "hello-with-checks:failing-check")
		require.Contains(t, out, "hello-with-checks:passing-container")
		require.Contains(t, out, "hello-with-checks:failing-container")

		// Now add ignoreChecks configuration to filter out failing checks
		modGen = modGen.WithNewFile("dagger.json", `{
  "name": "app",
  "engineVersion": "v0.16.0",
  "toolchains": [
    {
      "name": "hello-with-checks",
      "source": "../hello-with-checks",
      "ignoreChecks": [
        "failing-check",
        "failing-container"
      ]
    }
  ]
}`)

		// List checks again - should only show passing checks
		out, err = modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks:passing-check")
		require.Contains(t, out, "hello-with-checks:passing-container")
		require.NotContains(t, out, "hello-with-checks:failing-check")
		require.NotContains(t, out, "hello-with-checks:failing-container")

		// Run all checks - should only run passing checks (and succeed)
		out, err = modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `passingContainer.*OK`, out)
		require.NotContains(t, out, "failingCheck")
		require.NotContains(t, out, "failingContainer")
	})

	t.Run("ignore checks with glob patterns", func(ctx context.Context, t *testctx.T) {
		// Set up test environment
		modGen := c.Container().
			From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory(".", c.Host().Directory("./testdata/checks")).
			WithDirectory("app", c.Directory()).
			WithWorkdir("app").
			With(daggerExec("init"))

		// Install hello-with-checks as a toolchain
		modGen = modGen.With(daggerExec("toolchain", "install", "../hello-with-checks"))

		// Add ignoreChecks with wildcard patterns
		modGen = modGen.WithNewFile("dagger.json", `{
  "name": "app",
  "engineVersion": "v0.16.0",
  "toolchains": [
    {
      "name": "hello-with-checks",
      "source": "../hello-with-checks",
      "ignoreChecks": [
        "failing-*"
      ]
    }
  ]
}`)

		// List checks - should only show passing checks
		out, err := modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks:passing-check")
		require.Contains(t, out, "hello-with-checks:passing-container")
		require.NotContains(t, out, "hello-with-checks:failing-check")
		require.NotContains(t, out, "hello-with-checks:failing-container")

		// Run all checks - should succeed since only passing checks run
		out, err = modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `passingContainer.*OK`, out)
	})
}

func (ToolchainSuite) TestToolchainOverlay(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("basic overlay functionality", func(ctx context.Context, t *testctx.T) {
		// Install base toolchain first
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello"))

		// Verify base toolchain works
		out, err := modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")

		out, err = modGen.
			With(daggerExec("call", "hello", "configurable-message", "--message", "bonjour")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bonjour from blueprint")

		// Install overlay toolchain
		modGen = modGen.WithNewFile("dagger.json", `
{
  "name": "app",
  "engineVersion": "v0.19.4",
  "toolchains": [
    {
      "name": "hello",
      "source": "../hello"
    },
    {
      "name": "hello-overlay",
      "source": "../hello-overlay",
      "overlayFor": "hello"
    }
  ]
}
		`)

		// Verify overlay wraps the base toolchain
		out, err = modGen.
			With(daggerExec("call", "hello", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "overlay: hello from blueprint")

		out, err = modGen.
			With(daggerExec("call", "hello", "configurable-message", "--message", "bonjour")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "overlay says: bonjour from blueprint")
	})

	t.Run("overlay with customizations", func(ctx context.Context, t *testctx.T) {
		// Install base and overlay with customizations
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			WithNewFile("dagger.json", `
	{
	  "name": "app",
	  "engineVersion": "v0.19.4",
	  "toolchains": [
	    {
	      "name": "hello",
	      "source": "../hello",
	      "customizations": [
	        {
	          "function": ["configurableMessage"],
	          "argument": "message",
	          "default": "base custom"
	        }
	      ]
	    },
	    {
	      "name": "hello-overlay",
	      "source": "../hello-overlay",
	      "overlayFor": "hello",
	      "customizations": [
	        {
	          "function": ["configurableMessage"],
	          "argument": "message",
	          "default": "overlay custom"
	        }
	      ]
	    }
	  ]
	}
				`)

		// Verify overlay customization takes precedence
		out, err := modGen.
			With(daggerExec("call", "hello", "configurable-message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "overlay says: overlay custom from blueprint")
	})

	t.Run("error on missing base toolchain", func(ctx context.Context, t *testctx.T) {
		// Try to install overlay without base
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init"))

		// This should fail because hello doesn't exist
		_, err := modGen.
			With(daggerExec("toolchain", "install", "../hello-overlay", "--overlay-for", "hello")).
			Stdout(ctx)
		require.Error(t, err)
	})

	t.Run("error on circular overlay dependency", func(ctx context.Context, t *testctx.T) {
		// Try to create circular overlay
		modGen := toolchainTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			WithNewFile("dagger.json", `
	{
	  "name": "app",
	  "engineVersion": "v0.19.4",
	  "toolchains": [
	    {
	      "name": "toolchain-a",
	      "source": "../hello",
	      "overlayFor": "toolchain-b"
	    },
	    {
	      "name": "toolchain-b",
	      "source": "../hello-overlay",
	      "overlayFor": "toolchain-a"
	    }
	  ]
	}
				`)

		// This should fail with circular dependency error
		_, err := modGen.
			With(daggerExec("call", "toolchain-a", "message")).
			Stdout(ctx)
		require.Error(t, err)
	})
}
