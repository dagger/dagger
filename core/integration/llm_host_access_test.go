package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger/core"
	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// serveHostSocket listens on a unix socket on the test host, which is the
// CLI's host, and answers each request line with token, as a daemon would:
// what reaches a client proves its connection got here.
func serveHostSocket(t *testctx.T, token string) string {
	t.Helper()
	// Short: a socket path is limited to ~100 bytes.
	dir, err := os.MkdirTemp("", "sock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "host.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
				}
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				_, _ = conn.Write([]byte(token + " " + line))
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	return sock
}

// reachSocket is a snippet that connects to the socket source, mounted in a
// container, and returns what it answered.
func reachSocket(source string) string {
	return fmt.Sprintf(`container.from(%q).withExec(["apk", "add", "socat"]).withUnixSocket(path: "/tmp/host.sock", source: %s).withExec(["sh", "-c", "echo ping | socat -t 5 - UNIX-CONNECT:/tmp/host.sock"]).stdout`, alpineImage, source)
}

// agentHostWorkspace is a workspace on the test host whose runner module
// runs an agent (see testdata/workspaces/workspace-agent-host).
func agentHostWorkspace(ctx context.Context, t *testctx.T) string {
	t.Helper()
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.CopyFS(workdir, os.DirFS(filepath.Join("testdata", "workspaces", "workspace-agent-host"))))
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		gitCmd := exec.Command("git", args...)
		gitCmd.Dir = workdir
		out, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return workdir
}

// agentDangCommand is the test host's CLI running, after the shell statement
// before (if any), an agent (runner's "hostbot") whose canned model makes
// calls, then printing its transcript.
func agentDangCommand(ctx context.Context, t *testctx.T, workdir, before string, calls []dangToolCall) *exec.Cmd {
	t.Helper()
	c := connect(ctx, t)
	const prompt = "reach the host"
	script := core.NewQuery(c).LLM().WithPrompt(prompt)
	for i, call := range calls {
		args, err := json.Marshal(call.args)
		require.NoError(t, err)
		id := fmt.Sprintf("dang_%d", i)
		script = script.
			WithResponse([]core.LLMContentBlockInput{
				{Kind: core.LLMContentBlockKindToolCall, CallID: id, ToolName: call.tool, Arguments: core.JSON(args)},
			}).
			WithToolResult(id, "", false)
	}
	model := cannedRecordingModel(ctx, t, c, script.WithResponse([]core.LLMContentBlockInput{
		{Kind: core.LLMContentBlockKindText, Text: "done"},
	}))
	statements := fmt.Sprintf("runner | run --model=%q --prompt=%q", model, prompt)
	if before != "" {
		statements = before + "\n" + statements
	}
	return hostDaggerCommand(ctx, t, workdir, "--progress=plain", "-c", statements)
}

