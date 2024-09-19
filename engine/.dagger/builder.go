package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"dagger/engine/internal/dagger"

	"github.com/dagger/dagger/.dagger/consts"
)

type Builder struct {
	source       *dagger.Directory
	platform     dagger.Platform
	platformSpec ocispecs.Platform
	base         string
	gpuSupport   bool
	race         bool
	tag          string
	commit       string
	version      string
}

func newBuilder(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=[".git", "bin", "**/.DS_Store", "**/node_modules", "**/__pycache__", "**/.venv", "**/.mypy_cache", "**/.pytest_cache", "**/.ruff_cache", "sdk/python/dist", "sdk/python/**/sdk", "go.work", "go.work.sum", "**/*_test.go", "**/target", "**/deps", "**/cover", "**/_build"]
	source *dagger.Directory,
	// Git tag to use in binary version + also used as remote engine container tag
	tag string,
	// Git commit to use in binary version
	commit string,
) (*Builder, error) {
	version, err := dag.Version(dagger.VersionOpts{
		Tag:    tag,
		Commit: commit,
	}).Version(ctx)
	if err != nil {
		return nil, err
	}
	return &Builder{
		source:       source,
		platform:     dagger.Platform(platforms.DefaultString()),
		platformSpec: platforms.DefaultSpec(),
		tag:          tag,
		commit:       commit,
		version:      version,
	}, nil
}

func (build *Builder) WithCommit(commit string) *Builder {
	b := *build
	b.commit = commit
	return &b
}

func (build *Builder) WithTag(tag string) *Builder {
	b := *build
	b.tag = tag
	return &b
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

func (build *Builder) WithUbuntuBase() *Builder {
	b := *build
	b.base = "ubuntu"
	return &b
}

func (build *Builder) WithAlpineBase() *Builder {
	b := *build
	b.base = "alpine"
	return &b
}

func (build *Builder) WithWolfiBase() *Builder {
	b := *build
	b.base = "wolfi"
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

	var base *dagger.Container
	switch build.base {
	case "alpine", "":
		base = dag.
			Container(dagger.ContainerOpts{Platform: build.platform}).
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
				"dnsmasq", "iptables", "ip6tables", "iptables-legacy",
			}).
			WithExec([]string{"sh", "-c", `
				set -e
				ln -s /sbin/iptables-legacy /usr/sbin/iptables
				ln -s /sbin/iptables-legacy-save /usr/sbin/iptables-save
				ln -s /sbin/iptables-legacy-restore /usr/sbin/iptables-restore
				ln -s /sbin/ip6tables-legacy /usr/sbin/ip6tables
				ln -s /sbin/ip6tables-legacy-save /usr/sbin/ip6tables-save
				ln -s /sbin/ip6tables-legacy-restore /usr/sbin/ip6tables-restore
			`}).
			WithoutEnvVariable("DAGGER_APK_CACHE_BUSTER")
	case "ubuntu":
		base = dag.Container(dagger.ContainerOpts{Platform: build.platform}).
			From("ubuntu:"+consts.UbuntuVersion).
			WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
			WithEnvVariable("DAGGER_APT_CACHE_BUSTER", fmt.Sprintf("%d", time.Now().Truncate(24*time.Hour).Unix())).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{
				"apt-get", "install", "-y",
				"iptables", "git", "dnsmasq-base", "network-manager",
				"gpg", "curl",
			}).
			WithExec([]string{
				"update-alternatives",
				"--set", "iptables", "/usr/sbin/iptables-legacy",
			}).
			WithExec([]string{
				"update-alternatives",
				"--set", "ip6tables", "/usr/sbin/ip6tables-legacy",
			}).
			WithoutEnvVariable("DAGGER_APT_CACHE_BUSTER")
	case "wolfi":
		base = dag.
			Container(dagger.ContainerOpts{Platform: build.platform}).
			From(consts.WolfiImage).
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
			WithoutEnvVariable("DAGGER_APK_CACHE_BUSTER")
	default:
		return nil, fmt.Errorf("unsupported engine base %q", build.base)
	}

	type binAndPath struct {
		path string
		file *dagger.File
	}
	bins := []binAndPath{
		{path: consts.EngineServerPath, file: build.engineBinary(build.race)},
		{path: "/usr/bin/dial-stdio", file: build.dialstdioBinary()},
		{path: "/opt/cni/bin/dnsname", file: build.dnsnameBinary()},
		{path: consts.RuncPath, file: build.runcBin()},
		{path: consts.DumbInitPath, file: build.dumbInit()},
	}
	for _, bin := range build.qemuBins(ctx) {
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

	ctr := base
	for _, bin := range bins {
		ctr = ctr.WithFile(bin.path, bin.file)
		eg.Go(func() error {
			return build.verifyPlatform(ctx, bin.file)
		})
	}

	ctr = ctr.
		WithExec([]string{"ln", "-s", "/usr/bin/dial-stdio", "/usr/bin/buildctl"}).
		WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory())

	if build.gpuSupport {
		switch build.platformSpec.Architecture {
		case "amd64":
		default:
			return nil, fmt.Errorf("gpu support requires %q arch, not %q", "amd64", build.platformSpec.Architecture)
		}

		switch build.base {
		case "ubuntu":
			ctr = ctr.
				WithExec([]string{
					"sh", "-c",
					`curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg`,
				}).
				WithExec([]string{
					"sh", "-c",
					`curl -s -L https://nvidia.github.io/libnvidia-container/experimental/"$(. /etc/os-release;echo $ID$VERSION_ID)"/libnvidia-container.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list`,
				}).
				WithExec([]string{"apt-get", "update"}).
				WithExec([]string{"apt-get", "install", "-y", "nvidia-container-toolkit"})
		case "wolfi":
			ctr = ctr.
				WithExec([]string{"apk", "add", "chainguard-keys"}).
				WithExec([]string{
					"sh", "-c",
					`echo "https://packages.cgr.dev/extras" >> /etc/apk/repositories`,
				}).
				WithExec([]string{"apk", "update"}).
				WithExec([]string{"apk", "add", "nvidia-driver", "nvidia-tools"})
		default:
			return nil, fmt.Errorf("gpu support requires %q base, not %q", "ubuntu or wolfi", build.base)
		}
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	for _, content := range sdkContents {
		ctr = ctr.With(content.apply)
	}

	return ctr, nil
}

