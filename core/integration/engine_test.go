package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bkconfig "github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/identity"
	"github.com/pelletier/go-toml"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
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
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithExec([]string{
			"--addr", "tcp://0.0.0.0:1234",
			"--addr", "unix:///var/run/buildkit/buildkitd.sock",
			// avoid network conflicts with other tests
			"--network-name", deviceName,
			"--network-cidr", cidr,
		}, dagger.ContainerWithExecOpts{
			UseEntrypoint:            true,
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
	ctr := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		t.Helper()

		c = c.WithNewFile(
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

		// do a sync here so our timeout doesn't include overhead of importing the engine itself
		var err error
		c, err = c.Sync(ctx)
		require.NoError(t, err)
		return c
	})

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	t = t.WithContext(ctx)
	_, err := ctr.Sync(ctx)
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
	engineVersion := engine.Version + "-special"
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

	_, err = withCode.
		WithMountedCache("/events", eventsVol).
		WithExec([]string{"sh", "-c", "grep dagger.io/git.title $0", fmt.Sprintf("/events/%s/**/*.json", eventsID)}).
		WithExec([]string{"sh", "-c", "grep 'init test repo' $0", fmt.Sprintf("/events/%s/**/*.json", eventsID)}).
		Sync(ctx)
	require.NoError(t, err)
}

func (EngineSuite) TestVersionCompat(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tcs := []struct {
		name string

		engineVersion    string
		engineMinVersion string
		clientVersion    string
		clientMinVersion string

		errs []string
	}{
		{
			// v2.0.0 > v1.0.0 for both client and engine
			name:             "compatible",
			engineVersion:    "v2.0.0",
			engineMinVersion: "v1.0.0",
			clientVersion:    "v2.0.0",
			clientMinVersion: "v1.0.0",
		},
		{
			// v2.0.0 > v1.0.0 for both client and engine
			name:             "compatible",
			engineVersion:    "v2.0.0",
			engineMinVersion: "v2.0.0",
			clientVersion:    "v2.0.0",
			clientMinVersion: "v2.0.0",
		},
		{
			// v2.0.0 < v3.0.0 for the client
			name:             "client incompatible",
			engineVersion:    "v2.0.0",
			engineMinVersion: "v1.0.0",
			clientVersion:    "v2.0.0",
			clientMinVersion: "v3.0.0",
			errs: []string{
				"incompatible client version v2.0.0",
			},
		},
		{
			// v2.0.0 < v3.0.0 for the engine
			name:             "engine incompatible",
			engineVersion:    "v2.0.0",
			engineMinVersion: "v3.0.0",
			clientVersion:    "v2.0.0",
			clientMinVersion: "v1.0.0",
			errs: []string{
				"incompatible engine version v2.0.0",
			},
		},
		{
			// v2.0.0 < v3.0.0 for both client and engine
			name:             "client and engine incompatible",
			engineVersion:    "v2.0.0",
			engineMinVersion: "v3.0.0",
			clientVersion:    "v2.0.0",
			clientMinVersion: "v3.0.0",
			errs: []string{
				"incompatible engine version v2.0.0",
			},
		},
		{
			// v2.0.1-foobar > v2.0.0 for both client and engine
			name:             "new dev version",
			engineVersion:    "v2.0.1-foobar",
			engineMinVersion: "v2.0.0",
			clientVersion:    "v2.0.1-foobar",
			clientMinVersion: "v2.0.0",
		},
		{
			// v2.0.1-foobar > v2.0.0 for both client and engine
			name:             "old dev version",
			engineVersion:    "v2.0.0-foobar",
			engineMinVersion: "v2.0.0",
			clientVersion:    "v2.0.0-foobar",
			clientMinVersion: "v2.0.0",
			errs: []string{
				"incompatible engine version v2.0.0-foobar",
			},
		},

		{
			// dev versions for the same version are happily compatible (even
			// if not a perfect match)
			name:             "compatible dev versions",
			engineVersion:    "v2.0.0-dev-123",
			engineMinVersion: "v2.0.0-dev-456",
			clientVersion:    "v2.0.0-dev-456",
			clientMinVersion: "v2.0.0-dev-123",
		},
		{
			// but for different versions, they're incompatible
			name:             "incompatible dev versions",
			engineVersion:    "v2.0.0-dev-123",
			engineMinVersion: "v2.0.1-dev-456",
			clientVersion:    "v2.0.1-dev-456",
			clientMinVersion: "v2.0.0-dev-123",
			errs: []string{
				"incompatible engine version v2.0.0-dev-123",
			},
		},

		{
			// pre-releases match if they're exactly the same
			name:             "compatible prereleases",
			engineVersion:    "v2.0.0-foo-123",
			engineMinVersion: "v2.0.0-foo-123",
			clientVersion:    "v2.0.0-foo-123",
			clientMinVersion: "v2.0.0-foo-123",
		},
		{
			// but can't not be a perfect match (unlike dev versions)
			name:             "incompatible prereleases",
			engineVersion:    "v2.0.0-foo-123",
			engineMinVersion: "v2.0.0-foo-456",
			clientVersion:    "v2.0.0-foo-456",
			clientMinVersion: "v2.0.0-foo-123",
			errs: []string{
				"incompatible engine version v2.0.0-foo-123",
			},
		},

		// empty clients/engines can happen with manual builds
		{
			name:             "compatible empty client",
			engineVersion:    "v2.0.0",
			engineMinVersion: "v1.0.0",
			clientVersion:    "",
			clientMinVersion: "v1.0.0",
		},
		{
			name:             "compatible empty engine",
			engineVersion:    "",
			engineMinVersion: "v1.0.0",
			clientVersion:    "v2.0.0",
			clientMinVersion: "v1.0.0",
		},
	}

	engines := map[string]*dagger.Service{}
	enginesMu := sync.Mutex{}

	for _, tc := range tcs {
		// get a cached engine if possible (saves spinning up more engines than we need to)
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			devEngineSvcKey := tc.engineVersion + " " + tc.clientMinVersion
			enginesMu.Lock()
			devEngineSvc, ok := engines[devEngineSvcKey]
			if !ok {
				devEngineSvc = devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
					return c.
						WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", tc.engineVersion).
						WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", tc.clientMinVersion)
				}).AsService()
				engines[devEngineSvcKey] = devEngineSvc
			}
			enginesMu.Unlock()

			clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
			require.NoError(t, err)

			clientCtr = clientCtr.
				WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", tc.clientVersion).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", tc.engineMinVersion)

			if tc.errs == nil {
				clientCtr = clientCtr.
					WithNewFile("/query.graphql", `{ version }`).
					WithExec([]string{"sh", "-c", "dagger version && dagger query --debug --doc /query.graphql"})
			} else {
				clientCtr = clientCtr.
					WithNewFile("/query.graphql", `{ version }`).
					WithExec([]string{"sh", "-c", "! dagger query --debug --doc /query.graphql"})
			}

			if tc.errs == nil {
				stdout, err := clientCtr.Stdout(ctx)
				require.NoError(t, err)

				// check that both the client and engine versions appear
				// somewhere in the combined output
				require.Contains(t, stdout, tc.clientVersion)
				require.Contains(t, stdout, tc.engineVersion)
			} else {
				stderr, err := clientCtr.Stderr(ctx)
				require.NoError(t, err)

				// check the error is contained
				for _, tcerr := range tc.errs {
					require.Contains(t, stderr, tcerr)
				}
			}
		})
	}
}

