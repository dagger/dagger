package build

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/platforms"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/consts"
	"dagger/engine-dev/internal/dagger"
)

var dag = dagger.Connect()

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

	ws *dagger.Workspace
}

func NewBuilder(
	ctx context.Context,
	source *dagger.Directory,
	version string,
	vcsCommit string,
	vcsDirty bool,
	ws *dagger.Workspace,
) (*Builder, error) {
	return &Builder{
		source:       source,
		vcsCommit:    vcsCommit,
		vcsDirty:     vcsDirty,
		platform:     dagger.Platform(platforms.DefaultString()),
		platformSpec: platforms.DefaultSpec(),
		version:      version,
		ws:           ws,
	}, nil
}

func (build *Builder) WithRace(race bool) *Builder {
	b := *build
	b.race = race
	return &b
}

func (build *Builder) WithPlatform(p dagger.Platform) *Builder {
	b := *build
	b.platform = p
	b.platformSpec = platforms.Normalize(platforms.MustParse(string(p)))
	return &b
}

func (build *Builder) WithGPUSupport() *Builder {
	b := *build
	b.gpuSupport = true
	return &b
}

func (build *Builder) Engine(ctx context.Context) (*dagger.Container, error) {
	eg, ctx := errgroup.WithContext(ctx)

	sdks := []sdkContentF{build.goSDKContent, build.pythonSDKContent, build.typescriptSDKContent}
	sdkContents := make([]*sdkContent, len(sdks))
	for i, sdk := range sdks {
		eg.Go(func() error {
			content, err := sdk(ctx)
			if err != nil {
				return err
			}
			sdkContents[i] = content
			return nil
		})
	}

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
	}
	if build.gpuSupport {
		pkgs = append(pkgs, "nvidia-driver", "nvidia-tools")
	}
	base := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: pkgs,
			Arch:     build.platformSpec.Architecture,
		})

	if build.version != "" {
		base = base.WithAnnotation(versionAnnotation, build.version)
	}

	type binAndPath struct {
		path     string
		file     *dagger.File
		fileOpts []dagger.ContainerWithFileOpts
	}
	bins := []binAndPath{
		{path: consts.EngineServerPath, file: build.engineBinary(build.race)},
		{path: "/usr/bin/sshfs", file: build.sshfsBin()},
		{path: "/usr/bin/dial-stdio", file: build.dialstdioBinary()},
		{path: "/opt/cni/bin/dnsname", file: build.dnsnameBinary()},
		{path: consts.RuntimePath, file: build.runtimeBin()},
		{path: consts.DaggerInitPath, file: build.daggerInit()},
		{path: consts.DaggerInitSessionPath, file: build.binary("./cmd/init-session", false)},
		{path: consts.TiniPath, file: build.Init(), fileOpts: []dagger.ContainerWithFileOpts{{Permissions: 0o755}}},
	}
	qemuDir, qemuNames := build.qemuBins()
	for _, name := range qemuNames {
		bins = append(bins, binAndPath{path: filepath.Join("/usr/local/bin", name), file: qemuDir.File(name)})
	}
	for _, bin := range build.cniPlugins() {
		name, err := bin.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get name of binary: %w", err)
		}
		bins = append(bins, binAndPath{path: filepath.Join("/opt/cni/bin", name), file: bin})
	}

	ctr := base.
		WithExec([]string{"sh", "-c", "mkdir -p /etc && touch /etc/fuse.conf && (grep -qxF user_allow_other /etc/fuse.conf || printf '%s\\n' user_allow_other >> /etc/fuse.conf)"})
	for _, bin := range bins {
		ctr = ctr.WithFile(bin.path, bin.file, bin.fileOpts...)
		eg.Go(func() error {
			return build.verifyPlatform(ctx, bin.file)
		})
	}

	ctr = ctr.
		WithSymlink("/usr/bin/dial-stdio", "/usr/bin/buildctl").
		WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory())

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	for _, content := range sdkContents {
		ctr = ctr.With(content.apply)
	}

	return ctr, nil
}

func (build *Builder) CodegenBinary() *dagger.File {
	return build.binary("./cmd/codegen", false)
}

func (build *Builder) engineBinary(race bool) *dagger.File {
	return build.binaryWithSource("./cmd/engine", race, build.source)
}

func (build *Builder) dnsnameBinary() *dagger.File {
	return build.binary("./cmd/dnsname", false)
}

func (build *Builder) dialstdioBinary() *dagger.File {
	return build.binary("./cmd/dialstdio", false)
}

//nolint:unparam
func (build *Builder) binary(pkg string, race bool) *dagger.File {
	return build.binaryWithSource(pkg, race, build.source)
}

func (build *Builder) binaryWithSource(pkg string, race bool, source *dagger.Directory) *dagger.File {
	return build.goWithSource(source, race).
		Binary(pkg, dagger.GoBinaryOpts{
			Platform:  build.platform,
			NoSymbols: true,
			NoDwarf:   true,
		})
}

func (build *Builder) Go(race bool) *dagger.Go {
	return build.goWithSource(build.source, race)
}

