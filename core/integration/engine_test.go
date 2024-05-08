package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

// devEngineContainer returns a nested dev engine.
//
// Note! engineInstance *must* be unique for concurrent instances of dagger.
func devEngineContainer(c *dagger.Client, engineInstance uint8, withs ...func(*dagger.Container) *dagger.Container) *dagger.Container {
	// This loads the engine.tar file from the host into the container, that
	// was set up by the test caller. This is used to spin up additional dev
	// engines.
	var tarPath string
	if v, ok := os.LookupEnv("_DAGGER_TESTS_ENGINE_TAR"); ok {
		tarPath = v
	} else {
		tarPath = "./bin/engine.tar"
	}
	devEngineTar := c.Host().File(tarPath)

	ctr := c.Container().Import(devEngineTar)
	for _, with := range withs {
		ctr = with(ctr)
	}
	return ctr.
		WithEnvVariable("ENGINE_ID", fmt.Sprint(engineInstance)).
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec([]string{
			"--addr", "tcp://0.0.0.0:1234",
			"--addr", "unix:///var/run/buildkit/buildkitd.sock",
			// avoid network conflicts with other tests
			"--network-name", fmt.Sprintf("dagger%d", engineInstance),
			"--network-cidr", fmt.Sprintf("10.88.%d.0/24", engineInstance),
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})
}

func engineClientContainer(ctx context.Context, t *testing.T, c *dagger.Client, devEngine *dagger.Service) (*dagger.Container, error) {
	daggerCli := daggerCliFile(t, c)

	cliBinPath := "/bin/dagger"
	endpoint, err := devEngine.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
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
	t.Parallel()
	c, ctx := connect(t)

	// engine should shutdown with exit code 0 when receiving SIGTERM
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := devEngineContainer(c, 101, func(c *dagger.Container) *dagger.Container {
		return c.WithNewFile("/usr/local/bin/dagger-entrypoint.sh", dagger.ContainerWithNewFileOpts{
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
		})
	}).Sync(ctx)
	require.NoError(t, err)
}

func TestClientWaitsForEngine(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	devEngine := devEngineContainer(c, 102, func(c *dagger.Container) *dagger.Container {
		return c.
			WithNewFile("/usr/local/bin/slow-entrypoint.sh", dagger.ContainerWithNewFileOpts{
				Contents: strings.Join([]string{
					`#!/bin/sh`,
					`set -eux`,
					`sleep 15`,
					`echo my hostname is $(hostname)`,
					`exec /usr/local/bin/dagger-entrypoint.sh "$@"`,
				}, "\n"),
				Permissions: 0o700,
			}).
			WithEntrypoint([]string{"/usr/local/bin/slow-entrypoint.sh"})
	})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine.AsService())
	require.NoError(t, err)
	_, err = clientCtr.
		WithNewFile("/query.graphql", dagger.ContainerWithNewFileOpts{
			Contents: `{ defaultPlatform }`,
		}). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"}).Sync(ctx)

	require.NoError(t, err)
}

func TestEngineSetsNameFromEnv(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	engineName := "my-special-engine"
	devEngineSvc := devEngineContainer(c, 103, func(c *dagger.Container) *dagger.Container {
		return c.WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName)
	}).AsService()

	clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
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

	devEngine := devEngineContainer(c, 104).AsService()

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine)
	require.NoError(t, err)

	runCommand := `
	export NO_COLOR=1
	jq -n '{query:"{container{from(address: \"alpine:3.19.1\"){file(path: \"/etc/alpine-release\"){contents}}}}"}' | \
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
	require.Contains(t, stdout, "3.19.1")
	require.JSONEq(t, `{"data": {"container": {"from": {"file": {"contents": "3.19.1\n"}}}}}`, stdout)

	stderr, err := clientCtr.Stderr(ctx)
	require.NoError(t, err)
	// verify we got some progress output
	require.Contains(t, stderr, "Container.from")
}

func TestClientSendsLabelsInTelemetry(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	devEngine := devEngineContainer(c, 105).AsService()
	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)

	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"core/integration/testdata/basic-container/",
			"sdk/go/",
			"go.mod",
			"go.sum",
		},
	})

	eventsVol := c.CacheVolume("dagger-dev-engine-events-" + identity.NewID())

	withCode := c.Container().
		From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src")

	fakeCloud := withCode.
		WithMountedCache("/events", eventsVol).
		WithExec([]string{
			"go", "run", "./core/integration/testdata/telemetry/",
		}).
		WithExposedPort(8080).
		AsService()

	eventsID := identity.NewID()

	daggerCli := daggerCliFile(t, c)

	_, err = withCode.
		WithServiceBinding("dev-engine", devEngine).
		WithMountedFile("/bin/dagger", daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dev-engine:1234").
		WithServiceBinding("cloud", fakeCloud).
		WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+eventsID).
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "test").
		WithExec([]string{"git", "config", "--global", "init.defaultBranch", "main"}).
		WithExec([]string{"git", "config", "--global", "user.email", "test@example.com"}).
		// make sure we handle non-ASCII usernames
		WithExec([]string{"git", "config", "--global", "user.name", "Tiësto User"}).
		WithExec([]string{"git", "init"}). // init a git repo to test git labels
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init test repo"}).
		WithExec([]string{"dagger", "run", "go", "run", "./core/integration/testdata/basic-container/"}).
		Stderr(ctx)
	require.NoError(t, err)

	receivedEvents, err := withCode.
		WithMountedCache("/events", eventsVol).
		WithExec([]string{
			"sh", "-c", "cat $0", fmt.Sprintf("/events/%s/**/*.json", eventsID),
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, receivedEvents, "dagger.io/git.title")
	require.Contains(t, receivedEvents, "init test repo")
}
