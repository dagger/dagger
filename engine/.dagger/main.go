package main

import (
	"context"
	"fmt"
	"time"

	"dagger/engine/internal/dagger"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/distconsts"

	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"
)

type Distro string

const (
	DistroAlpine  = "alpine"
	DistroWolfi   = "wolfi"
	DistroUbuntu  = "ubuntu"
	ubuntuVersion = "22.04"
	cniVersion    = "v1.5.0"
	qemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"
)

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!**.go", "!**/go.mod", "!**/go.sum", "!**.graphqls", "!**.proto", "!**.json", "!**.yaml", "!**/testdata", "!**.sql"]
	source *dagger.Directory,
	// Git commit to include in engine version
	// +optional
	commit string,
	// Git tag to include in engine version, in short format
	// +optional
	tag string,
	// Custom engine config values
	// +optional
	config []string,
	// +optional
	args []string,
	// Build the engine with race checking mode
	// +optional
	race bool,
	// Build the engine with tracing enabled
	// +optional
	trace bool,
	// +optional
	// Set an instance name, to spawn different instances of the service, each
	// with their own lifecycle and state volume
	instanceName string,
	// +optional
	dockerConfig *dagger.Secret,
	// Build the engine with GPU support
	// +optional
	gpu bool,
	// +optional
	platform dagger.Platform,
	// Choose a flavor of base image
	// +optional
	// +default="alpine"
	distro Distro,
	// Go version to use when building the engine
	// +optional
	// +default="1.23.0"
	goVersion string,
) (*Engine, error) {
	if gpu {
		platformSpec := platforms.Normalize(platforms.MustParse(string(platform)))
		if arch := platformSpec.Architecture; arch != "amd64" {
			return nil, fmt.Errorf("gpu support requires %q arch, not %q", "amd64", arch)
		}
	}
	version, err := dag.Version(dagger.VersionOpts{
		Commit: commit,
		Tag:    tag,
	}).Version(ctx)
	if err != nil {
		return nil, err
	}
	cli := dag.DaggerCli(dagger.DaggerCliOpts{
		Tag:    tag,
		Commit: commit,
	}).Binary(dagger.DaggerCliBinaryOpts{
		Platform: platform,
	})
	// FIXME: load go base image, to pass to gomod
	// OR change gomod to being lazy
	return &Engine{
		Cli:          cli,
		Version:      version,
		Source:       source,
		Config:       config,
		Args:         args,
		Race:         race,
		Trace:        trace,
		InstanceName: instanceName,
		DockerConfig: dockerConfig,
		GPU:          gpu,
		Platform:     platform,
		Distro:       distro,
		GoVersion:    goVersion,
		Tag:          tag,
	}, nil
}

type Engine struct {
	Cli     *dagger.File      // +private
	Source  *dagger.Directory // +private
	Version string            // +private
	Tag     string            // +private
	Args    []string          // +private
	Config  []string          // +private
	Gomod   *dagger.Go        // +private

	Race  bool // +private
	Trace bool // +private

	InstanceName string          // +private
	DockerConfig *dagger.Secret  // +private
	GPU          bool            // +private
	Platform     dagger.Platform // +private
	Distro       Distro          // +private
	GoVersion    string          // +private
}

// Build one of the binaries involved in the engine build
func (e *Engine) Binary(pkg string) *dagger.File {
	return e.Gomod.Binary(pkg, dagger.GoBinaryOpts{
		Platform:  e.Platform,
		NoSymbols: true,
		NoDwarf:   true,
	})
}

// An environment to build this engine
func (e *Engine) Env() *dagger.Container {
	return e.Gomod.Env()
}

