package main

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/build"
	"github.com/dagger/dagger/ci/consts"
	"github.com/dagger/dagger/ci/internal/dagger"
	"github.com/dagger/dagger/ci/util"
)

type Engine struct {
	Dagger *Dagger // +private

	Args   []string // +private
	Config []string // +private

	GPUSupport bool // +private
}

func (e *Engine) WithConfig(key, value string) *Engine {
	e.Config = append(e.Config, key+"="+value)
	return e
}

func (e *Engine) WithArg(key, value string) *Engine {
	e.Args = append(e.Args, key+"="+value)
	return e
}

func (e *Engine) WithGPUSupport() *Engine {
	e.GPUSupport = true
	return e
}

// Build the engine container
func (e *Engine) Container(
	ctx context.Context,

	// +optional
	platform dagger.Platform,
) (*Container, error) {
	cfg, err := generateConfig(e.Config)
	if err != nil {
		return nil, err
	}
	entrypoint, err := generateEntrypoint(e.Args)
	if err != nil {
		return nil, err
	}

	builder, err := build.NewBuilder(ctx, e.Dagger.Source)
	if err != nil {
		return nil, err
	}
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}
	if e.GPUSupport {
		builder = builder.WithUbuntuBase().WithGPUSupport()
	}

	ctr, err := builder.Engine(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		WithFile(engineTomlPath, cfg).
		WithFile(engineEntrypointPath, entrypoint).
		WithEntrypoint([]string{filepath.Base(engineEntrypointPath)})
	return ctr, nil
}

// Create a test engine service
func (e *Engine) Service(
	ctx context.Context,
	name string,
) (*Service, error) {
	var cacheVolumeName string
	if name != "" {
		cacheVolumeName = "dagger-dev-engine-state-" + name
	} else {
		cacheVolumeName = "dagger-dev-engine-state"
	}
	cacheVolumeName += identity.NewID()

	e = e.
		WithConfig("grpc", `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`)
	devEngine, err := e.Container(ctx, "")
	if err != nil {
		return nil, err
	}
	devEngine = devEngine.
		WithExposedPort(1234, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume(cacheVolumeName)).
		WithExec(nil, ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	return devEngine.AsService(), nil
}

// Lint the engine
func (e *Engine) Lint(ctx context.Context) error {
	pkgs := []string{"", "ci"}

	ctr := dag.Container().
		From(consts.GolangLintImage).
		WithMountedDirectory("/app", util.GoDirectory(e.Dagger.Source))
	for _, pkg := range pkgs {
		ctr = ctr.
			WithWorkdir(path.Join("/app", pkg)).
			WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"})
	}
	_, err := ctr.Sync(ctx)
	return err
}

// Publish all engine images to a registry
func (e *Engine) Publish(
	ctx context.Context,

	image string,
	// +optional
	platform []Platform,

	// +optional
	registry *string,
	// +optional
	registryUsername *string,
	// +optional
	registryPassword *Secret,
) (string, error) {
	if len(platform) == 0 {
		platform = []Platform{Platform(platforms.DefaultString())}
	}
	builder, err := build.NewBuilder(ctx, e.Dagger.Source)
	if err != nil {
		return "", err
	}

	ref := fmt.Sprintf("%s:%s", image, builder.EngineVersion())
	if e.GPUSupport {
		ref += "-gpu"
	}

	engines := make([]*Container, 0, len(platform))
	for _, platform := range platform {
		ctr, err := e.Container(ctx, platform)
		if err != nil {
			return "", err
		}
		engines = append(engines, ctr)
	}

	ctr := dag.Container()
	if registry != nil && registryUsername != nil && registryPassword != nil {
		ctr = ctr.WithRegistryAuth(*registry, *registryUsername, registryPassword)
	}

	digest, err := ctr.
		Publish(ctx, ref, dagger.ContainerPublishOpts{
			PlatformVariants:  engines,
			ForcedCompression: dagger.Gzip, // use gzip to avoid incompatibility w/ older docker versions
		})
	if err != nil {
		return "", err
	}
	return digest, nil
}

// Verify that the engine builds without actually publishing anything
func (e *Engine) TestPublish(
	ctx context.Context,

	// +optional
	platform []Platform,
) error {
	if len(platform) == 0 {
		platform = []Platform{Platform(platforms.DefaultString())}
	}

	var eg errgroup.Group
	for _, platform := range platform {
		platform := platform
		eg.Go(func() error {
			ctr, err := e.Container(ctx, platform)
			if err != nil {
				return err
			}
			_, err = ctr.Sync(ctx)
			return err
		})
	}
	return eg.Wait()
}

func (e *Engine) Scan(ctx context.Context) (string, error) {
	target, err := e.Container(ctx, "")
	if err != nil {
		return "", err
	}

	return dag.Container().
		From("aquasec/trivy:0.50.1").
		WithMountedFile("/mnt/engine.tar", target.AsTarball()).
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache")).
		WithExec([]string{
			"image",
			"--format=json",
			"--no-progress",
			"--exit-code=1",
			"--vuln-type=os,library",
			"--severity=CRITICAL,HIGH",
			"--input",
			"/mnt/engine.tar",
		}).
		Stdout(ctx)
}
