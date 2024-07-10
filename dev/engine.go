package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dev/internal/build"
	"github.com/dagger/dagger/dev/internal/dagger"
)

type Engine struct {
	Dagger *DaggerDev // +private

	Args   []string // +private
	Config []string // +private

	Trace bool // +private

	Race bool // +private

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

func (e *Engine) WithRace() *Engine {
	e.Race = true
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
) (*dagger.Container, error) {
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
	builder = builder.
		WithVersion(e.Dagger.Version.String()).
		WithRace(e.Race)
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
	// +optional
	version *VersionInfo,
) (*dagger.Service, error) {
	var cacheVolumeName string
	if version != nil {
		cacheVolumeName = "dagger-dev-engine-state-" + version.String()
	} else {
		cacheVolumeName = "dagger-dev-engine-state-" + identity.NewID()
	}
	if name != "" {
		cacheVolumeName += "-" + name
	}

	e = e.
		WithConfig("grpc", `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`)
	devEngine, err := e.Container(ctx, "")
	if err != nil {
		return nil, err
	}
	devEngine = devEngine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			// only one engine can run off it's local state dir at a time; Private means that we will attempt to re-use
			// these cache volumes if they are not already locked to another running engine but otherwise will create a new
			// one, which gets us best-effort cache re-use for these nested engine services
			Sharing: dagger.Private,
		}).
		WithExec(nil, dagger.ContainerWithExecOpts{
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

	eg.Go(func() error {
		// Packages to lint
		packages := []string{
			"",
			// FIXME: should the CI lint itself?
			// FIXME: unsustainable to require keeping this list up to date by hand
			"dev",
			"dev/dirdiff",
			"dev/go",
			"dev/graphql",
			"dev/shellcheck",
			"dev/markdown",
		}
		// Packages that need codegen
		codegen := []string{
			"dev",
			"dev/dirdiff",
			"dev/go",
			"dev/graphql",
			"dev/shellcheck",
			"dev/markdown",
		}

		return e.Dagger.Go().
			WithCodegen(codegen).
			Lint(ctx, packages, all)
	})

	eg.Go(func() error {
		return e.LintGenerate(ctx)
	})

	return eg.Wait()
}

// Generate any engine-related files
func (e *Engine) Generate() *dagger.Directory {
	generated := e.Dagger.Go().Env().
		WithoutDirectory("sdk") // sdk generation happens separately

	// protobuf dependencies
	generated = generated.
		WithMountedDirectory(
			"engine/telemetry/opentelemetry-proto",
			dag.Git("https://github.com/open-telemetry/opentelemetry-proto.git").
				Commit("9d139c87b52669a3e2825b835dd828b57a455a55").
				Tree(),
		).
		WithExec([]string{"apk", "add", "protoc=~3.21.12"}).
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"})

	generated = generated.
		WithExec([]string{"go", "generate", "-v", "./..."})

	return generated.Directory(".").
		WithoutDirectory("engine/telemetry/opentelemetry-proto")
}

// Lint any generated engine-related files
func (e *Engine) LintGenerate(ctx context.Context) error {
	before := e.Dagger.Go().Env().WithoutDirectory("sdk").Directory(".")
	after := e.Generate()
	_, err := dag.Dirdiff().AssertEqual(ctx, before, after, []string{"."})
	return err
}

// Publish all engine images to a registry
func (e *Engine) Publish(
	ctx context.Context,

	image string,

	// Comma-separated list of tags to use
	tags string,

	// +optional
	platform []dagger.Platform,

	// +optional
	registry *string,
	// +optional
	registryUsername *string,
	// +optional
	registryPassword *dagger.Secret,
) ([]string, error) {
	if len(platform) == 0 {
		platform = []dagger.Platform{dagger.Platform(platforms.DefaultString())}
	}

	refs := strings.Split(tags, ",")

	engines := make([]*dagger.Container, 0, len(platform))
	for _, platform := range platform {
		ctr, err := e.Container(ctx, platform)
		if err != nil {
			return []string{}, err
		}
		engines = append(engines, ctr)
	}

	ctr := dag.Container()
	if registry != nil && registryUsername != nil && registryPassword != nil {
		ctr = ctr.WithRegistryAuth(*registry, *registryUsername, registryPassword)
	}

	digests := []string{}
	for _, ref := range refs {
		digest, err := ctr.
			Publish(ctx, fmt.Sprintf("%s:%s", image, ref), dagger.ContainerPublishOpts{
				PlatformVariants:  engines,
				ForcedCompression: dagger.Gzip, // use gzip to avoid incompatibility w/ older docker versions
			})
		if err != nil {
			return digests, err
		}
		digests = append(digests, digest)
	}
	return digests, nil
}

// Verify that the engine builds without actually publishing anything
func (e *Engine) TestPublish(
	ctx context.Context,

	// +optional
	platform []dagger.Platform,
) error {
	if len(platform) == 0 {
		platform = []dagger.Platform{dagger.Platform(platforms.DefaultString())}
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

	ignoreFiles := dag.Directory().WithDirectory("/", e.Dagger.Source, dagger.DirectoryWithDirectoryOpts{
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
