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
	"golang.org/x/mod/semver"

	"dagger/engine-dev/build"
	"dagger/engine-dev/internal/dagger"
)

const defaultVCSRepository = "https://github.com/dagger/dagger"

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
	// Credential-free HTTPS repository that owns the source revision.
	// +default="https://github.com/dagger/dagger"
	vcsRepository string,
) *EngineDev {
	commit, dirty := vcsInfo(ctx, ws)
	if vcsRepository == "" {
		vcsRepository = defaultVCSRepository
	}
	return &EngineDev{
		Ws: ws,
		Source: ws.Directory("/", dagger.WorkspaceDirectoryOpts{
			Exclude: []string{
				"*",
				"!.git",
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
				"!LICENSE",
				"!modules",
				"!toolchains",
				"!.changes",
			},
		}),
		VCSCommit:          commit,
		VCSDirty:           dirty,
		VCSRepository:      canonicalEngineRepository(vcsRepository),
		SubnetNumber:       subnetNumber,
		ClientDockerConfig: clientDockerConfig,
	}
}

// vcsInfo resolves the git HEAD commit and dirty state from the workspace for
// stamping into built binaries. Errors are swallowed — a build proceeds with
// whatever we collected (possibly nothing). Only the resolved scalars are
// threaded onward; the Workspace itself is never stored or passed into a
// build, which would taint the cache key of everything it touches.
func vcsInfo(ctx context.Context, ws *dagger.Workspace) (commit string, dirty bool) {
	if ws == nil {
		return "", false
	}
	git := ws.Git()
	commit, err := git.Head().Commit(ctx)
	if err != nil {
		return "", false
	}
	if clean, err := git.Uncommitted().IsEmpty(ctx); err == nil {
		dirty = !clean
	}
	return commit, dirty
}

