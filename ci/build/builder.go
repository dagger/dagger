package build

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"

	"dagger/consts"
	"dagger/internal/dagger"
	. "dagger/internal/dagger"
	"dagger/util"
)

var dag = dagger.Connect()

type Builder struct {
	Source *Directory

	Version *VersionInfo
}

func NewBuilder(ctx context.Context, source *Directory) (*Builder, error) {
	// XXX: can we make this lazy?
	version, err := getVersionFromGit(ctx, source.Directory(".git"))
	if err != nil {
		return nil, err
	}

	source = dag.Directory().WithDirectory("/", source, DirectoryWithDirectoryOpts{
		Exclude: []string{
			".git",
			"bin",
			"**/.DS_Store",

			// node
			"**/node_modules",

			// python
			"**/__pycache__",
			"**/.venv",
			"**/.mypy_cache",
			"**/.pytest_cache",
			"**/.ruff_cache",
			"sdk/python/dist",

			// go
			// go.work is ignored so that you can use ../foo during local dev and let
			// this exclude rule reflect what the PR would run with, as a reminder to
			// actually bump dependencies
			"go.work",
			"go.work.sum",

			// rust
			"**/target",

			// elixir
			"**/deps",
			"**/cover",
			"**/_build",
		},
	})

	return &Builder{
		Source:  source,
		Version: version,
	}, nil
}

func (build *Builder) Engine(
	ctx context.Context,
	platform dagger.Platform, // +optional
) (*Container, error) {
	container := dag.
		Container(ContainerOpts{Platform: platform}).
		From(consts.AlpineImage).
		// NOTE: wrapping the apk installs with this time based env ensures that the cache is invalidated
		// once-per day. This is a very unfortunate workaround for the poor caching "apk add" as an exec
		// gives us.
		// Fortunately, better approaches are on the horizon w/ Zenith, for which there are already apk
		// modules that fix this problem and always result in the latest apk packages for the given alpine
		// version being used (with optimal caching).
		WithEnvVariable("DAGGER_APK_CACHE_BUSTER", fmt.Sprintf("%d", time.Now().Truncate(24*time.Hour).Unix())).
		WithExec([]string{"apk", "upgrade"}).
		WithExec([]string{
			"apk", "add", "--no-cache",
			// for Buildkit
			"git", "openssh", "pigz", "xz",
			// for CNI
			"iptables", "ip6tables", "dnsmasq",
		}).
		WithoutEnvVariable("DAGGER_APK_CACHE_BUSTER").
		WithFile(consts.EngineServerPath, build.engineBinary(platform)).
		WithFile(consts.EngineShimPath, build.shimBinary(platform)).
		WithFile("/usr/bin/dialstdio", build.dialstdioBinary(platform)).
		WithExec([]string{"ln", "-s", "/usr/bin/dialstdio", "/usr/bin/buildctl"}).
		WithFile("/opt/cni/bin/dnsname", build.dnsnameBinary(platform)).
		WithFile("/usr/local/bin/runc", runcBin(platform), ContainerWithFileOpts{Permissions: 0o700}).
		WithDirectory("/usr/local/bin", qemuBins(platform)).
		With(build.goSDKContent(ctx, platform)).
		With(build.pythonSDKContent(ctx, platform)).
		With(build.typescriptSDKContent(ctx, platform)).
		WithDirectory("/", cniPlugins(platform, false)).
		WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory())
	return container, nil
}

func (build *Builder) codegenBinary(platform dagger.Platform) *File {
	return build.binary("./cmd/codegen", platform)
}

func (build *Builder) engineBinary(platform dagger.Platform) *File {
	return build.binary("./cmd/engine", platform)
}

func (build *Builder) shimBinary(platform dagger.Platform) *File {
	return build.binary("./cmd/shim", platform)
}

func (build *Builder) dnsnameBinary(platform dagger.Platform) *File {
	return build.binary("./cmd/dnsname", platform)
}

func (build *Builder) dialstdioBinary(platform dagger.Platform) *File {
	return build.binary("./cmd/dialstdio", platform)
}

