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

	mod := normalizeModName(modName)
	modDepsCache, modBuildCache := mixProjectCaches(dag, "module-"+mod)

	entrypoint := path.Join(ModSourceDirPath, subPath, mod)
	depsPath := path.Join(entrypoint, "deps")
	buildPath := path.Join(entrypoint, "_build")

	return ctr.
		WithMountedCache(depsPath, modDepsCache).
		WithMountedCache(buildPath, modBuildCache).
		WithEntrypoint([]string{
			"mix", "cmd",
			"--cd", entrypoint,
			"mix do deps.get + dagger.invoke",
		}), nil
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
		WithVCSIgnoredPaths([]string{"dagger"}), nil
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

	mod := normalizeModName(modName)

	codegenDepsCache, codegenBuildCache := mixProjectCaches(dag, "dagger-codegen")

	ctr := m.Base("").
		WithMountedDirectory(ModSourceDirPath, modSource.ContextDirectory()).
		WithMountedDirectory(sdkSrc, m.SDKSourceDir).
		WithMountedCache(codegenPath()+"/deps", codegenDepsCache).
		WithMountedCache(codegenPath()+"/_build", codegenBuildCache).
		With(installCodegen).
		WithNewFile(schemaPath, ContainerWithNewFileOpts{
			Contents: introspectionJson,
		}).
		WithWorkdir(path.Join(ModSourceDirPath, subPath)).
		WithDirectory(
			"dagger",
			m.SDKSourceDir,
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

	// Generate scaffolding code when no project exists.
	if _, err = ctr.Directory(mod).File("mix.exs").Sync(ctx); err != nil {
		ctr := ctr.
			WithExec([]string{"mix", "new", "--sup", mod}).
			WithExec([]string{"mkdir", "-p", mod + "/lib/mix/tasks"}).
			WithExec([]string{"elixir", "/sdk/runtime/template.exs", "generate", mod})

		return ctr, nil
	}
	return ctr, nil
}

func (m *ElixirSdk) Base(version string) *Container {
	if version == "" {
		version = defaultElixirVersion
	}

	mixCache := dag.CacheVolume(".mix-" + version)

	return dag.Container().
		From("hexpm/elixir:"+version).
		WithMountedCache("/root/.mix", mixCache).
		WithExec([]string{"apt", "update"}).
		WithExec([]string{"apt", "install", "-y", "--no-install-recommends", "git"}).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"}).
		WithEnvVariable("PATH", "/root/.mix/escripts:$PATH", ContainerWithEnvVariableOpts{
			Expand: true,
		})
}

func installCodegen(ctr *Container) *Container {
	return ctr.
		WithWorkdir(codegenPath()).
		WithExec([]string{"mix", "deps.get"}).
		WithExec([]string{"mix", "escript.build"}).
		WithExec([]string{"mix", "escript.install", "--force"})
}

func codegenPath() string {
	return path.Join(sdkSrc, "dagger_codegen")
}

func mixProjectCaches(dag *Client, prefix string) (depsCache *CacheVolume, buildCache *CacheVolume) {
	return dag.CacheVolume(prefix + "-deps"), dag.CacheVolume(prefix + "-build")
}

func normalizeModName(name string) string {
	return strings.Replace(strings.ToLower(name), "-", "_", -1)
}
