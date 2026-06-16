package core

// These tests cover `dagger.Connect` clients used by Go callers. They verify
// connection setup, teardown, and session behavior between a caller and the
// engine.
//
// See also:
// - suite_test.go: shared connection setup used by integration tests.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/koron-go/prefixw"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type ClientSuite struct{}

func TestClient(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ClientSuite{})
}

func (ClientSuite) TestClose(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	err := c.Close()
	require.NoError(t, err)
}

func (ClientSuite) TestMultiSameTrace(ctx context.Context, t *testctx.T) {
	rootCtx, span := otel.Tracer("dagger").Start(ctx, "root")
	defer span.End()

	newClient := func(ctx context.Context, name string) (*dagger.Client, *safeBuffer) {
		out := new(safeBuffer)
		c, err := dagger.Connect(ctx,
			dagger.WithLogOutput(io.MultiWriter(prefixw.New(testutil.NewTWriter(t), name+": "), out)),
		)
		require.NoError(t, err)
		t.Cleanup(func() { c.Close() })
		return c, out
	}

	ctx1, span := otel.Tracer("dagger").Start(rootCtx, "client 1")
	defer span.End()
	c1, out1 := newClient(ctx1, "client 1")

	// NOTE: the failure mode for these tests is to hang forever, so we'll set a
	// reasonable timeout
	const timeout = 60 * time.Second

	// try to insulate from network flakiness by resolving and using a fully
	// qualified ref beforehand.
	fqRef, err := c1.Container().From(alpineImage).ImageRef(ctx1)
	require.NoError(t, err)

	echo := func(ctx context.Context, c *dagger.Client, msg string) {
		_, err := c.Container().
			From(fqRef).
			// FIXME: have to echo first, then wait, then echo again, because we only
			// wait for logs once we see them the first time, and we only show spans
			// that are slow enough. this could be made more foolproof by adding a
			// span attribute like "hey wait until you see EOF for my logs on these
			// streams" but we don't control the span.
			// NOTE: have to echo slowly enough that the frontend doesn't consider it
			// "boring"
			WithExec([]string{"sh", "-c", "echo hey; sleep 0.5; echo echoed: $0", msg}).Sync(ctx)
		require.NoError(t, err)
	}

	c1msg := identity.NewID()
	echo(ctx1, c1, c1msg)
	require.Eventually(t, func() bool {
		return strings.Contains(out1.String(), "echoed: "+c1msg)
	}, timeout, 10*time.Millisecond)

	ctx2, span := otel.Tracer("dagger").Start(rootCtx, "client 2")
	defer span.End()

	// the timeout has to be established before connecting, so we apply it to c2
	// and make sure we close c2 first.
	timeoutCtx2, cancelTimeout := context.WithTimeout(ctx2, timeout)
	defer cancelTimeout()
	c2, out2 := newClient(timeoutCtx2, "client 2")

	c2msg := identity.NewID()
	echo(ctx2, c2, c2msg)
	require.Eventually(t, func() bool {
		return strings.Contains(out2.String(), "echoed: "+c2msg)
	}, timeout, 10*time.Millisecond)

	ctx3, span := otel.Tracer("dagger").Start(rootCtx, "client 3")
	defer span.End()
	timeoutCtx3, cancelTimeout := context.WithTimeout(ctx3, timeout)
	defer cancelTimeout()
	c3, out3 := newClient(timeoutCtx3, "client 3")

	c3msg := identity.NewID()
	echo(ctx3, c3, c3msg)
	require.Eventually(t, func() bool {
		return strings.Contains(out3.String(), "echoed: "+c3msg)
	}, timeout, 10*time.Millisecond)

	t.Logf("closing c2 (which has timeout)")
	require.NoError(t, c2.Close())

	t.Logf("closing c3 (which has timeout)")
	require.NoError(t, c3.Close())

	t.Logf("closing c1")
	require.NoError(t, c1.Close())

	t.Logf("asserting")
	require.Regexp(t, `withExec.*echo.*`+c1msg, out1.String())
	require.Regexp(t, `withExec.*DONE`, out1.String())
	require.NotContains(t, out1.String(), c2msg)
	require.Regexp(t, `withExec.*echo.*`+c2msg, out2.String())
	require.Regexp(t, `withExec.*DONE`, out2.String())
	require.Equal(t, 1, strings.Count(out1.String(), "echoed: "+c1msg))
	require.NotContains(t, out2.String(), c1msg)
	require.Equal(t, 1, strings.Count(out2.String(), "echoed: "+c2msg))
	require.Regexp(t, `withExec.*echo.*`+c3msg, out3.String())
	require.Regexp(t, `withExec.*DONE`, out3.String())
	require.Equal(t, 1, strings.Count(out3.String(), "echoed: "+c3msg))
	require.NotContains(t, out3.String(), c1msg)
}

