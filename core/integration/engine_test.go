package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/pelletier/go-toml"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type EngineSuite struct{}

func TestEngine(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(EngineSuite{})
}

func devEngineContainerAsService(ctr *dagger.Container) *dagger.Service {
	return ctr.AsService(dagger.ContainerAsServiceOpts{
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	})
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
		WithDefaultArgs([]string{
			"--addr", "tcp://0.0.0.0:1234",
			// avoid network conflicts with other tests
			"--network-name", deviceName,
			"--network-cidr", cidr,
		})
}

func engineWithConfig(ctx context.Context, t *testctx.T, cfgFns ...func(context.Context, *testctx.T, config.Config) config.Config) func(*dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		t.Helper()

		var cfg config.Config

		entries, err := ctr.Directory("/etc/dagger").Entries(ctx)
		require.NoError(t, err)
		if slices.Contains(entries, "engine.json") {
			existingCfgStr, err := ctr.File("/etc/dagger/engine.json").Contents(ctx)
			require.NoError(t, err)
			cfg, err = config.Load(strings.NewReader(existingCfgStr))
			require.NoError(t, err)
		}

		for _, cfgFn := range cfgFns {
			cfg = cfgFn(ctx, t, cfg)
		}

		var buf bytes.Buffer
		require.NoError(t, cfg.Save(&buf))
		return ctr.WithNewFile("/etc/dagger/engine.json", buf.String())
	}
}

func engineWithBkConfig(ctx context.Context, t *testctx.T, cfgFns ...func(context.Context, *testctx.T, bkconfig.Config) bkconfig.Config) func(*dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		t.Helper()

		var cfg bkconfig.Config

		entries, err := ctr.Directory("/etc/dagger").Entries(ctx)
		require.NoError(t, err)
		if slices.Contains(entries, "engine.toml") {
			existingCfgStr, err := ctr.File("/etc/dagger/engine.toml").Contents(ctx)
			require.NoError(t, err)

			cfg, err = bkconfig.Load(strings.NewReader(existingCfgStr))
			require.NoError(t, err)
		}

		for _, cfgFn := range cfgFns {
			cfg = cfgFn(ctx, t, cfg)
		}

		newCfgBytes, err := toml.Marshal(cfg)
		require.NoError(t, err)

		return ctr.WithNewFile("/etc/dagger/engine.toml", string(newCfgBytes))
	}
}

func engineClientContainer(ctx context.Context, t *testctx.T, c *dagger.Client, devEngine *dagger.Service) *dagger.Container {
	daggerCli := daggerCliFile(t, c)

	cliBinPath := "/bin/dagger"
	endpoint, err := devEngine.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: 1234, Scheme: "tcp"})
	require.NoError(t, err)
	return c.Container().From(alpineImage).
		WithServiceBinding("dev-engine", devEngine).
		WithMountedFile(cliBinPath, daggerCli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint)
}

// withNonNestedDevEngine configures a Container to use the same dev engine
// as the tests are running against but while avoiding use of a nested exec.
// This is needed occasionally for tests like TestClientGenerator where we
// can't use nested execs.
// It works because our integ test setup code in the dagger-dev module mount
// in the engine service's unix sock to the test container.
func nonNestedDevEngine(c *dagger.Client) func(*dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithUnixSocket("/run/dagger-engine.sock", c.Host().UnixSocket("/run/dagger-engine.sock")).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "unix:///run/dagger-engine.sock")
	}
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

func (EngineSuite) TestSetsNameFromEnv(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	engineName := "my-special-engine"
	engineVersion := engine.Version + "-special"
	devEngineSvc := devEngineContainerAsService(devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", engineVersion)
	}))

	clientCtr := engineClientContainer(ctx, t, c, devEngineSvc)

	clientCtr = clientCtr.WithExec([]string{"dagger", "core", "version"})

	// version call
	stdout, err := clientCtr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, engineVersion, strings.TrimSpace(stdout))

	// in progress output
	stderr, err := clientCtr.Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, engineName)
	require.Contains(t, stderr, engineVersion)

	clientCtr = clientCtr.WithExec([]string{"dagger", "core", "engine", "name"})

	// name call
	stdout, err = clientCtr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, engineName, strings.TrimSpace(stdout))
}

