package util

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"sort"
	"text/template"

	"dagger.io/dagger"
	"golang.org/x/exp/maps"
)

const (
	engineBinName = "dagger-engine"
	shimBinName   = "dagger-shim"
	alpineVersion = "3.17"
	runcVersion   = "v1.1.5"
	cniVersion    = "v1.2.0"
	qemuBinImage  = "tonistiigi/binfmt:buildkit-v7.1.0-30@sha256:45dd57b4ba2f24e2354f71f1e4e51f073cb7a28fd848ce6f5f2a7701142a6bf0"

	engineTomlPath = "/etc/dagger/engine.toml"
	// NOTE: this needs to be consistent with DefaultStateDir in internal/engine/docker.go
	EngineDefaultStateDir = "/var/lib/dagger"

	engineEntrypointPath = "/usr/local/bin/dagger-entrypoint.sh"

	CacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	ServicesDNSEnvName = "_EXPERIMENTAL_DAGGER_SERVICES_DNS"
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

func CIDevEngineContainerAndEndpoint(ctx context.Context, c *dagger.Client, opts ...DevEngineOpts) (*dagger.Container, string, error) {
	devEngine := CIDevEngineContainer(c, opts...)

	endpoint, err := devEngine.Endpoint(ctx, dagger.ContainerEndpointOpts{Port: 1234, Scheme: "tcp"})
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

	devEngine := devEngineContainer(c, runtime.GOARCH, engineOpts...)

	devEngine = devEngine.WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithMountedCache("/var/lib/dagger", c.CacheVolume(cacheVolumeName)).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	return devEngine
}

// DevEngineContainer returns a container that runs a dev engine
func DevEngineContainer(c *dagger.Client, arches []string, opts ...DevEngineOpts) []*dagger.Container {
	return devEngineContainers(c, arches, opts...)
}

func devEngineContainer(c *dagger.Client, arch string, opts ...DevEngineOpts) *dagger.Container {
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
			"apk", "add",
			// for Buildkit
			"git", "openssh", "pigz", "xz",
			// for CNI
			"iptables", "ip6tables", "dnsmasq",
		}).
		WithFile("/usr/local/bin/runc", runcBin(c, arch), dagger.ContainerWithFileOpts{
			Permissions: 0700,
		}).
		WithFile("/usr/local/bin/buildctl", buildctlBin(c, arch)).
		WithFile("/usr/local/bin/"+shimBinName, shimBin(c, arch)).
		WithFile("/usr/local/bin/"+engineBinName, engineBin(c, arch)).
		WithDirectory("/usr/local/bin", qemuBins(c, arch)).
		WithDirectory("/opt/cni/bin", cniPlugins(c, arch)).
		WithDirectory(EngineDefaultStateDir, c.Directory()).
		WithNewFile(engineTomlPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineConfig,
			Permissions: 0600,
		}).
		WithNewFile(engineEntrypointPath, dagger.ContainerWithNewFileOpts{
			Contents:    engineEntrypoint,
			Permissions: 755,
		}).
		WithEntrypoint([]string{"dagger-entrypoint.sh"})
}

func devEngineContainers(c *dagger.Client, arches []string, opts ...DevEngineOpts) []*dagger.Container {
	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		platformVariants = append(platformVariants, devEngineContainer(c, arch, opts...))
	}

	return platformVariants
}

// helper functions for building the dev engine container

func cniPlugins(c *dagger.Client, arch string) *dagger.Directory {
	cniURL := fmt.Sprintf(
		"https://github.com/containernetworking/plugins/releases/download/%s/cni-plugins-%s-%s-%s.tgz",
		cniVersion, "linux", arch, cniVersion,
	)

	return c.Container().
		From("alpine:"+alpineVersion).
		WithMountedFile("/tmp/cni-plugins.tgz", c.HTTP(cniURL)).
		WithDirectory("/opt/cni/bin", c.Directory()).
		WithExec([]string{
			"tar", "-xzf", "/tmp/cni-plugins.tgz",
			"-C", "/opt/cni/bin",
			// only unpack plugins we actually need
			"./bridge", "./firewall", // required by dagger network stack
			"./loopback", "./host-local", // implicitly required (container fails without them)
		}).
		WithFile("/opt/cni/bin/dnsname", dnsnameBinary(c, arch)).
		Directory("/opt/cni/bin")
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
}

func runcBin(c *dagger.Client, arch string) *dagger.File {
	return c.HTTP(fmt.Sprintf(
		"https://github.com/opencontainers/runc/releases/download/%s/runc.%s",
		runcVersion,
		arch,
	))
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

func engineBin(c *dagger.Client, arch string) *dagger.File {
	return goBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/" + engineBinName,
			"-ldflags", "-s -w",
			"/app/cmd/engine",
		}).
		File("./bin/" + engineBinName)
}

func qemuBins(c *dagger.Client, arch string) *dagger.Directory {
	return c.
		Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From(qemuBinImage).
		Rootfs()
}
