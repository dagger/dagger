package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/template"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/exp/maps"

	"github.com/dagger/dagger/engine/distconsts"
)

const (
	engineServerPath    = "/usr/local/bin/dagger-engine"
	engineDialStdioPath = "/usr/local/bin/dial-stdio"
	engineShimPath      = distconsts.EngineShimPath

	golangVersion = "1.21.7"
	alpineVersion = "3.18"
	ubuntuVersion = "22.04"
	runcVersion   = "v1.1.12"
	cniVersion    = "v1.3.0"
	qemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"

	engineTomlPath = "/etc/dagger/engine.toml"

	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"

	CacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	GPUSupportEnvName  = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
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
		EngineBin:         engineServerPath,
		EngineConfig:      engineTomlPath,
		EntrypointArgs:    mergedOpts,
		EntrypointArgKeys: keys,
	})
	if err != nil {
		return "", err
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
		return "", err
	}
	config = buf.String()

	return config, nil
}

func CIDevEngineContainerAndEndpoint(ctx context.Context, c *dagger.Client, opts ...DevEngineOpts) (*dagger.Service, string, error) {
	devEngine, err := CIDevEngineContainer(ctx, c, opts...)
	if err != nil {
		return nil, "", err
	}
	devSvc := devEngine.AsService()

	endpoint, err := devSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, "", err
	}
	return devSvc, endpoint, nil
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

func CIDevEngineContainer(ctx context.Context, c *dagger.Client, opts ...DevEngineOpts) (*dagger.Container, error) {
	versionInfo, err := DevelVersionInfo(ctx, c)
	if err != nil {
		return nil, err
	}

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

	devEngine, err := devEngineContainer(ctx, c, runtime.GOARCH, versionInfo.EngineVersion(), engineOpts...)
	if err != nil {
		return nil, err
	}

	devEngine = devEngine.WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, c.CacheVolume(cacheVolumeName)).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	return devEngine, nil
}

// DevEngineContainer returns a container that runs a dev engine
func DevEngineContainer(ctx context.Context, c *dagger.Client, arches []string, version string, opts ...DevEngineOpts) ([]*dagger.Container, error) {
	return devEngineContainers(ctx, c, arches, version, opts...)
}

// DevEngineContainerWithGPUSUpport returns a container that runs a dev engine
func DevEngineContainerWithGPUSupport(ctx context.Context, c *dagger.Client, arches []string, version string, opts ...DevEngineOpts) ([]*dagger.Container, error) {
	return devEngineContainersWithGPUSupport(ctx, c, arches, version, opts...)
}

func devEngineContainer(ctx context.Context, c *dagger.Client, arch string, version string, opts ...DevEngineOpts) (*dagger.Container, error) {
	if version == "" {
		return nil, fmt.Errorf("engine version must be specified")
	}

	engineConfig, err := getConfig(opts...)
	if err != nil {
		return nil, fmt.Errorf("could not get engine config: %w")
	}
	engineEntrypoint, err := getEntrypoint(opts...)
	if err != nil {
		return nil, fmt.Errorf("could not get engine entrypoint: %w")
	}

	container := c.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
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
		WithFile("/usr/local/bin/runc", runcBin(c, arch), dagger.ContainerWithFileOpts{
			Permissions: 0o700,
		}).
		WithFile(engineShimPath, shimBin(c, arch, version)).
		WithFile(engineServerPath, engineBin(c, arch, version)).
		With(goSDKContent(ctx, c, arch)).
		With(pythonSDKContent(ctx, c, arch)).
		With(typescriptSDKContent(ctx, c, arch)).
		WithDirectory("/usr/local/bin", qemuBins(c, arch)).
		WithDirectory("/", cniPlugins(c, arch, false)).
		WithDirectory("/", dialstdioFiles(c, arch)).
		WithDirectory(distconsts.EngineDefaultStateDir, c.Directory()).
		WithNewFile(engineTomlPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineConfig,
			Permissions: 0o600,
		}).
		WithNewFile(engineEntrypointPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineEntrypoint,
			Permissions: 0o755,
		})
	return container.WithEntrypoint([]string{filepath.Base(engineEntrypointPath)}), nil
}

