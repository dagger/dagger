package core

// These tests cover the CLI for detached sessions: `--detach` on `call` and
// `up`, the global `--session` flag, and `dagger sessions`. The CLI runs on
// the test host, so that its background processes outlive the commands that
// start them.
//
// See also:
// - session_detach_test.go: the engine side of detached sessions.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

type SessionCLISuite struct{}

func TestSessionCLI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(SessionCLISuite{})
}

// detachWorkspace creates a git workspace on the host whose entrypoint module
// has the functions these tests call. Its web service listens on port.
func detachWorkspace(t *testctx.T, port int) string {
	t.Helper()
	dir := t.TempDir()
	hostGitInit(t, dir)
	for path, contents := range detachWorkspaceFiles(port) {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(contents), 0o644))
	}
	return dir
}

// detachRemoteWorkspace serves the same workspace from a git server in c's
// session, and returns its git ref for -W.
func detachRemoteWorkspace(ctx context.Context, t *testctx.T, c *dagger.Client, port int) string {
	t.Helper()
	content := c.Directory()
	for path, contents := range detachWorkspaceFiles(port) {
		content = content.WithNewFile(path, contents)
	}
	return workspaceSelectionRemoteRef(ctx, t, c, content)
}

func detachWorkspaceFiles(port int) map[string]string {
	web := fmt.Sprintf(`container.from(%q).withExposedPort(%d).asService(args: ["sh", "-c", "mkdir -p /www && cat /proc/sys/kernel/random/uuid > /www/index.html && exec httpd -f -p %d -h /www"])`,
		busyboxImage, port, port)
	return map[string]string{
		"dagger.toml": `[modules.m]
source = ".dagger/modules/m"
entrypoint = true
`,
		".dagger/modules/m/dagger-module.toml": `name = "m"
engineVersion = "v1.0.0"
[runtime]
source = "dang"
`,
		".dagger/modules/m/main.dang": fmt.Sprintf(`type M {
  pub read(ws: Directory! @defaultPath(path: "/"), extra: Directory!): String! {
    ws.file("hello.txt").contents + extra.file("hello.txt").contents
  }
  pub echo(msg: String!): String! {
    container.from(%[1]q).withExec(["echo", msg]).stdout
  }
  pub slow(msg: String!): String! {
    "slow:" + container.from(%[1]q).withExec(["sh", "-c", "sleep 20; echo " + msg]).stdout
  }
  pub list(msg: String!): [String!]! {
    [msg, msg + "-2"]
  }
  pub ctr: Container! {
    container.from(%[1]q)
  }
  pub dir: Directory! {
    directory.withNewFile("a", "b")
  }
  pub fail(msg: String!): String! {
    container.from(%[1]q).withExec(["sh", "-c", "echo " + msg + "; exit 3"]).stdout
  }
  pub sleepy: String! {
    container.from(%[1]q).withExec(["sleep", "600"]).stdout
  }
  pub term: Container! {
    container.from(%[1]q).withEnvVariable("MARK", "term-marker").terminal
  }
  pub web: Service! {
    %[2]s
  }
  pub flags(detach: String!): String! {
    detach
  }
  pub fetch: String! {
    container.from(%[1]q).withServiceBinding("web", %[2]s).withExec(["wget", "-qO-", "http://web:%[3]d/"]).stdout
  }
  pub secretLen(s: Secret!): String! {
    "ok"
  }
  pub changes: Changeset! {
    directory.withNewFile("x", "y").changes(directory)
  }
}
`, alpineImage, web, port),
	}
}

var detachedSessionLine = regexp.MustCompile(`(?m)^Session: (\S+)$`)

// detach runs a command with --detach and returns the session it reports.
// The session is stopped when the test ends.
func detach(ctx context.Context, t *testctx.T, dir string, args ...string) string {
	t.Helper()
	id, _ := detachWithOutput(ctx, t, dir, args...)
	return id
}

// detachWithOutput is detach, also returning the command's output.
func detachWithOutput(ctx context.Context, t *testctx.T, dir string, args ...string) (string, string) {
	t.Helper()
	out, err := hostDaggerExec(ctx, t, dir, args...)
	require.NoError(t, err, string(out))
	m := detachedSessionLine.FindStringSubmatch(string(out))
	require.NotNil(t, m, "no session in output: %s", out)
	id := m[1]
	t.Cleanup(func() {
		_, _ = hostDaggerExec(context.WithoutCancel(ctx), t, dir, "sessions", "stop", id)
	})
	return id, string(out)
}

