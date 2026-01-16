// Creates a complete end-to-end build environment with CLI and engine for interactive testing
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

func New(
	// +defaultPath="/"
	// +ignore=[
	// "*",
	// "!.git",
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
	// "!internal",
	// "!sdk",
	// "sdk/**/examples",
	// "!cmd",
	// "!modules/wolfi",
	// "!modules/alpine"
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
	EBPFProgs      []string // +private

	Race               bool // +private
	ClientDockerConfig *dagger.Secret
}

func (dev *EngineDev) NetworkCidr() string {
	return fmt.Sprintf("10.%d.0.0/16", dev.SubnetNumber)
}

func (dev *EngineDev) IncrementSubnet() *EngineDev {
	dev.SubnetNumber++
	return dev
}

func (dev *EngineDev) WithEBPFProgs(names []string) *EngineDev {
	dev.EBPFProgs = append(dev.EBPFProgs, names...)
	return dev
}

func (dev *EngineDev) WithBuildkitConfig(key, value string) *EngineDev {
	dev.BuildkitConfig = append(dev.BuildkitConfig, key+"="+value)
	return dev
}

func (dev *EngineDev) WithRace() *EngineDev {
	dev.Race = true
	return dev
}

func (dev *EngineDev) WithLogLevel(level string) *EngineDev {
	dev.LogLevel = level
	return dev
}

func (dev *EngineDev) sourceWithEbpfObjects() *dagger.Directory {
	return dev.Source.With(build.EbpfGenerate)
}

// Build an ephemeral environment with the Dagger CLI and engine built from source, installed and ready to use
func (dev *EngineDev) Playground(
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
	svc, err := dev.Service(
		ctx,
		"", // name
		gpuSupport,
		sharedCache,
		metrics,
	)
	if err != nil {
		return nil, err
	}
	return dev.InstallClient(ctx, ctr, svc)
}

// Build the engine container
func (dev *EngineDev) Container(
	ctx context.Context,

	// +optional
	platform dagger.Platform,
	// +optional
	gpuSupport bool,
	// +optional
	version string,
	// +optional
	tag string,
) (*dagger.Container, error) {
	cfg, err := generateConfig(dev.LogLevel)
	if err != nil {
		return nil, err
	}
	bkcfg, err := generateBKConfig(dev.BuildkitConfig)
	if err != nil {
		return nil, err
	}
	entrypoint, err := generateEntrypoint()
	if err != nil {
		return nil, err
	}

	builder, err := build.NewBuilder(ctx, dev.Source, version, tag)
	if err != nil {
		return nil, err
	}
	builder = builder.WithRace(dev.Race)
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}

	if gpuSupport {
		builder = builder.WithGPUSupport()
	}

	ctr, err := builder.Engine(ctx)
	if err != nil {
		return nil, err
	}
	for _, prog := range dev.EBPFProgs {
		ctr = ctr.WithEnvVariable("DAGGER_EBPF_PROG_"+strings.ToUpper(prog), "y")
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
func (dev *EngineDev) Service(
	ctx context.Context,
	name string,
	// +optional
	gpuSupport bool,
	// +optional
	sharedCache bool,
	// +optional
	metrics bool,
) (*dagger.Service, error) {
	// Support 256 layers of nested dagger engines :-P
	dev = dev.IncrementSubnet()
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

	devEngine, err := dev.Container(ctx, "", gpuSupport, "", "")
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
			"--network-cidr", dev.NetworkCidr(),
		},
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	}), nil
}