func (build *Builder) binary(
	pkg string,
	platform dagger.Platform,
) *File {
	base := util.GoBase(build.Source)
	if p, err := platforms.Parse(string(platform)); err == nil {
		base = base.WithEnvVariable("GOOS", p.OS)
		base = base.WithEnvVariable("GOARCH", p.Architecture)
		if p.Variant != "" {
			base = base.WithEnvVariable("GOARM", p.Variant)
		}
	}

	ldflags := []string{
		"-s", "-w",
	}
	ldflags = append(ldflags, "-X", "github.com/dagger/dagger/engine.Version="+build.Version.EngineVersion())

	output := filepath.Join("./bin/", filepath.Base(pkg))
	result := base.
		WithExec(
			[]string{
				"go", "build",
				"-o", output,
				"-ldflags", strings.Join(ldflags, " "),
				pkg,
			},
		).
		File(output)
	return result
}

func runcBin(platform dagger.Platform) *File {
	p, err := platforms.Parse(string(platform))
	if err != nil {
		p = platforms.DefaultSpec()
	}

	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	buildCtr := dag.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", consts.GolangVersion, consts.AlpineVersion)).
		WithEnvVariable("GOARCH", p.Architecture).
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", string(platform)).
		WithEnvVariable("CGO_ENABLED", "1").
		WithExec([]string{"apk", "add", "clang", "lld", "git", "pkgconf"}).
		WithDirectory("/", dag.Container().From("tonistiigi/xx:1.2.1").Rootfs()).
		WithExec([]string{"xx-apk", "update"}).
		WithExec([]string{"xx-apk", "add", "build-base", "pkgconf", "libseccomp-dev", "libseccomp-static"}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/opencontainers/runc").Tag(consts.RuncVersion).Tree()).
		WithWorkdir("/src")

	// TODO: runc v1.1.x uses an old version of golang.org/x/net, which has a CVE:
	// https://github.com/advisories/GHSA-4374-p667-p6c8
	// We upgrade it here to avoid that showing up in our image scans. This can be removed
	// once runc has released a new minor version and we upgrade to it (the go.mod in runc
	// main branch already has the updated version).
	buildCtr = buildCtr.WithExec([]string{"go", "get", "golang.org/x/net"}).
		WithExec([]string{"go", "mod", "tidy"}).
		WithExec([]string{"go", "mod", "vendor"})

	return buildCtr.
		WithExec([]string{"xx-go", "build", "-trimpath", "-buildmode=pie", "-tags", "seccomp netgo osusergo", "-ldflags", "-X main.version=" + consts.RuncVersion + " -linkmode external -extldflags -static-pie", "-o", "runc", "."}).
		File("runc")
}

func qemuBins(platform dagger.Platform) *Directory {
	return dag.
		Container(ContainerOpts{Platform: platform}).
		From(consts.QemuBinImage).
		Rootfs()
}

func cniPlugins(platform dagger.Platform, gpuSupportEnabled bool) *Directory {
	p, err := platforms.Parse(string(platform))
	if err != nil {
		p = platforms.DefaultSpec()
	}

	// We build the CNI plugins from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	// If GPU support is enabled use a Debian image:
	ctr := dag.Container()
	if gpuSupportEnabled {
		// TODO: there's no guarantee the bullseye libc is compatible with the ubuntu image w/ rebase this onto
		ctr = ctr.From(fmt.Sprintf("golang:%s-bullseye", consts.GolangVersion)).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "git", "build-essential"})
	} else {
		ctr = ctr.From(fmt.Sprintf("golang:%s-alpine%s", consts.GolangVersion, consts.AlpineVersion)).
			WithExec([]string{"apk", "add", "build-base", "go", "git"})
	}

	ctr = ctr.WithMountedCache("/root/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/containernetworking/plugins").Tag(consts.CniVersion).Tree()).
		WithWorkdir("/src").
		WithEnvVariable("GOARCH", p.Architecture)

	pluginDir := dag.Directory()
	for _, pluginPath := range []string{
		"plugins/main/bridge",
		"plugins/main/loopback",
		"plugins/meta/firewall",
		"plugins/ipam/host-local",
	} {
		pluginName := filepath.Base(pluginPath)
		pluginDir = pluginDir.WithFile(filepath.Join("/opt/cni/bin", pluginName), ctr.
			WithWorkdir(pluginPath).
			WithExec([]string{"go", "build", "-o", pluginName, "-ldflags", "-s -w", "."}).
			File(pluginName))
	}

	return pluginDir
}
