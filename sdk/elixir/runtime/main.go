package main

import (
	"context"
	"fmt"
	"path"

	"github.com/iancoleman/strcase"
)

const (
	ModSourceDirPath = "/src"
	sdkSrc           = "/sdk"
	genDir           = "sdk"
	schemaPath       = "/schema.json"
	elixirVersion    = "1.16.2"
	otpVersion       = "26.2.4"
	elixirImage      = "hexpm/elixir:" + elixirVersion + "-erlang-" + otpVersion + "-debian-bookworm-20240423-slim@sha256:279f65ecc3e57a683362e62a46fcfb502ea156b0de76582c2f8e5cdccccbdd54"
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
	entrypoint := path.Join(ModSourceDirPath, subPath, mod)

	return ctr.
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

	codegenDepsCache, codegenBuildCache := mixProjectCaches("dagger-codegen")

	ctr := m.Base().
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

func (m *ElixirSdk) Base() *Container {
	mixCache := dag.CacheVolume(fmt.Sprintf(".mix-%s-%s", elixirVersion, otpVersion))

	return dag.Container().
		From(elixirImage).
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

func mixProjectCaches(prefix string) (depsCache *CacheVolume, buildCache *CacheVolume) {
	return dag.CacheVolume(prefix + "-deps"), dag.CacheVolume(prefix + "-build")
}

func normalizeModName(name string) string {
	return strcase.ToSnake(name)
}
