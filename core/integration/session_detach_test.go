package core

// These tests cover detached sessions: sessions that outlive the client that
// created them, the engine API to list, stop and close them, joining only an
// existing session, and the session telemetry stream.
//
// See also:
// - session_attachables_test.go: which client answers attachable calls.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

type DetachedSessionSuite struct{}

func TestDetachedSessions(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DetachedSessionSuite{})
}

// The engine notices a lost attachables connection after two failed health
// checks, 5s apart.
const attachablesLossDetection = 15 * time.Second

type engineSessionClient struct {
	ClientID   string
	Hostname   string
	PID        *int
	Command    *string
	Background bool
	Connected  bool
	Provides   []string
	Forwards   []struct {
		Port        int
		Description string
	}
}

type engineSession struct {
	SessionID string
	Detached  bool
	Clients   []engineSessionClient
}

const engineSessionSelection = `sessionID detached clients {
	clientID hostname pid command background connected provides
	forwards { port description }
}`

// trySessions lists the engine's sessions from c.
func trySessions(ctx context.Context, c *dagger.Client) ([]engineSession, error) {
	var res struct {
		Engine struct {
			Sessions []engineSession
		}
	}
	err := c.Do(ctx, &dagger.Request{
		Query: `{ engine { sessions { ` + engineSessionSelection + ` } } }`,
	}, &dagger.Response{Data: &res})
	return res.Engine.Sessions, err
}

func listSessions(ctx context.Context, t *testctx.T, c *dagger.Client) []engineSession {
	t.Helper()
	sessions, err := trySessions(ctx, c)
	require.NoError(t, err)
	return sessions
}

// findSession returns the session with the given ID from a listing.
func findSession(sessions []engineSession, id string) (engineSession, bool) {
	i := slices.IndexFunc(sessions, func(s engineSession) bool { return s.SessionID == id })
	if i < 0 {
		return engineSession{}, false
	}
	return sessions[i], true
}