func (EngineSuite) TestDaggerRun(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngine := devEngineContainerAsService(devEngineContainer(c))

	clientCtr := engineClientContainer(ctx, t, c, devEngine)

	command := fmt.Sprintf(`
		export NO_COLOR=1
		jq -n '{query:"{container{from(address: \"%s\"){file(path: \"/etc/alpine-release\"){contents}}}}"}' | \
		dagger run sh -c 'curl -s \
			-u $DAGGER_SESSION_TOKEN: \
			--max-time 30 \
			-H "content-type:application/json" \
			-d @- \
			http://127.0.0.1:$DAGGER_SESSION_PORT/query'`,
		alpineImage,
	)

	clientCtr = clientCtr.
		WithExec([]string{"apk", "add", "jq", "curl"}).
		WithExec([]string{"sh", "-c", command})

	stdout, err := clientCtr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, distconsts.AlpineVersion)
	require.JSONEq(t, `{"data": {"container": {"from": {"file": {"contents": "`+distconsts.AlpineVersion+`\n"}}}}}`, stdout)

	stderr, err := clientCtr.Stderr(ctx)
	require.NoError(t, err)
	// verify we got some progress output
	require.Contains(t, stderr, "Container.from")
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
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			devEngineSvcKey := tc.engineVersion + " " + tc.clientMinVersion
			enginesMu.Lock()
			devEngineSvc, ok := engines[devEngineSvcKey]
			if !ok {
				devEngine := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
					return c.
						WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", tc.engineVersion).
						WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", tc.clientMinVersion)
				})
				devEngineSvc = devEngineContainerAsService(devEngine)
				engines[devEngineSvcKey] = devEngineSvc
			}
			enginesMu.Unlock()

			clientCtr := engineClientContainer(ctx, t, c, devEngineSvc)

			clientCtr = clientCtr.
				WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", tc.clientVersion).
				WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", tc.engineMinVersion)

			if tc.errs == nil {
				clientCtr = clientCtr.
					WithNewFile("/query.graphql", `{ version }`).
					WithExec([]string{"sh", "-c", "dagger version && dagger query --doc /query.graphql"})
			} else {
				clientCtr = clientCtr.
					WithNewFile("/query.graphql", `{ version }`).
					WithExec([]string{"sh", "-c", "! dagger query --doc /query.graphql"})
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
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			devEngineSvcKey := tc.engineVersion + " " + tc.moduleMinVersion
			enginesMu.Lock()
			devEngineSvc, ok := engines[devEngineSvcKey]
			if !ok {
				devEngine := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
					return c.
						WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", tc.engineVersion).
						WithEnvVariable("_EXPERIMENTAL_DAGGER_MIN_VERSION", tc.moduleMinVersion)
				})
				devEngineSvc = devEngineContainerAsService(devEngine)
				engines[devEngineSvcKey] = devEngineSvc
			}
			enginesMu.Unlock()

			clientCtr := engineClientContainer(ctx, t, c, devEngineSvc)

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
					WithExec([]string{"sh", "-c", "dagger query --doc /query.graphql"})
			} else {
				clientCtr = clientCtr.
					WithExec([]string{"sh", "-c", "! dagger query --doc /query.graphql"})
			}

			stderr, err := clientCtr.Stderr(ctx)
			require.NoError(t, err)
			for _, tcerr := range tc.errs {
				require.Contains(t, stderr, tcerr)
			}
		})
	}
}

func (EngineSuite) TestModuleVersionCompatInvalid(ctx context.Context, t *testctx.T) {
	// a variant of the above test, but the format of the config file has
	// changed, and can't be correctly unmarshalled - but we should still get a
	// reasonable error

	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=bare", "--sdk=go")).
		WithNewFile("dagger.json", `{ "name": "bare", "engineVersion": "v100.0.0", "sdk": 123 }`)
	_, err := modGen.
		With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
		Stdout(ctx)
	require.Error(t, err)
	requireErrOut(t, err, `module requires dagger v100.0.0, but you have`)
}

func (EngineSuite) TestConcurrentCallContextCanceled(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		l.Close()
	})
	port := l.Addr().(*net.TCPAddr).Port

	hitCh := make(chan struct{}, 1)
	allDoneCh := make(chan struct{})
	httpSrv := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case hitCh <- struct{}{}:
			default:
			}

			select {
			case <-allDoneCh:
				w.Write([]byte("done"))
			default:
				w.Write([]byte("hi"))
			}
		}),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	go httpSrv.Serve(l)

	httpSvc := c.Host().Service([]dagger.PortForward{{
		Backend:  port,
		Frontend: port,
	}})

	ctr, err := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("srv", httpSvc).
		WithEnvVariable("PORT", fmt.Sprintf("%d", port)).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		Sync(ctx)
	require.NoError(t, err)
	ctr = ctr.WithExec([]string{"sh", "-c",
		// request http://srv:$PORT/ in a loop until it returns "done"
		"until [ \"$(curl -s http://srv:$PORT/)\" = \"done\" ]; do sleep 1; done",
	})

	ctx1, cancel1 := context.WithCancel(ctx)
	errCh1 := make(chan error, 1)
	go func() {
		defer close(errCh1)
		_, err := ctr.Sync(ctx1)
		errCh1 <- err
	}()
	// wait for the first hit to the server so we know the exec is running
	select {
	case <-hitCh:
	case <-time.After(60 * time.Second): // extremely generous for when the engine is very slow under load
	}

	// start the second duped exec
	errCh2 := make(chan error, 1)
	go func() {
		defer close(errCh2)
		_, err := ctr.Sync(ctx)
		errCh2 <- err
	}()

	// give the second dupe exec some time to start running
	for range 3 {
		select {
		case <-hitCh:
		case <-time.After(60 * time.Second):
		}
	}

	// cancel the first exec, verify it errors
	cancel1()
	select {
	case err := <-errCh1:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for errCh1")
	}

	// make sure the exec is still running despite cancelation of first client
	for range 2 {
		select {
		case <-hitCh:
		case <-time.After(60 * time.Second):
		}
	}

	// tell the exec to break its loop and exit, verify no error despite earlier cancelation
	close(allDoneCh)
	select {
	case err := <-errCh2:
		require.NoError(t, err)
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for errCh2")
	}
}

