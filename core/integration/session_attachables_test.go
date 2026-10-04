package core

// These tests cover how the engine picks the client that answers an attachable
// call when a session has more than one root client, and what happens when
// that client has left. A second root client joins the creator's session with
// client.Params.SessionID.
//
// See also:
// - lockfile_test.go: workspace lockfile write-back when a client closes.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/session/terminal"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

type SessionAttachablesSuite struct{}

func TestSessionAttachables(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SessionAttachablesSuite{})
}

// connectEngineClient connects a root client directly through engine/client,
// so the test controls its attachables and the session it joins.
func connectEngineClient(ctx context.Context, t testing.TB, params client.Params) *client.Client {
	t.Helper()
	params.RunnerHost = os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST")
	c, err := client.Connect(ctx, params)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func (SessionAttachablesSuite) TestTerminalGoesToRequestingClient(ctx context.Context, t *testctx.T) {
	var mu sync.Mutex
	var opened []string
	recordTerminal := func(name string) terminal.WithTerminalFunc {
		return func(func(io.Reader, io.Writer, io.Writer) error) error {
			mu.Lock()
			opened = append(opened, name)
			mu.Unlock()
			return errors.New("test terminal declined")
		}
	}

	creator := connectEngineClient(ctx, t, client.Params{WithTerminal: recordTerminal("creator")})
	joiner := connectEngineClient(ctx, t, client.Params{
		SessionID:    creator.SessionID,
		WithTerminal: recordTerminal("joiner"),
	})

	_, err := joiner.Dagger().Container().
		From(alpineImage).
		WithEnvVariable("BUST", identity.NewID()).
		Terminal().
		Sync(ctx)
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"joiner"}, opened)
}

type recordingPromptHandler struct {
	mu      sync.Mutex
	prompts []string
}

func (h *recordingPromptHandler) HandlePrompt(_ context.Context, title, _ string, dest any) error {
	h.mu.Lock()
	h.prompts = append(h.prompts, title)
	h.mu.Unlock()
	// Answer no: a yes would be persisted for every later client on this host.
	if b, ok := dest.(*bool); ok {
		*b = false
	}
	return nil
}

func (h *recordingPromptHandler) HandleForm(context.Context, *huh.Form) error {
	return errors.New("forms are not used by this test")
}

func (SessionAttachablesSuite) TestPromptGoesToClientWithPromptHandler(ctx context.Context, t *testctx.T) {
	// The creator has no prompt handler; the joiner has one.
	creator := connectEngineClient(ctx, t, client.Params{})
	prompts := &recordingPromptHandler{}
	connectEngineClient(ctx, t, client.Params{
		SessionID:     creator.SessionID,
		PromptHandler: prompts,
	})

	c := creator.Dagger()
	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("greet me").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Hello!"},
		}))

	// A remote module that uses the LLM asks for permission with a prompt.
	require.NoError(t, c.ModuleSource(directModuleRef).AsModule().Serve(ctx))
	err := c.Do(ctx, &dagger.Request{
		Query: `query($model: String!, $bust: String!) {
			llmDirect(model: $model) { prompt(stringArg: "greet me", cacheBuster: $bust) }
		}`,
		Variables: map[string]any{"model": model, "bust": identity.NewID()},
	}, &dagger.Response{})
	require.ErrorContains(t, err, "was denied LLM access")

	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	require.Equal(t, []string{"Allow LLM access?"}, prompts.prompts)
}

const (
	sessionHelperModeEnv    = "_DAGGER_TEST_SESSION_HELPER"
	sessionHelperSessionEnv = "_DAGGER_TEST_SESSION_HELPER_SESSION"
	sessionHelperArgEnv     = "_DAGGER_TEST_SESSION_HELPER_ARG"
	sessionHelperLine       = "SESSION-HELPER "
)

// sessionHelper is a root client run as a separate process, so that a test
// can kill it.
type sessionHelper struct {
	cmd   *exec.Cmd
	stdin io.Writer

	SessionID string
	ClientID  string
	// Value is what the helper's mode reports, such as an object ID.
	Value string
}