func (ClientSuite) TestClientStableID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	devEngine := devEngineContainer(c)
	clientCtr := engineClientContainer(ctx, t, c, devEngineContainerAsService(devEngine))

	// just run any dagger cli command that connects to the engine.
	// `dagger query` reads the GraphQL document from --doc (the positional arg
	// is an optional operation name), so point it at a trivially valid query.
	stableID, err := clientCtr.
		WithExec([]string{"adduser", "-u", "1234", "-D", "auser"}).
		WithUser("auser").
		WithWorkdir("/work").
		WithNewFile("/query.graphql", `{ version }`).
		WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}).
		File("/home/auser/.local/state/dagger/stable_client_id").
		Contents(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, stableID)
}

// TestQuerySchemaVersion checks that we can set the QueryOptions.Version
// parameter to determine which schema version we're served.
//
// We use this in tests to do quick-and-easy checks against the schemas served
// (without needing to do fancy module manipulation).
func (ClientSuite) TestQuerySchemaVersion(ctx context.Context, t *testctx.T) {
	v, err := testutil.Query[struct {
		SchemaVersion string `json:"__schemaVersion"`
	}](t, `{ __schemaVersion }`, nil, dagger.WithVersionOverride("v123.456.789"))
	require.NoError(t, err)
	require.Equal(t, "v123.456.789", v.SchemaVersion)
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

	clientCtr := engineClientContainer(ctx, t, c, devEngineContainerAsService(devEngine))
	_, err := clientCtr.
		WithNewFile("/query.graphql", `{ version }`). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}).Sync(ctx)

	require.NoError(t, err)
}

