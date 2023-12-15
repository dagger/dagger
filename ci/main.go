package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type DaggerCI struct {
}

const (
	engineBinName = "dagger-engine"
	shimBinName   = "dagger-shim"
	goVersion     = "1.20.6"
	alpineVersion = "3.18"
	runcVersion   = "v1.1.5"
	cniVersion    = "v1.2.0"
	qemuBinImage  = "tonistiigi/binfmt:buildkit-v7.1.0-30@sha256:45dd57b4ba2f24e2354f71f1e4e51f073cb7a28fd848ce6f5f2a7701142a6bf0" // nolint:gosec

	engineDefaultStateDir = "/var/lib/dagger"
	engineTomlPath        = "/etc/dagger/engine.toml"
	engineEntrypointPath  = "/usr/local/bin/dagger-entrypoint.sh"
	engineDefaultSockPath = "/var/run/buildkit/buildkitd.sock"
	devEngineListenPort   = 1234
)

func (*DaggerCI) CLI(ctx context.Context, version string, debug bool) (*File, error) {
	// TODO(vito)
	return nil, errors.New("not implemented")
}

type EngineOpts struct {
	Version               string
	TraceLogs             bool
	PrivilegedExecEnabled bool
}

func (*DaggerCI) EngineContainer(ctx context.Context, opts *EngineOpts) (*Container, error) {
	// TODO(vito)
	return nil, errors.New("not implemented")
}

func (*DaggerCI) EngineTests(ctx context.Context) error {
	devEngine := devEngineContainer()

	// This creates an engine.tar container file that can be used by the integration tests.
	// In particular, it is used by core/integration/remotecache_test.go to create a
	// dev engine that can be used to test remote caching.
	// I also load the dagger binary, so that the remote cache tests can use it to
	// run dagger queries.
	tmpDir, err := os.MkdirTemp("", "dagger-dev-engine-*")
	if err != nil {
		return err
	}

	engineTarPath := filepath.Join(tmpDir, "engine.tar")
	_, err = devEngine.Export(ctx, engineTarPath)
	if err != nil {
		return fmt.Errorf("failed to export dev engine: %w", err)
	}

	testEngineUtils := dag.Host().Directory(tmpDir, HostDirectoryOpts{
		Include: []string{"engine.tar"},
	}).WithFile("/dagger", daggerCLI(), DirectoryWithFileOpts{
		Permissions: 0755,
	})

	registrySvc := registry()
	devEngine = devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(devEngineListenPort, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithMountedCache(engineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state")).
		WithExec(nil, ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	endpoint, err := devEngine.Endpoint(ctx, ContainerEndpointOpts{Port: devEngineListenPort, Scheme: "tcp"})
	if err != nil {
		return fmt.Errorf("failed to get dev engine endpoint: %w", err)
	}

	cgoEnabledEnv := "0"
	args := []string{
		"gotestsum",
		"--format", "testname",
		"--no-color=false",
		"--jsonfile=./tests.log",
		"--",
		// go test flags
		"-parallel=16",
		"-count=1",
		"-timeout=15m",
	}

	/* TODO: re-add support
	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}
	*/

	args = append(args, "./...")
	cliBinPath := "/.dagger-cli"

	utilDirPath := "/dagger-dev"
	_, err = goBase().
		WithExec([]string{"go", "install", "gotest.tools/gotestsum@v1.10.0"}).
		WithMountedDirectory("/app", dag.Host().Directory(".")). // need all the source for extension tests
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithWorkdir("/app").
		WithServiceBinding("dagger-engine", devEngine).
		WithServiceBinding("registry", registrySvc).
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithMountedFile(cliBinPath, daggerCLI()).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithExec(args).
		WithFocus().
		WithExec([]string{"gotestsum", "tool", "slowest", "--jsonfile=./tests.log", "--threshold=1s"}).
		Sync(ctx)
	return err
}

func daggerCLI() *File {
	return goBase().
		WithExec(
			[]string{"go", "build", "-o", "./bin/dagger", "-ldflags", "-s -w", "./cmd/dagger"},
		).
		File("./bin/dagger")
}

func devEngineContainer() *Container {
	return dag.Container().
		From("alpine:"+alpineVersion).
		WithoutDefaultArgs().
		WithExec([]string{
			"apk", "add",
			// for Buildkit
			"git", "openssh", "pigz", "xz",
			// for CNI
			"iptables", "ip6tables", "dnsmasq",
		}).
		WithFile("/usr/local/bin/runc", runcBin(), ContainerWithFileOpts{
			Permissions: 0o700,
		}).
		WithFile("/usr/local/bin/buildctl", buildctlBin()).
		WithFile("/usr/local/bin/"+shimBinName, shimBin()).
		WithFile("/usr/local/bin/"+engineBinName, engineBin("")).
		WithDirectory("/usr/local/bin", qemuBins()).
		WithDirectory("/opt/cni/bin", cniPlugins()).
		WithDirectory(engineDefaultStateDir, dag.Directory()).
		WithNewFile(engineTomlPath, ContainerWithNewFileOpts{
			Contents:    devEngineConfig(),
			Permissions: 0o600,
		}).
		WithNewFile(engineEntrypointPath, ContainerWithNewFileOpts{
			Contents:    devEngineEntrypoint(),
			Permissions: 0o755,
		}).
		WithEntrypoint([]string{"dagger-entrypoint.sh"})
}

func baseEngineEntrypoint() string {
	const engineEntrypointCgroupSetup = `# cgroup v2: enable nesting
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
`

	builder := strings.Builder{}
	builder.WriteString("#!/bin/sh\n")
	builder.WriteString("set -exu\n")
	builder.WriteString(engineEntrypointCgroupSetup)
	builder.WriteString(fmt.Sprintf(`exec /usr/local/bin/%s --config %s `, engineBinName, engineTomlPath))
	return builder.String()
}

func devEngineEntrypoint() string {
	builder := strings.Builder{}
	builder.WriteString(baseEngineEntrypoint())
	builder.WriteString(`--network-name dagger-devenv --network-cidr 10.89.0.0/16 "$@"` + "\n")
	return builder.String()
}

func baseEngineConfig() string {
	builder := strings.Builder{}
	builder.WriteString("debug = true\n")
	builder.WriteString(fmt.Sprintf("root = %q\n", engineDefaultStateDir))
	builder.WriteString(`insecure-entitlements = ["security.insecure"]` + "\n")
	return builder.String()
}

func devEngineConfig() string {
	builder := strings.Builder{}
	builder.WriteString(baseEngineConfig())

	builder.WriteString("[grpc]\n")
	builder.WriteString(fmt.Sprintf("\taddress=[\"unix://%s\", \"tcp://0.0.0.0:%d\"]\n", engineDefaultSockPath, devEngineListenPort))

	builder.WriteString("[registry.\"docker.io\"]\n")
	builder.WriteString("\tmirrors = [\"mirror.gcr.io\"]\n")

	builder.WriteString("[registry.\"registry:5000\"]\n")
	builder.WriteString("\thttp = true\n")

	builder.WriteString("[registry.\"privateregistry:5000\"]\n")
	builder.WriteString("\thttp = true\n")

	return builder.String()
}

func repositoryGoCodeOnly() *Directory {
	return dag.Directory().WithDirectory("/", dag.Host().Directory("."), DirectoryWithDirectoryOpts{
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
		},
	})
}

func goBase() *Container {
	repo := repositoryGoCodeOnly()

	return dag.Container().
		From(fmt.Sprintf("golang:%s-alpine%s", goVersion, alpineVersion)).
		// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
		WithExec([]string{"apk", "add", "build-base"}).
		WithEnvVariable("CGO_ENABLED", "0").
		// adding the git CLI to inject vcs info into the go binaries
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", repo, ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", repo).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build"))
}

func runcBin() *File {
	return dag.HTTP(fmt.Sprintf(
		"https://github.com/opencontainers/runc/releases/download/%s/runc.%s",
		runcVersion,
		runtime.GOARCH,
	))
}

func buildctlBin() *File {
	return goBase().
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/buildctl",
			"-ldflags", "-s -w",
			"github.com/moby/buildkit/cmd/buildctl",
		}).
		File("./bin/buildctl")
}