// TestAgentHostAccess covers an agent's snippets (DangEval) reaching the
// CLI's host. A snippet runs in an in-engine client with no host of its own:
// a socket on the host (Address.socket, Host.unixSocket) is forwarded from
// the owner's host, the CLI's, once the owner approves (see
// Server.AuthorizeHostSocket). The other host addresses stay denied.
func (LLMSuite) TestAgentHostAccess(ctx context.Context, t *testctx.T) {
	workdir := agentHostWorkspace(ctx, t)

	t.Run("a host socket needs the owner's approval", func(ctx context.Context, t *testctx.T) {
		// A non-interactive CLI cannot approve. The socket is refused when
		// it is minted, not left to fail silently when the exec connects.
		token := "host-socket-" + rand.Text()
		sock := serveHostSocket(t, token)
		socket := fmt.Sprintf(`address(value: %q).socket`, sock)
		// The user first runs, in a session of their own on the same
		// engine, with their own host's socket, the very exec the agent's
		// snippet builds, as with `docker ps` over docker.sock. A host
		// socket's handle and digest derive from its path alone, the same
		// for every non-module client in every session, and so does the
		// exec's cache key: were the agent's socket minted unasked, that
		// session's cached result would answer it, without a connection.
		own := hostDaggerCommand(ctx, t, workdir, "-c",
			fmt.Sprintf(`container | from %s | with-exec apk add socat | with-unix-socket /tmp/host.sock $(host | unix-socket %s) | with-exec -- sh -c "echo ping | socat -t 5 - UNIX-CONNECT:/tmp/host.sock" | stdout`, alpineImage, sock))
		ownOut, err := own.CombinedOutput()
		require.NoError(t, err, string(ownOut))
		require.Contains(t, string(ownOut), token+" ping", "the CLI reaches its own host's socket")

		out, err := agentDangCommand(ctx, t, workdir, "", []dangToolCall{
			{"DangEval", map[string]string{"source": reachSocket(socket)}},
		}).CombinedOutput()
		require.NoError(t, err, string(out))
		require.NotContains(t, string(out), token, "the agent reached the host socket unasked")
		require.Contains(t, string(out), fmt.Sprintf(`access to socket %q on your machine requires approval: owning client cannot prompt (not interactive)`, sock))
	})

	t.Run("the owner approves or denies a host socket", func(ctx context.Context, t *testctx.T) {
		// Answering takes a terminal: run the CLI under a PTY, with the
		// plain frontend's yes/no question (yes allows for the session).
		// The first snippet is denied, and the denial must not stick.
		token := "host-socket-" + rand.Text()
		sock := serveHostSocket(t, token)
		reach := reachSocket(fmt.Sprintf(`address(value: %q).socket`, sock))
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()
		tty := console.Tty()
		require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 20, Cols: 400}))
		cmd := agentDangCommand(ctx, t, workdir, "", []dangToolCall{
			{"DangEval", map[string]string{"source": reach}},
			{"DangEval", map[string]string{"source": reach}},
		})
		cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
		require.NoError(t, cmd.Start())
		// The expect timeouts only bound silence: bound the whole exchange,
		// so a prompt that is missed or never answered fails rather than
		// hangs.
		exchange, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		stop := context.AfterFunc(exchange, func() {
			_ = cmd.Process.Kill()
			console.Close()
		})
		defer stop()
		expectString := func(s string) {
			t.Helper()
			_, err := console.ExpectString(s)
			require.NoError(t, err, "waiting for %q (deadline: %v)", s, exchange.Err())
		}
		for _, answer := range []string{"n", "y"} {
			expectString(fmt.Sprintf(`Allow access to socket %q on your machine (requested by agent "hostbot", module "runner"`, sock))
			_, err = console.SendLine(answer)
			require.NoError(t, err)
		}
		// The approved socket is forwarded from the CLI's host.
		expectString(token + " ping")
		go console.ExpectEOF()
		require.NoError(t, cmd.Wait())
		require.NoError(t, exchange.Err(), "the exchange ran out of time")
		stop()
		require.Contains(t, console.output.String(), fmt.Sprintf(`access to socket %q on your machine was denied by the owning client`, sock))
	})

	t.Run("a host service the CLI uses", func(ctx context.Context, t *testctx.T) {
		// The CLI tunnels its own host's port first, in the same session,
		// then the agent's snippet asks for the same host service in an
		// exec of its own. Host.service is scoped by session and caller
		// module, both of which the CLI and a module-less snippet share:
		// the snippet used to get the CLI's cached service, whose tunnel
		// reached the CLI's host live. (Across sessions, the session scope
		// already keeps them apart.)
		token := "host-port-" + rand.Text()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				_, _ = conn.Write([]byte(token + "\n"))
				_ = conn.Close()
			}
		}()
		port := l.Addr().(*net.TCPAddr).Port
		svc := fmt.Sprintf("tcp://localhost:%d", port)
		before := fmt.Sprintf(`container | from %s | with-exec apk add socat | with-service-binding h $(address %s | service) | with-exec -- socat -u TCP:h:%d - | stdout`, alpineImage, svc, port)
		out, err := agentDangCommand(ctx, t, workdir, before, []dangToolCall{
			{"DangEval", map[string]string{"source": fmt.Sprintf(`container.from(%q).withExec(["apk", "add", "socat"]).withEnvVariable(name: "BUST", value: %q).withServiceBinding(alias: "h", service: address(value: %q).service).withExec(["socat", "-u", "TCP:h:%d", "-"]).stdout`, alpineImage, rand.Text(), svc, port)}},
		}).CombinedOutput()
		require.NoError(t, err, string(out))
		require.Contains(t, string(out), token, "the CLI reaches its own host's port")
		start := strings.LastIndex(string(out), "[User]: reach the host")
		require.NotEqual(t, -1, start, string(out))
		transcript := string(out)[start:]
		require.NotContains(t, transcript, token, "the agent reached the CLI's host port unasked")
		require.Contains(t, transcript, fmt.Sprintf(`connecting to localhost:%d on your machine requires approval: owning client cannot prompt (not interactive)`, port))
	})

	t.Run("a host directory the CLI read", func(ctx context.Context, t *testctx.T) {
		// The CLI reads its own host's files first, in the same session;
		// the agent's snippet then asks for the same ones.
		token := "host-dir-" + rand.Text()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "token.txt"), []byte(token), 0o600))
		before := fmt.Sprintf("host | directory %s | file token.txt | contents\nhost | file %s | contents", dir, filepath.Join(dir, "token.txt"))
		out, err := agentDangCommand(ctx, t, workdir, before, []dangToolCall{
			{"DangEval", map[string]string{"source": fmt.Sprintf(`address(value: %q).directory.file("token.txt").contents`, dir)}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`address(value: %q).file.contents`, filepath.Join(dir, "token.txt"))}},
		}).CombinedOutput()
		require.NoError(t, err, string(out))
		start := strings.LastIndex(string(out), "[User]: reach the host")
		require.NotEqual(t, -1, start, string(out))
		require.NotContains(t, string(out)[start:], token, "the agent read the CLI's host files unasked")
	})

	t.Run("other host addresses stay denied", func(ctx context.Context, t *testctx.T) {
		token := "host-file-" + rand.Text()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "token.txt"), []byte(token), 0o600))
		initGitRepo(ctx, t, dir)
		for _, args := range [][]string{{"add", "."}, {"commit", "-m", "token"}} {
			gitCmd := exec.Command("git", args...)
			gitCmd.Dir = dir
			out, err := gitCmd.CombinedOutput()
			require.NoError(t, err, string(out))
		}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				_, _ = conn.Write([]byte(token + "\n"))
				_ = conn.Close()
			}
		}()
		port := l.Addr().(*net.TCPAddr).Port
		out, err := agentDangCommand(ctx, t, workdir, "", []dangToolCall{
			{"DangEval", map[string]string{"source": fmt.Sprintf(`address(value: %q).file.contents`, filepath.Join(dir, "token.txt"))}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`address(value: %q).directory.file("token.txt").contents`, dir)}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`address(value: %q).gitRepository.head.tree.file("token.txt").contents`, dir)}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`address(value: %q).gitRef.tree.file("token.txt").contents`, dir)}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`git(url: %q).head.tree.file("token.txt").contents`, dir)}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`moduleSource(refString: %q).contextDirectory.file("token.txt").contents`, dir)}},
			{"DangEval", map[string]string{"source": fmt.Sprintf(`container.from(%q).withExec(["apk", "add", "socat"]).withServiceBinding(alias: "h", service: address(value: "tcp://localhost:%d").service).withExec(["socat", "-u", "TCP:h:%d", "-"]).stdout`, alpineImage, port, port)}},
		}).CombinedOutput()
		require.NoError(t, err, string(out))
		require.NotContains(t, string(out), token)
		require.Contains(t, string(out), "SDK client access to host session attachables is denied")
	})
}
