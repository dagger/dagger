// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"

	"github.com/dagger/dagger/dev/internal/dagger"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Source  *Directory // +private
	Version *VersionInfo

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *Secret // +private
}

func New(
	ctx context.Context,
	source *Directory,

	// +optional
	version string,
	// +optional
	dockerCfg *Secret,
) (*DaggerDev, error) {
	versionInfo, err := newVersion(ctx, source, version)
	if err != nil {
		return nil, err
	}

	return &DaggerDev{
		Source:    source,
		Version:   versionInfo,
		DockerCfg: dockerCfg,
	}, nil
}

// Check that everything works. Use this as CI entrypoint.
func (dev *DaggerDev) Check(ctx context.Context) error {
	// FIXME: run concurrently
	if err := dev.Docs().Lint(ctx); err != nil {
		return err
	}
	if err := dev.Engine().Lint(ctx, false); err != nil {
		return err
	}
	if err := dev.Test().All(
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
	if err := dev.CLI().TestPublish(ctx); err != nil {
		return err
	}
	// FIXME: port all other function calls from Github Actions YAML
	return nil
}

// Develop the Dagger CLI
func (dev *DaggerDev) CLI() *CLI {
	return &CLI{Dagger: dev}
}

// Dagger's Go toolchain
func (dev *DaggerDev) Go() *GoToolchain {
	return &GoToolchain{Go: dag.Go(dev.Source)}
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

func (gtc *GoToolchain) Lint(
	ctx context.Context,
	packages []string,
	// +optional
	all bool,
) error {
	_, err := gtc.Go.Lint(ctx, packages, dagger.GoLintOpts{
		All: all,
	})
	return err
}

// Develop the Dagger engine container
func (dev *DaggerDev) Engine() *Engine {
	return &Engine{Dagger: dev}
}

// Develop the Dagger documentation
func (dev *DaggerDev) Docs() *Docs {
	return &Docs{Dagger: dev}
}

// Run Dagger scripts
func (dev *DaggerDev) Scripts() *Scripts {
	return &Scripts{Dagger: dev}
}

// Run all tests
func (dev *DaggerDev) Test() *Test {
	return &Test{Dagger: dev}
}

// Develop Dagger SDKs
func (dev *DaggerDev) SDK() *SDK {
	return &SDK{
		Go:         &GoSDK{Dagger: dev},
		Python:     &PythonSDK{Dagger: dev},
		Typescript: &TypescriptSDK{Dagger: dev},
		Rust:       &RustSDK{Dagger: dev},
		Elixir:     &ElixirSDK{Dagger: dev},
		PHP:        &PHPSDK{Dagger: dev},
		Java:       &JavaSDK{Dagger: dev},
	}
}

// Develop the Dagger helm chart
func (dev *DaggerDev) Helm() *Helm {
	return &Helm{Source: dev.Source.Directory("helm/dagger")}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (dev *DaggerDev) Dev(
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

	engine := dev.Engine()
	if experimentalGPUSupport {
		img := "ubuntu"
		engine = engine.WithBase(&img, &experimentalGPUSupport)
	}
	svc, err := engine.Service(ctx, "", dev.Version)
	if err != nil {
		return nil, err
	}
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	client, err := dev.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}

	return dev.Go().Env().
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", client).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithWorkdir("/mnt"), nil
}
