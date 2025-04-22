package main

import (
	"context"
	"encoding/json"
	"fmt"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsdistconsts"
)

type clientGenContainer struct {
	sdkSourceDir *dagger.Directory
	cfg          *moduleConfig
	ctr          *dagger.Container
	gitDepsJSON  string
}

func (c *clientGenContainer) GeneratedDirectory() *dagger.Directory {
	return c.ctr.Directory(ModSourceDirPath)
}

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
		gitDepsJSON:  "",
		ctr: baseCtr.
			WithoutEntrypoint().
			// install tsx from its bundled location in the engine image
			WithMountedDirectory("/usr/local/lib/node_modules/tsx", sdkSourceDir.Directory("/tsx_module")).
			WithExec([]string{"ln", "-s", "/usr/local/lib/node_modules/tsx/dist/cli.mjs", "/usr/local/bin/tsx"}).
			// Add dagger codegen binary.
			WithMountedFile(codegenBinPath, sdkSourceDir.File("/codegen")).
			WithWorkdir(ModSourceDirPath),
	}

	return clientGenCtr
}

func (c *clientGenContainer) withBundledSDK() *clientGenContainer {
	switch c.cfg.sdkLibOrigin {
	case Bundle:
		c.ctr = c.ctr.
			WithDirectory(
				GenDir,
				c.sdkSourceDir.Directory("/bundled_lib").
					WithDirectory("/", bundledStaticDirectoryForClient()),
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
		} else {
			c.ctr = c.ctr.
				WithExec([]string{"npm", "pkg", "set", "type=module"})

			_, ok := c.cfg.packageJSONConfig.Dependencies["typescript"]
			if !ok {
				c.ctr = c.ctr.
					WithExec([]string{"npm", "pkg", "set", "dependencies.typescript=^5.5.4"})
			}
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
		c.ctr = c.ctr.
			WithMountedFile("/opt/module/bin/__deno_config_updator.ts", denoConfigUpdatorFile()).
			WithExec([]string{
				"deno", "run", "-A", "/opt/module/bin/__deno_config_updator.ts",
				fmt.Sprintf("--sdk-lib-origin=%s", c.cfg.sdkLibOrigin),
				"--standalone-client=true",
				fmt.Sprintf("--client-dir=%s", outputDir),
			})
	}

	return c
}

func (c *clientGenContainer) withGeneratedClient(introspectionJSON *dagger.File, outputDir string) *clientGenContainer {
	codegenArgs := []string{
		codegenBinPath,
		"--lang", "typescript",
		"--output", outputDir,
		"--introspection-json-path", schemaPath,
		"--client-only",
	}

	if c.gitDepsJSON != "" {
		c.ctr = c.ctr.WithNewFile(dependenciesConfigPath, c.gitDepsJSON)
		codegenArgs = append(codegenArgs,
			fmt.Sprintf("--dependencies-json-file-path=%s", dependenciesConfigPath),
		)
	}

	c.ctr = c.ctr.
		WithMountedFile(schemaPath, introspectionJSON).
		// Execute the code generator using the given introspection file.
		WithExec(codegenArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})

	return c
}

// Same data structure as ModuleConfigDependency from core/modules/config.go#L183
type gitDependencyConfig struct {
	Name   string
	Pin    string
	Source string
}

func (c *clientGenContainer) withBundledGitDependenciesJSON(gitDepsJSON string) *clientGenContainer {
	c.gitDepsJSON = gitDepsJSON

	return c
}

func extraGitDependenciesFromModule(ctx context.Context, modSource *dagger.ModuleSource) (string, error) {
	dependencies, err := modSource.Dependencies(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get module dependencies: %w", err)
	}

	dependenciesConfig := []gitDependencyConfig{}
	// Add remote dependency reference to the codegen arguments.
	for _, dep := range dependencies {
		depKind, err := dep.Kind(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get dependency kind: %w", err)
		}

		if depKind != dagger.ModuleSourceKindGitSource {
			continue
		}

		depSource, err := dep.AsString(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get module dependency ref: %w", err)
		}

		depPin, err := dep.Pin(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get module dependency pin: %w", err)
		}

		depName, err := dep.ModuleOriginalName(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get module dependency name: %w", err)
		}

		dependenciesConfig = append(dependenciesConfig, gitDependencyConfig{
			Name:   depName,
			Pin:    depPin,
			Source: depSource,
		})
	}

	dependenciesJSONConfig, err := json.Marshal(dependenciesConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal dependencies config: %w", err)
	}

	return string(dependenciesJSONConfig), nil
}
