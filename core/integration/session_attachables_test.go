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
	joinerHelperEnv  = "_DAGGER_TEST_JOINER_SESSION"
	joinerHelperPort = "_DAGGER_TEST_JOINER_PORT"
	joinerHelperLine = "JOINER "
)

// TestSessionJoinerHelperProcess is not a test. It is the second root client
// of TestHostServiceFailsWhenSourceClientLeaves, run as a separate process so
// the test can kill it. It joins the session, creates a container-to-host
// service, prints its client ID and the service ID, and closes when it reads a
// line on stdin.
func TestSessionJoinerHelperProcess(t *testing.T) {
	sessionID := os.Getenv(joinerHelperEnv)
	if sessionID == "" {
		t.Skip("helper process for TestSessionAttachables")
	}
	ctx := context.Background()
	port, err := strconv.Atoi(os.Getenv(joinerHelperPort))
	require.NoError(t, err)

	joiner, err := client.Connect(ctx, client.Params{
		RunnerHost: os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"),
		SessionID:  sessionID,
	})
	require.NoError(t, err)

	svcID, err := joiner.Dagger().Host().
		Service([]dagger.PortForward{{Frontend: port, Backend: port}}, dagger.HostServiceOpts{Host: "127.0.0.1"}).
		ID(ctx)
	require.NoError(t, err)
	fmt.Printf("%s%s %s\n", joinerHelperLine, joiner.ID, svcID)

	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	require.NoError(t, joiner.Close())
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

			helper := exec.Command(os.Args[0], "-test.run=^TestSessionJoinerHelperProcess$")
			helper.Env = append(os.Environ(),
				joinerHelperEnv+"="+creator.SessionID,
				joinerHelperPort+"="+strconv.Itoa(port),
			)
			helper.Stderr = os.Stderr
			helperStdin, err := helper.StdinPipe()
			require.NoError(t, err)
			helperStdout, err := helper.StdoutPipe()
			require.NoError(t, err)
			require.NoError(t, helper.Start())
			t.Cleanup(func() {
				helper.Process.Kill()
				helper.Wait()
			})
			var joinerID, svcID string
			scanner := bufio.NewScanner(helperStdout)
			for scanner.Scan() {
				if fields, ok := strings.CutPrefix(scanner.Text(), joinerHelperLine); ok {
					joinerID, svcID, _ = strings.Cut(fields, " ")
					break
				}
			}
			require.NotEmpty(t, svcID, "helper did not report its service: %v", scanner.Err())
			go io.Copy(io.Discard, helperStdout)

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

			switch leave {
			case "shutdown":
				_, err = io.WriteString(helperStdin, "close\n")
				require.NoError(t, err)
				require.NoError(t, helper.Wait())
			case "kill":
				require.NoError(t, helper.Process.Kill())
				helper.Wait()
			}
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
