package build

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger/core"
	"github.com/containerd/platforms"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/mod/modfile"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
	golang "dagger/engine-dev/internal/dagger/clients/go"
	"dagger/engine-dev/internal/dagger/clients/wolfi"
)

var versionAnnotation = distconsts.OCIVersionAnnotation

type Builder struct {
	source *dagger.Directory

	// Resolved VCS info stamped into the built engine, threaded in from
	// engine-dev as scalars. Storing the source Workspace here instead would
	// make every build method's cache key session-specific and prevent
	// disk-cache reuse across engine restarts.
	vcsCommit string
	vcsDirty  bool

	version string

	platform     dagger.Platform
	platformSpec ocispecs.Platform

	gpuSupport bool

	race bool

	// rerun, when set, is in the environment of the build steps that have
	// built to different bytes before, so that builders with different
	// values run those steps apart in one engine (RerunOutputs).
	rerun string
}

func NewBuilder(
	ctx context.Context,
	source *dagger.Directory,
	version string,
	vcsCommit string,
	vcsDirty bool,
) (*Builder, error) {
	return &Builder{
		source:       source,
		vcsCommit:    vcsCommit,
		vcsDirty:     vcsDirty,
		platform:     dagger.Platform(platforms.DefaultString()),
		platformSpec: platforms.Normalize(platforms.DefaultSpec()),
		version:      version,
	}, nil
}

func (build *Builder) WithRace(race bool) *Builder {
	b := *build
	b.race = race
	return &b
}

// WithRerun is a builder whose build steps that have built to different
// bytes before carry run in their environment, so they run again rather
// than come from the cache: two builders with different runs build them
// twice in one engine.
func (build *Builder) WithRerun(run string) *Builder {
	b := *build
	b.rerun = run
	return &b
}

// withRerun is ctr with the builder's rerun value in its environment, if it
// has one.
func (build *Builder) withRerun(ctr *dagger.Container) *dagger.Container {
	if build.rerun == "" {
		return ctr
	}
	return ctr.WithEnvVariable("ENGINE_DEV_RERUN", build.rerun)
}

// RerunOutputs are the outputs of the build steps that have built to
// different bytes before, by what they are: QEMU's emulators, the Go SDK's
// files and the Python SDK's.
func (build *Builder) RerunOutputs(ctx context.Context) (map[string]*dagger.Directory, error) {
	qemu, _ := build.qemuBins()
	goFiles, err := build.goSDKFiles(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]*dagger.Directory{
		"QEMU's emulators":       qemu,
		"the Go SDK's files":     goFiles,
		"the Python SDK's files": build.pythonSDKFiles(),
	}, nil
}

func (build *Builder) WithPlatform(p dagger.Platform) *Builder {
	b := *build
	b.platform = p
	b.platformSpec = platforms.Normalize(platforms.MustParse(string(p)))
	return &b
}

// Platform is the platform the builder builds for, normalized, e.g.
// "linux/arm64".
func (build *Builder) Platform() string {
	return platforms.Format(build.platformSpec)
}

func (build *Builder) WithGPUSupport() *Builder {
	b := *build
	b.gpuSupport = true
	return &b
}

// Engine is the engine's container: EngineBase with the engine's binaries
// (WithEngineBinaries).
func (build *Builder) Engine(ctx context.Context) (*dagger.Container, error) {
	base, err := build.EngineBase(ctx)
	if err != nil {
		return nil, err
	}
	return build.WithEngineBinaries(ctx, base)
}

