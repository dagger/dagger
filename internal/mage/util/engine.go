package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/consts"
	"github.com/moby/buildkit/identity"
	"golang.org/x/exp/maps"
)

const (
	daggerBinName = "dagger" // CLI, not engine!
	engineBinName = "dagger-engine"
	shimBinName   = "dagger-shim"

	golangVersion = "1.21.2"
	alpineVersion = "3.18"
	runcVersion   = "v1.1.9"
	cniVersion    = "v1.3.0"
	qemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"

	engineTomlPath = "/etc/dagger/engine.toml"
	// NOTE: this needs to be consistent with DefaultStateDir in internal/engine/docker.go
	EngineDefaultStateDir = "/var/lib/dagger"

	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"

	CacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
)

const engineEntrypointTmpl = `#!/bin/sh
set -e

# cgroup v2: enable nesting
# see https://github.com/moby/moby/blob/38805f20f9bcc5e87869d6c79d432b166e1c88b4/hack/dind#L28
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	# move the processes from the root group to the /init group,
	# otherwise writing subtree_control fails with EBUSY.
	# An error during moving non-existent process (i.e., "cat") is ignored.
	mkdir -p /sys/fs/cgroup/init
	xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
	# enable controllers
	sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
		> /sys/fs/cgroup/cgroup.subtree_control
fi

exec {{.EngineBin}} --config {{.EngineConfig}} {{ range $key := .EntrypointArgKeys -}}--{{ $key }}="{{ index $.EntrypointArgs $key }}" {{ end -}} "$@"
`

const engineConfigTmpl = `
debug = true
insecure-entitlements = ["security.insecure"]
{{ range $key := .ConfigKeys }}
[{{ $key }}]
{{ index $.ConfigEntries $key }}
{{ end -}}
`

// DevEngineOpts are options for the dev engine
type DevEngineOpts struct {
	EntrypointArgs map[string]string
	ConfigEntries  map[string]string
	Name           string
}

func getEntrypoint(opts ...DevEngineOpts) (string, error) {
	mergedOpts := map[string]string{}
	for _, opt := range opts {
		maps.Copy(mergedOpts, opt.EntrypointArgs)
	}
	keys := maps.Keys(mergedOpts)
	sort.Strings(keys)

	var entrypoint string

	type entrypointTmplParams struct {
		Bridge            string
		EngineBin         string
		EngineConfig      string
		EntrypointArgs    map[string]string
		EntrypointArgKeys []string
	}
	tmpl := template.Must(template.New("entrypoint").Parse(engineEntrypointTmpl))
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, entrypointTmplParams{
		EngineBin:         "/usr/local/bin/" + engineBinName,
		EngineConfig:      engineTomlPath,
		EntrypointArgs:    mergedOpts,
		EntrypointArgKeys: keys,
	})
	if err != nil {
		panic(err)
	}
	entrypoint = buf.String()

	return entrypoint, nil
}

func getConfig(opts ...DevEngineOpts) (string, error) {
	mergedOpts := map[string]string{}
	for _, opt := range opts {
		maps.Copy(mergedOpts, opt.ConfigEntries)
	}
	keys := maps.Keys(mergedOpts)
	sort.Strings(keys)

	var config string

	type configTmplParams struct {
		ConfigEntries map[string]string
		ConfigKeys    []string
	}
	tmpl := template.Must(template.New("config").Parse(engineConfigTmpl))
	buf := new(bytes.Buffer)
	err := tmpl.Execute(buf, configTmplParams{
		ConfigEntries: mergedOpts,
		ConfigKeys:    keys,
	})
	if err != nil {
		panic(err)
	}
	config = buf.String()

	return config, nil
}

func CIDevEngineContainerAndEndpoint(ctx context.Context, c *dagger.Client, opts ...DevEngineOpts) (*dagger.Service, string, error) {
	devEngine := CIDevEngineContainer(c, opts...).AsService()

	endpoint, err := devEngine.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, "", err
	}
	return devEngine, endpoint, nil
}

var DefaultDevEngineOpts = DevEngineOpts{
	EntrypointArgs: map[string]string{
		"network-name": "dagger-dev",
		"network-cidr": "10.88.0.0/16",
	},
	ConfigEntries: map[string]string{
		"grpc":                 `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`,
		`registry."docker.io"`: `mirrors = ["mirror.gcr.io"]`,
	},
}