// noLocalProcess is what a detached command prints when it left no process.
const noLocalProcess = "No local process is running."

// requireNoProcess requires that the session's only client is a background
// client whose process has exited.
func requireNoProcess(ctx context.Context, t *testctx.T, id string) {
	t.Helper()
	clients := sessionClients(ctx, t, id)
	require.Len(t, clients, 1)
	require.True(t, clients[0].Background)
	pid := *clients[0].PID
	require.Eventually(t, func() bool { return processGone(pid) }, 10*time.Second, 200*time.Millisecond,
		"process %d still running", pid)
}

// sessionClients returns the clients of a session, as the engine lists them.
func sessionClients(ctx context.Context, t *testctx.T, id string) []engineSessionClient {
	t.Helper()
	mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
	sess, ok := findSession(listSessions(ctx, t, mgmt), id)
	require.True(t, ok, "session %s not listed", id)
	return sess.Clients
}

// waitBackgroundDone waits until no background client of the session is
// connected.
func waitBackgroundDone(ctx context.Context, t *testctx.T, id string) {
	t.Helper()
	mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
	require.Eventually(t, func() bool {
		sessions, err := trySessions(ctx, mgmt)
		if err != nil {
			return false
		}
		sess, _ := findSession(sessions, id)
		for _, c := range sess.Clients {
			if c.Background && c.Connected {
				return false
			}
		}
		return true
	}, 2*time.Minute, time.Second, "background clients of %s still connected", id)
}

// syncBuffer is a bytes.Buffer safe for concurrent writes and reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// attachUntil attaches to the session with plain progress until its output
// contains every marker, then stops the session and returns the output.
func attachUntil(ctx context.Context, t *testctx.T, dir, id string, markers ...string) string {
	t.Helper()
	var out syncBuffer
	cmd := hostDaggerCommand(ctx, t, dir, "--progress=plain", "sessions", "attach", id)
	cmd.Env = append(cmd.Env, "NO_COLOR=1")
	cmd.Stdout = &out
	cmd.Stderr = &out
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	hasMarkers := func() bool {
		output := out.String()
		for _, marker := range markers {
			if !strings.Contains(output, marker) {
				return false
			}
		}
		return true
	}
	for deadline := time.Now().Add(2 * time.Minute); !hasMarkers(); time.Sleep(time.Second) {
		if time.Now().After(deadline) {
			require.Fail(t, "attach output lacks markers", "%v:\n%s", markers, out.String())
		}
	}

	stopOut, err := hostDaggerExec(ctx, t, dir, "sessions", "stop", id)
	require.NoError(t, err, string(stopOut))
	select {
	case err := <-done:
		require.NoError(t, err, out.String())
	case <-time.After(time.Minute):
		require.Fail(t, "attach did not end with the session")
	}
	return out.String()
}

func httpGet(url string) (string, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)), err
}

func (SessionCLISuite) TestDetachedCallReadsWorkspace(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23451)
	wsMarker, argMarker := identity.NewID(), identity.NewID()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte(wsMarker), 0o644))
	extra := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(extra, "hello.txt"), []byte(argMarker), 0o644))

	id := detach(ctx, t, dir, "call", "--detach", "read", "--extra", extra)
	out := attachUntil(ctx, t, dir, id, wsMarker+argMarker)
	require.Regexp(t, `(?m)^\d+\s+: read --extra \S+ DONE`, out)
}

func (SessionCLISuite) TestAttachAfterBackgroundExits(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23452)

	t.Run("success", func(ctx context.Context, t *testctx.T) {
		marker := identity.NewID()
		id := detach(ctx, t, dir, "call", "--detach", "echo", "--msg", marker)
		waitBackgroundDone(ctx, t, id)
		out := attachUntil(ctx, t, dir, id, marker, "echo --msg "+marker+" DONE")
		require.Regexp(t, `(?m)^\d+\s+: \[[^]]+\] \| `+marker+`$`, out)
	})

	t.Run("failure", func(ctx context.Context, t *testctx.T) {
		marker := identity.NewID()
		id := detach(ctx, t, dir, "call", "--detach", "fail", "--msg", marker)
		waitBackgroundDone(ctx, t, id)
		out := attachUntil(ctx, t, dir, id, "fail --msg "+marker+" ERROR")
		require.Contains(t, out, marker)
		require.Contains(t, out, "exit code: 3")
	})
}

