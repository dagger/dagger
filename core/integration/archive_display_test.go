package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/tracesource"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// TestLazyArchiveDisplay crosses the real engine wire with a retained canonical
// agent archive. Display must neither evaluate that agent's recipe nor import
// the large, unrelated exec output just to inspect a small span's logs.
func (AgentRestoreSuite) TestLazyArchiveDisplay(ctx context.Context, t *testctx.T) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(context.WithoutCancel(ctx))
	sourceCtx, sourceSpan := provider.Tracer("archive-display-acceptance").Start(ctx, "source", trace.WithNewRoot())
	defer sourceSpan.End()
	source, sink := connectWithTrace(sourceCtx, t)
	// No model turn is needed, even to produce this fixture. Any accidental
	// turn against this empty recording fails instead of contacting a provider.
	seed, err := source.LLM(dagger.LLMOpts{Model: emptyReplayModel}).WithPrompt("retained display-only conversation").ID(sourceCtx)
	require.NoError(t, err)
	_, err = rehydrateAgent(sourceCtx, source, string(seed), "display-agent", "display-agent", "IDLE", "")
	require.NoError(t, err)
	node := sink.awaitRestorable(t, 1)["display-agent"]
	require.NotNil(t, node.Control)

	const selectedText = "SELECTED-ARCHIVE-OUTPUT-ONLY"
	const unrelatedText = "UNRELATED-ARCHIVE-OUTPUT"
	const unrelatedBytes = 4 << 20
	base := source.Container().From(alpineImage).WithEnvVariable("ARCHIVE_DISPLAY_FIXTURE", identity.NewID())
	_, err = base.WithNewFile("/unrelated.sh", "#!/bin/sh\nprintf '"+unrelatedText+"\\n'\nhead -c 4194304 /dev/zero | tr '\\000' x\nprintf '\\n'\n").
		WithExec([]string{"sh", "/unrelated.sh"}).Sync(sourceCtx)
	require.NoError(t, err)
	_, err = base.WithNewFile("/selected.sh", "#!/bin/sh\nprintf '"+selectedText+"\\n'\n").
		WithExec([]string{"sh", "/selected.sh"}).Sync(sourceCtx)
	require.NoError(t, err)
	require.NoError(t, source.Close())
	sourceSpan.End()

	// Establish that the fixture really contains multi-megabyte output and find
	// producer span IDs from the emitted logs, not guessed call/span names.
	_, captured := sink.capture()
	var selectedSpan, unrelatedSpan string
	var capturedBytes int
	for _, batch := range captured {
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					body := record.GetBody().GetStringValue()
					capturedBytes += len(body)
					if strings.Contains(body, selectedText) {
						selectedSpan = hex.EncodeToString(record.SpanId)
					}
					if strings.Contains(body, unrelatedText) {
						unrelatedSpan = hex.EncodeToString(record.SpanId)
					}
				}
			}
		}
	}
	require.GreaterOrEqual(t, capturedBytes, unrelatedBytes)
	require.NotEmpty(t, selectedSpan)
	require.NotEmpty(t, unrelatedSpan)
	require.NotEqual(t, selectedSpan, unrelatedSpan)

	targetCtx, targetSpan := provider.Tracer("archive-display-acceptance").Start(ctx, "reader", trace.WithNewRoot())
	defer targetSpan.End()
	target, targetSink := connectWithTrace(targetCtx, t)
	defer target.Close()
	transport := &displayArchiveTransport{next: targetSink.conn}
	client := archive.NewClient(transport).WithSourceSession(node.Control.Session)
	traceID := node.Control.Trace
	var manifest archive.Manifest
	require.Eventually(t, func() bool {
		manifests, err := client.ListAll(targetCtx, archive.ListOptions{})
		require.NoError(t, err)
		for _, candidate := range manifests {
			if candidate.TraceID == traceID && candidate.SourceSession == node.Control.Session {
				manifest = candidate
				return candidate.State != archive.StateActive && candidate.State != archive.StateFinalizing
			}
		}
		return false
	}, time.Minute, 100*time.Millisecond)
	require.Equal(t, archive.StateClosed, manifest.State, "archive failure: %s", manifest.Failure)
	require.NotEmpty(t, manifest.Bootstrap.SHA256, "fixture must retain canonical bootstrap evidence")
	require.NotEmpty(t, manifest.Generation)

	// A successful local selection may not consult Cloud, not even for auth.
	remote := func(context.Context) (tracesource.Source, func() error, error) {
		t.Fatal("retained archive must not open Cloud")
		return nil, nil, nil
	}
	open := func(generation string) tracesource.Open {
		return func(ctx context.Context) (tracesource.Source, func() error, error) {
			return tracesource.OpenArchive(ctx, client, traceID, generation)
		}
	}
	display, closeDisplay, err := tracesource.Select(targetCtx, manifest.Generation, open(manifest.Generation), remote)
	require.NoError(t, err)
	defer closeDisplay()
	require.EqualValues(t, 0, transport.closedLeases.Load())
	require.Equal(t, 0, transport.signalRequests(), "opening a display must only inspect metadata and acquire its lease")

	spanCount := 0
	err = display.FetchSpans(targetCtx, traceID, cloud.SpanSelection{Incremental: true, DagUIView: true}, func(_ context.Context, batch *coltracepb.ExportTraceServiceRequest) error {
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				spanCount += len(scope.Spans)
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Positive(t, spanCount)
	require.Equal(t, 0, transport.logRequests(), "priority display must not fetch historical logs")

	// Lazy expansion backfills just the selected branch, including the UI
	// annotations that tell a frontend it can request the span's output.
	selectedHasLogs := false
	err = display.FetchSpans(targetCtx, traceID, cloud.SpanSelection{NoRoot: true, Listen: []string{selectedSpan}, DagUIView: true}, func(_ context.Context, batch *coltracepb.ExportTraceServiceRequest) error {
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					id := hex.EncodeToString(span.SpanId)
					require.NotEqual(t, unrelatedSpan, id, "lazy expansion imported an unrelated branch")
					if id == selectedSpan {
						for _, attr := range span.Attributes {
							if attr.Key == telemetryattrs.UIHasLogsAttr {
								selectedHasLogs = attr.GetValue().GetBoolValue()
							}
						}
					}
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.True(t, selectedHasLogs, "selected span must advertise its retained output")
	require.Equal(t, 0, transport.logRequests(), "expansion must not eagerly import output")

	var selected strings.Builder
	err = display.FetchLogs(targetCtx, traceID, cloud.LogSelection{SpanID: selectedSpan, Records: cloud.LogRecordsLogs}, func(_ context.Context, batch *collogspb.ExportLogsServiceRequest) error {
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					require.Equal(t, selectedSpan, hex.EncodeToString(record.SpanId), "unrelated producer leaked into selected logs")
					selected.WriteString(record.GetBody().GetStringValue())
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, selectedText+"\n", selected.String())
	require.Equal(t, 1, transport.logRequests())
	require.NoError(t, closeDisplay())
	require.EqualValues(t, 1, transport.closedLeases.Load(), "display cleanup must close its retention lease")

	_, _, err = tracesource.Select(targetCtx, "stale-generation", open("stale-generation"), remote)
	require.Error(t, err, "stale selection must not silently display a different generation")
	require.NoError(t, target.Close())

	// Finally exercise the actual CLI report from a destination that cannot
	// load a module. Cloud is a trap endpoint, and there are no LLM credentials.
	var cloudRequests atomic.Int32
	cloudTrap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cloudRequests.Add(1)
		http.Error(w, "local display must not contact Cloud", http.StatusInternalServerError)
	}))
	defer cloudTrap.Close()
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "dagger.toml"), []byte("[modules.broken]\nsource = \"definitely-missing-module\"\n"), 0o600))
	bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN")
	require.NotEmpty(t, bin)
	cmd := exec.CommandContext(ctx, bin, "--progress=report", "trace", traceID, "--source-session", manifest.SourceSession, "--generation", manifest.Generation, "--span", selectedSpan)
	cmd.Dir = destination
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN", "TRACEPARENT", "TRACESTATE", "DAGGER_TUI_CONSOLE", "DAGGER_PROGRESS", "DAGGER_CLOUD_TOKEN", "DAGGER_CLOUD_URL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "DAGGER_CLOUD_URL="+cloudTrap.URL, "DAGGER_CLOUD_TOKEN=")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), selectedText)
	require.NotContains(t, string(output), unrelatedText)
	require.Less(t, len(output), 64<<10, "a small zoomed report must not render multi-megabyte unrelated output")
	require.EqualValues(t, 0, cloudRequests.Load())

	// Browse the same archive interactively, well after initial loading has
	// finished. This catches cleanup that releases the source when the run
	// callback returns instead of retaining it until the console itself exits.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	consoleCtx, stopConsole := context.WithCancel(ctx)
	defer stopConsole()
	console := exec.CommandContext(consoleCtx, bin, "--progress=tty", "trace", traceID, "--source-session", manifest.SourceSession, "--generation", manifest.Generation)
	console.Dir = destination
	console.Env = append(cmd.Env, "DAGGER_TUI_CONSOLE="+address)
	consoleOutput, err := os.CreateTemp(t.TempDir(), "archive-console-output")
	require.NoError(t, err)
	defer consoleOutput.Close()
	console.Stdout, console.Stderr = consoleOutput, consoleOutput
	require.NoError(t, console.Start())
	consoleDone := make(chan error, 1)
	go func() { consoleDone <- console.Wait() }()
	defer func() { stopConsole(); <-consoleDone }()
	defer func() {
		if t.Failed() {
			data, _ := os.ReadFile(consoleOutput.Name())
			t.Logf("Archive console output:\n%s", data)
		}
	}()
	httpClient := &http.Client{Timeout: 15 * time.Second}
	request := func(method, path, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
		require.NoError(t, err)
		res, err := httpClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res.StatusCode, string(data)
	}
	var lastScreen string
	defer func() {
		if t.Failed() {
			t.Logf("Last archive console response:\n%s", lastScreen)
		}
	}()
	require.Eventually(t, func() bool {
		status, body := request(http.MethodGet, "/spans", "")
		lastScreen = body
		return status == http.StatusOK && strings.TrimSpace(body) != ""
	}, time.Minute, 100*time.Millisecond, "archive console never loaded its initial spans")
	status, lastScreen := request(http.MethodPost, "/wait?quiet=3s&timeout=10s", "")
	require.Equal(t, http.StatusOK, status, "%s", lastScreen)

	// The first zoom can backfill a span absent from the priority set. Zoom
	// again once it is known so its hasLogs annotation requests the output.
	// Both happen after initial loading, and require a still-live reader.
	status, lastScreen = request(http.MethodPost, "/zoom", selectedSpan)
	require.Equal(t, http.StatusOK, status, "%s", lastScreen)
	status, lastScreen = request(http.MethodPost, "/zoom", selectedSpan)
	require.Equal(t, http.StatusOK, status, "%s", lastScreen)
	require.Eventually(t, func() bool {
		status, body := request(http.MethodGet, "/screen", "")
		lastScreen = body
		return status == http.StatusOK && strings.Contains(body, selectedText)
	}, 20*time.Second, 100*time.Millisecond, "late archive zoom did not load selected logs")
	require.NotContains(t, lastScreen, unrelatedText)
	require.NotContains(t, lastScreen, "context canceled")
	require.EqualValues(t, 0, cloudRequests.Load(), "lazy browsing must stay on its selected engine archive")
}