func devEngineContainerWithGPUSupport(ctx context.Context, c *dagger.Client, arch string, version string, opts ...DevEngineOpts) (*dagger.Container, error) {
	if arch != "amd64" {
		return nil, fmt.Errorf("unsupported architecture")
	}
	if version == "" {
		return nil, fmt.Errorf("engine version must be specified")
	}

	engineConfig, err := getConfig(opts...)
	if err != nil {
		return nil, fmt.Errorf("could not get engine config: %w")
	}
	engineEntrypoint, err := getEntrypoint(opts...)
	if err != nil {
		return nil, fmt.Errorf("could not get engine entrypoint: %w")
	}

	container := c.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From("ubuntu:"+ubuntuVersion).
		WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
		WithExec([]string{"apt-get", "update"}).
		WithExec([]string{
			"apt-get", "install", "-y",
			"iptables", "git", "dnsmasq-base", "network-manager",
			"gpg", "curl",
		}).
		WithFile("/usr/local/bin/runc", runcBin(c, arch), dagger.ContainerWithFileOpts{
			Permissions: 0o700,
		}).
		WithFile(engineShimPath, shimBin(c, arch, version)).
		WithFile(engineServerPath, engineBin(c, arch, version)).
		With(goSDKContent(ctx, c, arch)).
		With(pythonSDKContent(ctx, c, arch)).
		With(typescriptSDKContent(ctx, c, arch)).
		WithDirectory("/usr/local/bin", qemuBins(c, arch)).
		WithDirectory("/", cniPlugins(c, arch, true)).
		WithDirectory("/", dialstdioFiles(c, arch)).
		WithDirectory(distconsts.EngineDefaultStateDir, c.Directory()).
		WithNewFile(engineTomlPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineConfig,
			Permissions: 0o600,
		}).
		WithNewFile(engineEntrypointPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineEntrypoint,
			Permissions: 0o755,
		}).
		With(nvidiaSetup)

	return container.WithEntrypoint([]string{filepath.Base(engineEntrypointPath)}), nil
}

// install nvidia-container-toolkit in the container
func nvidiaSetup(ctr *dagger.Container) *dagger.Container {
	return ctr.
		With(shellExec(`curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg`)).
		With(shellExec(`curl -s -L https://nvidia.github.io/libnvidia-container/experimental/"$(. /etc/os-release;echo $ID$VERSION_ID)"/libnvidia-container.list | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list`)).
		With(shellExec(`apt-get update && apt-get install -y nvidia-container-toolkit`))
}

func shellExec(cmd string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec([]string{"sh", "-c", cmd})
	}
}

func devEngineContainers(ctx context.Context, c *dagger.Client, arches []string, version string, opts ...DevEngineOpts) ([]*dagger.Container, error) {
	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		ctr, err := devEngineContainer(ctx, c, arch, version, opts...)
		if err != nil {
			return nil, err
		}
		platformVariants = append(platformVariants, ctr)
	}

	return platformVariants, nil
}

func devEngineContainersWithGPUSupport(ctx context.Context, c *dagger.Client, arches []string, version string, opts ...DevEngineOpts) ([]*dagger.Container, error) {
	platformVariants := make([]*dagger.Container, 0, len(arches))
	// Restrict GPU images to amd64:
	ctr, err := devEngineContainerWithGPUSupport(ctx, c, "amd64", version, opts...)
	if err != nil {
		return nil, err
	}
	platformVariants = append(platformVariants, ctr)
	return platformVariants, nil
}

// helper functions for building the dev engine container

func pythonSDKContent(ctx context.Context, c *dagger.Client, arch string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		sdkCtrTarball := c.Container().
			WithRootfs(c.Host().Directory("sdk/python", dagger.HostDirectoryOpts{
				Include: []string{
					"pyproject.toml",
					"src/**/*.py",
					"src/**/*.typed",
					"runtime/",
					"LICENSE",
					"README.md",
				},
			})).
			WithFile("/codegen", goSDKCodegenBin(c, arch)).
			AsTarball(dagger.ContainerAsTarballOpts{
				ForcedCompression: dagger.Uncompressed,
			})

		sdkDir := c.Container().From("alpine:"+alpineVersion).
			WithMountedDirectory("/out", c.Directory()).
			WithMountedFile("/sdk.tar", sdkCtrTarball).
			WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
			Directory("/out")

		content, err := sdkContent(ctx, ctr, sdkDir, distconsts.PythonSDKManifestDigestEnvName)
		if err != nil {
			// FIXME: would be nice to not panic
			panic(err)
		}
		return content
	}
}

func typescriptSDKContent(ctx context.Context, c *dagger.Client, arch string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		sdkCtrTarball := c.Container().
			WithRootfs(c.Host().Directory("sdk/typescript", dagger.HostDirectoryOpts{
				Include: []string{
					"**/*.ts",
					"LICENSE",
					"README.md",
					"runtime",
					"package.json",
					"dagger.json",
				},
				Exclude: []string{
					"node_modules",
					"dist",
					"**/test",
					"**/*.spec.ts",
					"dev",
				},
			})).
			WithFile("/codegen", goSDKCodegenBin(c, arch)).
			AsTarball(dagger.ContainerAsTarballOpts{
				ForcedCompression: dagger.Uncompressed,
			})

		sdkDir := c.Container().From("alpine:"+alpineVersion).
			WithMountedDirectory("/out", c.Directory()).
			WithMountedFile("/sdk.tar", sdkCtrTarball).
			WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
			Directory("/out")

		content, err := sdkContent(ctx, ctr, sdkDir, distconsts.TypescriptSDKManifestDigestEnvName)
		if err != nil {
			// FIXME: would be nice to not panic
			panic(err)
		}
		return content
	}
}