// EngineBase is the engine's container without the binaries built from the
// source (WithEngineBinaries): Wolfi's packages, then the tools built from
// pinned releases in one layer. A commit of the source changes none of it, so
// an image of another commit shares these layers' bytes, and a host that holds
// one pulls none of them again.
func (build *Builder) EngineBase(ctx context.Context) (*dagger.Container, error) {
	eg, ctx := errgroup.WithContext(ctx)

	if build.gpuSupport {
		switch build.platformSpec.Architecture {
		case "amd64":
		default:
			return nil, fmt.Errorf("gpu support requires %q arch, not %q", "amd64", build.platformSpec.Architecture)
		}
	}

	pkgs := []string{
		"ca-certificates",
		"mount", "umount", "posix-libc-utils", "coreutils",
		// for the entrypoint to give the engine its own cgroup namespace
		"util-linux-misc",
		// for git
		"git", "openssh-client",
		// for SSHFS-backed volumes
		"fuse3", "glib",
		// for compression/decompression, containerd prefers igzip from the isa-l package as it's fastest
		"isa-l", "pigz", "xz",
		// for CNI (use nft variants for compatibility with kernels lacking legacy xtables)
		"nftables", "iptables-legacy", "dnsmasq",
		// for Kata Containers integration
		"e2fsprogs",
		// for Directory.search
		"ripgrep",
		// for dbs
		"sqlite",
		// the container runtime (distconsts.RuntimePath)
		"crun",
	}
	if build.gpuSupport {
		pkgs = append(pkgs,
			"nvidia-driver", "nvidia-tools",
			"nvidia-container-toolkit",
			// Wolfi packages the OCI prestart hook separately from the toolkit.
			"nvidia-container-toolkit-nvidia-container-runtime-hook",
		)
	}
	base := wolfi.New().
		Container(wolfi.WolfiContainerOpts{
			Packages: pkgs,
			Arch:     build.platformSpec.Architecture,
		})

	if build.gpuSupport {
		// Check the hook at the path used by the executor, and load the CLI's
		// shared libraries. Neither check requires a GPU on the build host.
		base = base.
			WithExec([]string{"/usr/bin/nvidia-container-runtime-hook", "--version"}).
			WithExec([]string{"/usr/bin/nvidia-container-cli", "--version"})
	}

	if build.version != "" {
		base = base.WithAnnotation(versionAnnotation, build.version)
	}

	bins := []binAndPath{
		{path: "/usr/bin/sshfs", file: build.sshfsBin()},
		{path: consts.TiniPath, file: build.Init(), permissions: 0o755},
	}
	if entire := build.gitRemoteEntireBin(); entire != nil {
		bins = append(bins, binAndPath{path: consts.GitRemoteEntirePath, file: entire})
	}
	qemuDir, qemuNames := build.qemuBins()
	for _, name := range qemuNames {
		bins = append(bins, binAndPath{path: filepath.Join("/usr/local/bin", name), file: qemuDir.File(name)})
	}
	for _, plugin := range build.cniPlugins() {
		bins = append(bins, binAndPath{path: filepath.Join("/opt/cni/bin", plugin.name), file: plugin.file})
	}
	tools := build.binDir(ctx, eg, bins).
		WithDirectory(distconsts.EngineDefaultStateDir, core.NewDirectory())

	ctr := base.
		WithExec([]string{"sh", "-c", "mkdir -p /etc && touch /etc/fuse.conf && (grep -qxF user_allow_other /etc/fuse.conf || printf '%s\\n' user_allow_other >> /etc/fuse.conf)"}).
		WithDirectory("/", tools)

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return ctr, nil
}

// WithEngineBinaries is ctr with the engine's binaries built from the source:
// its helpers in one layer, then the engine in its own. A commit of the
// source changes them, and a layer is made again, and unpacked again on a
// host, whenever a layer below it changes; so they go above every layer a
// commit leaves alone, the image's entries among them.
func (build *Builder) WithEngineBinaries(ctx context.Context, ctr *dagger.Container) (*dagger.Container, error) {
	eg, ctx := errgroup.WithContext(ctx)
	helpers := build.binDir(ctx, eg, []binAndPath{
		{path: "/usr/bin/dial-stdio", file: build.dialstdioBinary()},
		{path: "/opt/cni/bin/dnsname", file: build.dnsnameBinary()},
		{path: consts.DaggerInitPath, file: build.daggerInit()},
		{path: consts.DaggerInitSessionPath, file: build.binary("./cmd/init-session", false)},
	}).WithSymlink("/usr/bin/dial-stdio", "/usr/bin/buildctl")
	engine := build.engineBinary(build.race)
	eg.Go(func() error {
		return build.verifyPlatform(ctx, engine)
	})
	ctr = ctr.
		WithDirectory("/", helpers).
		WithFile(consts.EngineServerPath, engine)
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return ctr, nil
}

