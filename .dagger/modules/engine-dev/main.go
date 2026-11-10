package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/util/parallel"
	"golang.org/x/mod/semver"

	"dagger/engine-dev/build"
	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
	clidev "dagger/engine-dev/internal/dagger/clients/cli-dev"
	"dagger/engine-dev/internal/dagger/clients/codegen"
	gomod "dagger/engine-dev/internal/dagger/clients/go"
	"dagger/engine-dev/internal/dagger/clients/wolfi"
)

func New(
	ctx context.Context,
	ws *dagger.Workspace,
	// A configurable part of the IP subnet managed by the engine
	// Change this to allow nested dagger engines
	// +default=89
	subnetNumber int,
	// A docker config file with credentials to install on clients,
	// to ensure they can access private registries
	// +optional
	clientDockerConfig *dagger.Secret,
) *EngineDev {
	commit, dirty := vcsInfo(ctx, ws)
	return &EngineDev{
		Ws: ws,
		Source: ws.Directory("/", dagger.WorkspaceDirectoryOpts{
			// No .git: tests run their clients inside this source, and a .git
			// there would make every client a workspace client sharing one
			// dagger.lock. CI's remote workspace has no .git either. Built
			// binaries get their VCS info from ws instead; see vcsInfo.
			Exclude: []string{
				"*",
				"!dagger.json",
				"!**/dagger.json",
				"!dagger.toml",
				"!**/dagger.toml",
				"!**/go.*",
				"!**/*.dang",
				"!core",
				"!engine",
				"!util",
				"!network",
				"!dagql",
				"!analytics",
				"!auth",
				"!cmd",
				"!internal",
				"!sdk",
				"sdk/**/examples",
				"!cmd",
				"!modules",
				"!.dagger",
				"!.changes",
			},
		}),
		VCSCommit:          commit,
		VCSDirty:           dirty,
		SubnetNumber:       subnetNumber,
		ClientDockerConfig: clientDockerConfig,
	}
}

// vcsInfo resolves the git HEAD commit and dirty state from the workspace for
// stamping into built binaries. Errors are swallowed — a build proceeds with
// whatever we collected (possibly nothing). Only the resolved scalars are
// threaded onward; the Workspace itself is never stored or passed into a
// build, which would make every affected cache key session-specific.
func vcsInfo(ctx context.Context, ws *dagger.Workspace) (commit string, dirty bool) {
	if ws == nil {
		return "", false
	}
	git := ws.Git()
	commit, err := git.Head().CommitSHA(ctx)
	if err != nil {
		return "", false
	}
	if clean, err := git.Uncommitted().IsEmpty(ctx); err == nil {
		dirty = !clean
	}
	return commit, dirty
}

