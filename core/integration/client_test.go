package core

// These tests cover `dagger.Connect` clients used by Go callers. They verify
// connection setup, teardown, and session behavior between a caller and the
// engine.
//
// See also:
// - suite_test.go: shared connection setup used by integration tests.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
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

func (ClientSuite) TestSilentSessionExportsTelemetryToCloud(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)
	code := core.NewQuery(c).Host().Directory(thisRepoPath, core.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"core/integration/testdata/basic-container/",
			"sdk/go/",
			"go.mod",
			"go.sum",
		},
	})

	eventsVol := core.NewQuery(c).CacheVolume("dagger-silent-session-events-" + identity.NewID())
	base := core.NewQuery(c).Container().
		From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		With(goCache(c)).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src")

	fakeCloud := base.
		WithMountedCache("/events", eventsVol).
		WithDefaultArgs([]string{"go", "run", "./core/integration/testdata/telemetry/"}).
		WithExposedPort(8080).
		AsService()

	eventsID := identity.NewID()
	// The engine publishes the session's telemetry to the same Cloud.
	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *core.Container) *core.Container {
		return ctr.WithServiceBinding("cloud", fakeCloud)
	}))
	_, err = base.
		WithServiceBinding("dev-engine", devEngine).
		WithServiceBinding("cloud", fakeCloud).
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dev-engine:1234").
		WithEnvVariable("DAGGER_CLOUD_URL", "http://cloud:8080/"+eventsID).
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "test").
		WithEnvVariable("DAGGER_SILENT", "true").
		WithExec([]string{"go", "run", "./core/integration/testdata/basic-container/"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Sync(ctx)
	require.NoError(t, err, "silent SDK session handshake, query, and close must succeed")

	_, err = base.
		WithMountedCache("/events", eventsVol).
		WithExec([]string{"grep", "-F", "Container.withExec", fmt.Sprintf("/events/%s/v1/traces.json.names", eventsID)}).
		Sync(ctx)
	require.NoError(t, err, "the silent session's engine spans must reach Cloud")
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
	fqRef, err := core.NewQuery(c1).Container().From(alpineImage).ImageRef(ctx1)
	require.NoError(t, err)

	echo := func(ctx context.Context, c *dagger.Client, msg string) {
		_, err := core.NewQuery(c).Container().
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
		WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
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

// TestSessionRetiredByEngineReportsCause stops a `dagger session` process long
// enough for the engine to retire its session for missed health checks, then
// resumes it. Requests on the retired session must explain what happened,
// instead of failing with the engine's generic "already used and released"
// error, and the process must exit with that cause.
func (ClientSuite) TestSessionRetiredByEngineReportsCause(ctx context.Context, t *testctx.T) {
	cmd := exec.Command(daggerCliPath(t), "session")
	cleanupExec(t, cmd)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "DAGGER_SESSION_PORT" || key == "DAGGER_SESSION_TOKEN" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr safeBuffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	// Never leave a stopped process behind, even if the test fails.
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGCONT) })

	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "session params; stderr: %s", stderr.String())
	var params struct {
		Port         int    `json:"port"`
		SessionToken string `json:"session_token"`
	}
	require.NoError(t, json.Unmarshal([]byte(line), &params))
	query := func() (int, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%d/query", params.Port),
			strings.NewReader(`{"query":"{ version }"}`))
		require.NoError(t, err)
		req.SetBasicAuth(params.SessionToken, "")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}
	code, body := query()
	require.Equal(t, http.StatusOK, code, body)

	// The engine checks the client every 5s and gives up after two failed
	// checks with 30s and 45s timeouts, about 80s after the client stops.
	require.NoError(t, cmd.Process.Signal(syscall.SIGSTOP))
	select {
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, cmd.Process.Signal(syscall.SIGCONT))

	// The resumed process notices the closed connection right away; poll
	// briefly so a request racing that detection is not a failure.
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, body = query()
		if strings.Contains(body, "engine closed session") || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "engine closed session")
	require.Contains(t, body, "fails its health checks")
	require.NotContains(t, body, "already used and released")
	t.Logf("response on the retired session: %s", body)

	require.NoError(t, stdin.Close())
	err = cmd.Wait()
	require.Error(t, err, "stderr: %s", stderr.String())
	require.Contains(t, stderr.String(), "engine closed session")
}

func (ClientSuite) TestWaitsForEngine(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	devEngine := devEngineContainer(c, func(c *core.Container) *core.Container {
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
				core.ContainerWithNewFileOpts{Permissions: 0o700},
			).
			WithEntrypoint([]string{"/usr/local/bin/slow-entrypoint.sh"})
	})

	clientCtr := engineClientContainer(ctx, t, c, devEngineContainerAsService(devEngine))
	_, err := clientCtr.
		WithNewFile("/query.graphql", `{ version }`). // arbitrary valid query
		WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).Sync(ctx)

	require.NoError(t, err)
}

func (ClientSuite) TestSendsLabelsInTelemetry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)

	code := core.NewQuery(c).Host().Directory(thisRepoPath, core.HostDirectoryOpts{
		Include: []string{
			"core/integration/testdata/telemetry/",
			"core/integration/testdata/basic-container/",
			"sdk/go/",
			"go.mod",
			"go.sum",
		},
	})

	eventsVol := core.NewQuery(c).CacheVolume("dagger-dev-engine-events-" + identity.NewID())

	withCode := core.NewQuery(c).Container().
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
	// The engine publishes the session's telemetry to the same Cloud.
	devEngine := devEngineContainerAsService(devEngineContainer(c, func(ctr *core.Container) *core.Container {
		return ctr.WithServiceBinding("cloud", fakeCloud)
	}))

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
		WithExec([]string{"dagger", "run", "go", "run", "./core/integration/testdata/basic-container/"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Stderr(ctx)
	require.NoError(t, err)

	_, err = withCode.
		WithMountedCache("/events", eventsVol).
		WithExec([]string{"grep", "-R", "dagger.io/git.title", fmt.Sprintf("/events/%s", eventsID)}).
		WithExec([]string{"grep", "-R", "init test repo", fmt.Sprintf("/events/%s", eventsID)}).
		Sync(ctx)
	require.NoError(t, err)
}