func (build *Builder) goWithSource(source *dagger.Directory, race bool) *dagger.Go {
	return dag.Go(dagger.GoOpts{
		Ws:        build.ws,
		Source:    source,
		VcsCommit: build.vcsCommit,
		VcsDirty:  build.vcsDirty,
		Race:      race,
		Tags: []string{
			// The engine uses the dockerfile2llb code from buildkit, which makes use of tags
			// for enabling features at compile time:
			"dfexcludepatterns", // to support COPY/ADD --exclude=...
			"dfparents",         // to support COPY/ADD --parents
		},
	})
}

func (build *Builder) runtimeBin() *dagger.File {
	// crun is C: cross-compile it with xx, as runc is, statically linked,
	// from the checksummed release tarball, which carries its libocispec
	// submodule.
	src := dag.HTTP(
		"https://github.com/containers/crun/releases/download/"+consts.CrunVersion+"/crun-"+consts.CrunVersion+".tar.gz",
		dagger.HTTPOpts{Checksum: consts.CrunChecksum},
	)
	jsonc := dag.HTTP(
		"https://github.com/json-c/json-c/archive/refs/tags/json-c-"+consts.JSONCVersion+".tar.gz",
		dagger.HTTPOpts{Checksum: consts.JSONCChecksum},
	)
	return dag.Container().
		From(consts.AlpineImage).
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", string(build.platform)).
		WithExec([]string{"apk", "add", "--no-cache",
			"clang", "lld", "llvm", "make", "autoconf", "automake", "libtool", "pkgconf", "python3", "cmake",
		}).
		WithDirectory("/", dag.Container().From(consts.XxImage).Rootfs()).
		WithExec([]string{"xx-apk", "add", "--no-cache",
			"musl-dev", "gcc", "linux-headers",
			"libcap-dev", "libcap-static", "libseccomp-dev", "libseccomp-static", "argp-standalone",
		}).
		WithFile("/crun.tar.gz", src).
		WithFile("/json-c.tar.gz", jsonc).
		WithExec([]string{"sh", "-ec", `
sysroot=$(xx-info sysroot)
mkdir -p /json-c/build /src
tar xzf /json-c.tar.gz --strip-components=1 -C /json-c
cd /json-c/build
cmake .. $(xx-clang --print-cmake-defines) -DBUILD_SHARED_LIBS=OFF -DCMAKE_INSTALL_PREFIX=/usr -DCMAKE_INSTALL_LIBDIR=lib -DCMAKE_BUILD_TYPE=Release
make -j"$(nproc)"
make install DESTDIR="$sysroot"
tar xzf /crun.tar.gz --strip-components=1 -C /src
cd /src
./configure --host="$(xx-clang --print-target-triple)" CC=xx-clang --disable-systemd --disable-criu LIBS=-largp
make -j"$(nproc)" LDFLAGS=-all-static
llvm-strip crun
xx-verify --static crun
`}).
		File("/src/crun")
}

func (build *Builder) sshfsBin() *dagger.File {
	// Wolfi's sshfs package currently lags upstream at 3.7.4, so build the
	// known-good release from source. Prefer the Wolfi package again once it
	// catches up.
	src := dag.
		Git("https://github.com/libfuse/sshfs.git").
		Tag("sshfs-" + consts.SSHFSVersion).
		Tree()

	return dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
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

func (build *Builder) cniPlugins() (bins []*dagger.File) {
	src := dag.Git("github.com/containernetworking/plugins").Tag(consts.CniVersion).Tree()

	for _, pluginPath := range []string{
		"./plugins/main/bridge",
		"./plugins/main/loopback",
		"./plugins/meta/firewall",
		"./plugins/ipam/host-local",
	} {
		// CNI plugins are third-party; VCS stamping is irrelevant here, so we
		// don't thread any VCS info into their build.
		bin := dag.Go(dagger.GoOpts{Source: src, Ws: build.ws}).Binary(pluginPath, dagger.GoBinaryOpts{
			NoSymbols: true,
			NoDwarf:   true,
			Platform:  build.platform,
		})
		bins = append(bins, bin)
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
	return dag.HTTP(url)
}

func (build *Builder) goPlatformEnv(ctr *dagger.Container) *dagger.Container {
	ctr = ctr.WithEnvVariable("GOOS", build.platformSpec.OS)
	ctr = ctr.WithEnvVariable("GOARCH", build.platformSpec.Architecture)
	switch build.platformSpec.Architecture {
	case "arm", "arm64":
		switch build.platformSpec.Variant {
		case "", "v8":
		default:
			ctr = ctr.WithEnvVariable("GOARM", strings.TrimPrefix(build.platformSpec.Variant, "v"))
		}
	}
	return ctr
}

// this makes 100% sure that we built the binary for the right platform and didn't, e.g., forget
// to deal with mismatches between the engine host platform and the desired target platform
func (build *Builder) verifyPlatform(ctx context.Context, bin *dagger.File) error {
	name, err := bin.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get name of binary: %w", err)
	}
	mntPath := filepath.Join("/mnt", name)
	out, err := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
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