// Run engine tests
func (engine *Engine) Test(
	ctx context.Context,
	// Packages to test (default all)
	// +optional
	// +default=["./..."]
	pkgs []string,
	// Only run these tests
	// +optional
	run string,
	// Skip these tests
	// +optional
	skip string,
	// Abort test run on first failure
	// +optional
	failfast bool,
	// How many tests to run in parallel - defaults to the number of CPUs
	// +optional
	parallel int,
	// How long before timing out the test run
	// +optional
	timeout string,
	// +default=1
	// +optional
	count int,
) error {
	return engine.Gomod.Test(ctx, dagger.GoTestOpts{
		Pkgs:     pkgs,
		Run:      run,
		Skip:     skip,
		Failfast: failfast,
		Parallel: parallel,
		Timeout:  timeout,
		Count:    count,
	})
}

// List all engine tests, using 'go test -list=.'
func (e *Engine) Tests(
	ctx context.Context,
	// Packages to include in the test list
	// +optional
	pkgs []string,
) (string, error) {
	return e.Gomod.Tests(ctx, dagger.GoTestsOpts{
		Pkgs: pkgs,
	})
}

// Build the engine container
func (e *Engine) Container(
	ctx context.Context,
	// Build a dev container, with additional configuration for e2e testing
	// +optional
	dev bool,
	// Scan the container for vulnerabilities after building it
	// +optional
	scan bool,
	// Config files used by the vulnerability scanner
	// +optional
	// +defaultPath="."
	// +ignore=["*", "!.trivyignore", "!trivyignore.yml", "!trivyignore.yaml"]
	scanConfig *dagger.Directory,
) (*dagger.Container, error) {
	if dev {
		e.Config = append(e.Config, `grpc=address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`)
		e.Args = append(e.Args, `network-name=dagger-dev`, `network-cidr=10.88.0.0/16`)
	}
	entrypoint, err := generateEntrypoint(e.Args)
	if err != nil {
		return nil, err
	}
	cfg, err := generateConfig(e.Trace, e.Config)
	if err != nil {
		return nil, err
	}
	ctr := e.Base().
		WithFile("/usr/local/bin/dagger-engine", e.Binary("./cmd/engine")).
		WithFile("/usr/bin/dial-stdio", e.Binary("./cmd/dialstdio")).
		WithFile("/opt/cni/bin/dnsname", e.Binary("./cmd/dnsname")).
		WithFile("/usr/bin/dial-stdio", e.Binary("./cmd/dialstdio")).
		WithFile("/usr/local/bin/runc", dag.Runc(dagger.RuncOpts{
			Platform: e.Platform,
		}).Binary()).
		WithFile("/usr/local/bin/dumb-init", dag.DumbInit(dagger.DumbInitOpts{
			Platform: e.Platform,
		}).Binary()).
		WithDirectory("/usr/local/bin/", dag.Qemu(dagger.QemuOpts{
			Platform: e.Platform,
		}).Binaries()).
		WithDirectory("/opt/cni/bin/", dag.CniPlugins(dagger.CniPluginsOpts{
			Base: e.GoBase(),
		}).Build()).
		WithExec([]string{"ln", "-s", "/usr/bin/dial-stdio", "/usr/bin/buildctl"}).
		WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory()).
		// Add engine configuration
		WithFile("/etc/dagger/engine.toml", cfg).
		// Add generated container entrypoint
		WithFile("/usr/local/bin/dagger-entrypoint.sh", entrypoint).
		WithEntrypoint([]string{"dagger-entrypoint.sh"}).
		// Add dagger CLI
		WithFile("/usr/local/bin/dagger", e.Cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "unix:///var/run/buildkit/buildkitd.sock").
		// Additional settings for a dev engine
		With(func(c *dagger.Container) *dagger.Container {
			if dev {
				return c.
					WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
					WithMountedCache(
						distconsts.EngineDefaultStateDir,
						e.cacheVolume(),
						dagger.ContainerWithMountedCacheOpts{
							// only one engine can run off it's local state dir at a time; Private means that we will attempt to re-use
							// these cache volumes if they are not already locked to another running engine but otherwise will create a new
							// one, which gets us best-effort cache re-use for these nested engine services
							Sharing: dagger.Private,
						}).
					WithExec(nil, dagger.ContainerWithExecOpts{
						UseEntrypoint:            true,
						InsecureRootCapabilities: true,
					})
			}
			return c
		})
	// TODO: get builtin SDK contents
	// Scan the container if requested
	if scan {
		if _, err := e.Scan(ctx, scanConfig, ctr); err != nil {
			return ctr, err
		}
	}
	return ctr, nil
}

