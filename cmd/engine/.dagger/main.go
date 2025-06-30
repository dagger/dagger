package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/cmd/engine/.dagger/build"
	"github.com/dagger/dagger/cmd/engine/.dagger/internal/dagger"
)

type Distro string

const (
	DistroAlpine Distro = "alpine"
	DistroWolfi  Distro = "wolfi"
	DistroUbuntu Distro = "ubuntu"
)

func New(
	// +defaultPath="/"
	// +ignore=[".git", "bin", "**/.dagger", "**/.DS_Store", "**/node_modules", "**/__pycache__", "**/.venv", "**/.mypy_cache", "**/.pytest_cache", "**/.ruff_cache", "sdk/python/dist", "sdk/python/**/sdk", "go.work", "go.work.sum", "**/*_test.go", "**/target", "**/deps", "**/cover", "**/_build"]
	source *dagger.Directory,
) *DaggerEngine {
	return &DaggerEngine{
		Source: source,
	}
}

type DaggerEngine struct {
	Source *dagger.Directory

	BuildkitConfig []string // +private
	LogLevel       string   // +private

	Race bool // +private
}

func (e *DaggerEngine) WithBuildkitConfig(key, value string) *DaggerEngine {
	e.BuildkitConfig = append(e.BuildkitConfig, key+"="+value)
	return e
}

func (e *DaggerEngine) WithRace() *DaggerEngine {
	e.Race = true
	return e
}

func (e *DaggerEngine) WithLogLevel(level string) *DaggerEngine {
	e.LogLevel = level
	return e
}

// Build the engine container
func (e *DaggerEngine) Container(
	ctx context.Context,

	// +optional
	platform dagger.Platform,
	// +default="alpine"
	image Distro,
	// +optional
	gpuSupport bool,
) (*dagger.Container, error) {
	cfg, err := generateConfig(e.LogLevel)
	if err != nil {
		return nil, err
	}
	bkcfg, err := generateBKConfig(e.BuildkitConfig)
	if err != nil {
		return nil, err
	}
	entrypoint, err := generateEntrypoint()
	if err != nil {
		return nil, err
	}

	builder, err := build.NewBuilder(ctx, e.Source)
	if err != nil {
		return nil, err
	}
	builder = builder.WithRace(e.Race)
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}

	switch image {
	case DistroAlpine:
		builder = builder.WithAlpineBase()
	case DistroWolfi:
		builder = builder.WithWolfiBase()
	case DistroUbuntu:
		builder = builder.WithUbuntuBase()
	default:
		return nil, fmt.Errorf("unknown base image type %s", image)
	}

	if gpuSupport {
		builder = builder.WithGPUSupport()
	}

	ctr, err := builder.Engine(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		WithFile(engineJSONPath, cfg).
		WithFile(engineTOMLPath, bkcfg).
		WithFile(engineEntrypointPath, entrypoint).
		WithEntrypoint([]string{filepath.Base(engineEntrypointPath)})

	cli := dag.DaggerCli().Binary(dagger.DaggerCliBinaryOpts{
		Platform: platform,
	})
	ctr = ctr.
		WithFile(cliPath, cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", distconsts.DefaultEngineSockAddr)
	// ctr = ctr.WithEnvVariable("BUILDKIT_SCHEDULER_DEBUG", "1")

	return ctr, nil
}

// Create a test engine service
func (e *DaggerEngine) Service(
	ctx context.Context,
	name string,
	// +default="alpine"
	image Distro,
	// +optional
	gpuSupport bool,
	// +optional
	sharedCache bool,
	// +optional
	metrics bool,
) (*dagger.Service, error) {
	cacheVolumeName := "dagger-dev-engine-state"
	if !sharedCache {
		version, err := dag.Version().Version(ctx)
		if err != nil {
			return nil, err
		}
		if version != "" {
			cacheVolumeName = "dagger-dev-engine-state-" + version
		} else {
			cacheVolumeName = "dagger-dev-engine-state-" + identity.NewID()
		}
		if name != "" {
			cacheVolumeName += "-" + name
		}
	}

	devEngine, err := e.Container(ctx, "", image, gpuSupport)
	if err != nil {
		return nil, err
	}

	devEngine = devEngine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			// only one engine can run off it's local state dir at a time; Private means that we will attempt to re-use
			// these cache volumes if they are not already locked to another running engine but otherwise will create a new
			// one, which gets us best-effort cache re-use for these nested engine services
			Sharing: dagger.CacheSharingModePrivate,
		})

	if metrics {
		devEngine = devEngine.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_METRICS_ADDR", "0.0.0.0:9090").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL", "10s")
	}

	return devEngine.AsService(dagger.ContainerAsServiceOpts{
		Args: []string{
			"--addr", "tcp://0.0.0.0:1234",
			"--network-name", "dagger-dev",
			"--network-cidr", "10.88.0.0/16",
		},
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	}), nil
}

