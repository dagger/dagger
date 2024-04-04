package main

import (
	"context"
	"dagger/util"
	"path/filepath"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
)

type Test struct {
	Dagger *Dagger // +private

	CacheConfig string // +private
}

func (t *Test) WithCache(config string) *Test {
	clone := *t
	clone.CacheConfig = config
	return &clone
}

// Run all engine tests
func (t *Test) All(
	ctx context.Context,
	// +optional
	race bool,
) error {
	return t.test(ctx, race, "", "./...")
}

// Run "important" engine tests
func (t *Test) Important(
	ctx context.Context,
	// +optional
	race bool,
) error {
	// These tests give good basic coverage of functionality w/out having to run everything
	return t.test(ctx, race, `^(TestModule|TestContainer)`, "./...")
}

// Run custom engine tests
func (t *Test) Custom(
	ctx context.Context,
	run string,
	// +optional
	// +default="./..."
	pkg string,
	// +optional
	race bool,
) error {
	return t.test(ctx, race, run, pkg)
}

func (t *Test) test(
	ctx context.Context,
	race bool,
	testRegex string,
	pkg string,
) error {
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
		"-timeout=30m",
	}

	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}

	if testRegex != "" {
		args = append(args, "-run", testRegex)
	}

	args = append(args, pkg)

	cmd, err := t.testCmd(ctx)
	if err != nil {
		return err
	}

	_, err = cmd.
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithExec(args).
		WithExec([]string{"gotestsum", "tool", "slowest", "--jsonfile=./tests.log", "--threshold=1s"}).
		Sync(ctx)
	return err
}

func (t *Test) testCmd(ctx context.Context) (*Container, error) {
	engine := t.Dagger.Engine().
		WithConfig(`registry."registry:5000"`, `http = true`).
		WithConfig(`registry."privateregistry:5000"`, `http = true`).
		WithConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		WithConfig(`grpc`, `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`)
	devEngine, err := engine.Container(ctx, "")
	if err != nil {
		return nil, err
	}

	devBinary, err := t.Dagger.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}

	// This creates an engine.tar container file that can be used by the integration tests.
	// In particular, it is used by core/integration/remotecache_test.go to create a
	// dev engine that can be used to test remote caching.
	// I also load the dagger binary, so that the remote cache tests can use it to
	// run dagger queries.

	// These are used by core/integration/remotecache_test.go
	testEngineUtils := dag.Directory().
		WithFile("engine.tar", devEngine.AsTarball()).
		WithFile("dagger", devBinary, DirectoryWithFileOpts{
			Permissions: 0755,
		})

	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+identity.NewID())).
		WithExec(nil, ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		AsService()

	endpoint, err := devEngineSvc.Endpoint(ctx, ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinPath := "/.dagger-cli"

	utilDirPath := "/dagger-dev"
	tests := util.GoBase(t.Dagger.Source).
		WithExec([]string{"go", "install", "gotest.tools/gotestsum@v1.10.0"}).
		WithMountedDirectory("/app", t.Dagger.Source). // need all the source for extension tests
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithWorkdir("/app").
		WithServiceBinding("dagger-engine", devEngineSvc).
		WithServiceBinding("registry", registrySvc)

	if t.CacheConfig != "" {
		tests = tests.WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", t.CacheConfig)
	}

	return tests.
			WithMountedFile(cliBinPath, devBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint),
		// TODO: re-enable this, to avoid rate-limiting
		// introduced in https://github.com/dagger/dagger/commit/20241ba7a8d39da8f6ff185d512953b6b0ec1a55
		// WithMountedDirectory("/root/.docker", util.HostDockerDir(dag)),
		nil
}

func registry() *Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}

func privateRegistry() *Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return dag.Container().
		From("registry:2").
		WithNewFile("/auth/htpasswd", ContainerWithNewFileOpts{Contents: htpasswd}).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}