// Instantiate the engine as a service, and bind it to the given client
func (e *Engine) Bind(ctx context.Context, client *dagger.Container) *dagger.Container {
	return client.
		With(func(c *dagger.Container) *dagger.Container {
			ectr, err := e.Container(ctx, true, false, nil)
			if err != nil {
				return c.
					WithEnvVariable("ERR", err.Error()).
					WithExec([]string{"sh", "-c", "echo $ERR >/dev/stderr; exit 1"})
			}
			return c.WithServiceBinding("dagger-engine", ectr.AsService())
		}).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine:1234").
		WithMountedFile("/.dagger-cli", e.Cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/.dagger-cli").
		WithExec([]string{"ln", "-s", "/.dagger-cli", "/usr/local/bin/dagger"}).
		With(func(c *dagger.Container) *dagger.Container {
			if e.DockerConfig != nil {
				// this avoids rate limiting in our ci tests
				return c.WithMountedSecret("/root/.docker/config.json", e.DockerConfig)
			}
			return c
		})
}

func (e *Engine) cacheVolume() *dagger.CacheVolume {
	var name string
	if e.Version != "" {
		name = "dagger-dev-engine-state-" + e.Version
	} else {
		name = "dagger-dev-engine-state-" + identity.NewID()
	}
	if e.InstanceName != "" {
		name += "-" + e.InstanceName
	}
	return dagger.Connect().CacheVolume(name)
}

// Lint the engine source code
func (e *Engine) Lint(
	ctx context.Context,
) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		exclude := []string{"docs/.*", "core/integration/.*"}
		// Run dagger module codegen recursively before linting
		src := dag.Supermod(e.Source).
			DevelopAll(dagger.SupermodDevelopAllOpts{Exclude: exclude}).
			Source()
		// Lint each go module
		pkgs, err := dag.Dirdiff().
			Find(ctx, src, "go.mod", dagger.DirdiffFindOpts{Exclude: exclude})
		if err != nil {
			return err
		}
		return dag.Go(src).Lint(ctx, dagger.GoLintOpts{Packages: pkgs})
	})
	eg.Go(func() error {
		return e.LintGenerate(ctx)
	})

	return eg.Wait()
}

// Build the base image for the engine, based on configured flavor
func (e *Engine) Base() *dagger.Container {
	if e.Distro == DistroUbuntu {
		return e.ubuntuBase()
	}
	if e.Distro == DistroWolfi {
		return e.wolfiBase()
	}
	// Alpine is the default
	return e.alpineBase()
}

func (e *Engine) wolfiBase() *dagger.Container {
	// FIXME: use wolfi module
	return dag.
		Container(dagger.ContainerOpts{Platform: e.Platform}).
		From("cgr.dev/chainguard/wolfi-base").
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
		With(func(c *dagger.Container) *dagger.Container {
			// Extra configuration for GPU support
			if e.GPU {
				return c.
					WithExec([]string{"apk", "add", "chainguard-keys"}).
					WithExec([]string{
						"sh", "-c",
						`echo "https://packages.cgr.dev/extras" >> /etc/apk/repositories`,
					}).
					WithExec([]string{"apk", "update"}).
					WithExec([]string{"apk", "add", "nvidia-driver", "nvidia-tools"})
			}
			return c
		})
}

func (e *Engine) alpineBase() *dagger.Container {
	if e.GPU {
	}
	return dag.
		Container(dagger.ContainerOpts{Platform: e.Platform}).
		From(distconsts.AlpineImage).
		With(func(c *dagger.Container) *dagger.Container {
			if e.GPU {
				return c.WithExec([]string{
					"sh", "-c", `echo >&2 "can't build GPU-enabled engine on Alpine Linux base"; exit 1`})
			}
			return c
		}).
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
}

