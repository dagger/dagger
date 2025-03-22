package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
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

//go:embed template/mix.exs
var mixExs string

//go:embed template/lib/template.ex
var mainModuleEx string

//go:embed template/README.md
var moduleReadme string

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
		SdkSourceDir: sdkSourceDir,
		Container:    dag.Container(),
	}, nil
}

type ElixirSdk struct {
	SdkSourceDir *dagger.Directory

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

	ctr, err := m.Common(ctx, modSource, introspectionJSON)
	if err != nil {
		return nil, err
	}

	return ctr.
		WithEnvVariable("MIX_ENV", "prod").
		WithExec([]string{"mix", "deps.get", "--only", "prod"}).
		WithExec([]string{"mix", "deps.compile"}).
		WithExec([]string{"mix", "compile"}).
		WithEntrypoint([]string{
			"mix", "cmd",
			"--cd", path.Join(ModSourceDirPath, subPath),
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
		WithVCSIgnoredPaths([]string{
			genDir,
			// Elixir ignore files & directories from `mix new`.
			"_build",
			"cover",
			"deps",
			"doc",
			"erl_crash.dump",
			"*.ez",
			"template-*.tar",
			"tmp",
		}), nil
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
		WithNewElixirPackage(ctx, modName)
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
	entries, err := m.Container.Directory(".").Entries(ctx)
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
		app := dag.CurrentModule().Source().Directory("template")

		appName := toElixirApplicationName(modName)
		appContext := struct {
			AppName string
			ModName string
		}{
			AppName: appName,
			ModName: toElixirModuleName(modName),
		}

		mixExs, err := execTemplate(mixExs, appContext)
		if err != nil {
			m.err = err
			return m
		}
		mainModEx, err := execTemplate(mainModuleEx, appContext)
		if err != nil {
			m.err = err
			return m
		}
		readme, err := execTemplate(moduleReadme, appContext)
		if err != nil {
			m.err = err
			return m
		}

		m.Container = m.Container.
			WithDirectory(".", app, dagger.ContainerWithDirectoryOpts{
				Exclude: []string{"mix.exs", "lib/template.ex"},
			}).
			WithNewFile("mix.exs", mixExs).
			WithNewFile(fmt.Sprintf("lib/%s.ex", appName), mainModEx).
			WithNewFile("README.md", readme).
			WithExec([]string{"mix", "deps.get"})
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
	return m.baseContainer(dag.Container()).
		WithMountedDirectory(sdkSrc, m.SdkSourceDir).
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
	return ctr.
		From(elixirImage).
		WithExec([]string{"apk", "add", "--no-cache", "git"}).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"})
}

func toElixirApplicationName(name string) string {
	return strcase.ToSnake(name)
}

func toElixirModuleName(name string) string {
	return strcase.ToCamel(name)
}

func execTemplate(text string, data any) (string, error) {
	tmpl, err := template.New("template").Parse(text)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}
