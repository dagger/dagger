package main

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
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

	Trace bool // +private

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

func (e *Engine) WithTrace() *Engine {
	e.Trace = true
	return e
}

// Build the engine container
func (e *Engine) Container(
	ctx context.Context,

	// +optional
	platform dagger.Platform,
) (*Container, error) {
	cfg, err := generateConfig(e.Trace, e.Config)
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
	builder = builder.WithVersion(e.Dagger.Version.String())
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

	e = e.
		WithConfig("grpc", `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.89.0.0/16`)
	devEngine, err := e.Container(ctx, "")
	if err != nil {
		return nil, err
	}
	devEngine = devEngine.
		WithExposedPort(1234, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume(cacheVolumeName)).
		WithExec(nil, ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	return devEngine.AsService(), nil
}

// Lint the engine
func (e *Engine) Lint(
	ctx context.Context,
	// +optional
	all bool,
) error {
	eg, ctx := errgroup.WithContext(ctx)

	pkgs := []string{""}
	// pkgs := []string{"", "ci"}

	cmd := []string{"golangci-lint", "run", "-v", "--timeout", "5m"}
	if all {
		cmd = append(cmd, "--max-issues-per-linter=0", "--max-same-issues=0")
	}
	for _, pkg := range pkgs {
		golangci := dag.Container().
			From(consts.GolangLintImage).
			WithMountedDirectory("/app", util.GoDirectory(e.Dagger.Source)).
			WithWorkdir(path.Join("/app", pkg)).
			WithExec(cmd)
		eg.Go(func() error {
			_, err := golangci.Sync(ctx)
			return err
		})

		eg.Go(func() error {
			return util.DiffDirectoryF(ctx, util.GoDirectory(e.Dagger.Source), func(ctx context.Context) (*dagger.Directory, error) {
				return util.GoBase(e.Dagger.Source).
					WithExec([]string{"go", "mod", "tidy"}).
					Directory("."), nil
			}, "go.mod", "go.sum")
		})
	}

	return eg.Wait()
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

	ref := fmt.Sprintf("%s:%s", image, e.Dagger.Version)
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

	ignoreFiles := dag.Directory().WithDirectory("/", e.Dagger.Source, DirectoryWithDirectoryOpts{
		Include: []string{
			".trivyignore",
			".trivyignore.yml",
			".trivyignore.yaml",
		},
	})
	ignoreFileNames, err := ignoreFiles.Entries(ctx)
	if err != nil {
		return "", err
	}

	ctr := dag.Container().
		From("aquasec/trivy:0.50.4").
		WithMountedFile("/mnt/engine.tar", target.AsTarball()).
		WithMountedDirectory("/mnt/ignores", ignoreFiles).
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache"))

	args := []string{
		"image",
		"--format=json",
		"--no-progress",
		"--exit-code=1",
		"--vuln-type=os,library",
		"--severity=CRITICAL,HIGH",
		"--show-suppressed",
	}
	if len(ignoreFileNames) > 0 {
		args = append(args, "--ignorefile=/mnt/ignores/"+ignoreFileNames[0])
	}
	args = append(args, "--input", "/mnt/engine.tar")

	return ctr.WithExec(args).Stdout(ctx)
}