func (EngineSuite) TestPrometheusMetrics(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngineCtr := devEngineContainer(c, func(c *dagger.Container) *dagger.Container {
		return c.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_METRICS_ADDR", "0.0.0.0:9090").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL", "3s").
			WithExposedPort(9090, dagger.ContainerWithExposedPortOpts{
				Protocol: dagger.NetworkProtocolTcp,
			})
	})
	devEngine := devEngineContainerAsService(devEngineCtr)

	clientCtr := engineClientContainer(ctx, t, c, devEngine)

	var eg errgroup.Group
	clientCtx, clientCancel := context.WithCancel(ctx)
	t.Cleanup(clientCancel)
	eg.Go(func() error {
		_, err := clientCtr.
			With(daggerNonNestedExec("listen")).
			Sync(clientCtx)
		if strings.Contains(err.Error(), "context canceled") {
			return nil // expected, we cancel it later
		}
		if err != nil {
			t.Logf("error running dagger listen: %v", err)
		}
		return err
	})

	var foundAll bool
	for range 30 {
		out, err := clientCtr.
			WithExec([]string{"apk", "add", "curl"}).
			WithEnvVariable("CACHEBUST", rand.Text()).
			WithExec([]string{"sh", "-c", "curl -s http://dev-engine:9090/metrics"}).
			Stdout(ctx)
		if err != nil {
			t.Logf("error fetching metrics: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		// find the lines with metrics we care about testing
		soughtMetrics := map[string]struct{}{
			"dagger_connected_clients":                 {},
			"dagger_local_cache_total_disk_size_bytes": {},
			"dagger_local_cache_entries":               {},
		}
		foundMetrics := map[string]int{}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)

			for metricName := range soughtMetrics {
				numStr, found := strings.CutPrefix(line, metricName+" ")
				if !found {
					continue
				}
				num, err := strconv.Atoi(numStr)
				require.NoError(t, err)

				delete(soughtMetrics, metricName)
				foundMetrics[metricName] = num
			}

			if len(soughtMetrics) == 0 {
				break
			}
		}

		if len(soughtMetrics) != 0 {
			t.Logf("did not find all sought metrics in output: %v", soughtMetrics)
			time.Sleep(1 * time.Second)
			continue
		}

		// found everything, but validate values
		validatedAll := true
		for metricName, num := range foundMetrics {
			switch metricName {
			case "dagger_connected_clients":
				if num != 1 {
					t.Logf("expected dagger_connected_clients = 1, got %d", num)
					validatedAll = false
				}
			case "dagger_local_cache_total_disk_size_bytes":
				if num <= 0 {
					t.Logf("expected dagger_local_cache_total_disk_size_bytes > 0, got %d", num)
					validatedAll = false
				}
			case "dagger_local_cache_entries":
				if num <= 0 {
					t.Logf("expected dagger_local_cache_entries >= 0, got %d", num)
					validatedAll = false
				}
			default:
				t.Fatalf("unexpected metric %q found in output", metricName)
			}
		}

		if validatedAll {
			foundAll = true
			break // everything found + validated, exit retry loop
		}

		// retry again in a second
		time.Sleep(1 * time.Second)
	}
	require.True(t, foundAll, "did not find all expected metrics in output after 30 attempts")

	clientCancel()
	require.NoError(t, eg.Wait(), "error from client exec")
}

func (EngineSuite) TestClientMetadataReuse(ctx context.Context, t *testctx.T) {
	c1 := connect(ctx, t)
	c2 := connect(ctx, t)

	rando := rand.Text()

	base1, err := c1.Container().
		From(alpineImage).
		WithEnvVariable("CACHEBUSTER", rando).
		Sync(ctx)
	require.NoError(t, err)
	base2, err := c2.Container().
		From(alpineImage).
		WithEnvVariable("CACHEBUSTER", rando).
		Sync(ctx)
	require.NoError(t, err)

	var eg errgroup.Group

	// Run the same exec in both clients, but cancel the first one
	// in the middle and close the client right away. The second exec
	// should still successfully finish the exec.

	var err1 error
	eg.Go(func() error {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_, err1 = base1.
			WithExec([]string{"sh", "-c", "sleep 30; echo hello"}).
			Stdout(ctx)
		if err1 == nil {
			return fmt.Errorf("expected first exec to be canceled")
		}
		return c1.Close()
	})

	var out2 string
	eg.Go(func() error {
		time.Sleep(10 * time.Second)
		var err error
		out2, err = base2.
			WithExec([]string{"sh", "-c", "sleep 30; echo hello"}).
			Stdout(ctx)
		return err
	})

	require.NoError(t, eg.Wait())
	require.Equal(t, "hello\n", out2)
}
