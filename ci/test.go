package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/buildkit/identity"

	"github.com/dagger/dagger/engine/distconsts"
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
	failfast bool,
	// +optional
	parallel int,
	// +optional
	timeout string,
	// +optional
	race bool,
) error {
	return t.test(ctx, "", "./...", failfast, parallel, timeout, race, 1)
}

// Run "important" engine tests
func (t *Test) Important(
	ctx context.Context,
	// +optional
	failfast bool,
	// +optional
	parallel int,
	// +optional
	timeout string,
	// +optional
	race bool,
) error {
	// These tests give good basic coverage of functionality w/out having to run everything
	return t.test(ctx, `^(TestModule|TestContainer)`, "./...", failfast, parallel, timeout, race, 1)
}

// Run custom engine tests
func (t *Test) Custom(
	ctx context.Context,
	run string,
	// +optional
	// +default="./..."
	pkg string,
	// +optional
	failfast bool,
	// +optional
	parallel int,
	// +optional
	timeout string,
	// +optional
	race bool,
	// +default=1
	// +optional
	count int,
) error {
	return t.test(ctx, run, pkg, failfast, parallel, timeout, race, count)
}

func (t *Test) test(
	ctx context.Context,
	testRegex string,
	pkg string,
	failfast bool,
	parallel int,
	timeout string,
	race bool,
	count int,
) error {
	cgoEnabledEnv := "0"
	args := []string{
		"go",
		"test",
	}

	// All following are go test flags
	if failfast {
		args = append(args, "-failfast")
	}

	// Go will default parallel to number of CPUs, so only pass if set
	if parallel != 0 {
		args = append(args, fmt.Sprintf("-parallel=%d", parallel))
	}

	// Default timeout to 30m
	// No test suite should take more than 30 minutes to run
	if timeout == "" {
		timeout = "30m"
	}
	args = append(args, fmt.Sprintf("-timeout=%s", timeout))

	if race {
		args = append(args, "-race")
		cgoEnabledEnv = "1"
	}

	// Disable test caching, since these are integration tests
	args = append(args, fmt.Sprintf("-count=%d", count))

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
		WithExec([]string{engineEntrypointPath}, ContainerWithExecOpts{
			// FIXME: Replace the entrypoint with the following line after
			// https://github.com/dagger/dagger/pull/7136 is released:
			// UseEntrypoint: true,
			InsecureRootCapabilities: true,
		}).
		AsService()

	endpoint, err := devEngineSvc.Endpoint(ctx, ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinPath := "/.dagger-cli"

	utilDirPath := "/dagger-dev"
	tests := t.Dagger.Go().Env().
		WithMountedDirectory(utilDirPath, testEngineUtils).
		WithEnvVariable("_DAGGER_TESTS_ENGINE_TAR", filepath.Join(utilDirPath, "engine.tar")).
		WithServiceBinding("dagger-engine", devEngineSvc).
		WithServiceBinding("registry", registrySvc)

	if t.CacheConfig != "" {
		tests = tests.WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", t.CacheConfig)
	}

	// TODO: should use c.Dagger.installer (but this currently can't connect to services)
	tests = tests.
		WithMountedFile(cliBinPath, devBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint)
	if t.Dagger.HostDockerConfig != nil {
		// this avoids rate limiting in our ci tests
		tests = tests.WithMountedSecret("/root/.docker/config.json", t.Dagger.HostDockerConfig)
	}
	return tests, nil
}

func registry() *Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000, ContainerWithExposedPortOpts{Protocol: Tcp}).
		WithExec([]string{"/entrypoint.sh", "/etc/docker/registry/config.yml"}, ContainerWithExecOpts{
			SkipEntrypoint: true,
			// FIXME: Replace the entrypoint with the following line after
			// https://github.com/dagger/dagger/pull/7136 is released:
			// UseEntrypoint: true,
		}).
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
		WithExec([]string{"/entrypoint.sh", "/etc/docker/registry/config.yml"}, ContainerWithExecOpts{
			SkipEntrypoint: true,
			// FIXME: Replace the entrypoint with the following line after
			// https://github.com/dagger/dagger/pull/7136 is released:
			// UseEntrypoint: true,
		}).
		AsService()
}