// startSessionHelper starts TestSessionHelperProcess in the given mode and
// waits for it to report.
func startSessionHelper(t *testctx.T, mode, sessionID, arg string) *sessionHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionHelperProcess$")
	cmd.Env = append(os.Environ(),
		sessionHelperModeEnv+"="+mode,
		sessionHelperSessionEnv+"="+sessionID,
		sessionHelperArgEnv+"="+arg,
	)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	h := &sessionHelper{cmd: cmd, stdin: stdin}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if line, ok := strings.CutPrefix(scanner.Text(), sessionHelperLine); ok {
			fields := strings.Fields(line)
			require.Len(t, fields, 3)
			h.SessionID, h.ClientID, h.Value = fields[0], fields[1], fields[2]
			break
		}
	}
	require.NotEmpty(t, h.ClientID, "helper did not report: %v", scanner.Err())
	go io.Copy(io.Discard, stdout)
	return h
}

// leave makes the helper close its client ("shutdown") or kills it ("kill").
func (h *sessionHelper) leave(t *testctx.T, how string) {
	t.Helper()
	switch how {
	case "shutdown":
		_, err := io.WriteString(h.stdin, "close\n")
		require.NoError(t, err)
		require.NoError(t, h.cmd.Wait())
	case "kill":
		require.NoError(t, h.cmd.Process.Kill())
		h.cmd.Wait()
	default:
		t.Fatalf("unknown way to leave: %s", how)
	}
}

// webService serves body over HTTP on port 8080.
func webService(c *dagger.Client, body string) *dagger.Service {
	return c.Container().
		From(busyboxImage).
		WithNewFile("/www/index.html", body).
		WithExposedPort(8080).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"httpd", "-f", "-p", "8080", "-h", "/www"},
		})
}

// TestSessionHelperProcess is not a test. It is a root client that session
// tests run as a separate process, so they can kill it. It connects,
// does what its mode says, prints its session ID, client ID and a value, and
// closes when it reads a line on stdin. Modes:
//   - host-service: join the session and create a container-to-host service
//     forwarding the port in the argument; report the service ID.
//   - detached-creator: create a detached session and start a web service
//     serving the argument; report the service ID.
//   - tunnel: join the session and forward the service whose ID is the
//     argument to a host port; report the port.
func TestSessionHelperProcess(t *testing.T) {
	mode := os.Getenv(sessionHelperModeEnv)
	if mode == "" {
		t.Skip("helper process for session tests")
	}
	ctx := context.Background()
	arg := os.Getenv(sessionHelperArgEnv)
	params := client.Params{
		RunnerHost: os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"),
		SessionID:  os.Getenv(sessionHelperSessionEnv),
	}
	if mode == "detached-creator" {
		params.DetachedSession = true
	}
	c, err := client.Connect(ctx, params)
	require.NoError(t, err)
	dag := c.Dagger()

	var value string
	switch mode {
	case "host-service":
		port, err := strconv.Atoi(arg)
		require.NoError(t, err)
		id, err := dag.Host().
			Service([]dagger.PortForward{{Frontend: port, Backend: port}}, dagger.HostServiceOpts{Host: "127.0.0.1"}).
			ID(ctx)
		require.NoError(t, err)
		value = string(id)
	case "detached-creator":
		svc, err := webService(dag, arg).Start(ctx)
		require.NoError(t, err)
		id, err := svc.ID(ctx)
		require.NoError(t, err)
		value = string(id)
	case "tunnel":
		svc, err := dagger.Load[*dagger.Service](ctx, dag, dagger.ID(arg))
		require.NoError(t, err)
		tunnel, err := dag.Host().Tunnel(svc).Start(ctx)
		require.NoError(t, err)
		endpoint, err := tunnel.Endpoint(ctx)
		require.NoError(t, err)
		value = endpoint
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	fmt.Printf("%s%s %s %s\n", sessionHelperLine, c.SessionID, c.ID, value)

	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	require.NoError(t, c.Close())
}

func (SessionAttachablesSuite) TestHostServiceFailsWhenSourceClientLeaves(ctx context.Context, t *testctx.T) {
	// serveHost serves an HTTP endpoint on the test's host and reports each
	// request.
	serveHost := func(t *testctx.T, body string) (int, <-chan struct{}) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { listener.Close() })
		requests := make(chan struct{}, 16)
		go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, body)
			requests <- struct{}{}
		}))
		return listener.Addr().(*net.TCPAddr).Port, requests
	}

	for _, leave := range []string{"shutdown", "kill"} {
		t.Run(leave, func(ctx context.Context, t *testctx.T) {
			port, _ := serveHost(t, "HOSTC2H")
			readyPort, ready := serveHost(t, "")

			creator := connectEngineClient(ctx, t, client.Params{})
			c := creator.Dagger()

			joiner := startSessionHelper(t, "host-service", creator.SessionID, strconv.Itoa(port))
			joinerID, svcID := joiner.ClientID, joiner.Value

			// The joiner's host service, and a container service bound to it
			// that the creator starts.
			hostSvc, err := dagger.Load[*dagger.Service](ctx, c, dagger.ID(svcID))
			require.NoError(t, err)
			app, err := c.Container().
				From(alpineImage).
				WithServiceBinding("hs", hostSvc).
				WithEnvVariable("BUST", identity.NewID()).
				WithDefaultArgs([]string{"sleep", "3600"}).
				AsService().
				Start(ctx)
			require.NoError(t, err)
			probe := func(ctx context.Context) (string, error) {
				return c.Container().
					From(alpineImage).
					WithServiceBinding("hs", hostSvc).
					WithEnvVariable("BUST", identity.NewID()).
					WithExec([]string{"wget", "-T", "30", "-qO-", fmt.Sprintf("http://hs:%d/", port)}).
					Stdout(ctx)
			}

			out, err := probe(ctx)
			require.NoError(t, err)
			require.Equal(t, "HOSTC2H", out)

			joiner.leave(t, leave)
			// The engine notices a lost attachables connection after two failed
			// health checks, 5s apart.
			time.Sleep(15 * time.Second)

			// Watch the container service from an exec bound only to it. It
			// signals through a host service of the creator once it runs.
			readySvc := c.Host().Service(
				[]dagger.PortForward{{Frontend: readyPort, Backend: readyPort}},
				dagger.HostServiceOpts{Host: "127.0.0.1"})
			watchErr := make(chan error, 1)
			go func() {
				_, err := c.Container().
					From(alpineImage).
					WithServiceBinding("app", app).
					WithServiceBinding("ready", readySvc).
					WithEnvVariable("BUST", identity.NewID()).
					WithExec([]string{"sh", "-c", fmt.Sprintf("wget -qO- http://ready:%d/ && sleep 60", readyPort)}).
					Sync(ctx)
				watchErr <- err
			}()
			select {
			case <-ready:
			case err := <-watchErr:
				require.NoError(t, err, "watcher ended before it was ready")
			case <-time.After(60 * time.Second):
				require.Fail(t, "watcher did not start")
			}

			// A new connection through the host service fails fast, naming the
			// client that left.
			probeCtx, cancel := context.WithTimeout(ctx, 75*time.Second)
			defer cancel()
			start := time.Now()
			_, err = probe(probeCtx)
			require.Error(t, err)
			require.Less(t, time.Since(start), 10*time.Second, "probe should fail fast, got: %v", err)
			requireErrOut(t, err, "(aliased as hs) exited")
			requireErrOut(t, err, joinerID)

			// The container service bound to it stops because its dependency
			// exited.
			select {
			case err := <-watchErr:
				require.Error(t, err)
				requireErrOut(t, err, "(aliased as app) exited")
				requireErrOut(t, err, "(aliased as hs) exited")
			case <-time.After(30 * time.Second):
				require.Fail(t, "container service bound to the host service did not stop")
			}
		})
	}
}