func shimBin() *File {
	return goBase().
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/" + shimBinName,
			"-ldflags", "-s -w",
			"/app/cmd/shim",
		}).
		File("./bin/" + shimBinName)
}

func engineBin(version string) *File {
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
	return goBase().
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithExec(buildArgs).
		File("./bin/" + engineBinName)
}

func qemuBins() *Directory {
	return dag.Container().
		From(qemuBinImage).
		Rootfs()
}

func cniPlugins() *Directory {
	cniURL := fmt.Sprintf(
		"https://github.com/containernetworking/plugins/releases/download/%s/cni-plugins-%s-%s-%s.tgz",
		cniVersion, "linux", runtime.GOARCH, cniVersion,
	)

	return dag.Container().
		From("alpine:"+alpineVersion).
		WithMountedFile("/tmp/cni-plugins.tgz", dag.HTTP(cniURL)).
		WithDirectory("/opt/cni/bin", dag.Directory()).
		WithExec([]string{
			"tar", "-xzf", "/tmp/cni-plugins.tgz",
			"-C", "/opt/cni/bin",
			// only unpack plugins we actually need
			"./bridge", "./firewall", // required by dagger network stack
			"./loopback", "./host-local", // implicitly required (container fails without them)
		}).
		WithFile("/opt/cni/bin/dnsname", dnsnameBinary()).
		Directory("/opt/cni/bin")
}

func dnsnameBinary() *File {
	return goBase().
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/dnsname",
			"-ldflags", "-s -w",
			"/app/cmd/dnsname",
		}).
		File("./bin/dnsname")
}

func registry() *Container {
	return dag.Pipeline("registry").Container().From("registry:2").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil)
}

func privateRegistry() *Container {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return dag.Pipeline("private registry").Container().From("registry:2").
		WithNewFile("/auth/htpasswd", ContainerWithNewFileOpts{Contents: htpasswd}).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil)
}