func (EngineSuite) TestModuleVersionCompat(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	tcs := []struct {
		name string

		engineVersion    string
		moduleVersion    string
		moduleMinVersion string

		errs []string
	}{
		{
			name:             "compatible equal",
			engineVersion:    "v2.0.0",
			moduleVersion:    "v2.0.0",
			moduleMinVersion: "v1.0.0",
		},
		{
			name:             "compatible less",
			engineVersion:    "v2.0.0",
			moduleVersion:    "v1.0.0",
			moduleMinVersion: "v1.0.0",
		},
		{
			name:             "incompatible too old",
			engineVersion:    "v2.0.0",
			moduleVersion:    "v0.9.0",
			moduleMinVersion: "v1.0.0",
			errs: []string{
				"module requires dagger v0.9.0",
				"support for that version has been removed",
			},
		},
		{
			name:             "incompatible too new",
			engineVersion:    "v2.0.0",
			moduleVersion:    "v2.0.1",
			moduleMinVersion: "v1.0.0",
			errs: []string{
				"module requires dagger v2.0.1, but you have v2.0.0",
			},
		},
		{
			name:             "old style dev version",
			engineVersion:    "v2.0.0",
			moduleVersion:    "badbadbad",
			moduleMinVersion: "v1.0.0",
			errs: []string{
				"module requires dagger v0.11.9", // old-style dev versions are equivalent to v0.11.9
				"support for that version has been removed",
			},
		},
	}

	engines := map[string]*dagger.Service{}
	enginesMu := sync.Mutex{}

	for _, tc := range tcs {
		// get a cached engine if possible (saves spinning up more engines than we need to)
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			devEngineSvcKey := tc.engineVersion + " " + tc.moduleMinVersion
			enginesMu.Lock()
			devEngineSvc, ok := engines[devEngineSvcKey]
			if !ok {
				devEngineSvc = devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
					return c.
						WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", tc.engineVersion).
						WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", tc.moduleMinVersion)
				}).AsService()
				engines[devEngineSvcKey] = devEngineSvc
			}
			enginesMu.Unlock()

			clientCtr, err := engineClientContainer(ctx, t, c, devEngineSvc)
			require.NoError(t, err)
			clientCtr = clientCtr.
				WithWorkdir("/work").
				// set version to empty, this makes it the latest, we don't want to
				// test client compat (that's the previous tests)
				WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "").
				With(daggerExec("init", "--name=bare", "--sdk=go"))

			clientCtr = clientCtr.
				WithNewFile("/work/dagger.json", `{"name": "bare", "sdk": "go", "engineVersion": "`+tc.moduleVersion+`"}`).
				WithNewFile("/query.graphql", `{bare{containerEcho(stringArg:"hello"){stdout}}}`)

			if tc.errs == nil {
				clientCtr = clientCtr.
					WithExec([]string{"sh", "-c", "dagger query --debug --doc /query.graphql"})
			} else {
				clientCtr = clientCtr.
					WithExec([]string{"sh", "-c", "! dagger query --debug --doc /query.graphql"})
			}

			stderr, err := clientCtr.Stderr(ctx)
			require.NoError(t, err)
			for _, tcerr := range tc.errs {
				require.Contains(t, stderr, tcerr)
			}
		})
	}
}