type EngineDev struct {
	Source *dagger.Directory

	// Resolved VCS info stamped into built engine/CLI binaries. Stored as
	// scalars (not the source Workspace) so EngineDev's methods stay
	// content-addressed and their build results survive an engine restart.
	VCSCommit     string // +private
	VCSDirty      bool   // +private
	VCSRepository string // +private

	EngineConfig []string // +private
	LogLevel     string   // +private
	SubnetNumber int      // +private
	EBPFProgs    []string // +private

	Race               bool // +private
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

// WithGitSource replaces the injected workspace source with one immutable commit.
// The resolved commit is also the provenance stamped into the engine and CLI, so a
// caller cannot accidentally test one tree while advertising another revision.
func (dev *EngineDev) WithGitSource(
	repository string,
	revision string,
) *EngineDev {
	ref := dag.Git(repository).Commit(revision)
	dev.Source = ref.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	// The full commit is both the immutable Git object selector and the value
	// stamped into built artifacts; an absent object fails when the tree loads.
	dev.VCSCommit = revision
	dev.VCSDirty = false
	dev.VCSRepository = canonicalEngineRepository(repository)
	// Nested toolchains require an explicit workspace in module-runtime calls.
	// Deriving it from the same immutable ref prevents ambient checkout state
	// from entering the build while keeping those constructor calls valid.
	dev.Ws = ref.AsWorkspace()
	return dev
}

// WithSource replaces the injected workspace view without changing its VCS identity.
// Callers use this when one development operation needs a smaller content-addressed
// source boundary than the complete engine distribution.
func (dev *EngineDev) WithSource(source *dagger.Directory) *EngineDev {
	if source != nil {
		dev.Source = source
	}
	return dev
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
	//+optional
	version string,
) (*dagger.Container, error) {
	ctr := base
	if ctr == nil {
		ctr = dag.Wolfi().Container(dagger.WolfiContainerOpts{
			Packages: []string{"apk-tools", "git"},
		}).WithEnvVariable("HOME", "/root")
	}
	ctr = ctr.WithWorkdir("$HOME", dagger.ContainerWithWorkdirOpts{Expand: true})
	svc, err := dev.Service(
		ctx,
		"", // name
		gpuSupport,
		sharedCache,
		metrics,
		version,
	)
	if err != nil {
		return nil, err
	}
	return dev.InstallClient(ctx, ctr, svc, version)
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
	return dev.container(ctx, platform, gpuSupport, version, nil)
}

// ContainerWithRustSDKContent builds the engine while reusing content produced by
// RustSDKContent in the same top-level Dagger graph.
func (dev *EngineDev) ContainerWithRustSDKContent(
	ctx context.Context,
	rustSDKContent *RustEngineContent,
	// +optional
	platform dagger.Platform,
	// +optional
	gpuSupport bool,
	// +optional
	version string,
) (*dagger.Container, error) {
	if rustSDKContent == nil {
		return nil, fmt.Errorf("Rust SDK content is required")
	}
	return dev.container(ctx, platform, gpuSupport, version, rustSDKContent)
}

func (dev *EngineDev) container(
	ctx context.Context,
	platform dagger.Platform,
	gpuSupport bool,
	version string,
	rustSDKContent *RustEngineContent,
) (*dagger.Container, error) {
	builder, err := build.NewBuilder(ctx, dev.Source, dev.VCSRepository, version, dev.VCSCommit, dev.VCSDirty, dev.Ws)
	if err != nil {
		return nil, err
	}
	builder = builder.WithRace(dev.Race)
	if rustSDKContent != nil {
		builder, err = builder.WithRustSDKContent(
			rustSDKContent.Content,
			rustSDKContent.ManifestDigest,
			rustSDKContent.DescriptorDigest,
		)
		if err != nil {
			return nil, err
		}
	}
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
	return dev.configureContainer(ctr, platform, version)
}

func (dev *EngineDev) configureContainer(
	ctr *dagger.Container,
	platform dagger.Platform,
	version string,
) (*dagger.Container, error) {
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

	for _, prog := range dev.EBPFProgs {
		ctr = ctr.WithEnvVariable("DAGGER_EBPF_PROG_"+strings.ToUpper(prog), "y")
	}

	ctr = ctr.
		WithFile(engineJSONPath, cfg).
		WithFile(engineTOMLPath, engineTOML).
		WithFile(engineEntrypointPath, entrypoint).
		WithEntrypoint([]string{filepath.Base(engineEntrypointPath)})

	cli := dag.DaggerCli(dagger.DaggerCliOpts{
		Source:    dev.Source,
		Version:   version,
		VcsCommit: dev.VCSCommit,
		VcsDirty:  dev.VCSDirty,
		Ws:        dev.Ws,
	}).Binary(dagger.DaggerCliBinaryOpts{
		Platform: platform,
	})
	ctr = ctr.
		WithFile(cliPath, cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", distconsts.DefaultEngineSockAddr)

	// ctr = ctr.WithEnvVariable("BUILDKIT_SCHEDULER_DEBUG", "1")

	return ctr, nil
}

// RustEngineContent is one reusable OCI layout and its exact engine manifest identity.
type RustEngineContent struct {
	Content              *dagger.Directory
	ManifestDigest       string
	DescriptorDigest     string
	DependencyDescriptor string
}

// RustSDKContent builds the Rust SDK integration once so focused engine cases can reuse
// the same in-DAG content object instead of reconstructing its toolchain layer.
func (dev *EngineDev) RustSDKContent(
	ctx context.Context,
	// +optional
	platform dagger.Platform,
	// +optional
	version string,
	// Credential-free repository containing the public dagger-sdk package.
	// +optional
	dependencyRepository string,
	// Full reachable revision whose public dagger-sdk package matches this build.
	// +optional
	dependencyRevision string,
) (*RustEngineContent, error) {
	builder, err := build.NewBuilder(ctx, dev.Source, dev.VCSRepository, version, dev.VCSCommit, dev.VCSDirty, dev.Ws)
	if err != nil {
		return nil, err
	}
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}
	content, err := builder.RustSDKContent(ctx, dependencyRepository, dependencyRevision)
	if err != nil {
		return nil, err
	}
	dependencyDescriptor, err := content.DependencyDescriptor()
	if err != nil {
		return nil, err
	}
	return &RustEngineContent{
		Content:              content.Directory(),
		ManifestDigest:       content.ManifestDigest(),
		DescriptorDigest:     content.DescriptorDigest(),
		DependencyDescriptor: dependencyDescriptor,
	}, nil
}

// focusedEngineSupportSource selects everything inherited from the immutable
// baseline image. Comparing this slice across revisions prevents a focused build
// from concealing a runtime-support change behind an older published image.
func focusedEngineSupportSource(repository, revision string) *dagger.Directory {
	return dag.Git(repository).
		Commit(revision).
		Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}).
		Filter(dagger.DirectoryFilterOpts{Include: []string{
			"go.mod",
			"go.sum",
			"cmd/dialstdio/**",
			"cmd/dnsname/**",
			"cmd/init/**",
			"engine/distconsts/**",
			"modules/wolfi/**",
			"toolchains/engine-dev/build/**",
			"toolchains/engine-dev/consts/**",
			"toolchains/engine-dev/dagger-module.toml",
			"toolchains/go/**",
		}})
}