// binAndPath is a binary and where the engine's image installs it.
type binAndPath struct {
	path        string
	file        *dagger.File
	permissions int
}

// binDir is a directory of bins, each at its path, for an image to add in one
// layer, with a check in eg of each one's platform.
func (build *Builder) binDir(ctx context.Context, eg *errgroup.Group, bins []binAndPath) *dagger.Directory {
	dir := core.NewDirectory()
	for _, bin := range bins {
		dir = dir.WithFile(bin.path, bin.file, dagger.DirectoryWithFileOpts{Permissions: bin.permissions})
		eg.Go(func() error {
			return build.verifyPlatform(ctx, bin.file)
		})
	}
	return dir
}

func (build *Builder) CodegenBinary() *dagger.File {
	return build.binary("./cmd/codegen", false)
}

// ImageEntriesBinary exports the cache entries the engine image ships
// (cmd/engine-image-entries).
func (build *Builder) ImageEntriesBinary() *dagger.File {
	return build.binary("./cmd/engine-image-entries", false)
}

// IntrospectBinary is cmd/introspect, which prints the engine API's schema
// without running an engine.
func (build *Builder) IntrospectBinary() *dagger.File {
	return build.binary("./cmd/introspect", false)
}

// engineBinary is the engine. It reports its commit (engine.FullVersion), so
// it is stamped with it: with the CLI (cli-dev), the only binary of the image
// that is.
func (build *Builder) engineBinary(race bool) *dagger.File {
	return build.buildBinary("./cmd/engine", race, build.VCSValues())
}

func (build *Builder) dnsnameBinary() *dagger.File {
	return build.binary("./cmd/dnsname", false)
}

func (build *Builder) dialstdioBinary() *dagger.File {
	return build.binary("./cmd/dialstdio", false)
}

// binary is pkg, a main package of the source, built for the builder's
// platform. It carries no VCS info: a binary stamped with the commit differs
// at every commit, and so does every image layer and cache entry that holds
// it, though nothing else of it changed. Only the engine reports its commit
// (engineBinary).
func (build *Builder) binary(pkg string, race bool) *dagger.File {
	return build.buildBinary(pkg, race, nil)
}

// buildBinary is pkg, a main package of the source, built for the builder's
// platform with vcs, the -X definitions of VCS info (VCSValues), stamped in.
func (build *Builder) buildBinary(pkg string, race bool, vcs []string) *dagger.File {
	opts := golang.GoOpts{
		Version: consts.GolangVersion,
		BuildFlags: []string{
			// The engine uses the dockerfile2llb code from buildkit, which makes use of tags
			// for enabling features at compile time:
			// dfexcludepatterns to support COPY/ADD --exclude=...,
			// dfparents to support COPY/ADD --parents.
			// A build flag rather than GOFLAGS, which would be in every
			// package's exec and keep it from the CLI's and codegen's.
			"-tags=dfexcludepatterns,dfparents",
			ldflags([]string{"-s", "-w"}, vcs),
		},
	}
	if race {
		opts = build.withRace(opts)
	}
	return goBinary(build.source, pkg, build.platformSpec, opts)
}

// TestBinary is the test binary of the package in dir, a directory of the
// source's root Go module, built as go test -c builds it, with the source's
// VCS info stamped in, by dagger/go's per-package build. It is planned with
// the other packages of run, the package directories whose test binaries one
// test run builds, which include dir: they share one plan of what they
// compile, and no package outside run is compiled for it.
func (build *Builder) TestBinary(dir string, run []string, race bool) *dagger.File {
	opts := golang.GoOpts{Version: consts.GolangVersion}
	if values := build.VCSValues(); len(values) > 0 {
		opts.BuildFlags = []string{ldflags(nil, values)}
	}
	if race {
		opts = build.withRace(opts)
	}
	ws := build.source.AsWorkspace()
	return golang.New(opts).Module(ws, dir).Packages(ws).Subset(run).Get(dir).TestBinary(ws)
}