// Creates a complete end-to-end build environment with CLI and engine for interactive testing
type EngineDev struct {
	Source *dagger.Directory

	// Resolved VCS info stamped into built engine/CLI binaries. Stored as
	// scalars (not the source Workspace) so EngineDev's methods stay
	// content-addressed and their build results survive an engine restart.
	VCSCommit string // +private
	VCSDirty  bool   // +private

	EngineConfig []string // +private
	LogLevel     string   // +private
	SubnetNumber int      // +private
	EBPFProgs    []string // +private

	Race               bool // +private
	ImageEntries       bool // +private
	ClientDockerConfig *dagger.Secret

	Ws *dagger.Workspace // +private
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

func (dev *EngineDev) WithEngineConfig(key, value string) *EngineDev {
	dev.EngineConfig = append(dev.EngineConfig, key+"="+value)
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

// Build the engine with the cache entries its image ships, as a release
// does. Making them starts a separate engine of the build and costs minutes,
// while an engine that serves many clients, such as a test run's, builds the
// same results once for its first client, and one that loads no Go module
// never needs them; so an engine built for anything else goes without.
func (dev *EngineDev) WithImageEntries() *EngineDev {
	dev.ImageEntries = true
	return dev
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
) (*dagger.Container, error) {
	builder, err := build.NewBuilder(ctx, dev.Source, version, dev.VCSCommit, dev.VCSDirty, dev.Ws)
	if err != nil {
		return nil, err
	}
	builder = builder.WithRace(dev.Race)
	// The image's entries are made by an engine of this build that runs
	// here, for the image's platform: an engine runs its own platform's
	// binaries, and every build the entries hold compiles for any platform.
	native := builder
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}
	if gpuSupport {
		builder = builder.WithGPUSupport()
	}
	ctr, err := dev.engine(ctx, builder, platform, version)
	if err != nil {
		return nil, err
	}
	if !dev.ImageEntries {
		return ctr, nil
	}
	exporter := ctr
	if builder.Platform() != native.Platform() || gpuSupport {
		if exporter, err = dev.engine(ctx, native, "", version); err != nil {
			return nil, err
		}
	}
	entries, err := dev.imageEntries(ctx, native, exporter, builder.Platform())
	if err != nil {
		return nil, fmt.Errorf("engine image entries: %w", err)
	}
	return ctr.WithDirectory(distconsts.EngineContainerImageEntriesDir, entries), nil
}

// engine is the engine container builder builds, without the entries its
// image ships.
func (dev *EngineDev) engine(ctx context.Context, builder *build.Builder, platform dagger.Platform, version string) (*dagger.Container, error) {
	cfg, err := generateConfig(dev.LogLevel)
	if err != nil {
		return nil, err
	}
	engineTOML, err := generateEngineTOML(dev.EngineConfig)
	if err != nil {
		return nil, err
	}
	entrypoint, err := generateEntrypoint()
	if err != nil {
		return nil, err
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
		WithFile(engineTOMLPath, engineTOML).
		WithFile(engineEntrypointPath, entrypoint).
		WithEntrypoint([]string{filepath.Base(engineEntrypointPath)})

	cli := clidev.New(clidev.CliDevOpts{Source: dev.cliDevSource(), Version: version, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).Binary(clidev.CliDevBinaryOpts{
		Platform: platform,
	})
	ctr = ctr.
		WithFile(cliPath, cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", distconsts.DefaultEngineSockAddr)

	// ctr = ctr.WithEnvVariable("BUILDKIT_SCHEDULER_DEBUG", "1")

	return ctr, nil
}

// goSDK is go-sdk's tree at the commit this workspace's module go-sdk pins
// (dagger.toml): the go-sdk this repository's own Go modules are generated
// with.
func (dev *EngineDev) goSDK(ctx context.Context) (*dagger.Directory, error) {
	source, err := dev.Ws.Module("go-sdk").Source(ctx)
	if err != nil {
		return nil, fmt.Errorf("the workspace's go-sdk: %w", err)
	}
	return core.NewModuleSource(source).ContextDirectory(), nil
}

// imageEntries are the cache entries an engine image of platform ships for
// Go module runtimes, which the engine merges at start: the builds of the
// standard library and of what generated Go module code imports, the
// built-in Go SDK's (GoImageEntriesSeed) and go-sdk's
// (GoSDKImageEntriesSeed), gobuild's binary, which dagger/go builds every
// runtime with, and what a fresh engine would fetch to build with them
// (dagger/go's tree and the two images the builds run in), made by engine,
// an engine of the image's build that runs here, on a state of its own, and
// exported by cmd/engine-image-entries. An entry's identity is its engine
// build's, so an image's entries come from an engine of its own build; and
// dagger/go declares each build as made for the platform it compiles for
// (Directory.withPlatform), so an engine of the image's platform finds the
// builds an engine of another platform made for it. builder builds what
// runs here.
func (dev *EngineDev) imageEntries(ctx context.Context, builder *build.Builder, engine *dagger.Container, platform string) (*dagger.Directory, error) {
	// The state is the engine build's own, for the image's platform, named by
	// its binary's content and the platform: making the same image again
	// reuses its state and its cached entries, so the image is the same each
	// time, and another build never starts on it. Neither does another
	// platform's image: a result two platforms' builds both make, such as a
	// module download, is the same result, and the entries would ship the
	// other platform's call of it, which this image's engine never makes.
	client, exporter, err := dev.entriesExporter(ctx, builder, engine, platform, "dagger-image-entries")
	if err != nil {
		return nil, err
	}
	const out = "/entries"
	ran := client.WithExec(withoutOuterSession(append(exporter, "-out", out)...), dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	if err := entriesExported(ctx, ran, exporter); err != nil {
		return nil, err
	}
	return ran.Directory(out), nil
}

// entriesExporter is a client of a new engine service of engine's build, on
// a state named state, the engine binary's content and platform, with what
// cmd/engine-image-entries needs to make an image of platform's entries, and
// the command that makes them, but for its -out. builder builds what runs
// here.
func (dev *EngineDev) entriesExporter(ctx context.Context, builder *build.Builder, engine *dagger.Container, platform, state string) (*dagger.Container, []string, error) {
	seed, err := builder.GoImageEntriesSeed(ctx)
	if err != nil {
		return nil, nil, err
	}
	goSDK, err := dev.goSDK(ctx)
	if err != nil {
		return nil, nil, err
	}
	goSDKVersions, err := build.ReadGoSDKVersions(ctx, goSDK)
	if err != nil {
		return nil, nil, err
	}
	seedGoSDK, err := builder.GoSDKImageEntriesSeed(ctx, goSDK, goSDKVersions.DefaultTarget)
	if err != nil {
		return nil, nil, err
	}
	engineDigest, err := engine.File(consts.EngineServerPath).Digest(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("the engine binary: %w", err)
	}
	// Its network is the one after this engine-dev's, without changing
	// dev's: every image's entries are made at once, and an engine that took
	// another increment could land on a network an engine above it uses.
	nested := *dev
	nested.IncrementSubnet()
	service := engine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, core.NewCacheVolume(state+"-"+engineDigest+"-"+strings.ReplaceAll(platform, "/", "-")), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeLocked,
		}).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"--addr", "tcp://0.0.0.0:1234",
				// Its bridge is the name and "0": at most 15 characters.
				"--network-name", "dagger-seed",
				"--network-cidr", nested.NetworkCidr(),
			},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})
	client, err := dev.InstallClient(ctx, wolfi.New().Container(), service)
	if err != nil {
		return nil, nil, err
	}
	client = client.
		WithMountedFile("/usr/local/bin/engine-image-entries", builder.ImageEntriesBinary()).
		WithMountedDirectory("/seed", seed).
		WithMountedDirectory("/seed-go-sdk", seedGoSDK).
		WithMountedDirectory("/builtin-go-sdk", dev.Source.Directory("sdk/go/runtime"))
	return client, []string{"engine-image-entries",
		"-seed", "/seed",
		"-entrypoint-seed", "/seed-go-sdk",
		"-builtin-go-sdk", "/builtin-go-sdk",
		"-entrypoint-dagger-go", goSDKVersions.DaggerGo,
		"-go-version", distconsts.GolangVersion,
		"-platform", platform,
	}, nil
}

