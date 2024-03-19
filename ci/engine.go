package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"

	"dagger/build"
	"dagger/internal/dagger"
	"dagger/util"
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
	cacheVolumeName = cacheVolumeName + identity.NewID()

	e = e.
		WithConfig("grpc", `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`)
	devEngine, err := e.container(ctx, Platform(platforms.DefaultString()))
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

// Lint lints the engine
func (e *Engine) Lint(ctx context.Context) error {
	_, err := dag.Container().
		From("golangci/golangci-lint:v1.55-alpine").
		WithMountedDirectory("/app", util.GoDirectory(e.Dagger.Source)).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Sync(ctx)
	return err
}

func (e *Engine) Publish(
	ctx context.Context,

	engineImage string,
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

	ref := fmt.Sprintf("%s:%s", engineImage, builder.Version.EngineVersion())
	if e.GPUSupport {
		ref += "-gpu"
	}

	var engines []*Container
	for _, platform := range platform {
		ctr, err := e.container(ctx, platform)
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

func (e *Engine) container(ctx context.Context, platform dagger.Platform) (*Container, error) {
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
	builder = builder.WithPlatform(platform)
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

type CLI struct {
	Dagger *Dagger // +private

	Base *File // +private
}

func (e *CLI) File(ctx context.Context) (*File, error) {
	if e.Base == nil {
		builder, err := build.NewBuilder(ctx, e.Dagger.Source)
		if err != nil {
			return nil, err
		}
		e.Base, err = builder.CLI(ctx)
		if err != nil {
			return nil, err
		}
	}
	return e.Base, nil
}