// Tests are the test function names of each package of the source's root Go
// module that has any, by package directory. They are read from the source,
// without a build.
func (build *Builder) Tests(ctx context.Context) (map[string][]string, error) {
	ws := build.source.AsWorkspace()
	pkgs, err := golang.New(golang.GoOpts{Version: consts.GolangVersion}).
		Module(ws, ".").
		Packages(ws).
		List(ctx)
	if err != nil {
		return nil, err
	}
	tests := map[string][]string{}
	for _, pkg := range pkgs {
		dir, err := pkg.Path(ctx)
		if err != nil {
			return nil, err
		}
		names, err := pkg.Tests().Keys(ctx)
		if err != nil {
			return nil, err
		}
		if len(names) > 0 {
			tests[dir] = names
		}
	}
	return tests, nil
}

// Otelgotest is the runner the test binaries run under, built from otel-go's
// source at the version the source's go.mod requires.
func (build *Builder) Otelgotest(ctx context.Context) (*dagger.File, error) {
	const otelGo = "github.com/dagger/otel-go"
	contents, err := build.source.File("go.mod").Contents(ctx)
	if err != nil {
		return nil, err
	}
	mod, err := modfile.ParseLax("go.mod", []byte(contents), nil)
	if err != nil {
		return nil, err
	}
	var version string
	for _, req := range mod.Require {
		if req.Mod.Path == otelGo {
			version = req.Mod.Version
		}
	}
	if version == "" {
		return nil, fmt.Errorf("go.mod does not require %s", otelGo)
	}
	// The module's source as the Go module proxy serves it for that version,
	// the same files go install would build.
	src := core.NewContainer().
		From(consts.AlpineImage).
		WithMountedFile("/src.zip", core.HTTP("https://proxy.golang.org/"+otelGo+"/@v/"+version+".zip")).
		WithExec([]string{"unzip", "-q", "/src.zip", "-d", "/out"}).
		Directory("/out/" + otelGo + "@" + version)
	return goBinary(src, "cmd/otelgotest", build.platformSpec, golang.GoOpts{
		Version:    consts.GolangVersion,
		BuildFlags: []string{"-ldflags=-s -w"},
	}), nil
}

// VCSValues are the -X definitions that stamp the source's VCS info into a
// binary, or none without a commit.
func (build *Builder) VCSValues() []string {
	if build.vcsCommit == "" {
		return nil
	}
	return []string{
		"github.com/dagger/dagger/internal/version/buildinfo.InjectedVCS=git",
		"github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSRevision=" + build.vcsCommit,
		"github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSModified=" + strconv.FormatBool(build.vcsDirty),
	}
}

// withRace builds with the race detector. It needs cgo, and a cgo binary
// links the C library of the image it is built in, so it is built where it
// runs: wolfi.
func (build *Builder) withRace(opts golang.GoOpts) golang.GoOpts {
	opts.Base = wolfi.New().
		Container(wolfi.WolfiContainerOpts{
			Packages: []string{"go-" + consts.GolangVersion, "build-base"},
			Arch:     build.platformSpec.Architecture,
		}).
		WithEnvVariable("CGO_ENABLED", "1")
	opts.BuildFlags = append(opts.BuildFlags, "-race")
	return opts
}

// ldflags is one -ldflags build flag: the linker flags, then a -X for each
// definition.
func ldflags(flags []string, values []string) string {
	for _, val := range values {
		flags = append(flags, "-X '"+val+"'")
	}
	return "-ldflags=" + strings.Join(flags, " ")
}

// goBinary is pkg, a main package of source, built for platform by dagger/go's
// per-package build: one cached exec per Go package, so a change rebuilds
// only the packages it reaches. The source is the build's workspace, so the
// build is keyed by its content and never by the caller's session.
func goBinary(source *dagger.Directory, pkg string, platform ocispecs.Platform, opts golang.GoOpts) *dagger.File {
	ws := source.AsWorkspace()
	pkg = path.Clean(pkg)
	return golang.New(opts).
		Module(ws, pkg).
		Packages(ws).
		Get(pkg).
		Platforms().
		Get(platform.OS + "/" + platform.Architecture).
		Binary(ws)
}

