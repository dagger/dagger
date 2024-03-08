package main

import (
	"context"
	"path/filepath"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"

	"dagger/build"
	"dagger/util"
)

type Engine struct {
	Dagger *Dagger // +private

	Base   *Container // +private
	Args   []string   // +private
	Config []string   // +private

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
	e.Base = nil
	return e
}

// XXX: maybe we should private this?
func (e *Engine) Container(ctx context.Context) (*Container, error) {
	if e.Base == nil {
		opts := build.BuilderOpts{}
		if e.GPUSupport {
			opts.Base = "ubuntu"
			opts.GPUSupport = true
		}

		builder, err := build.NewBuilder(ctx, e.Dagger.Source, "linux/amd64", &opts)
		if err != nil {
			return nil, err
		}
		e.Base, err = builder.Engine(ctx)
		if err != nil {
			return nil, err
		}
	}

	cfg, err := generateConfig(e.Config)
	if err != nil {
		return nil, err
	}
	entrypoint, err := generateEntrypoint(e.Args)
	if err != nil {
		return nil, err
	}

	ctr := e.Base
	ctr = ctr.WithFile(engineTomlPath, cfg)
	ctr = ctr.WithFile(engineEntrypointPath, entrypoint)
	ctr = ctr.WithEntrypoint([]string{filepath.Base(engineEntrypointPath)})
	return ctr, nil
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
	devEngine, err := e.Container(ctx)
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

type CLI struct {
	Dagger *Dagger // +private

	Base *File // +private
}

func (e *CLI) File(ctx context.Context) (*File, error) {
	if e.Base == nil {
		builder, err := build.NewBuilder(ctx, e.Dagger.Source, "linux/amd64", nil)
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
