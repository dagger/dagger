package util

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"

	. "dagger/internal/dagger"
)

type Repository struct {
	Directory    *Directory
	GitDirectory *Directory
}

func NewRepository(source *Directory) *Repository {
	gitDir := source.Directory(".git")
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

	return &Repository{
		Directory:    source,
		GitDirectory: gitDir,
	}
}

func (repo *Repository) DirectoryForGo() *Directory {
	return dag.Directory().WithDirectory("/", repo.Directory, DirectoryWithDirectoryOpts{
		Include: []string{
			// go source
			"**/*.go",

			// modules
			"**/go.mod",
			"**/go.sum",

			// embedded files
			"**/*.tmpl",
			"**/*.ts.gtpl",
			"**/*.graphqls",
			"**/*.graphql",

			// misc
			".golangci.yml",
			"**/README.md", // needed for examples test
			"**/help.txt",  // needed for linting module bootstrap code
			"sdk/go/codegen/generator/typescript/templates/src/testdata/**/*",
			"core/integration/testdata/**/*",

			// Go SDK runtime codegen
			"**/dagger.json",
		},
		Exclude: []string{
			".git",
		},
	})
}

func (repo *Repository) GoBase() *Container {
	dir := repo.DirectoryForGo()
	return dag.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		WithExec([]string{"apk", "add", "build-base"}).
		WithEnvVariable("CGO_ENABLED", "0").
		// adding the git CLI to inject vcs info
		// into the go binaries
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", dir, ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", dir).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build"))
}

type VersionInfo struct {
	Tag      string
	Commit   string
	TreeHash string
}

func (info VersionInfo) EngineVersion() string {
	if info.Tag != "" {
		return info.Tag
	}
	if info.Commit != "" {
		return info.Commit
	}
	return info.TreeHash
}

func (repo *Repository) DevelVersionInfo(ctx context.Context) (*VersionInfo, error) {
	base := dag.Container().
		From(fmt.Sprintf("alpine:%s", alpineVersion)).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedDirectory("/app/.git", repo.GitDirectory).
		WithWorkdir("/app")

	info := &VersionInfo{}

	// use git write-tree to get a content hash of the current state of the repo
	var err error
	info.TreeHash, err = base.
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "write-tree"}).
		Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tree hash: %w", err)
	}
	info.TreeHash = strings.TrimSpace(info.TreeHash)

	return info, nil
}

func (repo *Repository) DaggerBinary(
	ctx context.Context,
	goos string, // +optional
	goarch string, // +optional
	goarm string, // +optional
) (*File, error) {
	version, err := repo.DevelVersionInfo(ctx)
	if err != nil {
		return nil, err
	}
	return repo.binary("./cmd/dagger", goos, goarch, goarm, version.EngineVersion()), nil
}

func (repo *Repository) DaggerEngine(
	ctx context.Context,
	goarch string, // +optional
	entrypointArgs []string, // +optional
	configEntries []string, // +optional
) (*Container, error) {
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	engineConfig, err := getConfig(configEntries)
	if err != nil {
		return nil, err
	}
	engineEntrypoint, err := getEntrypoint(entrypointArgs)
	if err != nil {
		return nil, err
	}

	version, err := repo.DevelVersionInfo(ctx)
	if err != nil {
		return nil, err
	}

	container := dag.
		Container(ContainerOpts{Platform: Platform("linux/" + goarch)}).
		From("alpine:"+alpineVersion).
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
		WithFile(engineServerPath, repo.engineBinary(goarch, version.EngineVersion())).
		WithFile(engineShimPath, repo.shimBinary(goarch, version.EngineVersion())).
		WithFile("/usr/bin/dialstdio", repo.dialstdioBinary(goarch)).
		WithExec([]string{"ln", "-s", "/usr/bin/dialstdio", "/usr/bin/buildctl"}).
		WithFile("/opt/cni/bin/dnsname", repo.dnsnameBinary(goarch)).
		WithFile("/usr/local/bin/runc", runcBin(goarch), ContainerWithFileOpts{Permissions: 0o700}).
		WithDirectory("/usr/local/bin", qemuBins(goarch)).
		With(repo.goSDKContent(ctx, goarch)).
		With(repo.pythonSDKContent(ctx, goarch)).
		With(repo.typescriptSDKContent(ctx, goarch)).
		WithDirectory("/", cniPlugins(goarch, false)).
		WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory()).
		WithNewFile(engineTomlPath, ContainerWithNewFileOpts{
			Contents:    engineConfig,
			Permissions: 0o600,
		}).
		WithNewFile(engineEntrypointPath, ContainerWithNewFileOpts{
			Contents:    engineEntrypoint,
			Permissions: 0o755,
		})
	return container.WithEntrypoint([]string{filepath.Base(engineEntrypointPath)}), nil
}