// Configure the given client container so that it can connect to the given engine service
func (dev *EngineDev) InstallClient(
	ctx context.Context,
	// The client container to configure
	client *dagger.Container,
	// The engine service to bind
	// +optional
	service *dagger.Service,
) (*dagger.Container, error) {
	if service == nil {
		var err error
		service, err = dev.Service(
			ctx,
			"",    // name
			false, // gpuSupport
			false, // sharedCache
			false, // metrics
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
	if cfg := dev.ClientDockerConfig; cfg != nil {
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
func (dev *EngineDev) IntrospectionJSON(ctx context.Context) (*dagger.File, error) {
	playground, err := dev.Playground(ctx, nil, false, false, false)
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
func (dev *EngineDev) GraphqlSchema(
	ctx context.Context,
	// +optional
	version string,
) (*dagger.File, error) {
	playground, err := dev.Playground(ctx, nil, false, false, false)
	if err != nil {
		return nil, err
	}
	schemaPath := "schema.graphqls"
	schema := playground.
		WithFile("/usr/local/bin/introspect", dev.IntrospectionTool()).
		WithExec(
			[]string{"introspect", "--version=" + version, "schema"},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaPath},
		).
		File(schemaPath)
	return schema, nil
}

// Build the `introspect` tool which introspects the engine API
func (dev *EngineDev) IntrospectionTool() *dagger.File {
	return dag.
		Go(dagger.GoOpts{Source: dev.Source}).
		Binary("./cmd/introspect")
}

// Generate the json schema for a dagger config file
// Currently supported: "dagger.json", "engine.json"
func (dev *EngineDev) ConfigSchema(filename string) *dagger.File {
	schemaFilename := strings.TrimSuffix(filename, ".json") + ".schema.json"
	// This tool has runtime dependencies on the engine source code itself
	return dag.Go(dagger.GoOpts{Source: dev.Source}).
		Env().
		WithExec(
			[]string{"go", "run", "./cmd/json-schema", filename},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaFilename},
		).
		File(schemaFilename)
}

// Generate any engine-related files
// Note: this is codegen of the 'go generate' variety, not 'dagger develop'
func (dev *EngineDev) Generate(ctx context.Context) (*dagger.Changeset, error) {
	// ebpf object files are actually expected to only be generated during a build, not
	// committed, so we remove stubs and real ones before+after go generate
	base := dev.Source
	ebpfObjectFiles, err := base.Glob(ctx, "**/*_bpfel.o")
	if err != nil {
		return nil, err
	}
	if len(ebpfObjectFiles) > 0 {
		base = base.WithoutFiles(ebpfObjectFiles)
	}

	withGoGenerate := dag.Go(dagger.GoOpts{
		Source: dev.Source,
		ExtraPackages: []string{
			"clang",
			"lld",
			"libbpf-dev",
		},
	}).Env().
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogo@v1.3.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogofaster@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"}).
		WithMountedDirectory("./github.com/gogo/googleapis", dag.Git("https://github.com/gogo/googleapis.git").Tag("v1.4.1").Tree()).
		WithMountedDirectory("./github.com/gogo/protobuf", dag.Git("https://github.com/gogo/protobuf.git").Tag("v1.3.2").Tree()).
		WithExec([]string{"go", "generate", "-v", "./..."}).
		WithExec([]string{"find", "engine/ebpf", "-name", "*_bpfel.o", "-delete"}).
		Directory(".")
	changes := changes(base, withGoGenerate, []string{"github.com"})
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
	Platforms  []dagger.Platform
	GPUSupport bool
}{
	{
		Name:      "wolfi (default)",
		Tag:       "%s",
		Platforms: []dagger.Platform{"linux/amd64", "linux/arm64"},
	},
	{
		Name:       "wolfi with nvidia variant",
		Tag:        "%s-gpu",
		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
}

type targetResult struct {
	Platforms []*dagger.Container
	Tags      []string
}

// +check
func (dev *EngineDev) ReleaseDryRun(ctx context.Context) error {
	return dev.Publish(
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
func (dev *EngineDev) Publish(
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
	targetResults, err := dev.buildTargets(ctx, tag)
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	return dev.pushTargets(ctx, targetResults, image, registryUsername, registryPassword)
}

func (dev *EngineDev) buildTargets(ctx context.Context, tags []string) ([]targetResult, error) {
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
					ctr, err := dev.Container(ctx, platform, target.GPUSupport, "", "")
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

func (dev *EngineDev) pushTargets(
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
