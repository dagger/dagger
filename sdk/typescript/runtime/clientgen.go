package main

import (
	"fmt"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
)

type clientGenContainer struct {
	sdkSourceDir *dagger.Directory
	cfg          *moduleConfig
	ctr          *dagger.Container
}

func (c *clientGenContainer) GeneratedDirectory() *dagger.Directory {
	return c.ctr.Directory(ModSourceDirPath)
}

// Create a base container for the client generator based on the given configuration.
// If Node or Bun: Create a container with the default Node image.
// If Deno: Create a container with the default Deno image.
// Add required binaries to the container:
// - tsx: to execute Typescript scripts
// - codegen: to generate the client bindings
func clientGenBaseContainer(cfg *moduleConfig, sdkSourceDir *dagger.Directory) *clientGenContainer {
	baseCtr := dag.Container()

	switch cfg.runtime {
	case Node, Bun:
		baseCtr = baseCtr.From(tsdistconsts.DefaultNodeImageRef)
	case Deno:
		baseCtr = baseCtr.From(tsdistconsts.DefaultDenoImageRef)
	}

	clientGenCtr := &clientGenContainer{
		sdkSourceDir: sdkSourceDir,
		cfg:          cfg,
		ctr: baseCtr.
			WithoutEntrypoint().
			// install tsx from its bundled location in the engine image
			WithMountedDirectory("/usr/local/lib/node_modules/tsx", sdkSourceDir.Directory("/tsx_module")).
			WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"}).
			// Add dagger codegen binary.
			WithMountedFile(codegenBinPath, sdkSourceDir.File("/codegen")).
			WithWorkdir(cfg.moduleRootPath()),
	}

	return clientGenCtr
}

// Returns the container with the SDK directory.
// If lib origin is bundled:
// - Add the bundle library (code.js & core.d.ts) to the sdk directory.
// - Add the static export setup (index.ts & client.gen.ts) to the sdk directory.
// If lib origin is local:
// - Copy the complete Typescript SDK directory
// Do nothing if remote.
// Note: this doesn't include the generated client, only SDK.
func (c *clientGenContainer) withBundledSDK() *clientGenContainer {
	switch c.cfg.sdkLibOrigin {
	case Bundle:
		staticBundledDirectory := c.sdkSourceDir.Directory("/bundled_lib")

		// If the SDK is a module implementing function and the source are at the
		// root directory. The static bundle should also
		// export the `dag` object from the generated client of that module
		// because they both use the same `sdk` directory.
		if c.cfg.sdk != "" && c.cfg.subPath == "." {
			staticBundledDirectory = staticBundledDirectory.
				WithDirectory("/", bundledStaticDirectoryForModule())
		} else {
			staticBundledDirectory = staticBundledDirectory.
				WithDirectory("/", bundledStaticDirectoryForClientOnly())
		}

		c.ctr = c.ctr.
			WithDirectory(
				GenDir,
				staticBundledDirectory,
			)
	case Local:
		c.ctr = c.ctr.WithDirectory(
			GenDir,
			c.sdkSourceDir.
				WithoutDirectory("codegen").
				WithoutDirectory("runtime").
				WithoutDirectory("tsx_module").
				WithoutDirectory("bundled_Lib"),
		)
	case Remote:
		break
	}

	return c
}

// Return the container with the updated execution environment.
// If Node or Bun:
// - Copy the local host `package.json` and `tsconfig.json` in the current directory.
// - Update the `package.json` to work with Dagger and add Typescript if it's not already set.
// - Update the `tsconfig.json` with the necessary configuration to work with the generated client.
// If Deno:
// - Copy the local host `deno.json` in the current directory.
// - Update the `deno.json` with necessary configuration to work with the generated client.
func (c *clientGenContainer) withUpdatedEnvironment(outputDir string) *clientGenContainer {
	switch c.cfg.runtime {
	case Node, Bun:
		c.ctr = c.ctr.WithDirectory(
			".",
			c.cfg.source,
			dagger.ContainerWithDirectoryOpts{
				Include: []string{"package.json", "tsconfig.json"},
			})

		if c.cfg.packageJSONConfig == nil {
			c.ctr = c.ctr.
				WithFile("package.json", templateDirectory().File("package.json"))

			c.cfg.packageJSONConfig = &packageJSONConfig{
				Dependencies: make(map[string]string),
			}
		}

		c.ctr = c.ctr.
			WithExec([]string{"npm", "pkg", "set", "type=module"})

		_, ok := c.cfg.packageJSONConfig.Dependencies["typescript"]
		if !ok {
			c.ctr = c.ctr.
				WithExec([]string{"npm", "pkg", "set", fmt.Sprintf("dependencies.typescript=%s", tsdistconsts.DefaultTypeScriptVersion)})
		}

		c.ctr = c.ctr.
			WithMountedFile("/opt/module/bin/__tsconfig.updator.ts", tsConfigUpdatorFile()).
			WithExec([]string{
				"tsx", "/opt/module/bin/__tsconfig.updator.ts",
				fmt.Sprintf("--sdk-lib-origin=%s", c.cfg.sdkLibOrigin),
				"--standalone-client=true",
				fmt.Sprintf("--client-dir=%s", outputDir),
			})

	case Deno:
		c.ctr = c.ctr.WithDirectory(
			".",
			c.cfg.source,
			dagger.ContainerWithDirectoryOpts{
				Include: []string{"deno.json"},
			})

		c.ctr = c.ctr.
			WithMountedFile("/opt/module/bin/__deno_config_updator.ts", denoConfigUpdatorFile()).
			WithExec([]string{
				"deno", "run", "-A", "/opt/module/bin/__deno_config_updator.ts",
				fmt.Sprintf("--sdk-lib-origin=%s", c.cfg.sdkLibOrigin),
				"--standalone-client=true",
				fmt.Sprintf("--client-dir=%s", outputDir),
				fmt.Sprintf("--default-typescript-version=%s", tsdistconsts.DefaultTypeScriptVersion),
			})
	}

	return c
}

// Return the container with the generated client inside it's current working directory.
func (c *clientGenContainer) withGeneratedClient(introspectionJSON *dagger.File, moduleSourceID dagger.ModuleSourceID, outputDir string) *clientGenContainer {
	codegenArgs := []string{
		codegenBinPath,
		"generate-client",
		"--lang", "typescript",
		"--output", outputDir,
		"--introspection-json-path", schemaPath,
		"--module-source-id", string(moduleSourceID),
	}

	c.ctr = c.ctr.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})

	return c
}
