package main

import (
	"context"
	"fmt"
	"path"
	"strings"
)

const (
	ModSourceDirPath     = "/src"
	sdkSrc               = "/sdk"
	genDir               = "sdk"
	schemaPath           = "/schema.json"
	defaultElixirVersion = "1.16.1-erlang-26.2.2-debian-bookworm-20240130-slim"
)

func New(
	// +optional
	sdkSourceDir *Directory,
) *ElixirSdk {
	return &ElixirSdk{SDKSourceDir: sdkSourceDir, RequiredPaths: []string{}}
}

type ElixirSdk struct {
	SDKSourceDir  *Directory
	RequiredPaths []string
}

func (m *ElixirSdk) ModuleRuntime(
	ctx context.Context,
	modSource *ModuleSource,
	introspectionJson string,
) (*Container, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, err
	}

	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}

	// TODO: clean me up.
	mod := strings.Replace(modName, "-", "_", -1)
	entrypoint := path.Join(ModSourceDirPath, subPath, mod)

	return ctr.
		WithEntrypoint([]string{"mix", "cmd",
			"--cd", entrypoint,
			"mix do deps.get + dagger.invoke"}), nil
}

func (m *ElixirSdk) Codegen(
	ctx context.Context,
	modSource *ModuleSource,
	introspectionJson string,
) (*GeneratedCode, error) {
	ctr, err := m.CodegenBase(ctx, modSource, introspectionJson)
	if err != nil {
		return nil, fmt.Errorf("could not load module config: %v", err)
	}

	return dag.GeneratedCode(ctr.Directory(ModSourceDirPath)).
		WithVCSGeneratedPaths([]string{genDir + "/**"}).
		WithVCSIgnoredPaths([]string{genDir}), nil
}

func (m *ElixirSdk) CodegenBase(
	ctx context.Context,
	modSource *ModuleSource,
	introspectionJson string,
) (*Container, error) {
	modName, err := modSource.ModuleName(ctx)
	if err != nil {
		return nil, err
	}
	subPath, err := modSource.SourceSubpath(ctx)
	if err != nil {
		return nil, err
	}

	sdk := dag.Git("https://github.com/dagger/dagger").
		Branch("main").
		Tree().
		Directory("sdk/elixir")

	// TODO: maybe call ToLower and then replace `-` with `_`?
	mod := strings.Replace(modName, "-", "_", -1)

	ctr := m.Base("").
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithMountedDirectory(sdkSrc, dag.CurrentModule().Source()).
		WithExec([]string{"mix", "escript.install",
			"github", "dagger/dagger", "branch", "main",
			"--sparse", "sdk/elixir/dagger_codegen", "--force"}).
		WithNewFile(schemaPath, ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		WithDirectory(
			"dagger",
			sdk,
			ContainerWithDirectoryOpts{Exclude: []string{
				"*.livemd",
				"*.md",
				".changes",
				"dagger_codegen",
				"runtime",
				"scripts",
				"test",
			}},
		).
		WithWorkdir(path.Join(ModSourceDirPath, subPath, "dagger")).
		WithExec([]string{
			"dagger_codegen", "generate",
			"--outdir", "lib/dagger/gen",
			"--introspection", schemaPath,
		}).
		WithExec([]string{
			"mix", "format",
		}).
		WithWorkdir(path.Join(ModSourceDirPath, subPath))

	// Project not exists.
	if _, err = ctr.Directory(mod).File("mix.exs").Sync(ctx); err != nil {
		ctr := ctr.
			WithExec([]string{"mix", "new", "--sup", mod}).
			WithExec([]string{"mkdir", "-p", mod + "/lib/mix/tasks"}).
			WithExec([]string{"sh", "-c", "elixir /sdk/template.exs gen_mix_exs " + mod + " > " + mod + "/mix.exs"}).
			WithExec([]string{"sh", "-c", "elixir /sdk/template.exs gen_module " + mod + " > " + mod + "/lib/" + mod + ".ex"}).
			WithExec([]string{"sh", "-c", "elixir /sdk/template.exs gen_application " + mod + " > " + mod + "/lib/" + mod + "/application.ex"}).
			WithExec([]string{"sh", "-c", "elixir /sdk/template.exs gen_mix_task " + mod + " > " + mod + "/lib/mix/tasks/dagger.invoke.ex"})

		return ctr, nil
	}
	return ctr, nil
}

func (m *ElixirSdk) Base(version string) *Container {
	if version == "" {
		version = defaultElixirVersion
	}
	// TODO: Mount cache.
	return dag.Container().
		From("hexpm/elixir:"+version).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "--no-install-recommends", "git"}).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"}).
		WithEnvVariable("PATH", "/root/.mix/escripts:$PATH", ContainerWithEnvVariableOpts{
			Expand: true,
		})
}