func (ClientSuite) TestSendsLabelsInTelemetry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngine := devEngineContainerAsService(devEngineContainer(c))
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
		WithDefaultArgs([]string{
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
		WithExec([]string{"grep", "-R", "dagger.io/git.title", fmt.Sprintf("/events/%s", eventsID)}).
		WithExec([]string{"grep", "-R", "init test repo", fmt.Sprintf("/events/%s", eventsID)}).
		Sync(ctx)
	require.NoError(t, err)
}

// Engine-side telemetry must reach Dagger Cloud with the auth and endpoint
// the client provides through its metadata: since the client/engine telemetry
// split, this is the only route for it. The fake cloud rejects any request
// without the expected Basic authorization.
//
// A marker is a unique string planted in an exec's output: finding it in
// what the fake cloud recorded proves that exec's telemetry arrived. Two
// things about the markers are load-bearing:
//
//   - The full marker must never appear on the dagger command line. The
//     client exports its own telemetry to the same fake cloud, and its root
//     span is named after the command line — that alone would satisfy the
//     grep even with engine-side export broken. So each marker ships as two
//     env var halves, assembled only inside the exec.
//
//   - The nested marker must run through a module call. A module runtime is
//     a real nested client; a privileged-nesting exec that never calls
//     dagger and a `dagger run` program both emit as the main client.
func (ClientSuite) TestEngineTelemetryToCloud(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"go.mod",
			"go.sum",
		},
	})

	eventsVol := c.CacheVolume("dagger-cloud-events-" + identity.NewID())
	eventsID := identity.NewID()

	fakeCloud := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/events", eventsVol).
		WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
		WithExposedPort(8080).
		AsService()

	// the engine-side exporters dial the cloud URL from the engine process,
	// so the engine needs to resolve the fake cloud, not just the client
	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding("cloud", fakeCloud)
	}))

	mainMarkerID := identity.NewID()
	nestedMarkerID := identity.NewID()
	mainMarker := "main-marker-" + mainMarkerID
	nestedMarker := "nested-marker-" + nestedMarkerID

	// the module function runs as a nested client and assembles the marker
	// inside its container exec, the only place the full string exists
	markerModuleSrc := fmt.Sprintf(`package main

import "context"

type Marker struct{}

func (m *Marker) Emit(ctx context.Context, prefix string, id string) error {
	_, err := dag.Container().
		From(%q).
		WithEnvVariable("MARKER_PREFIX", prefix).
		WithEnvVariable("MARKER_ID", id).
		WithExec([]string{"sh", "-c", "echo $MARKER_PREFIX$MARKER_ID"}).
		Sync(ctx)
	return err
}
`, alpineImage)

	_, err = engineClientContainer(ctx, t, c, devEngine).
		WithServiceBinding("cloud", fakeCloud).
		WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+eventsID).
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "test").
		WithExec([]string{
			"dagger", "core", "container",
			"from", "--address=" + alpineImage,
			"with-env-variable", "--name=MARKER_PREFIX", "--value=main-marker-",
			"with-env-variable", "--name=MARKER_ID", "--value=" + mainMarkerID,
			"with-exec", "--args=sh", "--args=-c", "--args=echo $MARKER_PREFIX$MARKER_ID",
			"stdout",
		}).
		WithWorkdir("/work/marker").
		WithNewFile("/work/marker/dagger.json", `{"name": "marker", "sdk": "go", "source": "."}`).
		WithNewFile("/work/marker/main.go", markerModuleSrc).
		WithExec([]string{"dagger", "call", "-m", ".", "emit", "--prefix=nested-marker-", "--id=" + nestedMarkerID}).
		Sync(ctx)
	require.NoError(t, err)

	// the engine flushes telemetry while handling /shutdown, before the
	// command above exits, so the fake cloud has everything by now
	events := c.Container().From(alpineImage).WithMountedCache("/events", eventsVol)
	for name, marker := range map[string]string{
		"main client":                     mainMarker,
		"nested client (module function)": nestedMarker,
	} {
		_, err := events.
			WithExec([]string{"grep", "-r", "-a", marker, fmt.Sprintf("/events/%s/", eventsID)}).
			Sync(ctx)
		require.NoError(t, err, "%s telemetry did not reach the fake cloud", name)
	}

	// all three signals must export, each through its own OTLP endpoint;
	// metrics regressed silently once before, when only spans and logs were
	// wired for auth refresh
	for _, signal := range []string{"traces", "logs", "metrics"} {
		_, err := events.
			WithExec([]string{"test", "-s", fmt.Sprintf("/events/%s/v1/%s.json", eventsID, signal)}).
			Sync(ctx)
		require.NoError(t, err, "no %s reached the fake cloud", signal)
	}
}