func (SessionCLISuite) TestDetachedUp(ctx context.Context, t *testctx.T) {
	const port = 23453
	dir := detachWorkspace(t, port)
	url := fmt.Sprintf("http://localhost:%d/", port)

	id := detach(ctx, t, dir, "up", "--detach")
	nonce, err := httpGet(url)
	require.NoError(t, err)

	// Kill the background forwarder: the port closes, the service runs on.
	var pid int
	for _, c := range sessionClients(ctx, t, id) {
		if c.Background && c.Connected {
			pid = *c.PID
		}
	}
	require.NotZero(t, pid)
	require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))
	require.Eventually(t, func() bool {
		_, err := httpGet(url)
		return err != nil
	}, 30*time.Second, 500*time.Millisecond, "port still open after the forwarder was killed")
	waitBackgroundDone(ctx, t, id)

	// Forward again into the same session: the same service instance.
	again := detach(ctx, t, dir, "--session", id, "up", "--detach")
	require.Equal(t, id, again)
	out, err := httpGet(url)
	require.NoError(t, err)
	require.Equal(t, nonce, out)
}

func (SessionCLISuite) TestAttachProvidesTerminal(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23454)
	id := detach(ctx, t, dir, "call", "--detach", "sleepy")

	console, err := newTUIConsole(t, 2*time.Minute)
	require.NoError(t, err)
	defer console.Close()
	tty := console.Tty()
	require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 20, Cols: 100}))
	attach := hostDaggerCommandRaw(ctx, t, dir, "sessions", "attach", id)
	attach.Stdin = tty
	attach.Stdout = tty
	attach.Stderr = tty
	require.NoError(t, attach.Start())
	_, err = console.ExpectString("sleepy")
	require.NoError(t, err)

	// Work in a background client of the session opens a terminal.
	detach(ctx, t, dir, "--session", id, "call", "--detach", "term")
	_, err = console.ExpectString(" $ ")
	require.NoError(t, err)
	_, err = console.SendLine(`echo "$MARK"`)
	require.NoError(t, err)
	_, err = console.ExpectString("term-marker")
	require.NoError(t, err)
	_, err = console.SendLine("exit")
	require.NoError(t, err)

	_, err = hostDaggerExec(ctx, t, dir, "sessions", "stop", id)
	require.NoError(t, err)
	go console.ExpectEOF()
	require.NoError(t, attach.Wait())
}

func (SessionCLISuite) TestAttachShowsSeveralCommands(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23455)
	first, second := identity.NewID(), identity.NewID()
	id := detach(ctx, t, dir, "call", "--detach", "echo", "--msg", first)
	detach(ctx, t, dir, "--session", id, "call", "--detach", "echo", "--msg", second)
	waitBackgroundDone(ctx, t, id)

	out := attachUntil(ctx, t, dir, id, "echo --msg "+first+" DONE", "echo --msg "+second+" DONE")
	t.Logf("attach output:\n%s", out)
	require.Regexp(t, `(?m)^\d+\s+: \[[^]]+\] \| `+first+`$`, out)
	require.Regexp(t, `(?m)^\d+\s+: \[[^]]+\] \| `+second+`$`, out)
}

func (SessionCLISuite) TestStopEndsBackgroundClients(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23456)
	id := detach(ctx, t, dir, "up", "--detach")
	detach(ctx, t, dir, "--session", id, "call", "--detach", "sleepy")

	var pids []int
	require.Eventually(t, func() bool {
		pids = nil
		for _, c := range sessionClients(ctx, t, id) {
			if c.Background && c.Connected {
				pids = append(pids, *c.PID)
			}
		}
		return len(pids) == 2
	}, time.Minute, time.Second)

	out, err := hostDaggerExec(ctx, t, dir, "sessions", "stop", id)
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "Stopped session "+id)

	for _, pid := range pids {
		require.Eventually(t, func() bool { return processGone(pid) }, 30*time.Second, 500*time.Millisecond,
			"background process %d still running", pid)
	}
	out, err = hostDaggerExec(ctx, t, dir, "sessions")
	require.NoError(t, err, string(out))
	require.NotContains(t, string(out), id)
}

