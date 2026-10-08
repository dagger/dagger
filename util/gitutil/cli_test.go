package gitutil

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestRunWithStdin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	blob := func(data string) string {
		sum := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(data), data))
		return hex.EncodeToString(sum[:])
	}
	hash := func(t *testing.T, out []byte, err error) string {
		t.Helper()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}

	var execs int
	cli := NewGitCLI(WithDir(t.TempDir()), WithExec(func(_ context.Context, cmd *exec.Cmd) error {
		execs++
		return cmd.Run()
	}))
	// Binary input, NULs and newlines included, reaches the exec hook's command.
	data := "a\x00b\n-c,\xe2\x98\x83"
	out, err := cli.RunWithStdin(ctx, strings.NewReader(data), "hash-object", "--stdin")
	require.Equal(t, blob(data), hash(t, out, err))
	require.Equal(t, 1, execs)

	// Stdin belongs to one invocation: neither Run on the same CLI nor a
	// derived CLI sees it.
	out, err = cli.Run(ctx, "hash-object", "--stdin")
	require.Equal(t, blob(""), hash(t, out, err))
	out, err = cli.New(WithArgs("-c", "core.autocrlf=false")).Run(ctx, "hash-object", "--stdin")
	require.Equal(t, blob(""), hash(t, out, err))
	require.Equal(t, 3, execs)

	// Without an exec hook, and with streams.
	var stdout strings.Builder
	streams := NewGitCLI(WithStreams(func(context.Context) (io.WriteCloser, io.WriteCloser, func()) {
		return nopWriteCloser{&stdout}, nopWriteCloser{io.Discard}, func() {}
	}))
	out, err = streams.RunWithStdin(ctx, strings.NewReader("hello\n"), "hash-object", "--stdin")
	require.Equal(t, blob("hello\n"), hash(t, out, err))
	require.Equal(t, blob("hello\n"), strings.TrimSpace(stdout.String()))
}

type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *logRecorder) OnEmit(_ context.Context, rec *sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, rec.Body().AsString())
	return nil
}

func (r *logRecorder) Shutdown(context.Context) error   { return nil }
func (r *logRecorder) ForceFlush(context.Context) error { return nil }

func (r *logRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

func (r *logRecorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "")
}

// Git plumbing output is data for the caller, and span logs end up verbatim
// in tool results: a successful command must not log anything, and a failing
// one logs only its stderr diagnostics.
func TestRunKeepsPlumbingOutOfSpanLogs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	rec := &logRecorder{}
	ctx := telemetry.WithLoggerProvider(t.Context(), sdklog.NewLoggerProvider(sdklog.WithProcessor(rec)))
	dir := t.TempDir()
	cli := NewGitCLI(WithDir(dir))

	_, err := cli.Run(ctx, "init")
	require.NoError(t, err)
	gitDir, err := cli.GitDir(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, gitDir)
	out, err := cli.Run(ctx, "cat-file", "-t", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	require.NoError(t, err)
	require.Equal(t, "tree\n", string(out))
	require.Empty(t, rec.text())

	_, err = cli.New(WithIgnoreError()).Run(ctx, "rev-parse", "--verify", "refs/heads/missing")
	require.NoError(t, err)
	require.Empty(t, rec.text(), "ignored failures are expected probes, not diagnostics")

	_, err = cli.Run(ctx, "rev-parse", "--verify", "refs/heads/missing")
	require.Error(t, err)
	require.Contains(t, rec.text(), "fatal:")
	require.NotContains(t, rec.text(), gitDir)
}
