// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/ci/internal/dagger"
)

// A dev environment for the Dagger Engine
type Dagger struct {
	Source  *Directory // +private
	Version *VersionInfo

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	HostDockerConfig *Secret // +private
}

func New(
	ctx context.Context,
	source *Directory,

	// +optional
	version string,
	// +optional
	hostDockerConfig *Secret,
) (*Dagger, error) {
	versionInfo, err := newVersion(ctx, source, version)
	if err != nil {
		return nil, err
	}

	return &Dagger{
		Source:           source,
		Version:          versionInfo,
		HostDockerConfig: hostDockerConfig,
	}, nil
}

// Check that everything works. Use this as CI entrypoint.
func (ci *Dagger) Check(ctx context.Context) error {
	// FIXME: run concurrently
	if err := ci.Docs().Lint(ctx); err != nil {
		return err
	}
	if err := ci.Engine().Lint(ctx); err != nil {
		return err
	}
	if err := ci.Test().All(
		ctx,
		// failfast
		false,
		// parallel
		16,
		// timeout
		"",
		// race
		true,
	); err != nil {
		return err
	}
	if err := ci.CLI().TestPublish(ctx); err != nil {
		return err
	}
	// FIXME: port all other function calls from Github Actions YAML
	return nil
}

// Generate all source files, across all dagger modules in the source
func (ci *Dagger) Generate(ctx context.Context) (*Directory, error) {
	// Find all dagger modules in the source
	src := ci.Source
	daggerJSONs, err := src.Glob(ctx, "**/dagger.json")
	if err != nil {
		return nil, err
	}
	// FIXME: parallelize
	for _, daggerJSON := range daggerJSONs {
		// Skip test data, which contains test modules
		if strings.HasPrefix(daggerJSON, "core/integration/testdata") {
			continue
		}
		modPath := filepath.Dir(daggerJSON)
		mod := src.AsModule(DirectoryAsModuleOpts{SourceRootPath: modPath})
		src = src.WithDirectory("/", mod.GeneratedContextDirectory())
	}
	return src, nil
}

func isSubPath(basePath, checkPath string) bool {
	basePath = filepath.Clean(basePath)
	checkPath = filepath.Clean(checkPath)
	if basePath != "/" {
		basePath = basePath + string(filepath.Separator)
	}
	return strings.HasPrefix(checkPath, basePath)
}

// Develop the Dagger CLI
func (ci *Dagger) CLI() *CLI {
	return &CLI{Dagger: ci}
}

// Dagger's Go toolchain
func (ci *Dagger) Go() *GoToolchain {
	return &GoToolchain{Go: dag.Go(ci.Source)}
}

type GoToolchain struct {
	// +private
	*Go
}

// Run codegen (equivalent to `dagger develop`) in the specified subdirectories
func (gtc *GoToolchain) WithCodegen(subdirs []string) *GoToolchain {
	src := gtc.Source()
	for _, subdir := range subdirs {
		codegen := src.
			AsModule(dagger.DirectoryAsModuleOpts{
				SourceRootPath: subdir,
			}).
			GeneratedContextDirectory()
		src = src.WithDirectory("", codegen)
	}
	return &GoToolchain{Go: dag.Go(src)}
}

func (gtc *GoToolchain) Env() *Container {
	return gtc.Go.Env()
}

// Develop the Dagger engine container
func (ci *Dagger) Engine() *Engine {
	return &Engine{Dagger: ci}
}

// Develop the Dagger documentation
func (ci *Dagger) Docs() *Docs {
	return &Docs{Dagger: ci}
}

// Run Dagger scripts
func (ci *Dagger) Scripts() *Scripts {
	return &Scripts{Dagger: ci}
}

// Run all tests
func (ci *Dagger) Test() *Test {
	return &Test{Dagger: ci}
}

// Develop Dagger SDKs
func (ci *Dagger) SDK() *SDK {
	return &SDK{
		Go:         &GoSDK{Dagger: ci},
		Python:     &PythonSDK{Dagger: ci},
		Typescript: &TypescriptSDK{Dagger: ci},
		Rust:       &RustSDK{Dagger: ci},
		Elixir:     &ElixirSDK{Dagger: ci},
		PHP:        &PHPSDK{Dagger: ci},
		Java:       &JavaSDK{Dagger: ci},
	}
}

// Develop the Dagger helm chart
func (ci *Dagger) Helm() *Helm {
	return &Helm{Dagger: ci}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (ci *Dagger) Dev(
	ctx context.Context,
	// Mount a directory into the container's workdir, for convenience
	// +optional
	target *Directory,
	// Enable experimental GPU support
	// +optional
	experimentalGPUSupport bool,
) (*Container, error) {
	if target == nil {
		target = dag.Directory()
	}

	engine := ci.Engine()
	if experimentalGPUSupport {
		img := "ubuntu"
		engine = engine.WithBase(&img, &experimentalGPUSupport)
	}
	svc, err := engine.Service(ctx, "", ci.Version)
	if err != nil {
		return nil, err
	}
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	client, err := ci.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}

	return ci.Go().Env().
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", client).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithWorkdir("/mnt"), nil
}