// processGone reports whether a process has exited. Exited processes that
// nobody reaped count as gone.
func processGone(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	// The state follows the parenthesized command name.
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	return len(fields) > 0 && fields[0] == "Z"
}

func (SessionCLISuite) TestSessionFlagReusesService(ctx context.Context, t *testctx.T) {
	const port = 23457
	dir := detachWorkspace(t, port)
	id := detach(ctx, t, dir, "up", "--detach")
	nonce, err := httpGet(fmt.Sprintf("http://localhost:%d/", port))
	require.NoError(t, err)

	out, err := hostDaggerOutput(ctx, t, dir, "--session", id, "call", "fetch")
	require.NoError(t, err)
	require.Equal(t, nonce, strings.TrimSpace(string(out)))
}

func (SessionCLISuite) TestSessionFlagsAfterFunctionName(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23458)

	t.Run("unknown session", func(ctx context.Context, t *testctx.T) {
		unknown := identity.NewID()
		out, err := hostDaggerExec(ctx, t, dir, "call", "echo", "--msg", "hi", "--session", unknown)
		require.Error(t, err)
		require.Contains(t, string(out), "--session must come before the function name")
		mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
		_, ok := findSession(listSessions(ctx, t, mgmt), unknown)
		require.False(t, ok)
	})

	t.Run("known session", func(ctx context.Context, t *testctx.T) {
		id := detach(ctx, t, dir, "call", "--detach", "sleepy")
		out, err := hostDaggerExec(ctx, t, dir, "call", "echo", "--msg", "hi", "--session", id)
		require.Error(t, err)
		require.Contains(t, string(out), "--session must come before the function name")
	})

	t.Run("detach", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, dir, "call", "echo", "--msg", "hi", "--detach")
		require.Error(t, err)
		require.Contains(t, string(out), "--detach must come before the function name")
	})
}

func (SessionCLISuite) TestFunctionDetachArgument(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23459)
	out, err := hostDaggerOutput(ctx, t, dir, "call", "flags", "--detach", "d1")
	require.NoError(t, err)
	require.Equal(t, "d1", strings.TrimSpace(string(out)))
}

func (SessionCLISuite) TestDetachedStartFailureLeavesNoSession(ctx context.Context, t *testctx.T) {
	dir := detachWorkspace(t, 23460)
	marker := "no-such-function-" + strings.ToLower(identity.NewID())
	out, err := hostDaggerExec(ctx, t, dir, "call", "--detach", marker)
	require.Error(t, err)
	require.Contains(t, string(out), marker)

	// The session the background command created is stopped.
	mgmt := connectEngineClient(ctx, t, client.Params{}).Dagger()
	require.Eventually(t, func() bool {
		sessions, err := trySessions(ctx, mgmt)
		if err != nil {
			return false
		}
		for _, sess := range sessions {
			for _, c := range sess.Clients {
				if c.Command != nil && strings.Contains(*c.Command, marker) {
					return false
				}
			}
		}
		return true
	}, time.Minute, time.Second, "the failed command's session is still listed")
}

func (SessionCLISuite) TestProcessFreeCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := detachRemoteWorkspace(ctx, t, c, 23461)
	dir := t.TempDir()
	marker := identity.NewID()

	id, out := detachWithOutput(ctx, t, dir, "-W", ref, "call", "--detach", "slow", "--msg", marker)
	require.Contains(t, out, noLocalProcess)
	require.NotContains(t, out, "Log:")

	// The process is gone within seconds, while the call sleeps for 20s; the
	// call still runs to its end.
	requireNoProcess(ctx, t, id)
	attached := attachUntil(ctx, t, dir, id, "slow:"+marker)
	t.Logf("attach output:\n%s", attached)
}