// entriesExported is nil when ran, an exec that makes an image's entries
// with exporter, exited 0, and otherwise says why it didn't, such as dagger/go
// unreachable at the Go SDK module's dependency, rather than only its exit code.
func entriesExported(ctx context.Context, ran *dagger.Container, exporter []string) error {
	exitCode, err := ran.ExitCode(ctx)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		output, err := ran.CombinedOutput(ctx)
		if err != nil {
			return err
		}
		return fmt.Errorf("make the engine image's entries, with %s (exit code %d):\n%s", strings.Join(exporter, " "), exitCode, tailLines(output, 40))
	}
	return nil
}

// The entries an engine image ships can be made again on the state an
// earlier making left after a prune cut part of it, as a later release's
// making does: an engine of this build makes them, prunes its cache to half
// its size, least recently used first, and makes them again.
// +check
func (dev *EngineDev) ImageEntriesAfterPrune(ctx context.Context) error {
	builder, err := build.NewBuilder(ctx, dev.Source, "", dev.VCSCommit, dev.VCSDirty, dev.Ws)
	if err != nil {
		return err
	}
	builder = builder.WithRace(dev.Race)
	engine, err := dev.engine(ctx, builder, "", "")
	if err != nil {
		return err
	}
	// A state of its own, which no image's entries are made on.
	client, exporter, err := dev.entriesExporter(ctx, builder, engine, builder.Platform(), "dagger-image-entries-after-prune")
	if err != nil {
		return err
	}
	ran := client.WithExec(withoutOuterSession(append([]string{"sh", "-c", `set -e
"$@" -out /entries-before
size=$(echo '{ engine { localCache { entrySet { diskSpaceBytes } } } }' | dagger query | grep -o '"diskSpaceBytes": *[0-9]*' | grep -o '[0-9]*$')
half=$((size / 2 / 1048576))MB
echo "pruning the cache of $size bytes to $half"
echo "{ engine { localCache { prune(maxUsedSpace: \"$half\", targetSpace: \"$half\") } } }" | dagger query
"$@" -out /entries
`, "sh"}, exporter...)...), dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	return entriesExported(ctx, ran, exporter)
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
		cacheVolumeName = "dagger-dev-engine-state-" + rand.Text()
		if name != "" {
			cacheVolumeName += "-" + name
		}
	}

	devEngine, err := dev.Container(ctx, "", gpuSupport, "")
	if err != nil {
		return nil, err
	}

	devEngine = devEngine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, core.NewCacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
			// Only one engine can safely use a state dir at a time. LOCKED keeps the
			// cache identity stable while serializing concurrent users.
			Sharing: dagger.CacheSharingModeLocked,
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
	// +optional
	client *dagger.Container,
	// The engine service to bind
	// +optional
	service *dagger.Service,
) (*dagger.Container, error) {
	if client == nil {
		// By default, start from a simple base container
		client = wolfi.New().Container()
	}
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
		WithMountedFile(cliPath, clidev.New(clidev.CliDevOpts{Source: dev.cliDevSource(), VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).Binary()).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliPath).
		WithSymlink(cliPath, "/usr/local/bin/dagger")
	if cfg := dev.ClientDockerConfig; cfg != nil {
		client = client.WithMountedSecret(
			"${HOME}/.docker/config.json",
			cfg,
			dagger.ContainerWithMountedSecretOpts{Expand: true},
		)
	}
	return client, nil
}