func goSDKContent(ctx context.Context, c *dagger.Client, arch string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		base := c.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
			From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion))

		sdkCtrTarball := base.
			WithEnvVariable("GOTOOLCHAIN", "auto").
			WithFile("/usr/local/bin/codegen", goSDKCodegenBin(c, arch)).
			WithEntrypoint([]string{"/usr/local/bin/codegen"}).
			AsTarball(dagger.ContainerAsTarballOpts{
				ForcedCompression: dagger.Uncompressed,
			})

		sdkDir := base.
			WithMountedDirectory("/out", c.Directory()).
			WithMountedFile("/sdk.tar", sdkCtrTarball).
			WithExec([]string{"tar", "xf", "/sdk.tar", "-C", "/out"}).
			Directory("/out")

		content, err := sdkContent(ctx, ctr, sdkDir, distconsts.GoSDKManifestDigestEnvName)
		if err != nil {
			// FIXME: would be nice to not panic
			panic(err)
		}
		return content
	}
}

func sdkContent(ctx context.Context, ctr *dagger.Container, sdkDir *dagger.Directory, envName string) (*dagger.Container, error) {
	var index ocispecs.Index
	indexContents, err := sdkDir.File("index.json").Contents(ctx)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(indexContents), &index); err != nil {
		return nil, err
	}
	manifest := index.Manifests[0]
	manifestDgst := manifest.Digest.String()

	return ctr.
		WithEnvVariable(envName, manifestDgst).
		WithDirectory(distconsts.EngineContainerBuiltinContentDir, sdkDir, dagger.ContainerWithDirectoryOpts{
			Include: []string{"blobs/"},
		}), nil
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

func cniPlugins(c *dagger.Client, arch string, gpuSupportEnabled bool) *dagger.Directory {
	// We build the CNI plugins from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	// If GPU support is enabled use a Debian image:
	ctr := c.Container()
	if gpuSupportEnabled {
		// TODO: there's no guarantee the bullseye libc is compatible with the ubuntu image w/ rebase this onto
		ctr = ctr.From(fmt.Sprintf("golang:%s-bullseye", golangVersion)).
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "git", "build-essential"})
	} else {
		ctr = ctr.From(fmt.Sprintf("golang:%s-alpine%s", golangVersion, alpineVersion)).
			WithExec([]string{"apk", "add", "build-base", "go", "git"})
	}

	ctr = ctr.WithMountedCache("/root/go/pkg/mod", c.CacheVolume("go-mod")).
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

func dialstdioFiles(c *dagger.Client, arch string) *dagger.Directory {
	outDir := "/out"
	installPath := filepath.Dir(engineDialStdioPath)
	buildArgs := []string{
		"go", "build",
		"-o", filepath.Join(outDir, installPath, filepath.Base(engineDialStdioPath)),
		"-ldflags",
	}
	ldflags := []string{"-s", "-w"}
	buildArgs = append(buildArgs, strings.Join(ldflags, " "))
	buildArgs = append(buildArgs, "/app/cmd/dialstdio")

	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedDirectory(outDir, c.Directory()).
		WithExec(buildArgs).
		// include a symlink from buildctl to dialstdio to be compatible w/ connhelper implementations from buildkit
		WithExec([]string{"ln", "-s", filepath.Base(engineDialStdioPath), filepath.Join(outDir, installPath, "buildctl")}).
		Directory(outDir)
}

func runcBin(c *dagger.Client, arch string) *dagger.File {
	// We build runc from source to enable upgrades to go and other dependencies that
	// can contain CVEs in the builds on github releases
	buildCtr := c.Container().
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

func shimBin(c *dagger.Client, arch string, version string) *dagger.File {
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/" + filepath.Base(engineShimPath),
			"-ldflags", "-s -w -X github.com/dagger/dagger/engine.Version=" + version,
			"/app/cmd/shim",
		}).
		File("./bin/" + filepath.Base(engineShimPath))
}

func engineBin(c *dagger.Client, arch string, version string) *dagger.File {
	buildArgs := []string{
		"go", "build",
		"-o", "/app/bin/" + filepath.Base(engineServerPath),
	}

	ldflags := []string{
		"-s", "-w",
		"-X", "github.com/dagger/dagger/engine.Version=" + version,
	}
	buildArgs = append(buildArgs, "-ldflags", strings.Join(ldflags, " "))

	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithWorkdir("/app/cmd/engine").
		WithExec(buildArgs).
		File("/app/bin/" + filepath.Base(engineServerPath))
}

func qemuBins(c *dagger.Client, arch string) *dagger.Directory {
	return c.
		Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From(qemuBinImage).
		Rootfs()
}
