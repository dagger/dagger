package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/util/parallel"

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
	// +optional
	version string,
	// +optional
	tag string,
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

	builder, err := build.NewBuilder(ctx, e.Source, version, tag)
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
			cacheVolumeName = "dagger-dev-engine-state-" + rand.Text()
		}
		if name != "" {
			cacheVolumeName += "-" + name
		}
	}

	devEngine, err := e.Container(ctx, "", image, gpuSupport, "", "")
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
func (e *DaggerEngine) Generate(_ context.Context) (*dagger.Changeset, error) {
	withGoGenerate := dag.Go(e.Source).Env().
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogo@v1.3.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogofaster@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"}).
		WithMountedDirectory("./github.com/gogo/googleapis", dag.Git("https://github.com/gogo/googleapis.git").Tag("v1.4.1").Tree()).
		WithMountedDirectory("./github.com/gogo/protobuf", dag.Git("https://github.com/gogo/protobuf.git").Tag("v1.3.2").Tree()).
		WithMountedDirectory("./github.com/tonistiigi/fsutil", dag.Git("https://github.com/tonistiigi/fsutil.git").Commit("069baf6a66f5c63a82fb679ff2319ed2ee970fbd").Tree()).
		WithExec([]string{"go", "generate", "-v", "./..."}).
		Directory(".")
	changes := changes(e.Source, withGoGenerate, []string{"github.com"})
	return changes, nil
}

// Return the changes between two directory, excluding the specified path patterns from the comparison
// FIXME: had to copy-paste across modules
func changes(before, after *dagger.Directory, exclude []string) *dagger.Changeset {
	if exclude == nil {
		return after.Changes(before)
	}
	return after.
		// 1. Remove matching files from after
		Filter(dagger.DirectoryFilterOpts{Exclude: exclude}).
		// 2. Copy matching files from before
		WithDirectory("", before.Filter(dagger.DirectoryFilterOpts{Include: exclude})).
		Changes(before)
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

type targetResult struct {
	Platforms []*dagger.Container
	Tags      []string
}

func (e *DaggerEngine) CheckReleaseDryRun(ctx context.Context) error {
	return e.Publish(
		ctx,
		"dagger-engine.dev", // image
		// FIXME: why not from HEAD like the SDKs?
		[]string{"main"}, // tag
		true,             // dryRun
		nil,              // registryUsername
		nil,              // registryPassword
	)
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
	targetResults, err := e.buildTargets(ctx, tag)
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	return e.pushTargets(ctx, targetResults, image, registryUsername, registryPassword)
}

func (e *DaggerEngine) buildTargets(ctx context.Context, tags []string) ([]targetResult, error) {
	targetResults := make([]targetResult, len(targets))
	jobs := parallel.New()
	for i, target := range targets {
		// determine the target tags
		for _, tag := range tags {
			targetResults[i].Tags = append(targetResults[i].Tags, fmt.Sprintf(target.Tag, tag))
		}
		// build all the target platforms
		targetResults[i].Platforms = make([]*dagger.Container, len(target.Platforms))
		for j, platform := range target.Platforms {
			jobs = jobs.WithJob(fmt.Sprintf("build %s for %s", target.Name, platform),
				func(ctx context.Context) error {
					ctr, err := e.Container(ctx, platform, target.Image, target.GPUSupport, "", "")
					if err != nil {
						return err
					}
					ctr, err = ctr.Sync(ctx)
					if err != nil {
						return err
					}
					targetResults[i].Platforms[j] = ctr
					return nil
				},
			)
		}
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return targetResults, nil
}

func (e *DaggerEngine) pushTargets(
	ctx context.Context,
	targetResults []targetResult,
	image string,
	registryUsername *string,
	registryPassword *dagger.Secret,
) error {
	ctr := dag.Container()
	if registryUsername != nil && registryPassword != nil {
		registry, _, _ := strings.Cut(image, "/")
		ctr = ctr.WithRegistryAuth(registry, *registryUsername, registryPassword)
	}
	jobs := parallel.New()
	for i, target := range targets {
		result := targetResults[i]
		jobs = jobs.WithJob(fmt.Sprintf("push target %s", target.Name),
			func(ctx context.Context) error {
				for _, tag := range result.Tags {
					if _, err := ctr.Publish(ctx, image+":"+tag, dagger.ContainerPublishOpts{
						PlatformVariants: result.Platforms,
						// use gzip to avoid incompatibility w/ older docker versions
						ForcedCompression: dagger.ImageLayerCompressionGzip,
					}); err != nil {
						return err
					}
				}
				return nil
			})
	}
	return jobs.Run(ctx)
}