func stopSession(ctx context.Context, c *dagger.Client, id string) error {
	return c.Do(ctx, &dagger.Request{
		Query:     `query($id: String!) { engine { session(id: $id) { stop } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{})
}

// stopSessionOnCleanup stops a detached session when the test ends, from a
// session of its own.
func stopSessionOnCleanup(ctx context.Context, t *testctx.T, id string) {
	t.Cleanup(func() {
		m := connectEngineClient(context.WithoutCancel(ctx), t, client.Params{})
		_ = stopSession(context.WithoutCancel(ctx), m.Dagger(), id)
	})
}

// waitSessionGone waits until the session is no longer listed.
func waitSessionGone(ctx context.Context, t *testctx.T, c *dagger.Client, id string) {
	t.Helper()
	require.Eventually(t, func() bool {
		sessions, err := trySessions(ctx, c)
		if err != nil {
			return false
		}
		_, ok := findSession(sessions, id)
		return !ok
	}, 60*time.Second, 500*time.Millisecond, "session %s still listed", id)
}

func fetchFromService(ctx context.Context, c *dagger.Client, svc *dagger.Service) (string, error) {
	return c.Container().
		From(alpineImage).
		WithServiceBinding("web", svc).
		WithEnvVariable("BUST", identity.NewID()).
		WithExec([]string{"wget", "-qO-", "http://web:8080/"}).
		Stdout(ctx)
}

func (DetachedSessionSuite) TestSessionOutlivesCreator(ctx context.Context, t *testctx.T) {
	for _, leave := range []string{"shutdown", "kill"} {
		t.Run(leave, func(ctx context.Context, t *testctx.T) {
			nonce := identity.NewID()
			creator := startSessionHelper(t, "detached-creator", "", nonce)
			stopSessionOnCleanup(ctx, t, creator.SessionID)

			creator.leave(t, leave)
			// Wait past the point where an attached session would have been
			// torn down.
			time.Sleep(attachablesLossDetection)

			joiner := connectEngineClient(ctx, t, client.Params{
				SessionID:           creator.SessionID,
				JoinExistingSession: true,
			})
			c := joiner.Dagger()
			out, err := c.Container().
				From(alpineImage).
				WithEnvVariable("BUST", identity.NewID()).
				WithExec([]string{"echo", "joined"}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "joined\n", out)

			// The service the creator started is still running.
			svc, err := dagger.Load[*dagger.Service](ctx, c, dagger.ID(creator.Value))
			require.NoError(t, err)
			out, err = fetchFromService(ctx, c, svc)
			require.NoError(t, err)
			require.Equal(t, nonce, out)
		})
	}
}

func (DetachedSessionSuite) TestStopSession(ctx context.Context, t *testctx.T) {
	for _, from := range []string{"management session", "inside the session"} {
		t.Run(from, func(ctx context.Context, t *testctx.T) {
			creator := connectEngineClient(ctx, t, client.Params{DetachedSession: true})
			_, err := webService(creator.Dagger(), identity.NewID()).Start(ctx)
			require.NoError(t, err)
			member := connectEngineClient(ctx, t, client.Params{SessionID: creator.SessionID})
			_, err = member.Dagger().DefaultPlatform(ctx)
			require.NoError(t, err)
			require.NoError(t, creator.Close())

			mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
			_, ok := findSession(listSessions(ctx, t, mgmt), creator.SessionID)
			require.True(t, ok)

			switch from {
			case "management session":
				require.NoError(t, stopSession(ctx, mgmt, creator.SessionID))
			case "inside the session":
				require.NoError(t, stopSession(ctx, member.Dagger(), creator.SessionID))
			}
			waitSessionGone(ctx, t, mgmt, creator.SessionID)

			// Its remaining client was closed with it.
			_, err = member.Dagger().Container().
				From(alpineImage).
				WithEnvVariable("BUST", identity.NewID()).
				WithExec([]string{"true"}).
				Sync(ctx)
			require.Error(t, err)
		})
	}
}

func (DetachedSessionSuite) TestListSessions(ctx context.Context, t *testctx.T) {
	creator := connectEngineClient(ctx, t, client.Params{
		DetachedSession: true,
		Background:      true,
		Command:         "dagger call --detach build",
	})
	stopSessionOnCleanup(ctx, t, creator.SessionID)
	joiner := connectEngineClient(ctx, t, client.Params{
		SessionID:     creator.SessionID,
		Command:       "dagger sessions attach",
		PromptHandler: &recordingPromptHandler{},
	})
	_, err := joiner.Dagger().DefaultPlatform(ctx)
	require.NoError(t, err)

	mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
	sess, ok := findSession(listSessions(ctx, t, mgmt), creator.SessionID)
	require.True(t, ok)
	require.True(t, sess.Detached)
	require.Len(t, sess.Clients, 2)

	hostname, err := os.Hostname()
	require.NoError(t, err)
	first, second := sess.Clients[0], sess.Clients[1]
	require.Equal(t, creator.ID, first.ClientID)
	require.Equal(t, hostname, first.Hostname)
	require.Equal(t, os.Getpid(), *first.PID)
	require.Equal(t, "dagger call --detach build", *first.Command)
	require.True(t, first.Background)
	require.True(t, first.Connected)
	require.Subset(t, first.Provides, []string{"files", "git", "registry-auth", "secrets", "sockets", "terminal", "tunnels"})
	require.NotContains(t, first.Provides, "prompt")

	require.Equal(t, joiner.ID, second.ClientID)
	require.False(t, second.Background)
	require.Equal(t, "dagger sessions attach", *second.Command)
	require.Contains(t, second.Provides, "prompt")

	// The lookups by ID return the same session and client.
	var res struct {
		Engine struct {
			Session struct {
				SessionID string
				Client    engineSessionClient
			}
		}
	}
	err = mgmt.Do(ctx, &dagger.Request{
		Query: `query($id: String!, $client: String!) { engine { session(id: $id) {
			sessionID client(id: $client) { clientID command }
		} } }`,
		Variables: map[string]any{"id": creator.SessionID, "client": joiner.ID},
	}, &dagger.Response{Data: &res})
	require.NoError(t, err)
	require.Equal(t, creator.SessionID, res.Engine.Session.SessionID)
	require.Equal(t, joiner.ID, res.Engine.Session.Client.ClientID)

	err = stopSession(ctx, mgmt, identity.NewID())
	require.ErrorContains(t, err, "not found")
}

// logBodies collects the bodies of exported log records.
type logBodies struct {
	mu     sync.Mutex
	bodies []string
}

func (e *logBodies) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rec := range records {
		e.bodies = append(e.bodies, rec.Body().AsString())
	}
	return nil
}
func (e *logBodies) Shutdown(context.Context) error   { return nil }
func (e *logBodies) ForceFlush(context.Context) error { return nil }

func (e *logBodies) contains(s string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.ContainsFunc(e.bodies, func(body string) bool { return strings.Contains(body, s) })
}

// pushSpan pushes one span named name to the engine as c, the way a root
// client pushes its own telemetry.
func pushSpan(t *testctx.T, c *client.Client, name string) {
	t.Helper()
	now := uint64(time.Now().UnixNano())
	body, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					TraceId:           []byte(identity.NewID()[:16]),
					SpanId:            []byte(identity.NewID()[:8]),
					Name:              name,
					StartTimeUnixNano: now,
					EndTimeUnixNano:   now,
				}},
			}},
		}},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.SetBasicAuth(c.SecretToken, "")
	resp := httptest.NewRecorder()
	c.ServeHTTP(resp, req)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
}

func (DetachedSessionSuite) TestSessionTelemetry(ctx context.Context, t *testctx.T) {
	marker := identity.NewID()
	creator := connectEngineClient(ctx, t, client.Params{DetachedSession: true})
	_, err := creator.Dagger().Container().
		From(alpineImage).
		WithExec([]string{"echo", "creator-" + marker}).
		Sync(ctx)
	require.NoError(t, err)

	joiner := connectEngineClient(ctx, t, client.Params{SessionID: creator.SessionID})
	_, err = joiner.Dagger().Container().
		From(alpineImage).
		WithExec([]string{"echo", "joiner-" + marker}).
		Sync(ctx)
	require.NoError(t, err)
	pushSpan(t, joiner, "pushed-"+marker)
	require.NoError(t, joiner.Close())
	require.NoError(t, creator.Close())

	// A client that subscribes after the work finished replays all of it.
	spans := tracetest.NewInMemoryExporter()
	logs := &logBodies{}
	attach := connectEngineClient(ctx, t, client.Params{
		SessionID:           creator.SessionID,
		JoinExistingSession: true,
		SessionTelemetry:    true,
		EngineTrace:         spans,
		EngineLogs:          logs,
	})
	require.Eventually(t, func() bool {
		return slices.ContainsFunc(spans.GetSpans(), func(s tracetest.SpanStub) bool {
			return s.Name == "pushed-"+marker
		}) && logs.contains("creator-"+marker) && logs.contains("joiner-"+marker)
	}, 60*time.Second, 500*time.Millisecond, "session stream lacks the session's work")

	// The stream ends when the session is stopped.
	mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
	require.NoError(t, stopSession(ctx, mgmt, creator.SessionID))
	done := make(chan error, 1)
	go func() { done <- attach.WaitTelemetry() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		require.Fail(t, "session telemetry stream did not end after the session stopped")
	}
}

func (DetachedSessionSuite) TestRegistryAuthWithoutProvider(ctx context.Context, t *testctx.T) {
	creator := connectEngineClient(ctx, t, client.Params{DetachedSession: true})
	stopSessionOnCleanup(ctx, t, creator.SessionID)
	c := creator.Dagger()

	// A service with Dagger access pulls an image that needs credentials once
	// its creator has left, and serves the result.
	pull := fmt.Sprintf(`container | from %s | sync`, privateRegistryRef("detached-auth"))
	script := fmt.Sprintf(`mkdir -p /www && echo waiting > /www/index.html && httpd -p 8080 -h /www
sleep %d
start=$(date +%%s)
dagger -s -c '%s' > /www/out 2>&1
echo "exit=$? seconds=$(( $(date +%%s) - start ))" >> /www/out
mv /www/out /www/index.html
sleep 3600`, int((attachablesLossDetection + 10*time.Second).Seconds()), pull)
	svc, err := c.Container().
		From(busyboxImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithEnvVariable("BUST", identity.NewID()).
		WithExposedPort(8080).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"sh", "-c", script}}).
		Start(ctx)
	require.NoError(t, err)
	svcID, err := svc.ID(ctx)
	require.NoError(t, err)
	require.NoError(t, creator.Close())

	// Read the result from a client that joins after the pull.
	time.Sleep(attachablesLossDetection + 20*time.Second)
	joiner := connectEngineClient(ctx, t, client.Params{
		SessionID:           creator.SessionID,
		JoinExistingSession: true,
	})
	jc := joiner.Dagger()
	svc, err = dagger.Load[*dagger.Service](ctx, jc, svcID)
	require.NoError(t, err)
	var out string
	require.Eventually(t, func() bool {
		out, err = fetchFromService(ctx, jc, svc)
		return err == nil && strings.Contains(out, "exit=")
	}, 60*time.Second, time.Second, "no pull result: %q %v", out, err)
	t.Logf("pull result: %s", out)

	// The pull went ahead without credentials, at once, and the registry
	// refused it.
	require.Contains(t, out, "exit=1")
	require.NotContains(t, out, "has left the session")
	require.Contains(t, out, "no basic auth credentials")
	var seconds int
	_, err = fmt.Sscanf(out[strings.LastIndex(out, "seconds="):], "seconds=%d", &seconds)
	require.NoError(t, err)
	require.Less(t, seconds, 10)
}

func (DetachedSessionSuite) TestDepartedClientForwardsStop(ctx context.Context, t *testctx.T) {
	nonce := identity.NewID()
	creator := connectEngineClient(ctx, t, client.Params{DetachedSession: true})
	stopSessionOnCleanup(ctx, t, creator.SessionID)
	c := creator.Dagger()
	svc, err := webService(c, nonce).Start(ctx)
	require.NoError(t, err)
	svcID, err := svc.ID(ctx)
	require.NoError(t, err)

	forwarder := startSessionHelper(t, "tunnel", creator.SessionID, string(svcID))
	// forwards returns how many forwards the forwarder is listed with, or -1.
	forwards := func() int {
		sessions, err := trySessions(ctx, c)
		if err != nil {
			return -1
		}
		sess, _ := findSession(sessions, creator.SessionID)
		for _, cl := range sess.Clients {
			if cl.ClientID == forwarder.ClientID {
				return len(cl.Forwards)
			}
		}
		return -1
	}
	require.Equal(t, 1, forwards())

	forwarder.leave(t, "kill")
	require.Eventually(t, func() bool { return forwards() == 0 },
		attachablesLossDetection+10*time.Second, 500*time.Millisecond,
		"the killed client's forward is still listed")

	// The service started separately keeps running: same instance.
	out, err := fetchFromService(ctx, c, svc)
	require.NoError(t, err)
	require.Equal(t, nonce, out)
}

func (DetachedSessionSuite) TestJoinExistingSessionOnly(ctx context.Context, t *testctx.T) {
	sessionID := identity.NewID()
	_, err := client.Connect(ctx, client.Params{
		RunnerHost:          os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"),
		SessionID:           sessionID,
		JoinExistingSession: true,
	})
	require.ErrorContains(t, err, fmt.Sprintf("session %q not found", sessionID))

	mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
	_, ok := findSession(listSessions(ctx, t, mgmt), sessionID)
	require.False(t, ok)
}