func (e *Engine) ubuntuBase() *dagger.Container {
	return dag.
		Container(dagger.ContainerOpts{Platform: e.Platform}).
		From("ubuntu:"+ubuntuVersion).
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
		WithoutEnvVariable("DAGGER_APT_CACHE_BUSTER").
		With(func(c *dagger.Container) *dagger.Container {
			if e.GPU {
				return c.
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
			}
			return c
		})
}

func (e *Engine) gomod() *dagger.Go {
	return dag.Go(e.Source, dagger.GoOpts{
		Base: e.GoBase(),
		Race: e.Race,
		Values: []string{
			"github.com/dagger/dagger/engine.Version=" + e.Version,
			"github.com/dagger/dagger/engine.Tag=" + e.Tag,
		},
	})
}

// Build a base image for go builds (distro-specific)
func (e *Engine) GoBase() *dagger.Container {
	if e.Distro == "ubuntu" {
		// This is a base for a build environment,
		// not to be confused with the base image of the engine
		// TODO: there's no guarantee the bullseye libc is compatible with the ubuntu image w/ rebase this onto
		return dag.Container().
			From(fmt.Sprintf("golang:%s-bullseye", e.GoVersion)).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "git", "build-essential"})
	}
	if e.Distro == "wolfi" {
		// FIXME: use
		return dag.Container().
			From("cgr.dev/chainguard/wolfi-base").
			WithExec([]string{"apk", "add", "build-base", "git"}).
			WithExec([]string{"apk", "add", "go-" + e.GoVersion})
	}
	// alpine is the default
	return dag.Container().
		From("golang:" + e.GoVersion + "-alpine").
		WithExec([]string{"apk", "add", "build-base", "git"})
}

// Generate any engine-related files
// Note: this is codegen of the 'go generate' variety, not 'dagger develop'
func (e *Engine) Generate() *dagger.Directory {
	return e.Env().
		WithoutDirectory("sdk"). // sdk generation happens separately
		// protobuf dependencies
		WithExec([]string{"apk", "add", "protoc=~3.21.12"}). // FIXME: use common apko module
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"}).
		WithExec([]string{"go", "generate", "-v", "./..."}).
		Directory(".")
}

// Lint any generated engine-related files
func (e *Engine) LintGenerate(ctx context.Context) error {
	return dag.Dirdiff().AssertEqual(
		ctx,
		e.Env().WithoutDirectory("sdk").Directory("."),
		e.Generate(),
		[]string{"."},
	)
}

func (e *Engine) Scan(
	ctx context.Context,
	// Trivy config files
	// +optional
	// +defaultPath="."
	// +ignore=["*", "!.trivyignore", "!trivyignore.yml", "!trivyignore.yaml"]
	ignoreFiles *dagger.Directory,
	// The container to scan
	target *dagger.Container,
) (string, error) {
	ignoreFileNames, err := ignoreFiles.Entries(ctx)
	if err != nil {
		return "", err
	}
	// FIXME: trivy module
	ctr := dag.Container().
		From("aquasec/trivy:0.50.4").
		WithMountedFile("/mnt/engine.tar", target.AsTarball()).
		WithMountedDirectory("/mnt/ignores", ignoreFiles).
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache"))
	args := []string{
		"trivy",
		"image",
		"--format=json",
		"--no-progress",
		"--exit-code=1",
		"--vuln-type=os,library",
		"--severity=CRITICAL,HIGH",
		"--show-suppressed",
	}
	if len(ignoreFileNames) > 0 {
		args = append(args, "--ignorefile=/mnt/ignores/"+ignoreFileNames[0])
	}
	args = append(args, "--input", "/mnt/engine.tar")
	return ctr.WithExec(args).Stdout(ctx)
}
