package main

import (
	"context"
	"fmt"
	"path/filepath"

	"ruby-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	RubyImage        = "ruby:3.3.6-alpine3.20"
	RubyDigest       = "sha256:caeab43b356463e63f87af54a03de1ae4687b36da708e6d37025c557ade450f8"
	ModSourceDirPath = "/src"
	GenPath          = "lib/dagger"
	codegenBinPath   = "/codegen"
	schemaPath       = "/schema.json"
)

type RubySdk struct {
	SDKSourceDir  *dagger.Directory
	RequiredPaths []string
	moduleConfig  moduleConfig
}

type moduleConfig struct {
	name    string
	subPath string
}

func (c *moduleConfig) modulePath() string {
	return filepath.Join(ModSourceDirPath, c.subPath)
}

func (c *moduleConfig) sdkPath() string {
	return filepath.Join(c.modulePath(), GenPath)
}

func New(
	// Directory with the ruby SDK source code.
	// +optional
	// +ignore=["**", "!**/*.rb", "!Gemfile"]
	sdkSourceDir *dagger.Directory,
) (*RubySdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &RubySdk{
		RequiredPaths: []string{},
		SDKSourceDir:  sdkSourceDir,
	}, nil
}

func (m *RubySdk) setModuleConfig(ctx context.Context, modSource *dagger.ModuleSource) error {
	name, err := modSource.ModuleOriginalName(ctx)
	if err != nil {
		return fmt.Errorf("could not load module name: %w", err)
	}

	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return fmt.Errorf("could not load source subpath: %w", err)
	}

	m.moduleConfig = moduleConfig{
		name:    name,
		subPath: subPath,
	}

	return nil
}

func (m *RubySdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}
	codegen := dag.
		Directory().
		WithDirectory(
			"/",
			ctr.Directory(ModSourceDirPath))

	return dag.GeneratedCode(
		codegen,
	).
		WithVCSGeneratedPaths([]string{
			GenPath + "/**",
		}).
		WithVCSIgnoredPaths([]string{
			GenPath,
		}), nil
}

func (m *RubySdk) CodegenBase(
	_ context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	base := m.base()

	// Get a directory with the SDK sources and the generated client.
	sdk := m.SDKSourceDir.
		WithoutDirectory("codegen").
		WithoutDirectory("runtime").
		WithDirectory(".", m.generateClient(base, introspectionJSON))

	base = base.
		WithMountedDirectory("/opt/module", dag.CurrentModule().Source().Directory(".")).
		WithDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		// WithDirectory(ModSourceDirPath,
		//	dag.Directory().WithDirectory("/", modSource.ContextDirectory(), dagger.DirectoryWithDirectoryOpts{
		//		Include: m.moduleConfigFiles(m.moduleConfig.subPath),
		//	})).
		WithDirectory(m.moduleConfig.modulePath(), m.SDKSourceDir, dagger.ContainerWithDirectoryOpts{
			Include: []string{
				"Gemfile",
				"lib",
			},
			Exclude: []string{
				"lib/dagger/client.gen.rb",
			},
		}).
		WithDirectory(filepath.Join(m.moduleConfig.modulePath(), GenPath), sdk, dagger.ContainerWithDirectoryOpts{
			Include: []string{
				"client.gen.rb",
			},
		}).
		WithWorkdir(m.moduleConfig.modulePath())
	// add template files
	base = base.
		WithDirectory(".", base.Directory("/opt/module/template"), dagger.ContainerWithDirectoryOpts{
			Include: []string{
				"dagger.rb",
				"dagger.gemspec",
				"main.rb",
			},
		}).
		WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/HelloDagger/%s/g", strcase.ToCamel(m.moduleConfig.name)), "dagger.rb"}).
		WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/HelloDagger/%s/g", strcase.ToCamel(m.moduleConfig.name)), "dagger.gemspec"}).
		WithExec([]string{"sed", "-i", "-e", fmt.Sprintf("s/HelloDagger/%s/g", strcase.ToCamel(m.moduleConfig.name)), "main.rb"}).
		WithExec([]string{"bundle", "install"})

	return base, nil
}

func (m *RubySdk) base() *dagger.Container {
	return dag.
		Container().
		From(fmt.Sprintf("%s@%s", RubyImage, RubyDigest)).
		WithExec([]string{"apk", "add", "git", "openssh", "curl"})
}

func (m *RubySdk) generateClient(
	ctr *dagger.Container,
	introspectionJSON *dagger.File,
) *dagger.Directory {
	return ctr.
		// Add dagger codegen binary.
		WithMountedFile(codegenBinPath, m.SDKSourceDir.File("/codegen")).
		// Mount the introspection file.
		WithMountedFile(schemaPath, introspectionJSON).
		// Generate the ruby client from the introspection file.
		WithExec([]string{
			codegenBinPath,
			"--lang", "ruby",
			"--output", ModSourceDirPath,
			"--module-name", m.moduleConfig.name,
			"--module-context-path", m.moduleConfig.modulePath(),
			"--introspection-json-path", schemaPath,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Directory(m.moduleConfig.sdkPath())
}

func (m *RubySdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	if err := m.setModuleConfig(ctx, modSource); err != nil {
		return nil, err
	}
	return m.CodegenBase(ctx, modSource, introspectionJSON)
}
