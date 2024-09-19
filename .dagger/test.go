package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/identity"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/engine/distconsts"
)

type Test struct {
	Dagger *DaggerDev // +private

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
	return t.test(ctx, "", "", "./...", failfast, parallel, timeout, race, 1)
}

// List all tests
func (t *Test) List(ctx context.Context) (string, error) {
	cmd, err := t.testCmd(ctx)
	if err != nil {
		return "", err
	}

	return cmd.
		WithExec([]string{"sh", "-c", "go test -list=. ./... | grep ^Test | sort"}).
		Stdout(ctx)
}

func (t *Test) test(
	ctx context.Context,
	runTestRegex string,
	skipTestRegex string,
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
		"-v",
	}

	// Add ldflags
	ldflags := []string{
		"-X", "github.com/dagger/dagger/engine.Version=" + t.Dagger.Version.String(),
		"-X", "github.com/dagger/dagger/engine.Tag=" + t.Dagger.Tag,
	}
	args = append(args, "-ldflags", strings.Join(ldflags, " "))

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

	if runTestRegex != "" {
		args = append(args, "-run", runTestRegex)
	}

	if skipTestRegex != "" {
		args = append(args, "-skip", skipTestRegex)
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

func (t *Test) testCmd(ctx context.Context) (*dagger.Container, error) {
	engine := t.Dagger.Engine().
		WithConfig(`registry."registry:5000"`, `http = true`).
		WithConfig(`registry."privateregistry:5000"`, `http = true`).
		WithConfig(`registry."docker.io"`, `mirrors = ["mirror.gcr.io"]`).
		WithConfig(`grpc`, `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`).
		WithArg(`debugaddr`, `0.0.0.0:6060`)
	devEngine, err := engine.Container(ctx)
	if err != nil {
		return nil, err
	}

	devBinary, err := t.Dagger.CLI().Binary(ctx, "")
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
		WithFile("dagger", devBinary, dagger.DirectoryWithFileOpts{
			Permissions: 0755,
		})

	registrySvc := registry()
	devEngineSvc := devEngine.
		WithServiceBinding("registry", registrySvc).
		WithServiceBinding("privateregistry", privateRegistry()).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, dag.CacheVolume("dagger-dev-engine-test-state"+identity.NewID())).
		WithExec(nil, dagger.ContainerWithExecOpts{
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		}).
		AsService()

	endpoint, err := devEngineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
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
	if t.Dagger.DockerCfg != nil {
		// this avoids rate limiting in our ci tests
		tests = tests.WithMountedSecret("/root/.docker/config.json", t.Dagger.DockerCfg)
	}
	return tests, nil
}

func registry() *dagger.Service {
	return dag.Container().
		From("registry:2").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec(nil, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
		AsService()
}

func privateRegistry() *dagger.Service {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return dag.Container().
		From("registry:2").
		WithNewFile("/auth/htpasswd", htpasswd).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec(nil, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
		AsService()
}
