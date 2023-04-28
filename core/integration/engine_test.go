package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func devEngineContainer(c *dagger.Client) *dagger.Container {
	// This loads the engine.tar file from the host into the container, that was set up by
	// internal/mage/engine.go:test or by ./hack/dev. This is used to spin up additional dev engines.
	var tarPath string
	if v, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR"); ok {
		tarPath = v
	} else {
		tarPath = "./bin/engine.tar"
	}
	parentDir := filepath.Dir(tarPath)
	tarFileName := filepath.Base(tarPath)
	devEngineTar := c.Host().Directory(parentDir, dagger.HostDirectoryOpts{Include: []string{tarFileName}}).File(tarFileName)
	return c.Container().Import(devEngineTar).
		WithEnvVariable("GOTRACEBACK", "all"). // if something goes wrong, dump all the goroutines
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp})
}

func engineClientContainer(ctx context.Context, c *dagger.Client, devEngine *dagger.Container) (*dagger.Container, error) {
	cliPath := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	if cliPath == "" {
		return nil, fmt.Errorf("missing _EXPERIMENTAL_DAGGER_CLI_BIN")
	}
	parentDir := filepath.Dir(cliPath)
	baseName := filepath.Base(cliPath)
	daggerCli := c.Host().Directory(parentDir, dagger.HostDirectoryOpts{Include: []string{baseName}}).File(baseName)

	cliBinPath := "/bin/dagger"
	endpoint, err := devEngine.Endpoint(ctx, dagger.ContainerEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	return c.Container().From("alpine:3.17").
		WithServiceBinding("dev-engine", devEngine).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint), nil
}

func TestEngineExitsZeroOnSignal(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	// engine should shutdown with exit code 0 when receiving SIGTERM
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := devEngineContainer(c).
		WithNewFile("/usr/local/bin/dagger-entrypoint.sh", dagger.ContainerWithNewFileOpts{
			Contents: `#!/bin/sh
set -ex
/usr/local/bin/dagger-engine --debug &
engine_pid=$!

sleep 5
kill -TERM $engine_pid
wait $engine_pid
exit $?
`,
			Permissions: 0700,
		}).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		ExitCode(ctx)
	require.NoError(t, err)
}

func TestEngineSetsNameFromEnv(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	// engine should shutdown with exit code 0 when receiving SIGTERM
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	engineName := "my-special-engine"
	devEngine := devEngineContainer(c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName).
		WithExec([]string{"--addr", "tcp://0.0.0.0:1234"}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	clientCtr, err := engineClientContainer(ctx, c, devEngine)
	require.NoError(t, err)

	out, err := clientCtr.
		WithNewFile("/query.graphql", dagger.ContainerWithNewFileOpts{
			Contents: `{ defaultPlatform }`}). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Connected to engine "+engineName)
}