// Count actual requests at the wire, rather than merely asserting what reached
// the importer. Fetching and discarding all logs is still a memory regression.
// Reject executable requests: a display transport cannot query/restore agents.
type displayArchiveTransport struct {
	next         archive.HTTPDoer
	mu           sync.Mutex
	paths        []string
	closedLeases atomic.Int32
}

func (d *displayArchiveTransport) Do(req *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(req.URL.Path, "/v1/telemetry/archives") {
		return nil, fmt.Errorf("display attempted executable request %s", req.URL.Path)
	}
	d.mu.Lock()
	d.paths = append(d.paths, req.URL.Path)
	d.mu.Unlock()
	resp, err := d.next.Do(req)
	if err == nil && strings.HasSuffix(req.URL.Path, "/lease") && resp.StatusCode == http.StatusOK {
		resp.Body = &displayLeaseBody{ReadCloser: resp.Body, closed: &d.closedLeases}
	}
	return resp, err
}

func (d *displayArchiveTransport) signalRequests() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, path := range d.paths {
		for _, suffix := range []string{"/traces", "/logs", "/metrics", "/bootstrap"} {
			if strings.HasSuffix(path, suffix) {
				count++
			}
		}
	}
	return count
}

func (d *displayArchiveTransport) logRequests() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, path := range d.paths {
		if strings.HasSuffix(path, "/logs") {
			count++
		}
	}
	return count
}

type displayLeaseBody struct {
	io.ReadCloser
	once   sync.Once
	closed *atomic.Int32
}

func (b *displayLeaseBody) Close() error {
	b.once.Do(func() { b.closed.Add(1) })
	return b.ReadCloser.Close()
}