func CIDevEngineContainer(c *dagger.Client, opts ...DevEngineOpts) *dagger.Container {
	engineOpts := []DevEngineOpts{}

	engineOpts = append(engineOpts, DefaultDevEngineOpts)
	engineOpts = append(engineOpts, opts...)

	var cacheVolumeName string
	if len(opts) > 0 {
		for _, opt := range opts {
			if opt.Name != "" {
				cacheVolumeName = opt.Name
			}
		}
	}
	if cacheVolumeName != "" {
		cacheVolumeName = "dagger-dev-engine-state-" + cacheVolumeName
	} else {
		cacheVolumeName = "dagger-dev-engine-state"
	}

	cacheVolumeName = cacheVolumeName + identity.NewID()

	devEngine := devEngineContainer(c, runtime.GOARCH, "", engineOpts...)

	devEngine = devEngine.WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithMountedCache("/var/lib/dagger", c.CacheVolume(cacheVolumeName)).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	return devEngine
}

// DevEngineContainer returns a container that runs a dev engine
func DevEngineContainer(c *dagger.Client, arches []string, version string, opts ...DevEngineOpts) []*dagger.Container {
	return devEngineContainers(c, arches, version, opts...)
}

func devEngineContainer(c *dagger.Client, arch string, version string, opts ...DevEngineOpts) *dagger.Container {
	engineConfig, err := getConfig(opts...)
	if err != nil {
		panic(err)
	}
	engineEntrypoint, err := getEntrypoint(opts...)
	if err != nil {
		panic(err)
	}
	return c.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From("alpine:"+alpineVersion).
		WithExec([]string{
			"apk", "add", "--no-cache",
			// for Buildkit
			"git", "openssh", "pigz", "xz",
			// for CNI
			"iptables", "ip6tables", "dnsmasq",
		}).
		WithFile("/usr/local/bin/runc", runcBin(c, arch), dagger.ContainerWithFileOpts{
			Permissions: 0o700,
		}).
		WithFile("/usr/local/bin/buildctl", buildctlBin(c, arch)).
		WithFile("/usr/local/bin/"+shimBinName, shimBin(c, arch)).
		WithFile("/usr/local/bin/"+engineBinName, engineBin(c, arch, version)).
		WithFile("/usr/local/bin/"+daggerBinName, daggerBin(c, arch, version)).
		WithFile(consts.GoSDKEngineContainerTarballPath, goSDKImageTarBall(c, arch)).
		WithDirectory(filepath.Dir(consts.PythonSDKEngineContainerModulePath), pythonSDK(c)).
		WithDirectory("/usr/local/bin", qemuBins(c, arch)).
		WithDirectory("/", cniPlugins(c, arch)).
		WithDirectory(EngineDefaultStateDir, c.Directory()).
		WithNewFile(engineTomlPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineConfig,
			Permissions: 0o600,
		}).
		WithNewFile(engineEntrypointPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineEntrypoint,
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"dagger-entrypoint.sh"})
}

func devEngineContainers(c *dagger.Client, arches []string, version string, opts ...DevEngineOpts) []*dagger.Container {
	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		platformVariants = append(platformVariants, devEngineContainer(c, arch, version, opts...))
	}

	return platformVariants
}

// helper functions for building the dev engine container

func pythonSDK(c *dagger.Client) *dagger.Directory {
	return c.Host().Directory("sdk/python", dagger.HostDirectoryOpts{
		Include: []string{
			"pyproject.toml",
			"src/**/*.py",
			"src/**/*.typed",
			"runtime/",
			"LICENSE",
			"README.md",
		},
	})
}

func goSDKImageTarBall(c *dagger.Client, arch string) *dagger.File {
	// TODO: update this to use Container.AsTarball once released
	ctx := context.Background()
	tmpDir, err := os.MkdirTemp("", "dagger-go-sdk")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)
	tarballPath := filepath.Join(tmpDir, filepath.Base(consts.GoSDKEngineContainerTarballPath))

	_, err = c.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		WithFile("/usr/local/bin/codegen", goSDKCodegenBin(c, arch)).
		WithEntrypoint([]string{"/usr/local/bin/codegen"}).
		Export(ctx, tarballPath)
	if err != nil {
		panic(err)
	}

	f, err := c.Host().File(tarballPath).Sync(ctx)
	if err != nil {
		panic(err)
	}
	return f
}

func goSDKCodegenBin(c *dagger.Client, arch string) *dagger.File {
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/codegen",
			"./cmd/codegen",
		}).
		File("./bin/codegen")
}

