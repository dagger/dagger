package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

type BlueprintSuite struct{}

func TestBlueprint(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(BlueprintSuite{})
}

func (BlueprintSuite) TestBlueprintInstallation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Simple blueprint module
	blueprintSrc := `package main

import (
	"context"
)

type Blueprint struct{}

func (m *Blueprint) Hello(ctx context.Context) (string, error) {
	return "hello from blueprint", nil
}
`

	// Test basic blueprint installation
	t.Run("basic blueprint install", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module in completely separate directory
			WithNewFile("/tmp/blueprint-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-test").
			With(daggerExec("init", "--name=blueprint-test", "--sdk=go", ".")).
			// Create target module in completely separate directory without SDK
			WithWorkdir("/tmp/target-test").
			With(daggerExec("init", "--name=target-test", ".")).
			// Install blueprint
			With(daggerExec("install", "--blueprint", "../blueprint-test"))

		// Verify blueprint was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")

		// Verify blueprint config was persisted
		config, err := modGen.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, "blueprint")
		require.Contains(t, config, "../blueprint-test")
	})

	// Test blueprint validation - cannot have both blueprint and SDK
	t.Run("blueprint with SDK validation", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module
			WithNewFile("/tmp/blueprint-sdk-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-sdk-test").
			With(daggerExec("init", "--name=blueprint-sdk-test", "--sdk=go", ".")).
			// Create target module with SDK already set
			WithWorkdir("/tmp/target-sdk-test").
			With(daggerExec("init", "--name=target-sdk-test", "--sdk=go", "."))

		// Try to install blueprint - should fail since SDK is already set
		_, err := modGen.
			With(daggerExec("install", "--blueprint", "../blueprint-sdk-test")).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot set blueprint on module that already has SDK")
	})

	// Test dagger init --blueprint functionality
	t.Run("init with blueprint", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module in completely separate directory
			WithNewFile("/tmp/blueprint-init-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-init-test").
			With(daggerExec("init", "--name=blueprint-init-test", "--sdk=go", ".")).
			// Create target module with blueprint in one command
			WithWorkdir("/tmp/target-init-test").
			With(daggerExec("init", "--name=target-init-test", "--blueprint=../blueprint-init-test", "."))

		// Verify blueprint was installed by calling function
		out, err := modGen.
			With(daggerExec("call", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")

		// Verify blueprint config was persisted
		config, err := modGen.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, "blueprint")
		require.Contains(t, config, "../blueprint-init-test")
	})

	// Test validation - cannot have both SDK and blueprint in init
	t.Run("init with both SDK and blueprint validation", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module
			WithNewFile("/tmp/blueprint-init-sdk-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-init-sdk-test").
			With(daggerExec("init", "--name=blueprint-init-sdk-test", "--sdk=go", ".")).
			// Try to init with both SDK and blueprint - should fail
			WithWorkdir("/tmp/target-init-sdk-test")

		// Try to init with both SDK and blueprint - should fail
		_, err := modGen.
			With(daggerExec("init", "--name=target-init-sdk-test", "--sdk=go", "--blueprint=../blueprint-init-sdk-test", ".")).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot specify both --sdk and --blueprint")
	})
}

func (BlueprintSuite) TestBlueprintContextSeparation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Blueprint that tests context separation
	blueprintSrc := `package main

import (
	"context"
	"dagger/blueprint-context-test/internal/dagger"
)

type Blueprint struct{}

// This should read blueprint's own config file
func (m *Blueprint) ReadBlueprintConfig(ctx context.Context) (string, error) {
	dir := dag.CurrentModule().Source().ContextDirectory()
	content, err := dir.File("blueprint-config.txt").Contents(ctx)
	if err != nil {
		return "", err
	}
	return content, nil
}

// This should return the target module's name, not blueprint's
func (m *Blueprint) GetActualModuleName(ctx context.Context) (string, error) {
	return dag.CurrentModule().Name(), nil
}
`

	// Test context separation
	t.Run("context separation", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module with its own config
			WithNewFile("/tmp/blueprint-context-test/main.go", blueprintSrc).
			WithNewFile("/tmp/blueprint-context-test/blueprint-config.txt", "blueprint-specific-config").
			WithWorkdir("/tmp/blueprint-context-test").
			With(daggerExec("init", "--name=blueprint-context-test", "--sdk=go", ".")).
			// Create target module with its own data
			WithWorkdir("/tmp/target-context-test").
			WithNewFile("/tmp/target-context-test/target-data.txt", "target-specific-data").
			With(daggerExec("init", "--name=target-context-test", ".")).
			// Install blueprint
			With(daggerExec("install", "--blueprint", "../blueprint-context-test"))

		// Test 1: Blueprint should be able to read its own config
		out, err := modGen.
			With(daggerExec("call", "read-blueprint-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "blueprint-specific-config")

		// Test 2: Module name should be target's name, not blueprint's
		out, err = modGen.
			With(daggerExec("call", "get-actual-module-name")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "target-context-test")
		require.NotContains(t, out, "blueprint-context-test")
	})
}

