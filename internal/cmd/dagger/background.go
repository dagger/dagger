package daggercmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adrg/xdg"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
)

// A command run with --detach continues in a background copy of this
// process: the same executable and arguments, without --detach, in the same
// directory and environment. The copy is an ordinary client of the session.
// It reports its progress to the foreground process over a status pipe, and
// writes its output to a log file.

// backgroundEnv marks the background copy. Its status pipe is file
// descriptor 3.
const backgroundEnv = "_EXPERIMENTAL_DAGGER_BACKGROUND"

var (
	// detachFlag is --detach, on the commands that support it.
	detachFlag bool

	// inBackground is set in the background copy of a detached command.
	inBackground bool
	// backgroundStatus reports to the foreground process.
	backgroundStatus *backgroundStatusPipe
	// backgroundTelemetry pushes this process's own telemetry to the engine.
	backgroundTelemetry *engineBoundTelemetry
)

// backgroundStatusMessage is one line on the status pipe.
type backgroundStatusMessage struct {
	Session string   `json:"session,omitempty"`
	Started bool     `json:"started,omitempty"`
	URLs    []string `json:"urls,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// setupBackgroundMode recognizes the background copy of a detached command.
func setupBackgroundMode() {
	if os.Getenv(backgroundEnv) == "" {
		return
	}
	// Processes this one starts are not background copies.
	os.Unsetenv(backgroundEnv)
	inBackground = true
	backgroundStatus = &backgroundStatusPipe{w: os.NewFile(3, "dagger-background-status")}
	backgroundTelemetry = &engineBoundTelemetry{ready: make(chan struct{})}
}

// backgroundClientParams marks the client of the command that runs in the
// background: it describes itself as a background client, and creates a
// detached session unless it joins one with --session.
func backgroundClientParams(params client.Params) client.Params {
	if !inBackground {
		return params
	}
	params.Background = true
	if sessionFlag == "" {
		params.DetachedSession = true
	}
	return params
}

func backgroundLogDir() string {
	return filepath.Join(xdg.StateHome, "dagger", "background")
}

func backgroundLogPath(pid int) string {
	return filepath.Join(backgroundLogDir(), fmt.Sprintf("%d.log", pid))
}

type backgroundStatusPipe struct {
	mu   sync.Mutex
	w    *os.File
	done bool
}

// send writes a status message. Once the command has started or failed, the
// foreground process is gone, so the pipe is closed.
func (p *backgroundStatusPipe) send(msg backgroundStatusMessage) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	_ = json.NewEncoder(p.w).Encode(msg)
	if msg.Started || msg.Error != "" {
		p.done = true
		p.w.Close()
	}
}

// reportBackgroundSession tells the foreground process the session ID.
func reportBackgroundSession(sessionID string) {
	backgroundStatus.send(backgroundStatusMessage{Session: sessionID})
}

// reportBackgroundStarted tells the foreground process that the command has
// started, so it can exit.
func reportBackgroundStarted(urls []string) {
	backgroundStatus.send(backgroundStatusMessage{Started: true, URLs: urls})
}

// finishBackground reports how the command ended if it had not started yet,
// and removes the log file when it succeeded.
func finishBackground(err error, code int) {
	if !inBackground {
		return
	}
	switch {
	case err != nil:
		var exit idtui.ExitError
		if errors.As(err, &exit) && exit.Original != nil {
			err = exit.Original
		}
		msg := strings.TrimSpace(telemetry.ErrorOriginRegex.ReplaceAllString(err.Error(), ""))
		backgroundStatus.send(backgroundStatusMessage{Error: msg})
	case code != 0:
		backgroundStatus.send(backgroundStatusMessage{Error: fmt.Sprintf("exited with code %d", code)})
	default:
		reportBackgroundStarted(nil)
	}
	// Keep the log of a failed command, and of one whose telemetry did not
	// reach the engine.
	if code == 0 && !backgroundTelemetry.failed.Load() {
		_ = os.Remove(backgroundLogPath(os.Getpid()))
	}
}

// runDetached starts the current command in the background and reports what
// it says once it has started.
func runDetached(out io.Writer) error {
	// Remove the --detach this command consumed. It comes before any
	// function name, so it is the first one.
	var args []string
	consumed := false
	for _, arg := range os.Args[1:] {
		if !consumed && (arg == "--detach" || arg == "--detach=true") {
			consumed = true
			continue
		}
		args = append(args, arg)
	}
	status, logPath, err := startBackground(args)
	if err != nil {
		return err
	}
	defer status.Close()

	var sessionID string
	scanner := bufio.NewScanner(status)
	for scanner.Scan() {
		var msg backgroundStatusMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return fmt.Errorf("read background status: %w", err)
		}
		if msg.Session != "" {
			sessionID = msg.Session
		}
		if msg.Error != "" {
			return fmt.Errorf("%s\nLog: %s", msg.Error, logPath)
		}
		if msg.Started {
			fmt.Fprintf(out, "Session: %s\n", sessionID)
			for _, url := range msg.URLs {
				fmt.Fprintf(out, "Forwarding: %s\n", url)
			}
			fmt.Fprintf(out, "Attach: dagger sessions attach %s\n", sessionID)
			fmt.Fprintf(out, "Stop: dagger sessions stop %s\n", sessionID)
			fmt.Fprintf(out, "Log: %s\n", logPath)
			return nil
		}
	}
	return fmt.Errorf("background command exited before it started\nLog: %s", logPath)
}

// engineBoundTelemetry pushes this process's spans and logs to the engine.
// Telemetry starts before the client connects, so exports wait until the
// connection exists.
type engineBoundTelemetry struct {
	ready     chan struct{}
	readyOnce sync.Once
	spans     sdktrace.SpanExporter
	logs      sdklog.Exporter
	// failed records that an export failed.
	failed atomic.Bool
}

type engineBoundTelemetryKey struct{}

// withEngineBoundTelemetry makes the telemetry initialized with ctx push to
// the engine too.
func withEngineBoundTelemetry(ctx context.Context, t *engineBoundTelemetry) context.Context {
	return context.WithValue(ctx, engineBoundTelemetryKey{}, t)
}

func engineBoundTelemetryFrom(ctx context.Context) *engineBoundTelemetry {
	t, _ := ctx.Value(engineBoundTelemetryKey{}).(*engineBoundTelemetry)
	return t
}

// connect starts pushing to the engine through c. Exports of a client that
// never connects are dropped.
func (t *engineBoundTelemetry) connect(ctx context.Context, c *client.Client) error {
	var err error
	if c != nil {
		t.spans, t.logs, err = c.EngineTelemetryExporters(ctx)
	}
	t.readyOnce.Do(func() { close(t.ready) })
	return err
}

func (t *engineBoundTelemetry) wait(ctx context.Context) bool {
	select {
	case <-t.ready:
		return t.spans != nil
	case <-ctx.Done():
		return false
	}
}

func (t *engineBoundTelemetry) record(err error) error {
	if err != nil {
		t.failed.Store(true)
	}
	return err
}

type engineBoundSpans struct{ t *engineBoundTelemetry }

func (e engineBoundSpans) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if !e.t.wait(ctx) {
		return nil
	}
	return e.t.record(e.t.spans.ExportSpans(ctx, spans))
}

func (e engineBoundSpans) Shutdown(ctx context.Context) error {
	if !e.t.wait(ctx) {
		return nil
	}
	return e.t.record(e.t.spans.Shutdown(ctx))
}

type engineBoundLogs struct{ t *engineBoundTelemetry }

func (e engineBoundLogs) Export(ctx context.Context, records []sdklog.Record) error {
	if !e.t.wait(ctx) {
		return nil
	}
	return e.t.record(e.t.logs.Export(ctx, records))
}

func (e engineBoundLogs) ForceFlush(ctx context.Context) error {
	if !e.t.wait(ctx) {
		return nil
	}
	return e.t.record(e.t.logs.ForceFlush(ctx))
}

func (e engineBoundLogs) Shutdown(ctx context.Context) error {
	if !e.t.wait(ctx) {
		return nil
	}
	return e.t.record(e.t.logs.Shutdown(ctx))
}
