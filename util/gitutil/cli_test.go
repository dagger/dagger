package gitutil

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

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
