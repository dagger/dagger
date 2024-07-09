package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bkconfig "github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/identity"
	"github.com/pelletier/go-toml"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type EngineSuite struct{}

func TestEngine(t *testing.T) {
	testctx.Run(testCtx, t, EngineSuite{}, Middleware()...)
}

// devEngineContainer returns a nested dev engine.
func devEngineContainer(c *dagger.Client, withs ...func(*dagger.Container) *dagger.Container) *dagger.Container {
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

	deviceName, cidr := testutil.GetUniqueNestedEngineNetwork()
	return ctr.
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec([]string{
			"--addr", "tcp://0.0.0.0:1234",
			"--addr", "unix:///var/run/buildkit/buildkitd.sock",
			// avoid network conflicts with other tests
			"--network-name", deviceName,
			"--network-cidr", cidr,
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})
}

func engineWithConfig(ctx context.Context, t *testctx.T, cfgFns ...func(context.Context, *testctx.T, bkconfig.Config) bkconfig.Config) func(*dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		t.Helper()
		existingCfgStr, err := ctr.File("/etc/dagger/engine.toml").Contents(ctx)
		require.NoError(t, err)

		cfg, err := bkconfig.Load(strings.NewReader(existingCfgStr))
		require.NoError(t, err)
		for _, cfgFn := range cfgFns {
			cfg = cfgFn(ctx, t, cfg)
		}

		newCfgBytes, err := toml.Marshal(cfg)
		require.NoError(t, err)

		return ctr.WithNewFile("/etc/dagger/engine.toml", string(newCfgBytes))
	}
}

func engineClientContainer(ctx context.Context, t *testctx.T, c *dagger.Client, devEngine *dagger.Service) (*dagger.Container, error) {
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

func (EngineSuite) TestExitsZeroOnSignal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// engine should shutdown with exit code 0 when receiving SIGTERM
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	t = t.WithContext(ctx)
	_, err := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.WithNewFile(
			"/usr/local/bin/dagger-entrypoint.sh",
			`#!/bin/sh
set -ex
/usr/local/bin/dagger-engine --debug &
engine_pid=$!

sleep 5
kill -TERM $engine_pid
wait $engine_pid
exit $?
`,
			dagger.ContainerWithNewFileOpts{Permissions: 0o700},
		)
	}).Sync(ctx)
	require.NoError(t, err)
}

func (ClientSuite) TestWaitsForEngine(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngine := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.
			WithNewFile(
				"/usr/local/bin/slow-entrypoint.sh",
				strings.Join([]string{
					`#!/bin/sh`,
					`set -eux`,
					`sleep 15`,
					`echo my hostname is $(hostname)`,
					`exec /usr/local/bin/dagger-entrypoint.sh "$@"`,
				}, "\n"),
				dagger.ContainerWithNewFileOpts{Permissions: 0o700},
			).
			WithEntrypoint([]string{"/usr/local/bin/slow-entrypoint.sh"})
	})

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine.AsService())
	require.NoError(t, err)
	_, err = clientCtr.
		WithNewFile("/query.graphql", `{ version }`). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"}).Sync(ctx)

	require.NoError(t, err)
}

func (EngineSuite) TestSetsNameFromEnv(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	engineName := "my-special-engine"
	engineVersion := "v1000.0.0-special"
	devEngineSvc := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", engineVersion)
	}).AsService()

	clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
	require.NoError(t, err)

	clientCtr = clientCtr.
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"dagger", "query", "--debug", "--doc", "/query.graphql"})
	stdout, err := clientCtr.Stdout(ctx)
	require.NoError(t, err)
	stderr, err := clientCtr.Stderr(ctx)
	require.NoError(t, err)

	require.Contains(t, stderr, engineName)
	require.Contains(t, stderr, engineVersion)

	require.Contains(t, stdout, engineVersion)
}

func (EngineSuite) TestDaggerRun(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngine := devEngineContainer(c).AsService()

	clientCtr, err := engineClientContainer(ctx, t, c, devEngine)
	require.NoError(t, err)

	runCommand := fmt.Sprintf(`
		export NO_COLOR=1
		jq -n '{query:"{container{from(address: \"%s\"){file(path: \"/etc/alpine-release\"){contents}}}}"}' | \
		dagger run sh -c 'curl -s \
			-u $DAGGER_SESSION_TOKEN: \
			-H "content-type:application/json" \
			-d @- \
			http://127.0.0.1:$DAGGER_SESSION_PORT/query'`,
		alpineImage,
	)

	clientCtr = clientCtr.
		WithExec([]string{"apk", "add", "jq", "curl"}).
		WithExec([]string{"sh", "-c", runCommand})

	stdout, err := clientCtr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, distconsts.AlpineVersion)
	require.JSONEq(t, `{"data": {"container": {"from": {"file": {"contents": "`+distconsts.AlpineVersion+`\n"}}}}}`, stdout)

	stderr, err := clientCtr.Stderr(ctx)
	require.NoError(t, err)
	// verify we got some progress output
	require.Contains(t, stderr, "Container.from")
}

func (ClientSuite) TestSendsLabelsInTelemetry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngine := devEngineContainer(c).AsService()
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

func (EngineSuite) TestVersionCompat(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngineVersion := "v2.0.0"
	devEngineSvc := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", devEngineVersion).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v2.0.0")
	}).AsService()

	clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
	require.NoError(t, err)

	// versions are compatible!
	stderr, err := clientCtr.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v2.0.0").
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"sh", "-c", "dagger query --debug --doc /query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, devEngineVersion)

	// client version is a development version
	stderr, err = clientCtr.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "foobar").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v2.0.0").
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"sh", "-c", "dagger query --debug --doc /query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, devEngineVersion)

	// client version is too old (v1.0.0 < v2.0.0)
	stderr, err = clientCtr.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v1.0.0").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v2.0.0").
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"sh", "-c", "! dagger query --debug --doc /query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "incompatible client version")

	// server version is too old (v2.0.0 < v3.0.0)
	stderr, err = clientCtr.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v3.0.0").
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"sh", "-c", "! dagger query --debug --doc /query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "incompatible engine version")

	// both versions are too old
	stderr, err = clientCtr.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v1.0.0").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", "v3.0.0").
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"sh", "-c", "! dagger query --debug --doc /query.graphql"}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "incompatible engine version")
}
