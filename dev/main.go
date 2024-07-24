// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"

	"github.com/dagger/dagger/dev/internal/dagger"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Src     *dagger.Directory // +private
	Version *VersionInfo
	// When set, module codegen is automatically applied when retrieving the Dagger source code
	ModCodegen bool

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private
}

func New(
	ctx context.Context,
	source *dagger.Directory,

	// +optional
	version string,
	// +optional
	dockerCfg *dagger.Secret,
) (*DaggerDev, error) {
	versionInfo, err := newVersion(ctx, source, version)
	if err != nil {
		return nil, err
	}

	return &DaggerDev{
		Src:       source,
		Version:   versionInfo,
		DockerCfg: dockerCfg,
	}, nil
}

// Enable module auto-codegen when retrieving the dagger source code
func (dev *DaggerDev) WithModCodegen() *DaggerDev {
	dev.ModCodegen = true
	return dev
}

// Check that everything works. Use this as CI entrypoint.
func (dev *DaggerDev) Check(ctx context.Context) error {
	// FIXME: run concurrently
	if err := dev.Docs().Lint(ctx); err != nil {
		return err
	}
	if err := dev.Engine().Lint(ctx); err != nil {
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

// Return the Dagger source code
func (dev *DaggerDev) Source() *dagger.Directory {
	if !dev.ModCodegen {
		return dev.Src
	}
	// FIXME: build this list dynamically, by scanning the source for modules
	modules := []string{
		"dev",
		"dev/dirdiff",
		"dev/go",
		"dev/golangci",
		"dev/graphql",
		"dev/markdown",
		"dev/shellcheck",
		"sdk/elixir/runtime",
		// FIXME: broken
		// "sdk/python/dev",
		"sdk/python/runtime",
		"sdk/typescript/dev",
		"sdk/typescript/dev/node",
		"sdk/typescript/runtime",
	}

	src := dev.Src
	for _, module := range modules {
		layer := dev.Src.
			AsModule(dagger.DirectoryAsModuleOpts{
				SourceRootPath: module,
			}).
			GeneratedContextDirectory().
			Directory(module)
		src = src.WithDirectory(module, layer)
	}
	return src
}

// Dagger's Go toolchain
func (dev *DaggerDev) Go() *GoToolchain {
	return &GoToolchain{Go: dag.Go(dev.Source())}
}

type GoToolchain struct {
	// +private
	*dagger.Go
}

func (gtc *GoToolchain) Env() *dagger.Container {
	return gtc.Go.Env()
}

func (gtc *GoToolchain) Lint(
	ctx context.Context,
	packages []string,
) error {
	return gtc.Go.Lint(ctx, dagger.GoLintOpts{Packages: packages})
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
	return &Helm{Source: dev.Source().Directory("helm/dagger")}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (dev *DaggerDev) Dev(
	ctx context.Context,
	// Mount a directory into the container's workdir, for convenience
	// +optional
	target *dagger.Directory,
	// Enable experimental GPU support
	// +optional
	experimentalGPUSupport bool,
) (*dagger.Container, error) {
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
