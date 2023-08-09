package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

const alpineImage = "alpine:3.18.2"

func devEngineContainer(c *dagger.Client) *dagger.Container {
	// This loads the engine.tar file from the host into the container, that was set up by
	// internal/mage/engine.go:test or by ./hack/dev. This is used to spin up additional dev engines.
	var tarPath string
	if v, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR"); ok {
		tarPath = v
	} else {
		tarPath = "./bin/engine.tar"
	}
	devEngineTar := c.Host().File(tarPath)
	return c.Container().Import(devEngineTar).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp})
}

func engineClientContainer(ctx context.Context, t *testing.T, c *dagger.Client, devEngine *dagger.Container) (*dagger.Container, error) {
	daggerCli := daggerCliFile(t, c)

	cliBinPath := "/bin/dagger"
	endpoint, err := devEngine.Endpoint(ctx, dagger.ContainerEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return nil, err
	}
	return c.Container().From(alpineImage).
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
			Permissions: 0o700,
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

	devEngine := devEngineContainer(c)
	entrypoint, err := devEngine.File("/usr/local/bin/dagger-entrypoint.sh").Contents(ctx)

	require.NoError(t, err)
	before, after, found := strings.Cut(entrypoint, "set -e")
	require.True(t, found, "missing set -e in entrypoint")
	entrypoint = before + "set -e \n" + "sleep 15\n" + "echo my hostname is $(hostname)\n" + after

	devEngine = devEngine.
		WithNewFile("/usr/local/bin/dagger-entrypoint.sh", dagger.ContainerWithNewFileOpts{
			Contents:    entrypoint,
			Permissions: 0o700,
		}).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine)
	require.NoError(t, err)
	_, err = clientCtr.
		WithNewFile("/query.graphql", dagger.ContainerWithNewFileOpts{
			Contents: `{ defaultPlatform }`,
		}). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"}).Sync(ctx)

	require.NoError(t, err)
}

func TestEngineSetsNameFromEnv(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	engineName := "my-special-engine"
	devEngine := devEngineContainer(c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExec([]string{"--addr", "tcp://0.0.0.0:1234"}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine)
	require.NoError(t, err)

	out, err := clientCtr.
		WithNewFile("/query.graphql", dagger.ContainerWithNewFileOpts{
			Contents: `{ defaultPlatform }`,
		}). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Connected to engine "+engineName)
}

func TestDaggerRun(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	devEngine := devEngineContainer(c).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine)
	require.NoError(t, err)

	runCommand := `
	jq -n '{query:"{container{from(address: \"alpine:3.18.2\"){file(path: \"/etc/alpine-release\"){contents}}}}"}' | \
	dagger run sh -c 'curl -s \
		-u $DAGGER_SESSION_TOKEN: \
		-H "content-type:application/json" \
		-d @- \
		http://127.0.0.1:$DAGGER_SESSION_PORT/query'`

	clientCtr = clientCtr.
		WithExec([]string{"apk", "add", "jq", "curl"}).
		WithExec([]string{"sh", "-c", runCommand})

	stdout, err := clientCtr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, "3.18.2")

	stderr, err := clientCtr.Stderr(ctx)
	require.NoError(t, err)
	// verify we got some progress output
	require.Contains(t, stderr, "resolve image config for")
}
