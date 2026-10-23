package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// liveLogsCLIEnv is the environment for running the from-source CLI against
// the test engine as a fresh client: no session or trace to join, no Cloud,
// and no OTEL exporters, which would need every log body pushed.
func liveLogsCLIEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_") {
			continue
		}
		switch key {
		case "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN", "TRACEPARENT", "TRACESTATE", "DAGGER_TUI_CONSOLE", "DAGGER_PROGRESS", "DAGGER_CLOUD_TOKEN", "DAGGER_CLOUD_URL":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "DAGGER_NO_NAG=1")
}

// TestLiveLogsOnDemand runs the CLI against an engine that pushes log
// presence instead of display text: the report fetches what it renders at
// exit and prints the primary output once, in order; the live TUI holds no
// log bodies for spans it never rendered, and shows a span's output -- caught
// up, then followed -- once it renders it.
func (ClientSuite) TestLiveLogsOnDemand(ctx context.Context, t *testctx.T) {
	bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	require.NotEmpty(t, bin)
	env := liveLogsCLIEnv()
	marker := rand.Text()
	from := []string{"core", "container", "from", "--address=" + alpineImage, "with-env-variable", "--name=MARKER", "--value=" + marker}

	t.Run("report", func(ctx context.Context, t *testctx.T) {
		// The primary output, the CLI's own (ingested) output read back at
		// exit, comes out once and in order.
		args := append([]string{"--progress=report"}, from...)
		args = append(args, "with-exec", "--args=sh,-c,printf 'out-1\\nout-2\\nout-3\\n'", "stdout")
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		require.NoError(t, cmd.Run(), "%s", stderr.String())
		require.Equal(t, "out-1\nout-2\nout-3\n", stdout.String())

		// A failure's output is fetched for the report at exit.
		args = append([]string{"--progress=report"}, from...)
		args = append(args, "with-exec", "--args=sh,-c,echo FAILURE-DETAIL-$MARKER; exit 3", "stdout")
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(out), "FAILURE-DETAIL-"+marker)
	})

	t.Run("tui", func(ctx context.Context, t *testctx.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		address := listener.Addr().String()
		require.NoError(t, listener.Close())
		consoleCtx, stopConsole := context.WithCancel(ctx)
		defer stopConsole()
		args := append([]string{"--progress=tty"}, from...)
		args = append(args,
			// Never rendered: it runs while the view is zoomed elsewhere.
			"with-exec", "--args=sh,-c,echo UNRENDERED-$MARKER",
			// Rendered once zoomed into: its first lines are caught up, the
			// last followed.
			"with-exec", "--args=sh,-c,echo RENDERED-$MARKER-1; echo RENDERED-$MARKER-2; sleep 5; echo RENDERED-$MARKER-3; sleep 3",
			"stdout")
		console := exec.CommandContext(consoleCtx, bin, args...)
		console.Env = append([]string{"DAGGER_TUI_CONSOLE=" + address}, env...)
		var consoleOutput bytes.Buffer
		console.Stdout, console.Stderr = &consoleOutput, &consoleOutput
		require.NoError(t, console.Start())
		consoleDone := make(chan error, 1)
		go func() { consoleDone <- console.Wait() }()
		defer func() { stopConsole(); <-consoleDone }()

		httpClient := &http.Client{Timeout: 15 * time.Second}
		var last string
		defer func() {
			if t.Failed() {
				t.Logf("last console response:\n%s\nconsole output:\n%s", last, consoleOutput.String())
			}
		}()
		request := func(method, path, body string) (int, string) {
			req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
			require.NoError(t, err)
			res, err := httpClient.Do(req)
			if err != nil {
				return 0, err.Error()
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			last = string(data)
			return res.StatusCode, last
		}
		spansNamed := func(name string) [][]string {
			_, body := request(http.MethodGet, "/spans?q="+name, "")
			var rows [][]string
			for _, line := range strings.Split(body, "\n") {
				if fields := strings.Fields(line); len(fields) >= 3 && strings.HasPrefix(fields[2], name) {
					rows = append(rows, fields)
				}
			}
			return rows
		}

		// Zoom away from everything as soon as the command's root span is
		// there (the frontend zooms to it first), before the engine runs
		// anything.
		require.Eventually(t, func() bool {
			status, body := request(http.MethodGet, "/spans", "")
			return status == http.StatusOK && strings.TrimSpace(body) != ""
		}, time.Minute, 50*time.Millisecond)
		status, _ := request(http.MethodPost, "/zoom", "00000000000000ff")
		require.Equal(t, http.StatusOK, status)

		// The rows are the withExec calls' own spans (the others beneath
		// them are passthrough), created as the query resolves, before
		// either exec runs.
		var unrendered, rendered string
		require.Eventually(t, func() bool {
			var calls []string
			for _, row := range spansNamed("Container.withExec") {
				if _, detail := request(http.MethodGet, "/span?id="+row[0], ""); strings.Contains(detail, "flags:    (none)") {
					calls = append(calls, row[0])
				}
			}
			if len(calls) != 2 {
				return false
			}
			unrendered, rendered = calls[0], calls[1]
			return true
		}, 2*time.Minute, 100*time.Millisecond)

		status, _ = request(http.MethodPost, "/zoom", rendered)
		require.Equal(t, http.StatusOK, status)
		require.Eventually(t, func() bool {
			_, screen := request(http.MethodGet, "/screen", "")
			return strings.Contains(screen, "RENDERED-"+marker+"-2")
		}, time.Minute, 100*time.Millisecond, "the zoomed span's output was not caught up")
		require.Eventually(t, func() bool {
			_, screen := request(http.MethodGet, "/screen", "")
			return strings.Contains(screen, "RENDERED-"+marker+"-3")
		}, time.Minute, 100*time.Millisecond, "the zoomed span's output was not followed")

		_, detail := request(http.MethodGet, "/span?id="+unrendered, "")
		require.Contains(t, detail, "logs:     yes", "presence did not flag the unrendered span")
		require.Contains(t, detail, "held log lines: 0 (requested: false)", "the unrendered span's output was received")
		_, detail = request(http.MethodGet, "/span?id="+rendered, "")
		require.Contains(t, detail, "(requested: true)")
		require.NotContains(t, detail, "held log lines: 0 ")
		_, screen := request(http.MethodGet, "/screen", "")
		require.Equal(t, 1, strings.Count(screen, "RENDERED-"+marker+"-1"), "caught-up output shown twice")
	})
}
