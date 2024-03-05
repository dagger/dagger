package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
)

// Lint lints the engine
func (ci *Dagger) Lint(ctx context.Context) error {
	_, err := dag.Container().
		From("golangci/golangci-lint:v1.55-alpine").
		WithMountedDirectory("/app", ci.repo().DirectoryForGo()).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		Sync(ctx)
	return err
}

func (ci *Dagger) CLI(ctx context.Context) (*File, error) {
	return ci.repo().DaggerBinary(ctx, "", "", "")
}

func (ci *Dagger) Engine(ctx context.Context) (*Container, error) {
	return ci.repo().DaggerEngine(ctx, "", nil, nil)
}

func (ci *Dagger) Dev(
	ctx context.Context,
	target *Directory, // +optional
) (*Container, error) {
	if target == nil {
		target = ci.repo().Directory
	}

	binary, err := ci.repo().DaggerBinary(ctx, "", "", "")
	if err != nil {
		return nil, err
	}

	svc, err := ci.EngineService(ctx)
	if err != nil {
		return nil, err
	}

	return ci.repo().GoBase().
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", binary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine").
		WithWorkdir("/mnt"), nil
}

func (ci *Dagger) EngineService(ctx context.Context) (*Service, error) {
	return ci.repo().DaggerEngineService(ctx, "foo", nil, nil, "", nil, []string{
		`grpc=address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`,
	})
}

// Test runs Engine tests
func (ci *Dagger) Test(ctx context.Context) error {
	return ci.test(ctx, false, "")
}

// TestRace runs Engine tests with go race detector enabled
func (ci *Dagger) TestRace(ctx context.Context) error {
	return ci.test(ctx, true, "")
}

// TestImportant runs Engine Container+Module tests, which give good basic coverage
// of functionality w/out having to run everything
func (ci *Dagger) TestImportant(ctx context.Context) error {
	return ci.test(ctx, true, `^(TestModule|TestContainer)`)
}

func (ci *Dagger) test(ctx context.Context, race bool, testRegex string) error {
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

	args = append(args, "./...")

	cmd, err := ci.testCmd(ctx)
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

func (ci *Dagger) testCmd(ctx context.Context) (*Container, error) {
	dag := dag.Pipeline("engine").Pipeline("test")

	configEntries := []string{
		`registry."registry:5000"=http = true`,
		`registry."privateregistry:5000"=http = true`,
		`grpc=address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`,
		`registry."docker.io"=mirrors = ["mirror.gcr.io"]`,
	}
	entrypointArgs := []string{
		"network-name=dagger-dev",
		"network-cidr=10.88.0.0/16",
	}
	devEngine, err := ci.repo().DaggerEngine(ctx, "", entrypointArgs, configEntries)
	if err != nil {
		return nil, err
	}
	devBinary, err := ci.repo().DaggerBinary(ctx, "", "", "")
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

	registrySvc := registry(dag)
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry(dag)).
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
	tests := ci.repo().GoBase().
		WithExec([]string{"go", "install", "gotest.tools/gotestsum@v1.10.0"}).
		WithMountedDirectory("/app", ci.repo().Directory). // need all the source for extension tests
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithWorkdir("/app").
		WithServiceBinding("dagger-engine", devEngineSvc).
		WithServiceBinding("registry", registrySvc)

	// TODO use Container.With() to set this. It'll be much nicer.
	cacheEnv, set := os.LookupEnv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG")
	if set {
		tests = tests.WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", cacheEnv)
	}

	return tests.
			WithMountedFile(cliBinPath, devBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint),
		// XXX: ...why is this necessary?
		// WithMountedDirectory("/root/.docker", util.HostDockerDir(dag)),
		nil
}

func registry(c *Client) *Service {
	return c.Pipeline("registry").Container().From("registry:2").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}

func privateRegistry(c *Client) *Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return c.Pipeline("private registry").Container().From("registry:2").
		WithNewFile("/auth/htpasswd", ContainerWithNewFileOpts{Contents: htpasswd}).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec(nil).
		AsService()
}