func (SessionCLISuite) TestProcessFreeResults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := detachRemoteWorkspace(ctx, t, c, 23462)
	dir := t.TempDir()

	// requireSameResult runs a call in the foreground, then process-free, and
	// requires that attach shows each line the foreground printed, as the
	// output of the detached command.
	requireSameResult := func(ctx context.Context, t *testctx.T, args ...string) {
		foreground, err := hostDaggerOutput(ctx, t, dir, append([]string{"-W", ref, "call"}, args...)...)
		require.NoError(t, err)
		var lines []string
		for _, line := range strings.Split(strings.TrimRight(string(foreground), "\n"), "\n") {
			lines = append(lines, "| "+line)
		}
		require.NotEmpty(t, lines)

		id, out := detachWithOutput(ctx, t, dir, append([]string{"-W", ref, "call", "--detach"}, args...)...)
		require.Contains(t, out, noLocalProcess)
		attached := attachUntil(ctx, t, dir, id, lines[len(lines)-1])
		t.Logf("attach output:\n%s", attached)
		for _, line := range lines {
			require.Regexp(t, `(?m)^\d+\s+: \[[^]]+\] `+regexp.QuoteMeta(line)+`$`, attached)
		}
	}

	t.Run("plain", func(ctx context.Context, t *testctx.T) {
		requireSameResult(ctx, t, "list", "--msg", identity.NewID())
	})

	t.Run("json", func(ctx context.Context, t *testctx.T) {
		requireSameResult(ctx, t, "--json", "list", "--msg", identity.NewID())
	})

	t.Run("json scalar", func(ctx context.Context, t *testctx.T) {
		requireSameResult(ctx, t, "--json", "echo", "--msg", identity.NewID())
	})

	t.Run("id", func(ctx context.Context, t *testctx.T) {
		requireSameResult(ctx, t, "ctr")
	})

	t.Run("failure", func(ctx context.Context, t *testctx.T) {
		marker := identity.NewID()
		id, out := detachWithOutput(ctx, t, dir, "-W", ref, "call", "--detach", "fail", "--msg", marker)
		require.Contains(t, out, noLocalProcess)
		attached := attachUntil(ctx, t, dir, id, "fail --msg "+marker+" ERROR")
		t.Logf("attach output:\n%s", attached)
		require.Contains(t, attached, marker)
		require.Contains(t, attached, "exit code: 3")
	})
}

func (SessionCLISuite) TestProcessFreeStop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := detachRemoteWorkspace(ctx, t, c, 23463)
	dir := t.TempDir()

	id, out := detachWithOutput(ctx, t, dir, "-W", ref, "call", "--detach", "sleepy")
	require.Contains(t, out, noLocalProcess)
	requireNoProcess(ctx, t, id)

	// Stopping waits for the session's work, so a query it did not cancel
	// would keep it waiting for 10 minutes.
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stopOut, err := hostDaggerExec(stopCtx, t, dir, "sessions", "stop", id)
	require.NoError(t, err, string(stopOut))
	require.Contains(t, string(stopOut), "Stopped session "+id)
}

func (SessionCLISuite) TestDetachKeepsProcessForHostDependencies(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := detachRemoteWorkspace(ctx, t, c, 23464)
	local := detachWorkspace(t, 23464)
	dir := t.TempDir()
	extra := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(extra, "hello.txt"), []byte("extra"), 0o644))

	for _, tc := range []struct {
		name string
		dir  string
		args []string
	}{
		{"no workspace flag", local, []string{"call", "--detach", "echo", "--msg", "hi"}},
		{"local directory", dir, []string{"-W", ref, "call", "--detach", "read", "--extra", extra}},
		{"secret", dir, []string{"-W", ref, "call", "--detach", "secret-len", "--s", "env://HOME"}},
		{"output", dir, []string{"-W", ref, "call", "--detach", "-o", filepath.Join(dir, "out.txt"), "echo", "--msg", "hi"}},
		{"changeset", dir, []string{"-W", ref, "call", "--detach", "changes"}},
		{"core export", dir, []string{"-W", ref, "call", "--detach", "dir", "export", "--path", filepath.Join(dir, "exported")}},
		{"core up", dir, []string{"-W", ref, "call", "--detach", "web", "up"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			_, out := detachWithOutput(ctx, t, tc.dir, tc.args...)
			require.Contains(t, out, "Log: ")
			require.NotContains(t, out, noLocalProcess)
		})
	}
}