func (SessionAttachablesSuite) TestSecretFallsBackToLiveBinder(ctx context.Context, t *testctx.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	secretValue := identity.NewID()
	require.NoError(t, os.WriteFile(secretPath, []byte(secretValue), 0o600))

	creator := connectEngineClient(ctx, t, client.Params{})
	c := creator.Dagger()
	_, err := c.Container().From(alpineImage).Sync(ctx)
	require.NoError(t, err)

	source := connectEngineClient(ctx, t, client.Params{SessionID: creator.SessionID})
	secretID, err := source.Dagger().Secret("file://" + secretPath).ID(ctx)
	require.NoError(t, err)
	require.NoError(t, source.Close())

	secret, err := dagger.Load[*dagger.Secret](ctx, c, secretID)
	require.NoError(t, err)
	useSecret := func() error {
		_, err := c.Container().
			From(alpineImage).
			WithSecretVariable("S", secret).
			WithEnvVariable("BUST", identity.NewID()).
			WithExec([]string{"sh", "-c", `test "$S" = "` + secretValue + `"`}).
			Sync(ctx)
		return err
	}

	start := time.Now()
	err = useSecret()
	require.ErrorContains(t, err, "no available client binding")
	require.Less(t, time.Since(start), 5*time.Second)

	// A live client that bound the same value can serve it.
	binder := connectEngineClient(ctx, t, client.Params{SessionID: creator.SessionID})
	_, err = binder.Dagger().Secret("file://" + secretPath).ID(ctx)
	require.NoError(t, err)
	require.NoError(t, useSecret())
}