// Generate any engine-related files
// Note: this is codegen of the 'go generate' variety, not 'dagger develop'
func (e *DaggerEngine) Generate() *dagger.Directory {
	generated := dag.Go(e.Source.WithoutDirectory("sdk")).Env()
	original := generated.Directory(".")

	// protobuf dependencies
	generated = generated.
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"})

	generated = generated.
		WithExec([]string{"go", "generate", "-v", "./..."})

	return original.Diff(generated.Directory("."))
}

// Lint any generated engine-related files
func (e *DaggerEngine) LintGenerate(ctx context.Context) error {
	before := dag.Go(e.Source.WithoutDirectory("sdk")).Env().Directory(".")
	after := before.WithDirectory(".", e.Generate())
	return dag.Dirdiff().AssertEqual(ctx, before, after, []string{"."})
}

var targets = []struct {
	Name       string
	Tag        string
	Image      Distro
	Platforms  []dagger.Platform
	GPUSupport bool
}{
	{
		Name:      "alpine (default)",
		Tag:       "%s",
		Image:     DistroAlpine,
		Platforms: []dagger.Platform{"linux/amd64", "linux/arm64"},
	},
	{
		Name:       "ubuntu with nvidia variant",
		Tag:        "%s-gpu",
		Image:      DistroUbuntu,
		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
	{
		Name:      "wolfi",
		Tag:       "%s-wolfi",
		Image:     DistroWolfi,
		Platforms: []dagger.Platform{"linux/amd64"},
	},
	{
		Name:       "wolfi with nvidia variant",
		Tag:        "%s-wolfi-gpu",
		Image:      DistroWolfi,
		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
}

// Publish all engine images to a registry
func (e *DaggerEngine) Publish(
	ctx context.Context,

	// Image target to push to
	// +default="ghcr.io/dagger/engine"
	image string,
	// List of tags to use
	tag []string,

	// +optional
	dryRun bool,

	// +optional
	registryUsername *string,
	// +optional
	registryPassword *dagger.Secret,
) error {
	// collect all the targets that we are trying to build together, along with
	// where they need to go to
	targetResults := make([]struct {
		Platforms []*dagger.Container
		Tags      []string
	}, len(targets))
	eg := errgroup.Group{}
	for i, target := range targets {
		// determine the target tags
		for _, tag := range tag {
			targetResults[i].Tags = append(targetResults[i].Tags, fmt.Sprintf(target.Tag, tag))
		}

		// build all the target platforms
		targetResults[i].Platforms = make([]*dagger.Container, len(target.Platforms))
		for j, platform := range target.Platforms {
			egCtx, span := Tracer().Start(ctx, fmt.Sprintf("building %s [%s]", target.Name, platform))
			eg.Go(func() (rerr error) {
				defer func() {
					if rerr != nil {
						span.SetStatus(codes.Error, rerr.Error())
					}
					span.End()
				}()

				ctr, err := e.Container(egCtx, platform, target.Image, target.GPUSupport)
				if err != nil {
					return err
				}
				ctr, err = ctr.Sync(egCtx)
				if err != nil {
					return err
				}

				targetResults[i].Platforms[j] = ctr
				return nil
			})
		}
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	// push all the targets
	ctr := dag.Container()
	if registryUsername != nil && registryPassword != nil {
		registry, _, _ := strings.Cut(image, "/")
		ctr = ctr.WithRegistryAuth(registry, *registryUsername, registryPassword)
	}
	for i, target := range targets {
		result := targetResults[i]

		if err := func() (rerr error) {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("pushing %s", target.Name))
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()

			for _, tag := range result.Tags {
				_, err := ctr.
					Publish(ctx, fmt.Sprintf("%s:%s", image, tag), dagger.ContainerPublishOpts{
						PlatformVariants:  result.Platforms,
						ForcedCompression: dagger.ImageLayerCompressionGzip, // use gzip to avoid incompatibility w/ older docker versions
					})
				if err != nil {
					return err
				}
			}
			return nil
		}(); err != nil {
			return err
		}
	}

	return nil
}
