package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/util/parallel"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"dagger/engine-dev/build"
	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
	clidev "dagger/engine-dev/internal/dagger/clients/cli-dev"
	"dagger/engine-dev/internal/dagger/clients/wolfi"
)

func New(
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

// vcs is the workspace's git commit and whether its tree has uncommitted
// changes, which stamped builds carry. It is read when a build stamps it, not
// when engine-dev is made: reading it brings the workspace's git checkout
// into the engine, which a call that stamps nothing doesn't need.
func (dev *EngineDev) vcs(ctx context.Context) (commit string, dirty bool) {
	return vcsInfo(ctx, dev.Ws)
}

// builder builds the engine's source for version, stamped with the
// workspace's git commit.
func (dev *EngineDev) builder(ctx context.Context, version string) (*build.Builder, error) {
	commit, dirty := dev.vcs(ctx)
	return build.NewBuilder(ctx, dev.Source, version, commit, dirty)
}

// Creates a complete end-to-end build environment with CLI and engine for interactive testing
type EngineDev struct {
	Source *dagger.Directory

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

// Build the engine with the Go builds its image's entries ship, as a release
// does. An engine of the image's own build makes them (imageEntries). An
// engine built without ships only the built-in SDKs' files in its entries.
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
	builder, err := dev.builder(ctx, version)
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
	base, err := dev.engineBase(ctx, builder)
	if err != nil {
		return nil, err
	}
	// The built-in SDKs' files reach the engine only through its image's
	// entries, so every image ships them, made for its platform.
	sdks, err := builder.SDKFiles(ctx)
	if err != nil {
		return nil, err
	}
	// The entries are made by an engine of this build that runs here, for
	// the image's platform: an engine runs its own platform's binaries, and
	// every build the entries hold compiles for any platform.
	native, err := dev.builder(ctx, "")
	if err != nil {
		return nil, err
	}
	native = native.WithRace(dev.Race)
	// The image's engine and its CLI build while the entries are made, and
	// nothing in them waits for the entries.
	var engineBuilt errgroup.Group
	engineBuilt.Go(func() error {
		ctr, err := dev.withEngine(ctx, base, builder, platform, version)
		if err != nil {
			return err
		}
		_, err = ctr.Sync(ctx)
		return err
	})
	entries, digests, err := dev.imageEntries(ctx, native, builder.Platform(), sdks, dev.ImageEntries)
	builtErr := engineBuilt.Wait()
	if err != nil {
		return nil, fmt.Errorf("engine image entries: %w", err)
	}
	if builtErr != nil {
		return nil, builtErr
	}
	// The entries go under the binaries a commit changes and over
	// everything else: first their blobs, which change only when what they
	// hold does, then the bundle that names them, which differs at every
	// build.
	ctr := base.
		WithDirectory(distconsts.EngineContainerImageEntriesDir, entries.WithoutFile(imageEntriesBundleFile)).
		WithFile(path.Join(distconsts.EngineContainerImageEntriesDir, imageEntriesBundleFile), entries.File(imageEntriesBundleFile))
	ctr, err = dev.withEngine(ctx, ctr, builder, platform, version)
	if err != nil {
		return nil, err
	}
	for _, sdk := range sdks {
		ctr = ctr.WithEnvVariable(sdk.EnvName, digests[sdk.Name])
	}
	return ctr, nil
}

// imageEntriesBundleFile is the bundle in an image's entries, beside the
// blobs it names (core/schema's EngineImageBundleFile).
const imageEntriesBundleFile = "bundle.json"

// engine is the engine container builder builds, without the entries its
// image ships.
func (dev *EngineDev) engine(ctx context.Context, builder *build.Builder, platform dagger.Platform, version string) (*dagger.Container, error) {
	base, err := dev.engineBase(ctx, builder)
	if err != nil {
		return nil, err
	}
	return dev.withEngine(ctx, base, builder, platform, version)
}

// engineBase is the engine container builder builds, with its configuration,
// without the binaries a commit of the source changes (withEngine).
func (dev *EngineDev) engineBase(ctx context.Context, builder *build.Builder) (*dagger.Container, error) {
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

	ctr, err := builder.EngineBase(ctx)
	if err != nil {
		return nil, err
	}
	for _, prog := range dev.EBPFProgs {
		ctr = ctr.WithEnvVariable("DAGGER_EBPF_PROG_"+strings.ToUpper(prog), "y")
	}

	return ctr.
		WithFile(engineJSONPath, cfg).
		WithFile(engineTOMLPath, engineTOML).
		WithFile(engineEntrypointPath, entrypoint).
		WithEntrypoint([]string{filepath.Base(engineEntrypointPath)}), nil
}

// withEngine is ctr with the binaries a commit of the source changes: the
// engine's (builder's WithEngineBinaries), then the CLI. They are the image's
// last layers, so that an image of another commit shares every layer below
// them.
func (dev *EngineDev) withEngine(ctx context.Context, ctr *dagger.Container, builder *build.Builder, platform dagger.Platform, version string) (*dagger.Container, error) {
	ctr, err := builder.WithEngineBinaries(ctx, ctr)
	if err != nil {
		return nil, err
	}
	cli := clidev.New(clidev.CLIDevOpts{Version: version, Ws: dev.Ws}).Binary(clidev.CLIDevBinaryOpts{
		Platform: platform,
	})
	return ctr.
		WithFile(cliPath, cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", distconsts.DefaultEngineSockAddr), nil
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

// imageEntries are the cache entries an engine image of platform ships, which
// the engine merges at start, and the content digest of each built-in SDK's
// files by its name. They are the SDKs' files, sdks, and the Go SDK's
// container made from its files; and with goBuilds, what Go module runtimes
// build with: the builds of the standard library and of what generated Go
// module code imports, the built-in Go SDK's (GoImageEntriesSeed) and
// go-sdk's (GoSDKImageEntriesSeed), gobuild's binary, which dagger/go builds
// every runtime with, and what a fresh engine would fetch to build with them
// (dagger/go's tree and the two images the builds run in). dagger/go declares
// each build as made for the platform it compiles for (Directory.withPlatform),
// so an engine of the image's platform finds the builds made for it here.
//
// An engine of the image's own build makes them, inside one exec, on state of
// its own (withSeedEngine): whatever the engine running this build holds is
// never exported, and a remote cache's offers, which carry keys the image's
// engine refuses at start, never reach the image. With the Go builds, that
// engine starts holding the entries of the release the engine running this
// build is (releaseEntries), and builds whatever they don't hold; what it
// takes from them through an equivalent result it exports under its own
// recipe or with its contents. builder builds what runs here.
func (dev *EngineDev) imageEntries(ctx context.Context, builder *build.Builder, platform string, sdks []build.SDKFiles, goBuilds bool) (*dagger.Directory, map[string]string, error) {
	engine, err := dev.engine(ctx, builder, "", "")
	if err != nil {
		return nil, nil, err
	}
	ctr, exporter, err := dev.entriesTool(ctx, builder, engine, platform, sdks, goBuilds)
	if err != nil {
		return nil, nil, err
	}
	var seed seedEntries
	if goBuilds {
		seed = releaseEntries(ctx, platform)
	}
	const out = "/entries"
	const sdkDigests = "/sdk-digests.json"
	ran := dev.withSeedEngine(ctr, seed, append(exporter, "-out", out, "-sdk-digests", sdkDigests))
	if err := entriesExported(ctx, ran, exporter); err != nil {
		return nil, nil, err
	}
	raw, err := ran.File(sdkDigests).Contents(ctx)
	if err != nil {
		return nil, nil, err
	}
	digests := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &digests); err != nil {
		return nil, nil, fmt.Errorf("the built-in SDKs' digests: %w", err)
	}
	for _, sdk := range sdks {
		if digests[sdk.Name] == "" {
			return nil, nil, fmt.Errorf("the built-in SDKs' digests name none for %s", sdk.Name)
		}
	}
	return ran.Directory(out), digests, nil
}

// entriesTool is client with what cmd/engine-image-entries needs to make an
// image of platform's entries, with the built-in SDKs' files, sdks, and with
// goBuilds the Go builds, on the engine client is a client of, and the
// command that makes them, but for its -out and -sdk-digests. builder builds
// what runs here.
func (dev *EngineDev) entriesTool(ctx context.Context, builder *build.Builder, client *dagger.Container, platform string, sdks []build.SDKFiles, goBuilds bool) (*dagger.Container, []string, error) {
	client = client.
		WithMountedFile("/usr/local/bin/engine-image-entries", builder.ImageEntriesBinary())
	exporter := []string{"engine-image-entries",
		"-go-version", distconsts.GolangVersion,
		"-platform", platform,
	}
	for _, sdk := range sdks {
		path := "/builtin-sdks/" + sdk.Name
		client = client.WithMountedDirectory(path, sdk.Dir)
		exporter = append(exporter, "-sdk", sdk.Name+"="+path)
	}
	if !goBuilds {
		return client, exporter, nil
	}
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
	client = client.
		WithMountedDirectory("/seed", seed).
		WithMountedDirectory("/seed-go-sdk", seedGoSDK).
		WithMountedDirectory("/builtin-go-sdk", dev.Source.Directory("sdk/go/runtime"))
	return client, append(exporter,
		"-seed", "/seed",
		"-entrypoint-seed", "/seed-go-sdk",
		"-builtin-go-sdk", "/builtin-go-sdk",
		"-entrypoint-dagger-go", goSDKVersions.DaggerGo,
	), nil
}

// seedEntries are the entries an engine that makes an image's entries starts
// holding, as its own image's (entries), or why it starts without any (none).
type seedEntries struct {
	entries *dagger.Directory
	none    string
}

// releaseEntriesPath is where the engine that makes an image's entries finds
// the release's entries it may start holding.
const releaseEntriesPath = "/release-entries"

// releaseEntries are the entries the image of the release the engine running
// this build is ships for platform: what that engine started from. They hold
// the Go builds of the release's dagger/go, which the builds of another
// dagger/go mostly find through equivalent results. When that engine is no
// release, or its image ships no entries, there are none, and why.
func releaseEntries(ctx context.Context, platform string) seedEntries {
	version, err := core.Version(ctx)
	if err != nil {
		return seedEntries{none: fmt.Sprintf("the engine running this build has no version: %v", err)}
	}
	tag, _, _ := strings.Cut(version, "+")
	if !releaseVersion(tag) {
		return seedEntries{none: fmt.Sprintf("the engine running this build, %s, is no release", version)}
	}
	image := engineImageRepo + ":" + tag
	entries, err := core.NewContainer(dagger.ContainerOpts{Platform: dagger.Platform(platform)}).
		From(image).
		Directory(distconsts.EngineContainerImageEntriesDir).
		Sync(ctx)
	if err != nil {
		return seedEntries{none: fmt.Sprintf("no entries from %s: %s", image, firstLine(err.Error()))}
	}
	return seedEntries{entries: entries}
}

// engineImageRepo is where releases' engine images are.
const engineImageRepo = "registry.dagger.io/engine"

// releaseVersion reports whether version has the shape of a release's tag,
// such as v0.19.1 or v1.0.0-beta.27, rather than of a build from source
// between releases. A build whose version is a release's tag with build
// metadata, as an engine built from that release's source reports, starts
// from that release's entries too: they are only what its builds can find.
func releaseVersion(version string) bool {
	if !semver.IsValid(version) || semver.Build(version) != "" {
		return false
	}
	pre := semver.Prerelease(version)
	if pre == "" {
		return true
	}
	kind, n, ok := strings.Cut(strings.TrimPrefix(pre, "-"), ".")
	if !ok || (kind != "alpha" && kind != "beta" && kind != "rc") {
		return false
	}
	_, err := strconv.Atoi(n)
	return err == nil
}

// firstLine is s up to its first newline.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// withSeedEngine is ctr, a container of an engine's build, after an exec
// that starts that engine, runs cmd as its client once it listens, and stops
// it. The engine's state is a mounted empty directory: a snapshot with no
// parent, which the outer engine mounts as a plain directory on its disk, so
// the engine inside can put its overlays on it, and which has no name, so the
// exec is cached exactly when its inputs are the same. The engine starts
// holding seed's entries as its own image's when ctr's engine-image-entries
// finds their bundle of the version it merges, and otherwise, or with none,
// starts without any and says why.
func (dev *EngineDev) withSeedEngine(ctr *dagger.Container, seed seedEntries, cmd []string) *dagger.Container {
	// Its network is the one after this engine-dev's, without changing
	// dev's: an engine that took another increment could land on a network
	// an engine above it uses.
	nested := *dev
	nested.IncrementSubnet()
	if seed.entries != nil {
		ctr = ctr.WithMountedDirectory(releaseEntriesPath, seed.entries, core.ContainerWithMountedDirectoryOpts{ReadOnly: true})
	} else if seed.none != "" {
		ctr = ctr.WithEnvVariable("SEED_ENTRIES_NONE", seed.none)
	}
	return ctr.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliPath).
		WithMountedDirectory(distconsts.EngineDefaultStateDir, core.NewDirectory()).
		WithExec(
			withoutOuterSession(append([]string{"sh", "-c", seedEngineScript, "seed-engine", nested.NetworkCidr()}, cmd...)...),
			dagger.ContainerWithExecOpts{InsecureRootCapabilities: true},
		)
}

// seedEngineScript starts the container's engine on network $1, waits until
// its socket is there, runs the rest of its arguments as its client, and stops
// the engine. The engine starts holding the release's entries mounted at
// releaseEntriesPath as its own image's, when engine-image-entries finds their
// bundle of the version it merges. The engine's log goes to the exec's
// stderr as it is written, so the exec's output holds it whether the exec
// succeeds, fails or is cancelled.
const seedEngineScript = `set -eu
network_cidr=$1
shift
entries=` + distconsts.EngineContainerImageEntriesDir + `
release=` + releaseEntriesPath + `
if [ -d "$release" ]; then
	if engine-image-entries -check-bundle "$release/bundle.json"; then
		mkdir -p "${entries%/*}"
		ln -s "$release" "$entries"
		echo "starting from the release's entries" >&2
	else
		echo "starting without the release's entries" >&2
	fi
elif [ -n "${SEED_ENTRIES_NONE:-}" ]; then
	echo "starting without a release's entries: $SEED_ENTRIES_NONE" >&2
fi
# Its bridge is the name and "0": at most 15 characters.
dagger-entrypoint.sh --network-name dagger-seed --network-cidr "$network_cidr" >&2 &
engine=$!
stop() {
	kill "$engine" 2>/dev/null || :
	wait "$engine" 2>/dev/null || :
}
failed() {
	echo "$1" >&2
	stop
	exit 1
}
started=$(date +%s)
until [ -S /run/dagger/engine.sock ]; do
	kill -0 "$engine" 2>/dev/null || failed "the engine exited before it listened"
	[ $(($(date +%s) - started)) -lt 120 ] || failed "the engine didn't listen within 120 s"
	sleep 0.1
done
"$@" || failed "$1 exited $?"
stop
`

// entriesExported is nil when ran, an exec that makes an image's entries
// with exporter, succeeded, and otherwise says why it didn't, such as dagger/go
// unreachable at the Go SDK module's dependency, rather than only its exit code.
// ran expects success: a run that fails is an error, which isn't cached, so a
// later build runs the exporter again rather than being served the failure.
func entriesExported(ctx context.Context, ran *dagger.Container, exporter []string) error {
	_, err := ran.Sync(ctx)
	var execErr *dagger.ExecError
	if errors.As(err, &execErr) {
		return fmt.Errorf("make the engine image's entries, with %s (exit code %d):\n%s", strings.Join(exporter, " "), execErr.ExitCode, tailLines(execErr.Stdout+"\n"+execErr.Stderr, 40))
	}
	return err
}

// The entries an engine image ships can be made again on the state an
// earlier making left after a prune cut part of it, as a later release's
// making does: an engine of this build makes them, prunes its cache to half
// its size, least recently used first, and makes them again. ReleaseDryRun
// checks it.
func (dev *EngineDev) ImageEntriesAfterPrune(ctx context.Context) error {
	builder, err := dev.builder(ctx, "")
	if err != nil {
		return err
	}
	builder = builder.WithRace(dev.Race)
	engine, err := dev.engine(ctx, builder, "", "")
	if err != nil {
		return err
	}
	sdks, err := builder.SDKFiles(ctx)
	if err != nil {
		return err
	}
	// An engine of this build, on state of its own, which it prunes.
	ctr, exporter, err := dev.entriesTool(ctx, builder, engine, builder.Platform(), sdks, true)
	if err != nil {
		return err
	}
	ran := dev.withSeedEngine(ctr, seedEntries{}, append([]string{"sh", "-c", `set -e
"$@" -out /entries-before -sdk-digests /sdk-digests-before.json
size=$(echo '{ engine { localCache { entrySet { diskSpaceBytes } } } }' | dagger query | grep -o '"diskSpaceBytes": *[0-9]*' | grep -o '[0-9]*$')
half=$((size / 2 / 1048576))MB
echo "pruning the cache of $size bytes to $half"
echo "{ engine { localCache { prune(maxUsedSpace: \"$half\", targetSpace: \"$half\") } } }" | dagger query
"$@" -out /entries -sdk-digests /sdk-digests.json
`, "sh"}, exporter...))
	return entriesExported(ctx, ran, exporter)
}

// The build steps that have built to different bytes before build to the
// same bytes when they run twice: QEMU's emulators, crun and the Go and
// Python SDKs' files. Two engines that each build the image then hold the
// same image, and the cache can serve what one built on it to the other.
// ReleaseDryRun checks it.
func (dev *EngineDev) ReproducibleBuild(ctx context.Context) error {
	builder, err := dev.builder(ctx, "")
	if err != nil {
		return err
	}
	var runs [2]map[string]*dagger.Directory
	for i := range runs {
		runs[i], err = builder.WithRerun(strconv.Itoa(i + 1)).RerunOutputs(ctx)
		if err != nil {
			return err
		}
	}
	var names []string
	for name := range runs[0] {
		names = append(names, name)
	}
	sort.Strings(names)
	differ := make([]string, len(names))
	eg, ctx := errgroup.WithContext(ctx)
	for i, name := range names {
		eg.Go(func() error {
			a, b := runs[0][name], runs[1][name]
			sumA, err := a.Digest(ctx)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			sumB, err := b.Digest(ctx)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if sumA == sumB {
				return nil
			}
			files, err := differentFiles(ctx, a, b)
			if err != nil {
				return fmt.Errorf("%s differ; listing the files that do: %w", name, err)
			}
			differ[i] = name + ": " + strings.Join(files, ", ")
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	differ = slices.DeleteFunc(differ, func(s string) bool { return s == "" })
	if len(differ) > 0 {
		return fmt.Errorf("two builds gave different bytes:\n%s", strings.Join(differ, "\n"))
	}
	return nil
}

// differentFiles are the paths of the regular files whose content differs
// between a and b, or that only one of them has.
func differentFiles(ctx context.Context, a, b *dagger.Directory) ([]string, error) {
	var sums [2]map[string]string
	for i, dir := range []*dagger.Directory{a, b} {
		paths, err := dir.Glob(ctx, "**")
		if err != nil {
			return nil, err
		}
		sums[i] = map[string]string{}
		for _, path := range paths {
			regular, err := dir.Exists(ctx, path, core.DirectoryExistsOpts{
				ExpectedType:        core.ExistsTypeRegularType,
				DoNotFollowSymlinks: true,
			})
			if err != nil {
				return nil, err
			}
			if !regular {
				continue
			}
			sums[i][path], err = dir.File(path).Digest(ctx)
			if err != nil {
				return nil, err
			}
		}
	}
	var differ []string
	for path, sum := range sums[0] {
		if sums[1][path] != sum {
			differ = append(differ, path)
		}
	}
	for path := range sums[1] {
		if _, ok := sums[0][path]; !ok {
			differ = append(differ, path)
		}
	}
	sort.Strings(differ)
	return differ, nil
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
	client = client.
		// Reach the engine by the alias it is bound under. The service's
		// own hostname is chosen by the engine, and two engines can choose
		// differently; as an environment variable it would be in the cache
		// key of everything run in this container.
		WithServiceBinding("dagger-engine", service).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine:1234").
		WithMountedFile(cliPath, clidev.New(clidev.CLIDevOpts{Source: dev.cliDevSource(), Ws: dev.Ws}).Binary()).
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
// This file is used by SDKs to generate clients. It is the engine's answer to
// an introspection query from a client of its own version, computed
// in-process by the introspect tool, so no engine is built or started.
func (dev *EngineDev) IntrospectionJSON(ctx context.Context) (*dagger.File, error) {
	ctr, err := dev.introspectContainer(ctx)
	if err != nil {
		return nil, err
	}
	schemaPath := "schema.json"
	return ctr.
		WithExec(
			[]string{"introspect", "introspect"},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaPath},
		).
		File(schemaPath), nil
}

// Introspect the engine API schema, and return it as a graphql schema
func (dev *EngineDev) GraphqlSchema(
	ctx context.Context,
	// +optional
	version string,
) (*dagger.File, error) {
	ctr, err := dev.introspectContainer(ctx)
	if err != nil {
		return nil, err
	}
	schemaPath := "schema.graphqls"
	return ctr.
		WithExec(
			[]string{"introspect", "--version=" + version, "schema"},
			dagger.ContainerWithExecOpts{RedirectStdout: schemaPath},
		).
		File(schemaPath), nil
}

// introspectContainer is a container with the introspect tool installed. The
// tool and the container it runs in are built at the same time: neither needs
// the other.
func (dev *EngineDev) introspectContainer(ctx context.Context) (*dagger.Container, error) {
	base := wolfi.New().Container()
	var introspect *dagger.File
	eg, egctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		// Built without the source's commit: the schema doesn't read it, and
		// a stamp would rebuild the tool, and rerun everything generated
		// from its schema, on every commit.
		builder, err := build.NewBuilder(egctx, dev.Source, "", "", false)
		if err != nil {
			return err
		}
		introspect = builder.IntrospectBinary()
		_, err = introspect.Sync(egctx)
		return err
	})
	eg.Go(func() error {
		_, err := base.Sync(egctx)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return base.WithFile("/usr/local/bin/introspect", introspect), nil
}

// Build the `introspect` tool which introspects the engine API
func (dev *EngineDev) IntrospectionTool(ctx context.Context) (*dagger.File, error) {
	builder, err := dev.builder(ctx, "")
	if err != nil {
		return nil, err
	}
	return builder.IntrospectBinary(), nil
}

// Build the export driver the Go SDK's code generation loads packages
// through: a GOPACKAGESDRIVER that serves the export data of what they import
// from dagger/go's archives, so the go command compiles none of it.
func (dev *EngineDev) ExportDriver(ctx context.Context) (*dagger.File, error) {
	// Unstamped: the driver doesn't read the commit.
	builder, err := build.NewBuilder(ctx, dev.Source, "", "", false)
	if err != nil {
		return nil, err
	}
	return builder.ExportDriver(), nil
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

// Every engine image target of a release builds, with its entries, without
// being pushed, with only the engine and the CLI carrying the commit; the
// entries can be made again after a prune; and the build steps that have
// built to different bytes before build to the same bytes twice.
// +check
func (dev *EngineDev) ReleaseDryRun(ctx context.Context) error {
	// One check rather than three: each is a few minutes of work, which as
	// a check of its own would take a CI engine of its own.
	return parallel.New().
		WithJob("build the release's engine images", func(ctx context.Context) error {
			// As Publish builds them, without pushing them.
			// FIXME: why not from HEAD like the SDKs?
			results, err := dev.WithImageEntries().buildTargets(ctx, []string{"main"})
			if err != nil {
				return err
			}
			return dev.onlyEngineStamped(ctx, results[0].Platforms[0])
		}).
		WithJob("make the image's entries again after a prune", dev.ImageEntriesAfterPrune).
		WithJob("build reproducibly", dev.ReproducibleBuild).
		Run(ctx)
}

// onlyEngineStamped fails unless, of what image, an engine image, holds that
// was built from the source, only the engine and the CLI carry the commit:
// any other binary stamped with it, or a cache entry that holds one, differs
// at every commit, and so does its layer, which every host then pulls again.
func (dev *EngineDev) onlyEngineStamped(ctx context.Context, image *dagger.Container) error {
	commit, _ := dev.vcs(ctx)
	if commit == "" {
		// Built without a commit, nothing carries one.
		return nil
	}
	_, err := image.WithExec([]string{"sh", "-c", onlyEngineStampedScript, "sh",
		commit, consts.EngineServerPath, cliPath,
	}).Sync(ctx)
	var execErr *dagger.ExecError
	if errors.As(err, &execErr) {
		return fmt.Errorf("the image's binaries and the commit %s: %s", commit, strings.TrimSpace(execErr.Stdout))
	}
	return err
}

// onlyEngineStampedScript fails unless the commit $1 is in the engine, $2,
// and the CLI, $3, and in no other file under /usr or /opt, the image's
// entries among them (distconsts.EngineContainerImageEntriesDir).
const onlyEngineStampedScript = `commit=$1 engine=$2 cli=$3
for f in "$engine" "$cli"; do
	grep -q "$commit" "$f" || { echo "$f doesn't carry it"; exit 1; }
done
others=$(grep -rl "$commit" /usr /opt | grep -vx -e "$engine" -e "$cli")
[ -z "$others" ] || { echo "carried by more than the engine and the CLI:"; echo "$others"; exit 1; }
`

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

// imageSourceDateEpoch is the time every file of a published engine image
// carries (Container.publish's sourceDateEpoch), in seconds: one constant, not
// the commit's time, so that a layer whose files didn't change is the same
// bytes in every image, and a host that holds one image pulls only the layers
// the next one changed. It is 1980-01-02T00:00:00Z: zip can't hold a time
// before 1980, and in no time zone is this one before it.
const imageSourceDateEpoch = 315619200

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
						SourceDateEpoch:   imageSourceDateEpoch,
					}); err != nil {
						return err
					}
				}
				return nil
			})
	}
	return jobs.Run(ctx)
}