func validateFocusedEngineBase(
	ctx context.Context,
	repository string,
	baseRevision string,
	targetRevision string,
) error {
	if len(baseRevision) != 40 || len(targetRevision) != 40 {
		return fmt.Errorf("focused engine base and target require full immutable revisions")
	}
	baseDigest, err := focusedEngineSupportSource(repository, baseRevision).Digest(ctx)
	if err != nil {
		return fmt.Errorf("resolve focused engine baseline support identity: %w", err)
	}
	targetDigest, err := focusedEngineSupportSource(repository, targetRevision).Digest(ctx)
	if err != nil {
		return fmt.Errorf("resolve focused engine target support identity: %w", err)
	}
	if baseDigest != targetDigest {
		return fmt.Errorf("focused engine baseline support differs from target revision")
	}
	return nil
}

func (dev *EngineDev) focusedRustContainer(
	ctx context.Context,
	rustSDKContent *RustEngineContent,
	baseImage string,
	baseRevision string,
	targetRepository string,
	targetRevision string,
	platform dagger.Platform,
	version string,
) (*dagger.Container, error) {
	if rustSDKContent == nil {
		return nil, fmt.Errorf("Rust SDK content is required")
	}
	targetRepository = canonicalEngineRepository(targetRepository)
	if targetRepository == "" {
		return nil, fmt.Errorf("focused engine target repository is required")
	}
	if err := validateFocusedEngineBase(ctx, targetRepository, baseRevision, targetRevision); err != nil {
		return nil, err
	}

	builder, err := build.NewBuilder(ctx, dev.Source, dev.VCSRepository, version, dev.VCSCommit, dev.VCSDirty, dev.Ws)
	if err != nil {
		return nil, err
	}
	builder = builder.WithRace(dev.Race)
	builder, err = builder.WithRustSDKContent(
		rustSDKContent.Content,
		rustSDKContent.ManifestDigest,
		rustSDKContent.DescriptorDigest,
	)
	if err != nil {
		return nil, err
	}
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}

	targetRef := dag.Git(targetRepository).Commit(targetRevision)
	targetBuilder, err := build.NewBuilder(
		ctx,
		targetRef.Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}),
		targetRepository,
		version,
		targetRevision,
		false,
		targetRef.AsWorkspace(),
	)
	if err != nil {
		return nil, err
	}
	if platform != "" {
		targetBuilder = targetBuilder.WithPlatform(platform)
	}
	ctr, err := builder.FocusedRustEngine(ctx, baseImage, targetBuilder)
	if err != nil {
		return nil, err
	}
	return dev.configureContainer(ctr, platform, version)
}

func canonicalEngineRepository(repository string) string {
	repository = strings.TrimSuffix(repository, ".git")
	if path, found := strings.CutPrefix(repository, "git@github.com:"); found {
		return "https://github.com/" + path
	}
	if path, found := strings.CutPrefix(repository, "ssh://git@github.com/"); found {
		return "https://github.com/" + path
	}
	if strings.HasPrefix(repository, "github.com/") {
		return "https://" + repository
	}
	return repository
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
	// +optional
	version string,
) (*dagger.Service, error) {
	return dev.service(ctx, nil, name, gpuSupport, sharedCache, metrics, version)
}

// ServiceWithRustSDKContent starts an engine from one previously built Rust SDK
// content object, preserving its manifest and descriptor identities unchanged.
func (dev *EngineDev) ServiceWithRustSDKContent(
	ctx context.Context,
	rustSDKContent *RustEngineContent,
	name string,
	// +optional
	gpuSupport bool,
	// +optional
	sharedCache bool,
	// +optional
	metrics bool,
	// +optional
	version string,
) (*dagger.Service, error) {
	if rustSDKContent == nil {
		return nil, fmt.Errorf("Rust SDK content is required")
	}
	return dev.service(ctx, rustSDKContent, name, gpuSupport, sharedCache, metrics, version)
}

// ServiceWithFocusedRustSDKContent starts a development engine by overlaying the
// current engine binary, exact-target Go SDK, and reusable Rust SDK content onto a
// digest-pinned baseline whose support slice is proven equal to the target revision.
// The complete release builder remains the authority outside this focused test path.
func (dev *EngineDev) ServiceWithFocusedRustSDKContent(
	ctx context.Context,
	rustSDKContent *RustEngineContent,
	name string,
	baseImage string,
	baseRevision string,
	targetRepository string,
	targetRevision string,
	// +optional
	sharedCache bool,
	// +optional
	metrics bool,
	// +optional
	version string,
) (*dagger.Service, error) {
	// Support 256 layers of nested dagger engines :-P
	dev = dev.IncrementSubnet()
	devEngine, err := dev.focusedRustContainer(
		ctx,
		rustSDKContent,
		baseImage,
		baseRevision,
		targetRepository,
		targetRevision,
		"",
		version,
	)
	if err != nil {
		return nil, err
	}
	return dev.serviceFromContainer(
		devEngine,
		name,
		sharedCache,
		metrics,
		version,
		rustSDKContent.ManifestDigest,
	), nil
}

