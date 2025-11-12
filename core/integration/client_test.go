package core

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
			dagger.WithLogOutput(io.MultiWriter(prefixw.New(testutil.NewTWriter(t), name+": "), out)))
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

	// just run any dagger cli command that connects to the engine
	stableID, err := clientCtr.
		WithExec([]string{"adduser", "-u", "1234", "-D", "auser"}).
		WithUser("auser").
		WithWorkdir("/work").
		WithExec([]string{"dagger", "init", "--sdk=go"}).
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
		WithExec([]string{"sh", "-c", "grep dagger.io/git.title $0", fmt.Sprintf("/events/%s/**/*.json", eventsID)}).
		WithExec([]string{"sh", "-c", "grep 'init test repo' $0", fmt.Sprintf("/events/%s/**/*.json", eventsID)}).
		Sync(ctx)
	require.NoError(t, err)
}
