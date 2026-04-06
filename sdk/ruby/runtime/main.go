// Runtime module for the Ruby SDK

package main

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"

	"ruby-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	RubyImage        = "ruby:3.3-alpine"
	ModSourceDirPath = "/src"
	SDKDir           = "sdk"
	SDKDaggerDir     = "sdk/dagger"
	codegenBinPath   = "/codegen"
	schemaPath       = "/schema.json"
)

type RubySdk struct {
	SDKSourceDir *dagger.Directory
}

type moduleConfig struct {
	name    string
	subPath string
}

func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

func New(
	// Directory with the Ruby SDK source code.
	// +optional
	sdkSourceDir *dagger.Directory,
) (*RubySdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &RubySdk{
		SDKSourceDir: sdkSourceDir,
	}, nil
}

func (m *RubySdk) moduleConfig(ctx context.Context, modSource *dagger.ModuleSource) (*moduleConfig, error) {
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load module name: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not load source subpath: %w", err)
	}

	return &moduleConfig{
		name:    name,
		subPath: subPath,
	}, nil
}

// Generated code for the Ruby module
func (m *RubySdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	cfg, err := m.moduleConfig(ctx, modSource)
	if err != nil {
		return nil, err
	}

	ctr, err := m.codegenBase(ctx, cfg, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return dag.GeneratedCode(
		ctr.Directory(ModSourceDirPath),
	).
		WithVCSGeneratedPaths([]string{
			SDKDir + "/**",
			"entrypoint.rb",
		}).
		WithVCSIgnoredPaths([]string{
			SDKDir,
			"entrypoint.rb",
		}), nil
}

// Container for executing the Ruby module runtime
func (m *RubySdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	cfg, err := m.moduleConfig(ctx, modSource)
	if err != nil {
		return nil, err
	}

	ctr, err := m.codegenBase(ctx, cfg, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	camelName := strcase.ToCamel(cfg.name)

	modPath := cfg.modulePath()
	entryPointFile := filepath.Join(modPath, "entrypoint.rb")

	ctr = ctr.
		WithEnvVariable("DAGGER_MODULE_NAME", camelName).
		WithWorkdir(modPath).
		WithEntrypoint([]string{
			"/bin/sh", "-c",
			fmt.Sprintf("cd %s && bundle exec ruby %s", modPath, entryPointFile),
		})

	return ctr, nil
}

func (m *RubySdk) base() *dagger.Container {
	return dag.Container().
		From(RubyImage).
		WithExec([]string{"apk", "add", "--no-cache", "git", "openssh", "curl", "build-base", "pkgconf", "ruby-dev"}).
		WithExec([]string{"gem", "install", "bundler", "--no-document"})
}

func (m *RubySdk) codegenBase(
	ctx context.Context,
	cfg *moduleConfig,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	base := m.base()

	// Mount the template directory from the runtime module
	base = base.
		WithMountedDirectory("/opt/module", dag.CurrentModule().Source().Directory(".")).
		WithDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithWorkdir(cfg.modulePath())

	// Generate the SDK (client bindings + SDK library)
	base = base.With(m.withGeneratedSDK(cfg, introspectionJSON))

	// Clean old files and re-mount context without sdk/ and entrypoint.rb
	base = base.
		WithDirectory(
			ModSourceDirPath,
			dag.Directory().WithDirectory("/", modSource.ContextDirectory(), dagger.DirectoryWithDirectoryOpts{
				Exclude: []string{
					filepath.Join(cfg.subPath, "entrypoint.rb"),
					filepath.Join(cfg.subPath, "sdk"),
				},
			}),
		).
		WithWorkdir(cfg.modulePath())

	// Re-apply generated SDK on top
	base = base.With(m.withGeneratedSDK(cfg, introspectionJSON))

	// Template new files if needed
	base, err := m.withTemplate(ctx, cfg, base)
	if err != nil {
		return nil, err
	}

	return base, nil
}

func (m *RubySdk) generateClient(
	ctr *dagger.Container,
	cfg *moduleConfig,
	introspectionJSON *dagger.File,
) *dagger.Directory {
	modPath := cfg.modulePath()
	return ctr.
		WithMountedFile(codegenBinPath, m.SDKSourceDir.File("/codegen")).
		WithMountedFile(schemaPath, introspectionJSON).
		WithExec([]string{
			codegenBinPath,
			"generate-module",
			"--lang", "ruby",
			"--output", modPath,
			"--module-name", cfg.name,
			"--module-source-path", modPath,
			"--introspection-json-path", schemaPath,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory(filepath.Join(modPath, SDKDaggerDir))
}

func (m *RubySdk) withGeneratedSDK(
	cfg *moduleConfig,
	introspectionJSON *dagger.File,
) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		generatedClient := m.generateClient(ctr, cfg, introspectionJSON)

		// Copy the SDK library files
		ctr = ctr.WithDirectory(
			filepath.Join(cfg.modulePath(), "sdk"),
			m.SDKSourceDir.Directory("lib/"),
		)

		// Overlay the generated client
		ctr = ctr.WithDirectory(
			filepath.Join(cfg.modulePath(), SDKDaggerDir),
			generatedClient,
			dagger.ContainerWithDirectoryOpts{
				Include: []string{"client.gen.rb"},
			},
		)

		return ctr
	}
}

func (m *RubySdk) withTemplate(
	ctx context.Context,
	cfg *moduleConfig,
	ctr *dagger.Container,
) (*dagger.Container, error) {
	camelName := strcase.ToCamel(cfg.name)
	snakeName := strcase.ToSnake(cfg.name)
	modulePath := cfg.modulePath()
	entryPointFile := filepath.Join(modulePath, "entrypoint.rb")
	tmplMainFile := filepath.Join("lib", "main.rb")
	moduleFileName := filepath.Join("lib", snakeName+".rb")

	// Check if lib/ has any .rb files
	moduleFiles, err := ctr.Directory(".").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list module source entries: %w", err)
	}

	if !slices.Contains(moduleFiles, "lib") {
		ctr = ctr.WithDirectory("lib", dag.Directory())
	}

	moduleSourceFiles, err := ctr.Directory("lib").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list module source entries: %w", err)
	}

	if !slices.ContainsFunc(moduleSourceFiles, func(s string) bool {
		return path.Ext(s) == ".rb"
	}) {
		// Copy the template module file and rename it
		ctr = ctr.
			WithDirectory(filepath.Dir(moduleFileName), ctr.Directory("/opt/module/template"), dagger.ContainerWithDirectoryOpts{
				Include: []string{"main.rb"},
			}).
			WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/DaggerModule/%s/g", camelName), tmplMainFile}).
			WithExec([]string{"mv", tmplMainFile, moduleFileName})
	}

	// Always copy the entrypoint and Gemfile
	ctr = ctr.
		WithDirectory(".", ctr.Directory("/opt/module/template"), dagger.ContainerWithDirectoryOpts{
			Include: []string{
				"Gemfile",
				"entrypoint.rb",
			},
		}).
		WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/DaggerModule/%s/g", camelName), entryPointFile}).
		WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/dagger_module/%s/g", snakeName), entryPointFile}).
		WithExec([]string{"bundle", "install"})

	return ctr, nil
}
