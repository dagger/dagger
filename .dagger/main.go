// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"golang.org/x/sync/errgroup"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Src *dagger.Directory // +private

	Version string
	Tag     string
	Git     *dagger.VersionGit // +private

	// When set, module codegen is automatically applied when retrieving the Dagger source code
	ModCodegen        bool
	ModCodegenTargets []string

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private
}

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=["bin", ".git", "**/node_modules", "**/.venv", "**/__pycache__"]
	source *dagger.Directory,

	// +optional
	dockerCfg *dagger.Secret,
) (*DaggerDev, error) {
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}

	dev := &DaggerDev{
		Src:       source,
		Tag:       tag,
		Git:       v.Git(),
		Version:   version,
		DockerCfg: dockerCfg,
	}

	modules, err := dev.containing(ctx, "dagger.json")
	if err != nil {
		return nil, err
	}
	for _, module := range modules {
		if strings.HasPrefix(module, "docs/") {
			continue
		}
		if strings.HasPrefix(module, "core/integration/") {
			continue
		}
		if strings.HasPrefix(module, "dagql/idtui/viztest/broken/") {
			continue
		}
		dev.ModCodegenTargets = append(dev.ModCodegenTargets, module)
	}

	return dev, nil
}

// Enable module auto-codegen when retrieving the dagger source code
func (dev *DaggerDev) WithModCodegen() *DaggerDev {
	clone := *dev
	clone.ModCodegen = true
	return &clone
}

func (dev *DaggerDev) WithModCodegenTargets(targets []string) *DaggerDev {
	clone := *dev
	clone.ModCodegen = true
	clone.ModCodegenTargets = targets
	return &clone
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

	src := dev.Src
	for _, module := range dev.ModCodegenTargets {
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

func (dev *DaggerDev) containing(ctx context.Context, filename string) ([]string, error) {
	entries, err := dev.Src.Glob(ctx, "**/"+filename)
	if err != nil {
		return nil, err
	}

	var parents []string
	for _, entry := range entries {
		entry = filepath.Clean(entry)
		parent := strings.TrimSuffix(entry, filename)
		if parent == "" {
			parent = "."
		}
		parents = append(parents, parent)
	}

	return parents, nil
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
func (dev *DaggerDev) Engine() *DaggerEngine {
	return &DaggerEngine{Dagger: dev}
}

// Run Dagger scripts
func (dev *DaggerDev) Scripts() *Scripts {
	return &Scripts{Dagger: dev}
}

// Find test suites to run
func (dev *DaggerDev) Test() *Test {
	return &Test{Dagger: dev}
}

// Find benchmark suites to run
func (dev *DaggerDev) Bench() *Bench {
	return &Bench{Test: dev.Test()}
}

// Run all code generation - SDKs, docs, etc
func (dev *DaggerDev) Generate(ctx context.Context) (*dagger.Directory, error) {
	var docs, sdks, engine *dagger.Directory
	var eg errgroup.Group

	eg.Go(func() error {
		var err error
		docs = dag.Docs().Generate()
		docs, err = docs.Sync(ctx)
		return err
	})

	eg.Go(func() error {
		var err error
		sdks, err = dev.SDK().All().Generate(ctx)
		if err != nil {
			return err
		}
		sdks, err = sdks.Sync(ctx)
		return err
	})

	eg.Go(func() error {
		var err error
		engine = dev.Engine().Generate()
		docs, err = engine.Sync(ctx)
		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return dag.Directory().
		WithDirectory("", docs).
		WithDirectory("", sdks).
		WithDirectory("", engine), nil
}

// Develop Dagger SDKs
func (dev *DaggerDev) SDK() *SDK {
	return &SDK{
		Go:         &GoSDK{Dagger: dev},
		Python:     &PythonSDK{Dagger: dev},
		Typescript: &TypescriptSDK{Dagger: dev},
		Elixir:     &ElixirSDK{Dagger: dev},
		Rust:       &RustSDK{Dagger: dev},
		PHP:        &PHPSDK{Dagger: dev},
		Java:       &JavaSDK{Dagger: dev},
		Dotnet:     &DotnetSDK{Dagger: dev},
	}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (dev *DaggerDev) Dev(
	ctx context.Context,
	// Mount a directory into the container's workdir, for convenience
	// +optional
	target *dagger.Directory,
	// Set target distro
	// +optional
	image *Distro,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
	// Share cache globally
	// +optional
	sharedCache bool,
) (*dagger.Container, error) {
	if target == nil {
		target = dag.Directory()
	}

	svc, err := dev.Engine().Service(ctx, "", image, gpuSupport, sharedCache)
	if err != nil {
		return nil, err
	}
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	return dev.Go().Env().
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", dag.DaggerCli().Binary()).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithWorkdir("/mnt"), nil
}

func (dev *DaggerDev) withDockerCfg(ctr *dagger.Container) *dagger.Container {
	if dev.DockerCfg == nil {
		return ctr
	}
	return ctr.WithMountedSecret("/root/.docker/config.json", dev.DockerCfg)
}