func (dev *EngineDev) service(
	ctx context.Context,
	rustSDKContent *RustEngineContent,
	name string,
	gpuSupport bool,
	sharedCache bool,
	metrics bool,
	version string,
) (*dagger.Service, error) {
	// Support 256 layers of nested dagger engines :-P
	dev = dev.IncrementSubnet()
	devEngine, err := dev.container(ctx, "", gpuSupport, version, rustSDKContent)
	if err != nil {
		return nil, err
	}
	rustManifestDigest := ""
	if rustSDKContent != nil {
		rustManifestDigest = rustSDKContent.ManifestDigest
	}
	return dev.serviceFromContainer(
		devEngine,
		name,
		sharedCache,
		metrics,
		version,
		rustManifestDigest,
	), nil
}

func (dev *EngineDev) serviceFromContainer(
	devEngine *dagger.Container,
	name string,
	sharedCache bool,
	metrics bool,
	version string,
	rustManifestDigest string,
) *dagger.Service {
	cacheVolumeName := engineStateCacheVolume(version, rustManifestDigest, name, sharedCache)

	devEngine = devEngine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume(cacheVolumeName), dagger.ContainerWithMountedCacheOpts{
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
	})
}

func engineStateCacheVolume(
	version string,
	rustManifestDigest string,
	name string,
	shared bool,
) string {
	const prefix = "dagger-dev-engine-state"
	if shared {
		return prefix
	}

	identity := version
	if rustManifestDigest != "" {
		// Engine state may retain resolved built-in modules. Binding it to the Rust
		// manifest prevents a rebuilt SDK from inheriting an adapter loaded from an
		// older content object carrying the same engine version.
		rustIdentity := strings.TrimPrefix(rustManifestDigest, "sha256:")
		if identity != "" {
			identity += "-"
		}
		identity += rustIdentity
	}
	if identity == "" {
		identity = rand.Text()
	}
	if name != "" {
		identity += "-" + name
	}
	return prefix + "-" + identity
}

// Configure the given client container so that it can connect to the given engine service
func (dev *EngineDev) InstallClient(
	ctx context.Context,
	// The client container to configure
	client *dagger.Container,
	// The engine service to bind
	// +optional
	service *dagger.Service,
	// +optional
	version string,
) (*dagger.Container, error) {
	if service == nil {
		var err error
		service, err = dev.Service(
			ctx,
			"",    // name
			false, // gpuSupport
			false, // sharedCache
			false, // metrics
			version,
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
		WithMountedFile(cliPath, dag.DaggerCli(dagger.DaggerCliOpts{Version: version, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).Binary()).
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

// Introspect the engine API schema, and return it as a json-encoded file.
// This file is used by SDKs to generate clients.
func (dev *EngineDev) IntrospectionJSON(ctx context.Context) (*dagger.File, error) {
	playground, err := dev.Playground(ctx, nil, false, false, false, "")
	if err != nil {
		return nil, err
	}
	introspectionJSON := playground.
		WithFile("/usr/local/bin/codegen", dag.Codegen(dagger.CodegenOpts{Ws: dev.Ws}).Binary()).
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
	playground, err := dev.Playground(ctx, nil, false, false, false, "")
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
		Go(dagger.GoOpts{Source: dev.Source, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).
		Binary("./cmd/introspect")
}

// Generate the json schema for a dagger config file
// Currently supported: "dagger.json", "dagger-module.toml", "dagger.toml", "engine.json"
func (dev *EngineDev) ConfigSchema(filename string) *dagger.File {
	schemaFilename := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".schema.json"
	// This tool has runtime dependencies on the engine source code itself
	return dag.Go(dagger.GoOpts{Source: dev.Source, VcsCommit: dev.VCSCommit, VcsDirty: dev.VCSDirty, Ws: dev.Ws}).
		Env().
		WithExec(
			[]string{"go", "run", "./cmd/json-schema", filename},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaFilename},
		).
		File(schemaFilename)
}

// Generate any engine-related files
// Note: this is codegen of the 'go generate' variety, not 'dagger develop'
// +generate
func (dev *EngineDev) Generate(_ context.Context) (*dagger.Changeset, error) {
	base := dev.Source
	withGoGenerate := dag.Go(dagger.GoOpts{
		Ws:        dev.Ws,
		Source:    dev.Source,
		VcsCommit: dev.VCSCommit,
		VcsDirty:  dev.VCSDirty,
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
		WithExec([]string{"go", "test", "./dagql", "-update"}).
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
