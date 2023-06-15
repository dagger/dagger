package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func engineClientContainer(ctx context.Context, t *testing.T, c *dagger.Client, devEngine *dagger.Service) (*dagger.Container, error) {
	daggerCli := daggerCliFile(t, c)

	cliBinPath := "/bin/dagger"
	endpoint, err := devEngine.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
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
		Sync(ctx)
	require.NoError(t, err)
}

func TestClientWaitsForEngine(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	devEngine := devEngineContainer(c).WithoutExposedPort(1234, dagger.ContainerWithoutExposedPortOpts{Protocol: dagger.Tcp})
	entrypoint, err := devEngine.File("/usr/local/bin/dagger-entrypoint.sh").Contents(ctx)

	require.NoError(t, err)
	before, after, found := strings.Cut(entrypoint, "set -e")
	require.True(t, found, "missing set -e in entrypoint")
	entrypoint = before + "set -e \n" + "sleep 30\n" + after

	devEngineSvc := devEngine.
		WithNewFile("/usr/local/bin/dagger-entrypoint.sh", dagger.ContainerWithNewFileOpts{
			Contents:    entrypoint,
			Permissions: 0700,
		}).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		Service(nil, dagger.ContainerServiceOpts{
			InsecureRootCapabilities: true,
		})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
	require.NoError(t, err)
	_, err = clientCtr.
		WithNewFile("/query.graphql", dagger.ContainerWithNewFileOpts{
			Contents: `{ defaultPlatform }`}). // arbitrary valid query
		WithExec([]string{"time", "dagger", "query", "--debug", "--doc", "/query.graphql"}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).Sync(ctx)

	require.NoError(t, err)
}

func TestEngineSetsNameFromEnv(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	engineName := "my-special-engine"
	devEngineSvc := devEngineContainer(c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName).
		Service([]string{"--addr", "tcp://0.0.0.0:1234"}, dagger.ContainerServiceOpts{
			InsecureRootCapabilities: true,
		})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
	require.NoError(t, err)

	out, err := clientCtr.
		WithNewFile("/query.graphql", dagger.ContainerWithNewFileOpts{
			Contents: `{ defaultPlatform }`}). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Connected to engine "+engineName)
}