// withoutOuterSession wraps a command whose Dagger client must reach the dev
// engine that InstallClient configures. On the v1.0 API, execs get a session
// to the engine running them (DAGGER_SESSION_PORT/TOKEN), and Dagger clients
// prefer that session over _EXPERIMENTAL_DAGGER_RUNNER_HOST. The engine sets
// those variables when the process starts, so WithoutEnvVariable cannot
// remove them; unset them at the process boundary instead. This works
// whether or not the outer engine nests by default.
func withoutOuterSession(args ...string) []string {
	return append([]string{"env", "-u", "DAGGER_SESSION_PORT", "-u", "DAGGER_SESSION_TOKEN"}, args...)
}

// Introspect the engine API schema, and return it as a json-encoded file.
// This file is used by SDKs to generate clients.
func (dev *EngineDev) IntrospectionJSON(ctx context.Context) (*dagger.File, error) {
	ctr, err := dev.InstallClient(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	introspectionJSON := ctr.
		WithFile("/usr/local/bin/codegen", codegen.New(dev.Ws).Binary()).
		WithExec(withoutOuterSession("codegen", "introspect", "-o", "/schema.json")).
		File("/schema.json")
	return introspectionJSON, nil
}

// Introspect the engine API schema, and return it as a graphql schema
func (dev *EngineDev) GraphqlSchema(
	ctx context.Context,
	// +optional
	version string,
) (*dagger.File, error) {
	ctr, err := dev.InstallClient(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	schemaPath := "schema.graphqls"
	schema := ctr.
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
	return gomod.New(gomod.GoOpts{Source: dev.Source, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).
		Binary("./cmd/introspect")
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
	// The released image ships its entries.
	targetResults, err := dev.WithImageEntries().buildTargets(ctx, tag)
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	return dev.pushTargets(ctx, targetResults, image, registryUsername, registryPassword)
}

func (dev *EngineDev) buildTargets(ctx context.Context, tags []string) ([]targetResult, error) {
	releaseVersion := releaseVersionFromTags(tags)
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
					ctr, err := dev.Container(ctx, platform, target.GPUSupport, releaseVersion)
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

func releaseVersionFromTags(tags []string) string {
	for _, tag := range tags {
		if semver.IsValid(tag) {
			return tag
		}
	}
	return ""
}

func (dev *EngineDev) pushTargets(
	ctx context.Context,
	targetResults []targetResult,
	image string,
	registryUsername *string,
	registryPassword *dagger.Secret,
) error {
	ctr := core.NewContainer()
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