// gitRemoteEntireBin is the git remote helper for entire:// remotes, taken
// from the checksummed Entire CLI release. Entire publishes it for amd64 and
// arm64 only; engines for other architectures do not support entire://.
func (build *Builder) gitRemoteEntireBin() *dagger.File {
	arch := build.platformSpec.Architecture
	checksum, ok := consts.EntireCLIChecksums[arch]
	if !ok {
		return nil
	}
	archive := core.HTTP(
		"https://github.com/entireio/cli/releases/download/v"+consts.EntireCLIVersion+"/entire_linux_"+arch+".tar.gz",
		dagger.HTTPOpts{Checksum: checksum},
	)
	return core.NewContainer().
		From(consts.AlpineImage).
		WithMountedFile("/entire.tar.gz", archive).
		WithExec([]string{"tar", "xzf", "/entire.tar.gz", "-C", "/", "git-remote-entire"}).
		File("/git-remote-entire")
}

func (build *Builder) sshfsBin() *dagger.File {
	// Wolfi's sshfs package currently lags upstream at 3.7.4, so build the
	// known-good release from source. Prefer the Wolfi package again once it
	// catches up.
	src := core.
		Git("https://github.com/libfuse/sshfs.git").
		Tag("sshfs-" + consts.SSHFSVersion).
		Tree()

	return wolfi.New().
		Container(wolfi.WolfiContainerOpts{
			Packages: []string{
				"build-base",
				"ca-certificates-bundle",
				"coreutils",
				"fuse3-dev",
				"glib-dev",
				"meson",
			},
			Arch: build.platformSpec.Architecture,
		}).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"meson", "setup", "build", "--buildtype=release"}).
		WithExec([]string{"meson", "compile", "-C", "build"}).
		File("/src/build/sshfs")
}

// cniPlugin is a CNI plugin binary and the name the engine image installs it
// under.
type cniPlugin struct {
	name string
	file *dagger.File
}

// cniPlugins are the CNI plugin binaries, always in the same order: the engine
// image adds them in this order, so its recipe, and every cached result built on
// it, depends on the order.
func (build *Builder) cniPlugins() []cniPlugin {
	src := core.Git("github.com/containernetworking/plugins").Tag(consts.CniVersion).Tree()

	var bins []cniPlugin
	for _, pluginPath := range []string{
		"plugins/main/bridge",
		"plugins/main/loopback",
		"plugins/meta/firewall",
		"plugins/ipam/host-local",
	} {
		// CNI plugins are third-party; VCS stamping is irrelevant here, so we
		// don't thread any VCS info into their build. Go names a binary after
		// its package's last path element.
		bins = append(bins, cniPlugin{
			name: path.Base(pluginPath),
			file: goBinary(src, pluginPath, build.platformSpec, golang.GoOpts{
				Version:    consts.GolangVersion,
				BuildFlags: []string{"-ldflags=-s -w"},
			}),
		})
	}
	return bins
}

func (build *Builder) daggerInit() *dagger.File {
	return build.binary("./cmd/init", false)
}

func (build *Builder) Init() *dagger.File {
	var url string

	switch build.platformSpec.Architecture {
	case "amd64":
		url = "https://github.com/krallin/tini/releases/download/v0.19.0/tini-amd64"
	case "arm64":
		url = "https://github.com/krallin/tini/releases/download/v0.19.0/tini-arm64"
	}
	return core.HTTP(url)
}

// this makes 100% sure that we built the binary for the right platform and didn't, e.g., forget
// to deal with mismatches between the engine host platform and the desired target platform
func (build *Builder) verifyPlatform(ctx context.Context, bin *dagger.File) error {
	name, err := bin.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get name of binary: %w", err)
	}
	mntPath := filepath.Join("/mnt", name)
	out, err := wolfi.New().
		Container(wolfi.WolfiContainerOpts{
			Packages: []string{"file"},
		}).
		WithMountedFile(mntPath, bin).
		WithExec([]string{"file", mntPath}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to call file on binary %s: %w", name, err)
	}
	if !strings.Contains(out, platformToFileArch[build.platformSpec.Architecture]) {
		return fmt.Errorf("binary %s is not for %s", name, build.platformSpec.Architecture)
	}
	return nil
}

var platformToFileArch = map[string]string{
	"amd64": "x86-64",
	"arm64": "aarch64",
}
