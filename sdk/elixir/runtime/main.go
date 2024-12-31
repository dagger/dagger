package main

import (
	"context"
	"fmt"
	"path"

	"elixir-sdk/internal/dagger"

	"github.com/iancoleman/strcase"
)

const (
	ModSourceDirPath = "/src"
	sdkSrc           = "/sdk"
	genDir           = "dagger_sdk"
	schemaPath       = "/schema.json"
	elixirImage      = "hexpm/elixir:1.17.3-erlang-27.2-alpine-3.20.3@sha256:557156f12d23b0d2aa12d8955668cc3b9a981563690bb9ecabd7a5a951702afe"
)

func New(
	// Directory with the Elixir SDK source code.
	// +optional
	// +defaultPath="/sdk/elixir"
	// +ignore=["**","!LICENSE","!lib/**/*.ex","!.formatter.exs","!mix.exs","!mix.lock","!dagger_codegen/lib/**/*.ex","!dagger_codegen/mix.exs","!dagger_codegen/mix.lock"]
	sdkSourceDir *dagger.Directory,
) (*ElixirSdk, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &ElixirSdk{
		SdkSourceDir:  sdkSourceDir,
		RequiredPaths: []string{},
		Container:     dag.Container(),
	}, nil
}

type ElixirSdk struct {
	SdkSourceDir  *dagger.Directory
	RequiredPaths []string

	Container *dagger.Container
	// An error during processing.
	err error
}

func (m *ElixirSdk) ModuleRuntime(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.Container, error) {
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}

	elixirApplication := toElixirApplicationName(modName)

	ctr, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return ctr.
		WithWorkdir(elixirApplication).
		WithExec([]string{"mix", "deps.get", "--only", "dev"}).
		WithEntrypoint([]string{
			"mix", "cmd",
			"--cd", path.Join(ModSourceDirPath, subPath, elixirApplication),
			fmt.Sprintf("mix dagger.entrypoint.invoke %s", toElixirModuleName(modName)),
		}), nil
}

func (m *ElixirSdk) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	ctr, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{genDir + "/**"}).
		WithVCSIgnoredPaths([]string{genDir}), nil
}

func (m *ElixirSdk) Common(ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
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
		WithSDK(introspectionJSON).
		WithNewElixirPackage(ctx, toElixirApplicationName(modName))
	if m.err != nil {
		return nil, m.err
	}
	return m.Container, nil
}

func (m *ElixirSdk) Base(modSource *dagger.ModuleSource, subPath string) *ElixirSdk {
	m.Container = m.baseContainer(m.Container).
		WithMountedDirectory(sdkSrc, m.SdkSourceDir).
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
			WithExec([]string{"mix", "new", modName}).
			WithDirectory(modName+"/lib/mix/tasks", dag.Directory()).
			WithMountedFile("/template.exs", dag.CurrentModule().Source().File("template.exs")).
			WithExec([]string{"elixir", "/template.exs", "generate", modName})
	}
	return m
}

// Generate the SDK into the container.
func (m *ElixirSdk) WithSDK(introspectionJSON *dagger.File) *ElixirSdk {
	if m.err != nil {
		return m
	}
	m.Container = m.Container.
		WithDirectory(genDir, m.SdkSourceDir.
			WithoutDirectory("dagger_codegen").
			WithoutDirectory("lib/dagger/gen"),
		).
		WithDirectory(path.Join(genDir, "lib", "dagger", "gen"), m.GenerateCode(introspectionJSON))
	return m
}

func (m *ElixirSdk) WithDaggerCodegen() *dagger.Container {
	codegenPath := path.Join(sdkSrc, "dagger_codegen")
	_, codegenBuildCache := mixProjectCaches("dagger-codegen")
	return m.baseContainer(dag.Container()).
		WithMountedDirectory(sdkSrc, m.SdkSourceDir).
		WithMountedCache(path.Join(codegenPath, "_build"), codegenBuildCache).
		WithWorkdir(codegenPath)
}

func (m *ElixirSdk) GenerateCode(introspectionJSON *dagger.File) *dagger.Directory {
	return m.WithDaggerCodegen().
		WithMountedFile(schemaPath, introspectionJSON).
		WithExec([]string{
			"mix", "dagger.codegen", "generate",
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
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"})
}

func mixProjectCaches(prefix string) (depsCache *dagger.CacheVolume, buildCache *dagger.CacheVolume) {
	return dag.CacheVolume(prefix + "-deps"), dag.CacheVolume(prefix + "-build")
}

func toElixirApplicationName(name string) string {
	return strcase.ToSnake(name)
}

func toElixirModuleName(name string) string {
	return strcase.ToCamel(name)
}