func (BlueprintSuite) TestBlueprintConfigurationPersistence(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Simple blueprint module
	blueprintSrc := `package main

import (
	"context"
)

type Blueprint struct{}

func (m *Blueprint) Hello(ctx context.Context) (string, error) {
	return "hello from persisted blueprint", nil
}
`

	// Test that blueprint configuration is properly persisted and restored
	t.Run("configuration persistence", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module
			WithNewFile("/tmp/blueprint-persist-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-persist-test").
			With(daggerExec("init", "--name=blueprint-persist-test", "--sdk=go", ".")).
			// Create target module
			WithWorkdir("/tmp/target-persist-test").
			With(daggerExec("init", "--name=target-persist-test", ".")).
			// Install blueprint
			With(daggerExec("install", "--blueprint", "../blueprint-persist-test"))

		// Verify blueprint config was persisted to dagger.json
		config, err := modGen.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `"blueprint"`)
		require.Contains(t, config, `"name": "blueprint-persist-test"`)
		require.Contains(t, config, `"source": "../blueprint-persist-test"`)

		// Verify blueprint works immediately after installation
		out, err := modGen.
			With(daggerExec("call", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from persisted blueprint")

		// Simulate a fresh session by creating a new container with the same context
		// This tests that the blueprint configuration is properly loaded from dagger.json
		freshModGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Copy the blueprint module (simulating it exists from previous session)
			WithNewFile("/tmp/blueprint-persist-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-persist-test").
			With(daggerExec("init", "--name=blueprint-persist-test", "--sdk=go", ".")).
			// Copy the target module with its dagger.json (simulating persisted state)
			WithWorkdir("/tmp/target-persist-test").
			With(daggerExec("init", "--name=target-persist-test", ".")).
			// Copy the persisted dagger.json from the previous session
			WithNewFile("/tmp/target-persist-test/dagger.json", config)

		// Verify blueprint still works after simulated restart (configuration was persisted)
		out, err = freshModGen.
			With(daggerExec("call", "hello")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from persisted blueprint")

		// Verify the blueprint configuration is still correct in the fresh session
		freshConfig, err := freshModGen.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, freshConfig, `"blueprint"`)
		require.Contains(t, freshConfig, `"name": "blueprint-persist-test"`)
		require.Contains(t, freshConfig, `"source": "../blueprint-persist-test"`)
	})

	// Test blueprint removal and configuration cleanup
	t.Run("blueprint removal", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/tmp").
			// Create blueprint module
			WithNewFile("/tmp/blueprint-remove-test/main.go", blueprintSrc).
			WithWorkdir("/tmp/blueprint-remove-test").
			With(daggerExec("init", "--name=blueprint-remove-test", "--sdk=go", ".")).
			// Create target module
			WithWorkdir("/tmp/target-remove-test").
			With(daggerExec("init", "--name=target-remove-test", ".")).
			// Install blueprint
			With(daggerExec("install", "--blueprint", "../blueprint-remove-test"))

		// Verify blueprint is installed
		config, err := modGen.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `"blueprint"`)

		// Remove blueprint
		modGen = modGen.With(daggerExec("uninstall", "../blueprint-remove-test"))

		// Verify blueprint was removed from configuration
		configAfterRemoval, err := modGen.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, configAfterRemoval, `"blueprint"`)
	})
}
