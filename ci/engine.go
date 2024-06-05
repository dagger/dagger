package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/build"
	"github.com/dagger/dagger/ci/internal/dagger"
)

type Engine struct {
	Dagger *Dagger // +private

	Args   []string // +private
	Config []string // +private

	Trace bool // +private

	GPUSupport bool   // +private
	ImageBase  string // +private
}

func (e *Engine) WithConfig(key, value string) *Engine {
	e.Config = append(e.Config, key+"="+value)
	return e
}

func (e *Engine) WithArg(key, value string) *Engine {
	e.Args = append(e.Args, key+"="+value)
	return e
}

func (e *Engine) WithTrace() *Engine {
	e.Trace = true
	return e
}

func (e *Engine) WithBase(
	// +optional
	image *string,
	// +optional
	gpuSupport *bool,
) *Engine {
	if image != nil {
		e.ImageBase = *image
	}
	if gpuSupport != nil {
		e.GPUSupport = *gpuSupport
	}
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

	if e.ImageBase != "" {
		switch e.ImageBase {
		case "wolfi":
			builder = builder.WithWolfiBase()
		case "alpine":
			builder = builder.WithAlpineBase()
		case "ubuntu":
			builder = builder.WithUbuntuBase()
		default:
			return nil, fmt.Errorf("unknown base image type %s", e.ImageBase)
		}
	}

	if e.GPUSupport {
		builder = builder.WithGPUSupport()
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
	// Packages to lint
	packages := []string{
		"",
		// FIXME: should the CI lint itself?
		"ci",
		"ci/dirdiff",
		"ci/std/go",
		"ci/std/graphql",
	}
	// Packages that need codegen
	codegen := []string{
		"",
		"ci/dirdiff",
		"ci/std/go",
		"ci/std/graphql",
	}

	return e.Dagger.Go().
		WithCodegen(codegen).
		Lint(ctx, packages, all)
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
