package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/util/parallel"

	"dagger/engine-dev/build"

	"dagger/engine-dev/internal/dagger"
)

type Distro string

const (
	DistroAlpine Distro = "alpine"
	DistroWolfi  Distro = "wolfi"
	DistroUbuntu Distro = "ubuntu"
)

func New(
	// +defaultPath="/"
	// +ignore[
	// "*",
	// "!**/go.*",
	// "!version",
	// "!core",
	// "!engine",
	// "!util",
	// "!network",
	// "!dagql",
	// "!analytics",
	// "!auth",
	// "!cmd",
	// "!internal"
	// ]
	source *dagger.Directory,
	// A configurable part of the IP subnet managed by the engine
	// Change this to allow nested dagger engines
	// +default=89
	subnetNumber int,
	// A docker config file with credentials to install on clients,
	// to ensure they can access private registries
	// +optional
	clientDockerConfig *dagger.Secret,
) *EngineDev {
	return &EngineDev{
		Source:             source,
		SubnetNumber:       subnetNumber,
		ClientDockerConfig: clientDockerConfig,
	}
}

type EngineDev struct {
	Source *dagger.Directory

	BuildkitConfig []string // +private
	LogLevel       string   // +private
	SubnetNumber   int      // +private

	Race               bool // +private
	ClientDockerConfig *dagger.Secret
}

func (e *EngineDev) NetworkCidr() string {
	return fmt.Sprintf("10.%d.0.0/16", e.SubnetNumber)
}

func (e *EngineDev) IncrementSubnet() *EngineDev {
	e.SubnetNumber++
	return e
}

func (e *EngineDev) WithBuildkitConfig(key, value string) *EngineDev {
	e.BuildkitConfig = append(e.BuildkitConfig, key+"="+value)
	return e
}

func (e *EngineDev) WithRace() *EngineDev {
	e.Race = true
	return e
}

func (e *EngineDev) WithLogLevel(level string) *EngineDev {
	e.LogLevel = level
	return e
}

// Build an ephemeral environment with the Dagger CLI and engine built from source, installed and ready to use
func (e *EngineDev) Playground(
	ctx context.Context,
	// Build from a custom base image
	// +optional
	base *dagger.Container,
	// Enable experimental GPU support
	// +optional
	gpuSupport bool,
	// Share cache globally
	// +optional
	sharedCache bool,
	// +optional
	metrics bool,
) (*dagger.Container, error) {
	ctr := base
	if ctr == nil {
		ctr = dag.Alpine().Container().WithEnvVariable("HOME", "/root")
	}
	ctr = ctr.WithWorkdir("$HOME", dagger.ContainerWithWorkdirOpts{Expand: true})
	svc, err := e.Service(
		ctx,
		"",       // name
		"alpine", // distro
		gpuSupport,
		sharedCache,
		metrics,
	)
	if err != nil {
		return nil, err
	}
	return e.InstallClient(ctx, ctr, svc)
}

// Build the engine container
func (e *EngineDev) Container(
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
func (e *EngineDev) Service(
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
	// Support 256 layers of nested dagger engines :-P
	e = e.IncrementSubnet()
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
			"--network-cidr", e.NetworkCidr(),
		},
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	}), nil
}

// Configure the given client container so that it can connect to the given engine service
func (e *EngineDev) InstallClient(
	ctx context.Context,
	// The client container to configure
	client *dagger.Container,
	// The engine service to bind
	// +optional
	service *dagger.Service,
) (*dagger.Container, error) {
	if service == nil {
		var err error
		service, err = e.Service(
			ctx,
			"",           // name
			DistroAlpine, // distro
			false,        // gpuSupport
			false,        // sharedCache
			false,        // metrics
		)
		if err != nil {
			return nil, err
		}
	}
	cliPath := "/.dagger-cli"
	endpoint, err := service.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	client = client.
		WithServiceBinding("dagger-engine", service).
		// FIXME: retrieve endpoint dynamically?
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliPath, dag.DaggerCli().Binary()).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliPath).
		WithExec([]string{"ln", "-s", cliPath, "/usr/local/bin/dagger"})
	if cfg := e.ClientDockerConfig; cfg != nil {
		client = client.WithMountedSecret(
			"${HOME}/.docker/config.json",
			cfg,
			dagger.ContainerWithMountedSecretOpts{Expand: true},
		)
	}
	return client, nil
}

// Introspect the engine API schema, and return it as a json-encoded file.
// This file is used by SDKs to generate clients.
func (e *EngineDev) IntrospectionJSON(ctx context.Context) (*dagger.File, error) {
	playground, err := e.Playground(ctx, nil, false, false, false)
	if err != nil {
		return nil, err
	}
	introspectionJSON := playground.
		WithFile("/usr/local/bin/codegen", dag.Codegen().Binary()).
		WithExec([]string{"codegen", "introspect", "-o", "/schema.json"}).
		File("/schema.json")
	return introspectionJSON, nil
}

// Introspect the engine API schema, and return it as a graphql schema
func (e *EngineDev) GraphqlSchema(
	ctx context.Context,
	// +optional
	version string,
) (*dagger.File, error) {
	playground, err := e.Playground(ctx, nil, false, false, false)
	if err != nil {
		return nil, err
	}
	schemaPath := "schema.graphqls"
	schema := playground.
		WithFile("/usr/local/bin/introspect", e.IntrospectionTool()).
		WithExec(
			[]string{"introspect", "--version=" + version, "schema"},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaPath},
		).
		File(schemaPath)
	return schema, nil
}

// Build the `introspect` tool which introspects the engine API
func (e *EngineDev) IntrospectionTool() *dagger.File {
	return dag.
		Go(dagger.GoOpts{Source: e.Source}).
		Binary("./cmd/introspect")
}

// Generate the json schema for a dagger config file
// Currently supported: "dagger.json", "engine.json"
func (e *EngineDev) ConfigSchema(filename string) *dagger.File {
	schemaFilename := strings.TrimSuffix(filename, ".json") + ".schema.json"
	// This tool has runtime dependencies on the engine source code itself
	return dag.Go(dagger.GoOpts{Source: e.Source}).
		Env().
		WithExec(
			[]string{"go", "run", "./cmd/json-schema", filename},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaFilename},
		).
		File(schemaFilename)
}

// Generate any engine-related files
// Note: this is codegen of the 'go generate' variety, not 'dagger develop'
func (e *EngineDev) Generate(_ context.Context) (*dagger.Changeset, error) {
	withGoGenerate := dag.Go(dagger.GoOpts{Source: e.Source}).Env().
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogo@v1.3.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogofaster@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"}).
		WithMountedDirectory("./github.com/gogo/googleapis", dag.Git("https://github.com/gogo/googleapis.git").Tag("v1.4.1").Tree()).
		WithMountedDirectory("./github.com/gogo/protobuf", dag.Git("https://github.com/gogo/protobuf.git").Tag("v1.3.2").Tree()).
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

// +check
func (e *EngineDev) ReleaseDryRun(ctx context.Context) error {
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
// +cache="session"
func (e *EngineDev) Publish(
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

func (e *EngineDev) buildTargets(ctx context.Context, tags []string) ([]targetResult, error) {
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

func (e *EngineDev) pushTargets(
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