func (repo *Repository) DaggerEngineService(
	ctx context.Context,
	name string,
	cloudToken *Secret, // +optional
	cloudURL *Secret, // +optional
	goarch string, // +optional
	entrypointArgs []string, // +optional
	configEntries []string, // +optional
) (*Service, error) {
	var cacheVolumeName string
	if name != "" {
		cacheVolumeName = "dagger-dev-engine-state-" + name
	} else {
		cacheVolumeName = "dagger-dev-engine-state"
	}
	cacheVolumeName = cacheVolumeName + identity.NewID()

	devEngine, err := repo.DaggerEngine(ctx, goarch, entrypointArgs, configEntries)
	if err != nil {
		return nil, err
	}

	if cloudToken != nil {
		devEngine = devEngine.WithSecretVariable("_EXPERIMENTAL_DAGGER_CLOUD_TOKEN", cloudToken)
	}
	if cloudURL != nil {
		devEngine = devEngine.WithSecretVariable("_EXPERIMENTAL_DAGGER_CLOUD_URL", cloudURL)
	}

	devEngine = devEngine.
		WithExposedPort(1234, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume(cacheVolumeName)).
		WithExec(nil, ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	return devEngine.AsService(), nil
}

func (repo *Repository) codegenBinary(arch string, version string) *File {
	return repo.binary("./cmd/codegen", "linux", arch, "", version)
}

func (repo *Repository) engineBinary(arch string, version string) *File {
	return repo.binary("./cmd/engine", "linux", arch, "", version)
}

func (repo *Repository) shimBinary(arch string, version string) *File {
	return repo.binary("./cmd/shim", "linux", arch, "", version)
}

func (repo *Repository) dnsnameBinary(arch string) *File {
	return repo.binary("./cmd/dnsname", "linux", arch, "", "")
}

func (repo *Repository) dialstdioBinary(arch string) *File {
	return repo.binary("./cmd/dialstdio", "linux", arch, "", "")
}

func (repo *Repository) binary(
	pkg string,
	goos string, // +optional
	goarch string, // +optional
	goarm string, // +optional
	version string, // +optional
) *File {
	base := repo.GoBase()
	if goos != "" {
		base = base.WithEnvVariable("GOOS", goos)
	}
	if goarch != "" {
		base = base.WithEnvVariable("GOARCH", goarch)
	}
	if goarm != "" {
		base = base.WithEnvVariable("GOARM", goarm)
	}

	ldflags := []string{
		"-s", "-w",
	}
	if version != "" {
		ldflags = append(ldflags, "-X", "github.com/dagger/dagger/engine.Version="+version)
	}

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

func runcBin(arch string) *File {
	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	buildCtr := dag.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		WithEnvVariable("GOARCH", arch).
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", "linux/"+arch).
		WithEnvVariable("CGO_ENABLED", "1").
		WithExec([]string{"apk", "add", "clang", "lld", "git", "pkgconf"}).
		WithDirectory("/", dag.Container().From("tonistiigi/xx:1.2.1").Rootfs()).
		WithExec([]string{"xx-apk", "update"}).
		WithExec([]string{"xx-apk", "add", "build-base", "pkgconf", "libseccomp-dev", "libseccomp-static"}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/opencontainers/runc").Tag(runcVersion).Tree()).
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
		WithExec([]string{"xx-go", "build", "-trimpath", "-buildmode=pie", "-tags", "seccomp netgo osusergo", "-ldflags", "-X main.version=" + runcVersion + " -linkmode external -extldflags -static-pie", "-o", "runc", "."}).
		File("runc")
}

func qemuBins(arch string) *Directory {
	return dag.
		Container(ContainerOpts{Platform: Platform("linux/" + arch)}).
		From(qemuBinImage).
		Rootfs()
}

func cniPlugins(arch string, gpuSupportEnabled bool) *Directory {
	// We build the CNI plugins from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	// If GPU support is enabled use a Debian image:
	ctr := dag.Container()
	if gpuSupportEnabled {
		// TODO: there's no guarantee the bullseye libc is compatible with the ubuntu image w/ rebase this onto
		ctr = ctr.From(fmt.Sprintf("golang:%s-bullseye", golangVersion)).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "git", "build-essential"})
	} else {
		ctr = ctr.From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
			WithExec([]string{"apk", "add", "build-base", "go", "git"})
	}

	ctr = ctr.WithMountedCache("/root/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build")).
		WithMountedDirectory("/src", dag.Git("github.com/containernetworking/plugins").Tag(cniVersion).Tree()).
		WithWorkdir("/src").
		WithEnvVariable("GOARCH", arch)

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
