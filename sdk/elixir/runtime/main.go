package main

import (
	"context"
	"path"

	"elixir-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	ModSourceDirPath = "/src"
	sdkSrc           = "/sdk"
	genDir           = "dagger_sdk"
	schemaPath       = "/schema.json"
	elixirImage      = "hexpm/elixir:1.16.3-erlang-26.2.5-debian-bookworm-20240612-slim@sha256:5aed25e4525ae7a5c96a8a880673bcc66d99b6f2a590161e42c8370fbebc4235"
)

func New(
	// +optional
	sdkSourceDir *dagger.Directory,
) *ElixirSdk {
	if sdkSourceDir == nil {
		sdkSourceDir = dag.CurrentModule().
			Source().
			Directory("..").
			WithoutDirectory("runtime").
			WithoutFile("dagger.json")
	}
	return &ElixirSdk{
		SDKSourceDir:  sdkSourceDir,
		RequiredPaths: []string{},
		Container:     dag.Container(),
	}
}

type ElixirSdk struct {
	SDKSourceDir  *dagger.Directory
	RequiredPaths []string

	Container *dagger.Container
	// An error during processing.
	err error
}

func (m *ElixirSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.Container, error) {
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}

	ctr, err := m.Common(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	return ctr.
		WithEntrypoint([]string{
			"mix", "cmd",
			"--cd", path.Join(ModSourceDirPath, subPath, normalizeModName(modName)),
			"mix do deps.get + dagger.invoke",
		}), nil
}

func (m *ElixirSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := m.Common(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{genDir + "/**"}).
		WithVCSIgnoredPaths([]string{genDir}), nil
}

func (m *ElixirSdk) Common(ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJson *dagger.File,
) (*dagger.Container, error) {
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}
	m = m.Base(modSource, subPath).
		WithSDK(introspectionJson).
		WithNewElixirPackage(ctx, normalizeModName(modName))
	if m.err != nil {
		return nil, m.err
	}
	return m.Container, nil
}

func (m *ElixirSdk) Base(modSource *dagger.ModuleSource, subPath string) *ElixirSdk {
	m.Container = m.baseContainer(m.Container).
		WithMountedDirectory(sdkSrc, m.SDKSourceDir).
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithWorkdir(path.Join(ModSourceDirPath, subPath))
	return m
}

// Generate a new Elixir package named by `modName`. This step will ignored if the
// package already generated.
func (m *ElixirSdk) WithNewElixirPackage(ctx context.Context, modName string) *ElixirSdk {
	// Ensure to have a directory to list files/directories.
	ctr := m.Container.WithExec([]string{"mkdir", "-p", modName})
	entries, err := ctr.Directory(modName).Entries(ctx)
	if err != nil {
		m.err = err
		return m
	}

	alreadyNewPackage := false
	for _, entry := range entries {
		if entry == "mix.exs" {
			alreadyNewPackage = true
		}
	}

	// Generate scaffolding code when no project exists.
	if !alreadyNewPackage {
		m.Container = m.Container.
			WithExec([]string{"mix", "new", "--sup", modName}).
			WithExec([]string{"mkdir", "-p", modName + "/lib/mix/tasks"}).
			// TODO: moved it to WithSource.
			WithExec([]string{"elixir", "/sdk/template.exs", "generate", modName})
	}
	return m
}

// Generate the SDK into the container.
func (m *ElixirSdk) WithSDK(introspectionJson *dagger.File) *ElixirSdk {
	if m.err != nil {
		return m
	}
	m.Container = m.Container.
		WithDirectory(genDir, m.SDKSourceDir, dagger.ContainerWithDirectoryOpts{
			Include: []string{
				".gitignore",
				".gitattributes",
				".formatter.exs",
				"mix.exs",
				"mix.lock",
				"LICENSE",
				"lib/**/*.ex",
			},
			Exclude: []string{
				// We'll do generate code on the next step.
				"lib/dagger/gen",
			},
		}).
		WithDirectory(path.Join(genDir, "lib", "dagger", "gen"), m.GenerateCode(introspectionJson))
	return m
}

func (m *ElixirSdk) WithDaggerCodegen() *dagger.Container {
	codegenPath := path.Join(sdkSrc, "dagger_codegen")
	codegenDepsCache, codegenBuildCache := mixProjectCaches("dagger-codegen")
	return m.baseContainer(dag.Container()).
		WithMountedDirectory(sdkSrc, m.SDKSourceDir).
		WithMountedCache(path.Join(codegenPath, "deps"), codegenDepsCache).
		WithMountedCache(path.Join(codegenPath, "_build"), codegenBuildCache).
		WithWorkdir(codegenPath).
		WithExec([]string{"mix", "deps.get"}).
		WithExec([]string{"mix", "escript.install", "--force"})
}

func (m *ElixirSdk) GenerateCode(introspectionJson *dagger.File) *dagger.Directory {
	return m.WithDaggerCodegen().
		WithMountedFile(schemaPath, introspectionJson).
		WithExec([]string{
			"dagger_codegen", "generate",
			"--outdir", "/gen",
			"--introspection", schemaPath,
		}).
		Directory("/gen")
}

func (m *ElixirSdk) baseContainer(ctr *dagger.Container) *dagger.Container {
	mixCache := dag.CacheVolume(".mix")
	return ctr.
		From(elixirImage).
		WithMountedCache("/root/.mix", mixCache).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "--no-install-recommends", "git"}).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"}).
		WithEnvVariable("PATH", "/root/.mix/escripts:$PATH", dagger.ContainerWithEnvVariableOpts{
			Expand: true,
		})
}

func mixProjectCaches(prefix string) (depsCache *dagger.CacheVolume, buildCache *dagger.CacheVolume) {
	return dag.CacheVolume(prefix + "-deps"), dag.CacheVolume(prefix + "-build")
}

func normalizeModName(name string) string {
	return strcase.ToSnake(name)
}