func cniPlugins(c *dagger.Client, arch string) *dagger.Directory {
	// We build the CNI plugins from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	ctr := c.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		WithExec([]string{"apk", "add", "build-base", "go", "git"}).
		WithMountedCache("/root/go/pkg/mod", c.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", c.CacheVolume("go-build")).
		WithMountedDirectory("/src", c.Git("github.com/containernetworking/plugins").Tag(cniVersion).Tree()).
		WithWorkdir("/src").
		WithEnvVariable("GOARCH", arch)

	pluginDir := c.Directory().WithFile("/opt/cni/bin/dnsname", dnsnameBinary(c, arch))
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

func dnsnameBinary(c *dagger.Client, arch string) *dagger.File {
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/dnsname",
			"-ldflags", "-s -w",
			"/app/cmd/dnsname",
		}).
		File("./bin/dnsname")
}

func buildctlBin(c *dagger.Client, arch string) *dagger.File {
	/* TODO: the commented code is what we *should* be doing, but need this PR to be merged:
	https://github.com/moby/buildkit/pull/4318

	The reason being that when we build buildctl using our go.mod, we end up
	with conflicting otel deps w/ buildkit's upstream go.mod, which can cause
	buildctl to crash.

	So for now, we are falling back to just using buildctl from upstream's
	image. This is okay temporarily because we don't need any customizations
	to it.

	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/buildctl",
			"-ldflags", "-s -w",
			"github.com/moby/buildkit/cmd/buildctl",
		}).
		File("./bin/buildctl")
	*/

	return c.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From("moby/buildkit:master@sha256:5d05b3dc8dbab4422d3017014e47322b0a6168a5a2f88928baf9c607e3ac9fe1").
		File("/usr/bin/buildctl")
}

func runcBin(c *dagger.Client, arch string) *dagger.File {
	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	return c.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
		WithEnvVariable("GOARCH", arch).
		WithEnvVariable("BUILDPLATFORM", "linux/"+runtime.GOARCH).
		WithEnvVariable("TARGETPLATFORM", "linux/"+arch).
		WithEnvVariable("CGO_ENABLED", "1").
		WithExec([]string{"apk", "add", "clang", "lld", "git", "pkgconf"}).
		WithDirectory("/", c.Container().From("tonistiigi/xx:1.2.1").Rootfs()).
		WithExec([]string{"xx-apk", "update"}).
		WithExec([]string{"xx-apk", "add", "build-base", "pkgconf", "libseccomp-dev", "libseccomp-static"}).
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
		WithMountedCache("/root/.cache/go-build", c.CacheVolume("go-build")).
		WithMountedDirectory("/src", c.Git("github.com/opencontainers/runc").Tag(runcVersion).Tree()).
		WithWorkdir("/src").
		WithExec([]string{"xx-go", "build", "-trimpath", "-buildmode=pie", "-tags", "seccomp netgo osusergo", "-ldflags", "-X main.version=" + runcVersion + " -linkmode external -extldflags -static-pie", "-o", "runc", "."}).
		File("runc")
}

func shimBin(c *dagger.Client, arch string) *dagger.File {
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/" + shimBinName,
			"-ldflags", "-s -w",
			"/app/cmd/shim",
		}).
		File("./bin/" + shimBinName)
}

func engineBin(c *dagger.Client, arch string, version string) *dagger.File {
	buildArgs := []string{
		"go", "build",
		"-o", "./bin/" + engineBinName,
		"-ldflags",
	}
	ldflags := []string{"-s", "-w"}
	if version != "" {
		ldflags = append(ldflags, "-X", "github.com/dagger/dagger/engine.Version="+version)
	}
	buildArgs = append(buildArgs, strings.Join(ldflags, " "))
	buildArgs = append(buildArgs, "/app/cmd/engine")
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec(buildArgs).
		File("./bin/" + engineBinName)
}

func daggerBin(c *dagger.Client, arch string, version string) *dagger.File {
	buildArgs := []string{
		"go", "build",
		"-o", "./bin/" + daggerBinName,
		"-ldflags",
	}
	ldflags := []string{"-s", "-w"}
	if version != "" {
		ldflags = append(ldflags, "-X", "github.com/dagger/dagger/engine.Version="+version)
	}
	buildArgs = append(buildArgs, strings.Join(ldflags, " "))
	buildArgs = append(buildArgs, "/app/cmd/dagger")
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		// dagger CLI must be statically linked, because it gets mounted into
		// containers when nesting is enabled
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec(buildArgs).
		File("./bin/" + daggerBinName)
}

func qemuBins(c *dagger.Client, arch string) *dagger.Directory {
	return c.
		Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From(qemuBinImage).
		Rootfs()
}
