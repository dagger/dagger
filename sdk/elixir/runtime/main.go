package main

import (
	"context"
	"path"

	"elixir-sdk/internal/dagger"

	"github.com/ettle/strcase"
)

var caser *strcase.Caser

func init() {
	var splitFn = strcase.NewSplitFn(
		[]rune{'*', '.', ',', '-', '_'},
		strcase.SplitCase,
		strcase.SplitAcronym,
		strcase.PreserveNumberFormatting,
		strcase.SplitBeforeNumber,
		strcase.SplitAfterNumber,
	)

	caser = strcase.NewCaser(false, nil, splitFn)
}

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
		// TODO: Replace with a *default path from context* when
		// https://github.com/dagger/dagger/pull/7744 becomes available.
		sdkSourceDir = dag.Directory().
			// NB: these patterns should match those in `dagger.json`.
			// When `--sdk` points to a git remote the files aren't filtered
			// using `dagger.json` include/exclude patterns since the whole
			// repo is cloned. It's still useful to have the same patterns in
			// `dagger.json` though, to avoid the unnecessary uploads when
			// loading the SDK from a local path.
			WithDirectory(
				"/",
				dag.CurrentModule().Source().Directory(".."),
				dagger.DirectoryWithDirectoryOpts{
					Include: []string{
						"LICENSE",
						"lib/**/*.ex",
						".formatter.exs",
						"mix.exs",
						"mix.lock",
						"dagger_codegen/lib/**/*.ex",
						"dagger_codegen/mix.exs",
						"dagger_codegen/mix.lock",
					},
				},
			)
	}
	return &ElixirSdk{
		SdkSourceDir:  sdkSourceDir,
		RequiredPaths: []string{},
		Container:     dag.Container(),
	}
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
			WithExec([]string{"mix", "new", "--sup", modName}).
			WithDirectory(modName+"/lib/mix/tasks", dag.Directory()).
			WithMountedFile("/template.exs", dag.CurrentModule().Source().File("template.exs")).
			WithExec([]string{"elixir", "/template.exs", "generate", modName})
	}
	return m
}

// Generate the SDK into the container.
func (m *ElixirSdk) WithSDK(introspectionJson *dagger.File) *ElixirSdk {
	if m.err != nil {
		return m
	}
	m.Container = m.Container.
		WithDirectory(genDir, m.SdkSourceDir.
			WithoutDirectory("dagger_codegen").
			WithoutDirectory("lib/dagger/gen"),
		).
		WithDirectory(path.Join(genDir, "lib", "dagger", "gen"), m.GenerateCode(introspectionJson))
	return m
}

func (m *ElixirSdk) WithDaggerCodegen() *dagger.Container {
	codegenPath := path.Join(sdkSrc, "dagger_codegen")
	codegenDepsCache, codegenBuildCache := mixProjectCaches("dagger-codegen")
	return m.baseContainer(dag.Container()).
		WithMountedDirectory(sdkSrc, m.SdkSourceDir).
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
	return caser.ToSnake(name)
}