func (build *Builder) engineBinary(race bool) *dagger.File {
	return build.binary("./cmd/engine", true, race)
}

func (build *Builder) dnsnameBinary() *dagger.File {
	return build.binary("./cmd/dnsname", false, false)
}

func (build *Builder) dialstdioBinary() *dagger.File {
	return build.binary("./cmd/dialstdio", false, false)
}

func (build *Builder) binary(pkg string, version bool, race bool) *dagger.File {
	base := dag.Go(build.source).Env().With(build.goPlatformEnv)
	// ldflags are arguments passed to the Go linker
	// -s: omit the symbol table and debug information
	// -w: omit the DWARF symbol table
	ldflags := []string{
		"-s", "-w",
	}
	if version && build.version != "" {
		ldflags = append(ldflags, "-X", "github.com/dagger/dagger/engine.Version="+build.version)
	}
	if version && build.tag != "" {
		ldflags = append(ldflags, "-X", "github.com/dagger/dagger/engine.Tag="+build.tag)
	}

	output := filepath.Join("./bin/", filepath.Base(pkg))
	buildArgs := []string{
		"go", "build",
		"-o", output,
		"-ldflags", strings.Join(ldflags, " "),
	}
	if race {
		// -race requires cgo
		base = base.WithEnvVariable("CGO_ENABLED", "1")
		buildArgs = append(buildArgs, "-race")
	}
	buildArgs = append(buildArgs, pkg)

	result := base.
		WithExec(buildArgs).
		File(output)
	return result
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
	buildCtr = buildCtr.WithExec([]string{"go", "get", "golang.org/x/net@v0.25.0"}).
		WithExec([]string{"go", "mod", "tidy"}).
		WithExec([]string{"go", "mod", "vendor"})

	return buildCtr.
		WithExec([]string{"xx-go", "build", "-trimpath", "-buildmode=pie", "-tags", "seccomp netgo osusergo", "-ldflags", "-X main.version=" + consts.RuncVersion + " -linkmode external -extldflags -static-pie", "-o", "runc", "."}).
		File("runc")
}

func (build *Builder) qemuBins(ctx context.Context) []*dagger.File {
	dir := dag.
		Container(dagger.ContainerOpts{Platform: build.platform}).
		From(consts.QemuBinImage).
		Rootfs()

	binNames, err := dir.Entries(ctx)
	if err != nil {
		panic(err)
	}

	var bins []*dagger.File
	for _, binName := range binNames {
		bins = append(bins, dir.File(binName))
	}
	return bins
}

func (build *Builder) cniPlugins() []*dagger.File {
	// We build the CNI plugins from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	// If GPU support is enabled use a Debian image:
	ctr := dag.Container()
	switch build.base {
	case "alpine", "":
		ctr = ctr.From(consts.GolangImage).
			WithExec([]string{"apk", "add", "build-base", "git"})
	case "ubuntu":
		// TODO: there's no guarantee the bullseye libc is compatible with the ubuntu image w/ rebase this onto
		ctr = ctr.From(fmt.Sprintf("golang:%s-bullseye", consts.GolangVersion)).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "git", "build-essential"})
	case "wolfi":
		ctr = ctr.From(fmt.Sprintf("%s:%s", consts.WolfiImage, consts.WolfiVersion)).
			WithExec([]string{"apk", "add", "build-base", "go", "git"})
	}

	ctr = ctr.WithMountedCache("/root/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/containernetworking/plugins").Tag(consts.CniVersion).Tree()).
		WithWorkdir("/src").
		With(build.goPlatformEnv)

	var bins []*dagger.File
	for _, pluginPath := range []string{
		"plugins/main/bridge",
		"plugins/main/loopback",
		"plugins/meta/firewall",
		"plugins/ipam/host-local",
	} {
		pluginName := filepath.Base(pluginPath)
		bins = append(bins, ctr.
			WithWorkdir(pluginPath).
			WithExec([]string{"go", "build", "-o", pluginName, "-ldflags", "-s -w", "."}).
			File(pluginName))
	}

	return bins
}

func (build *Builder) dumbInit() *dagger.File {
	// dumb init is static, so we can use it on any base image
	return dag.
		Container(dagger.ContainerOpts{Platform: build.platform}).
		From(consts.AlpineImage).
		WithExec([]string{"apk", "add", "build-base", "bash"}).
		WithMountedDirectory("/src", dag.Git("github.com/yelp/dumb-init").Ref(consts.DumbInitVersion).Tree()).
		WithWorkdir("/src").
		WithExec([]string{"make"}).
		File("dumb-init")
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
	out, err := dag.Container().From(consts.AlpineImage).
		WithExec([]string{"apk", "add", "file"}).
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
