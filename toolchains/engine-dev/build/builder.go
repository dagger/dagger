package build

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
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
	// taint the cache key of every build method and break disk-cache reuse
	// across engine restarts.
	vcsCommit     string
	vcsDirty      bool
	vcsRepository string

	version string

	platform     dagger.Platform
	platformSpec ocispecs.Platform

	gpuSupport bool

	race bool

	rustSDKContent *sdkContent

	ws *dagger.Workspace
}

func NewBuilder(
	ctx context.Context,
	source *dagger.Directory,
	vcsRepository string,
	version string,
	vcsCommit string,
	vcsDirty bool,
	ws *dagger.Workspace,
) (*Builder, error) {
	return &Builder{
		source:        source,
		vcsRepository: vcsRepository,
		vcsCommit:     vcsCommit,
		vcsDirty:      vcsDirty,
		platform:      dagger.Platform(platforms.DefaultString()),
		platformSpec:  platforms.DefaultSpec(),
		version:       version,
		ws:            ws,
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

// WithRustSDKContent reuses a previously built OCI layout after validating both
// identities which the engine will expose to the loader and operation adapter.
func (build *Builder) WithRustSDKContent(
	directory *dagger.Directory,
	manifestDigest string,
	descriptorDigest string,
	dependencyDescriptor string,
) (*Builder, error) {
	if directory == nil || !isCanonicalDigest(manifestDigest) || !isCanonicalDigest(descriptorDigest) {
		return nil, fmt.Errorf("reusable Rust SDK content requires canonical manifest and descriptor identities")
	}
	var dependency sdkDependencyCoordinates
	if err := json.Unmarshal([]byte(dependencyDescriptor), &dependency); err != nil {
		return nil, fmt.Errorf("decode reusable Rust SDK dependency descriptor: %w", err)
	}
	canonicalDependency, err := json.Marshal(dependency)
	if err != nil || string(canonicalDependency) != dependencyDescriptor {
		return nil, fmt.Errorf("reusable Rust SDK dependency descriptor is not canonical")
	}
	if err := validateReusableRustSDKDependency(dependency); err != nil {
		return nil, err
	}
	dependencyDigest := sha256.Sum256(canonicalDependency)
	b := *build
	b.rustSDKContent = &sdkContent{
		index: ocispecs.Index{Manifests: []ocispecs.Descriptor{{
			Digest: digest.Digest(manifestDigest),
		}}},
		sdkDir:        directory,
		envName:       distconsts.RustSDKManifestDigestEnvName,
		sdkDependency: dependency,
		extraEnv: map[string]string{
			distconsts.RustSDKDescriptorDigestEnvName:     descriptorDigest,
			distconsts.RustSDKDependencyDescriptorEnvName: dependencyDescriptor,
			distconsts.RustSDKDependencyDigestEnvName:     fmt.Sprintf("sha256:%x", dependencyDigest),
		},
	}
	return &b, nil
}

func validateReusableRustSDKDependency(dependency sdkDependencyCoordinates) error {
	if dependency.PackageName != "dagger-sdk" {
		return fmt.Errorf("reusable Rust SDK dependency descriptor names an unsupported package")
	}
	switch dependency.Source {
	case "registry":
		if dependency.Registry != "crates-io" || dependency.ExactVersion == "" ||
			dependency.URL != "" || dependency.Revision != "" {
			return fmt.Errorf("reusable Rust SDK registry dependency descriptor is incomplete")
		}
	case "git":
		parsed, err := url.Parse(dependency.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || dependency.Registry != "" ||
			dependency.ExactVersion != "" || len(dependency.Revision) != 40 {
			return fmt.Errorf("reusable Rust SDK Git dependency descriptor is incomplete")
		}
		for _, character := range dependency.Revision {
			if !strings.ContainsRune("0123456789abcdef", character) {
				return fmt.Errorf("reusable Rust SDK Git dependency revision is not lowercase hexadecimal")
			}
		}
	default:
		return fmt.Errorf("reusable Rust SDK dependency descriptor uses an unsupported source")
	}
	return nil
}

func (build *Builder) Engine(ctx context.Context) (*dagger.Container, error) {
	eg, ctx := errgroup.WithContext(ctx)

	rustSDKContent := func(ctx context.Context) (*sdkContent, error) {
		return build.RustSDKContent(ctx, "", "")
	}
	if build.rustSDKContent != nil {
		rustSDKContent = func(context.Context) (*sdkContent, error) {
			return build.rustSDKContent, nil
		}
	}
	sdks := []sdkContentF{build.goSDKContent, build.pythonSDKContent, build.typescriptSDKContent, rustSDKContent}
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
		{path: consts.RuncPath, file: build.runcBin()},
		{path: consts.DaggerInitPath, file: build.daggerInit()},
		{path: consts.TiniPath, file: build.Init(), fileOpts: []dagger.ContainerWithFileOpts{{Permissions: 0o755}}},
	}
	qemuBins, err := build.qemuBins(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch qemu binaries: %w", err)
	}
	for _, bin := range qemuBins {
		name, err := bin.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get name of binary: %w", err)
		}
		bins = append(bins, binAndPath{path: filepath.Join("/usr/local/bin", name), file: bin})
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

// FocusedRustEngine overlays the changing engine and Rust integration onto one
// immutable engine baseline. The baseline retains the production runtime support
// binaries; its Go SDK content is replaced because the packaged Rust adapter is a Go
// Dagger module and must compile against the exact target schema.
//
// This is a development construction path only. Engine and release builds continue
// through Engine, which assembles and verifies the complete distribution.
func (build *Builder) FocusedRustEngine(
	ctx context.Context,
	baseImage string,
	target *Builder,
) (*dagger.Container, error) {
	if err := validateDigestPinnedImage(baseImage); err != nil {
		return nil, fmt.Errorf("focused Rust engine base: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("focused Rust engine requires target engine source")
	}
	if build.rustSDKContent == nil {
		return nil, fmt.Errorf("focused Rust engine requires reusable Rust SDK content")
	}

	goSDKContent, err := target.goSDKContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("build focused engine target Go SDK content: %w", err)
	}
	engineBinary := build.engineBinary(build.race)
	if err := build.verifyPlatform(ctx, engineBinary); err != nil {
		return nil, fmt.Errorf("verify focused Rust engine binary: %w", err)
	}

	ctr := dag.Container(dagger.ContainerOpts{Platform: build.platform}).
		From(baseImage).
		WithFile(consts.EngineServerPath, engineBinary).
		With(goSDKContent.apply).
		With(build.rustSDKContent.apply)
	if build.version != "" {
		ctr = ctr.WithAnnotation(versionAnnotation, build.version)
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

func (build *Builder) runcBin() *dagger.File {
	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	buildCtr := dag.Container().
		From(consts.GolangImage).
		With(build.goPlatformEnv).
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", string(build.platform)).
		WithEnvVariable("CGO_ENABLED", "1").
		WithExec([]string{"apk", "add", "clang", "lld", "git", "pkgconf"}).
		WithDirectory("/", dag.Container().From(consts.XxImage).Rootfs()).
		WithExec([]string{"xx-apk", "update"}).
		WithExec([]string{"xx-apk", "add", "build-base", "pkgconf", "libseccomp-dev", "libseccomp-static"}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/opencontainers/runc").Tag(consts.RuncVersion).Tree()).
		WithWorkdir("/src")

	return buildCtr.
		WithExec([]string{"xx-go", "build", "-trimpath", "-buildmode=pie", "-tags", "seccomp netgo osusergo", "-ldflags", "-X main.version=" + consts.RuncVersion + " -linkmode external -extldflags -static-pie", "-o", "runc", "."}).
		File("runc")
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

func (build *Builder) qemuBins(ctx context.Context) ([]*dagger.File, error) {
	dir := dag.
		Container(dagger.ContainerOpts{Platform: build.platform}).
		From(consts.QemuBinImage).
		Rootfs()

	binNames, err := dir.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list qemu binaries: %w", err)
	}

	var bins []*dagger.File
	for _, binName := range binNames {
		bins = append(bins, dir.File(binName))
	}
	return bins, nil
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