// OAuth sessions outlive their access tokens: OTel invokes the cloud
// exporters from background goroutines long after the original request
// contexts are gone, and the engine must still be able to refresh — reading
// the refresh token from the *client host's* credentials file through the
// session attachables, persisting the new token back, and exporting with it.
// The fake cloud only issues instantly-expiring tokens, so every export has
// to take that path; and it rejects any token it didn't issue, so telemetry
// arriving at all proves no export ever ran with stale auth.
//
// The engine and client refresh through separate token endpoints, so
// engine-issued tokens are recognizable in the fake cloud's request log. The
// per-signal assertions rely on that: the client exports its own telemetry to
// the same fake cloud, so file contents alone can't tell the two apart. The
// marker ships as env var halves for the same reason — see
// TestEngineTelemetryToCloud.
func (ClientSuite) TestEngineTelemetryCloudOAuthRefresh(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"go.mod",
			"go.sum",
		},
	})

	eventsVol := c.CacheVolume("dagger-cloud-events-" + identity.NewID())
	eventsID := identity.NewID()

	fakeCloud := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/events", eventsVol).
		WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
		WithExposedPort(8080).
		AsService()

	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithServiceBinding("cloud", fakeCloud).
			WithEnvVariable("DAGGER_CLOUD_AUTH_URL", "http://cloud:8080/"+eventsID+"/engine")
	}))

	markerID := identity.NewID()
	marker := "refresh-marker-" + markerID
	staleCreds := `{"access_token":"stale-token","token_type":"Bearer","refresh_token":"test-refresh-token","expiry":"2020-01-01T00:00:00Z"}`

	clientCtr := engineClientContainer(ctx, t, c, devEngine).
		WithServiceBinding("cloud", fakeCloud).
		WithEnvVariable("XDG_CONFIG_HOME", "/root/.config").
		WithNewFile("/root/.config/dagger/credentials.json", staleCreds).
		WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+eventsID).
		WithEnvVariable("DAGGER_CLOUD_AUTH_URL", "http://cloud:8080/"+eventsID+"/client").
		WithExec([]string{
			"dagger", "core", "container",
			"from", "--address=" + alpineImage,
			"with-env-variable", "--name=MARKER_PREFIX", "--value=refresh-marker-",
			"with-env-variable", "--name=MARKER_ID", "--value=" + markerID,
			"with-exec", "--args=sh", "--args=-c", "--args=echo $MARKER_PREFIX$MARKER_ID",
			"stdout",
		})
	_, err = clientCtr.Sync(ctx)
	require.NoError(t, err)

	events := c.Container().From(alpineImage).WithMountedCache("/events", eventsVol)

	// telemetry arrived, and only issued (refreshed) tokens are accepted
	_, err = events.
		WithExec([]string{"grep", "-r", "-a", marker, fmt.Sprintf("/events/%s/", eventsID)}).
		Sync(ctx)
	require.NoError(t, err, "engine telemetry did not reach the fake cloud")

	// the engine refreshed through its own endpoint: it read the refresh
	// token from the client host's credentials file and exchanged it
	_, err = events.
		WithExec([]string{"test", "-s", fmt.Sprintf("/events/%s/engine/issued-tokens.txt", eventsID)}).
		Sync(ctx)
	require.NoError(t, err, "the engine never refreshed the OAuth token")

	// all three signals must keep flowing across refreshes *from the engine*:
	// the engine-side metric exporter once missed the refresh wiring, so after
	// the first expiry traces and logs kept arriving while metrics silently
	// stopped. The request log pins each signal to an engine-issued token
	// (fresh-token-<id>-engine-N), so the client's own exports can't mask an
	// engine-side regression.
	for _, signal := range []string{"traces", "logs", "metrics"} {
		_, err := events.
			WithExec([]string{
				"grep", "-E",
				fmt.Sprintf("^fresh-token-.*-engine-[0-9]+ /%s/v1/%s$", eventsID, signal),
				"/events/requests.log",
			}).
			Sync(ctx)
		require.NoError(t, err, "the engine exported no %s with a refreshed token", signal)
	}

	// refreshed credentials were persisted back over the stale ones
	creds, err := clientCtr.WithExec([]string{"cat", "/root/.config/dagger/credentials.json"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, creds, "fresh-token-")
	require.NotContains(t, creds, "stale-token")
}

// A Dagger Cloud outage must cost telemetry, never the build. The fake cloud
// here simulates the worst outage mode: it accepts requests and never answers
// (a hard-down endpoint at least fails exports fast; a hanging one eats
// timeouts). When a command exits, the client gives the engine 10 seconds to
// shut down, then fails the build — and the engine pushes its remaining
// telemetry to Cloud inside that window. The engine must give up on Cloud
// early enough to answer the client in time: the command has to exit 0, just
// without its telemetry.
func (ClientSuite) TestEngineTelemetryCloudOutage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"go.mod",
			"go.sum",
		},
	})

	fakeCloud := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
		WithExposedPort(8080).
		AsService()

	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithServiceBinding("cloud", fakeCloud)
	}))

	_, err = engineClientContainer(ctx, t, c, devEngine).
		WithServiceBinding("cloud", fakeCloud).
		WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/hang/"+identity.NewID()).
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "test").
		WithExec([]string{
			"dagger", "core", "container",
			"from", "--address=" + alpineImage,
			"with-exec", "--args=true",
			"stdout",
		}).
		Sync(ctx)
	require.NoError(t, err, "a hanging cloud must never fail the build")
}
