package build

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/platforms"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/engine/distconsts"

	"github.com/dagger/dagger/.dagger/consts"
	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/.dagger/util"
)

var dag = dagger.Connect()

type Builder struct {
	source *dagger.Directory

	version string
	tag     string

	platform     dagger.Platform
	platformSpec ocispecs.Platform

	base       string
	gpuSupport bool

	race bool
}

func NewBuilder(ctx context.Context, source *dagger.Directory) (*Builder, error) {
	source = dag.Directory().WithDirectory("/", source, dagger.DirectoryWithDirectoryOpts{
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
			"sdk/python/**/sdk",

			// go
			// go.work is ignored so that you can use ../foo during local dev and let
			// this exclude rule reflect what the PR would run with, as a reminder to
			// actually bump dependencies
			"go.work",
			"go.work.sum",

			// don't rebuild on test-only-changes
			"**/*_test.go",

			// rust
			"**/target",

			// elixir
			"**/deps",
			"**/cover",
			"**/_build",
		},
	})
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}
	return &Builder{
		source:       source,
		platform:     dagger.Platform(platforms.DefaultString()),
		platformSpec: platforms.DefaultSpec(),
		version:      version,
		tag:          tag,
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

	if build.gpuSupport {
		switch build.platformSpec.Architecture {
		case "amd64":
		default:
			return nil, fmt.Errorf("gpu support requires %q arch, not %q", "amd64", build.platformSpec.Architecture)
		}

		switch build.base {
		case "ubuntu":
		case "wolfi":
		default:
			return nil, fmt.Errorf("gpu support requires %q base, not %q", "ubuntu or wolfi", build.base)
		}
	}

	var base *dagger.Container
	switch build.base {
	case "alpine", "":
		base = dag.
			Alpine(dagger.AlpineOpts{
				Branch: consts.AlpineVersion,
				Packages: []string{
					// for Buildkit
					"git", "openssh-client", "pigz", "xz",
					// for CNI
					"dnsmasq", "iptables", "ip6tables", "iptables-legacy",
				},
				Arch: build.platformSpec.Architecture,
			}).
			Container().
			WithExec([]string{"sh", "-c", `
				set -e
				ln -s /sbin/iptables-legacy /usr/sbin/iptables
				ln -s /sbin/iptables-legacy-save /usr/sbin/iptables-save
				ln -s /sbin/iptables-legacy-restore /usr/sbin/iptables-restore
				ln -s /sbin/ip6tables-legacy /usr/sbin/ip6tables
				ln -s /sbin/ip6tables-legacy-save /usr/sbin/ip6tables-save
				ln -s /sbin/ip6tables-legacy-restore /usr/sbin/ip6tables-restore
			`})
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
		if build.gpuSupport {
			base = base.
				With(util.ShellCmd(`curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg`)).
				With(util.ShellCmd(`curl -s -L https://nvidia.github.io/libnvidia-container/experimental/"$(. /etc/os-release;echo $ID$VERSION_ID)"/libnvidia-container.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list`)).
				With(util.ShellCmd(`apt-get update && apt-get install -y nvidia-container-toolkit`))
		}
	case "wolfi":
		pkgs := []string{
			// for Buildkit
			"git", "openssh-client", "pigz", "xz",
			// for CNI
			"iptables", "ip6tables", "dnsmasq",
		}
		if build.gpuSupport {
			pkgs = append(pkgs, "nvidia-driver", "nvidia-tools")
		}
		base = dag.
			Wolfi().
			Container(dagger.WolfiContainerOpts{
				Packages: pkgs,
				Arch:     build.platformSpec.Architecture,
			})
	default:
		return nil, fmt.Errorf("unsupported engine base %q", build.base)
	}

	if build.version != "" {
		base = base.WithAnnotation(distconsts.OCIVersionAnnotation, build.version)
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
		{path: consts.DaggerInitPath, file: build.daggerInit()},
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

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	for _, content := range sdkContents {
		ctr = ctr.With(content.apply)
	}

	return ctr, nil
}

func (build *Builder) CodegenBinary() *dagger.File {
	return build.binary("./cmd/codegen", false, false)
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
		WithDirectory("/", dag.Container().From(consts.XxImage).Rootfs()).
		WithExec([]string{"xx-apk", "update"}).
		WithExec([]string{"xx-apk", "add", "build-base", "pkgconf", "libseccomp-dev", "libseccomp-static"}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/opencontainers/runc").Tag(consts.RuncVersion).Tree()).
		WithWorkdir("/src")

	// TODO: runc v1.1.x uses an old version of golang.org/x/net, which has a CVE:
	// https://github.com/advisories/GHSA-w32m-9786-jp63
	// We upgrade it here to avoid that showing up in our image scans. This can be removed
	// once runc has released a new minor version and we upgrade to it (the go.mod in runc
	// main branch already has the updated version).
	buildCtr = buildCtr.WithExec([]string{"go", "get", "golang.org/x/net@v0.33.0"}).
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
	var ctr *dagger.Container
	switch build.base {
	case "alpine", "":
		ctr = dag.
			Alpine(dagger.AlpineOpts{
				Branch:   consts.AlpineVersion,
				Packages: []string{"build-base", "go", "git"},
			}).
			Container()
	case "ubuntu":
		// TODO: there's no guarantee the bullseye libc is compatible with the ubuntu image w/ rebase this onto
		ctr = dag.
			Container().
			From(fmt.Sprintf("golang:%s-bullseye", consts.GolangVersion)).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "git", "build-essential"})
	case "wolfi":
		ctr = dag.
			Wolfi().
			Container(dagger.WolfiContainerOpts{Packages: []string{
				"build-base", "go", "git",
			}})
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

func (build *Builder) daggerInit() *dagger.File {
	return build.binary("./cmd/init", false, false)
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
		Alpine(dagger.AlpineOpts{
			Branch:   consts.AlpineVersion,
			Packages: []string{"file"},
		}).
		Container().
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
