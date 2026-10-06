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

	"dagger.io/dagger"
	"github.com/adrg/xdg"
	"github.com/spf13/pflag"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/callresult"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/querybuilder"
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
	// NoProcess reports that the command started and leaves no process.
	NoProcess bool `json:"noProcess,omitempty"`
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
	mu      sync.Mutex
	w       *os.File
	done    bool
	started bool
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
	p.started = p.started || msg.Started
	if msg.Started || msg.Error != "" {
		p.done = true
		p.w.Close()
	}
}

// hasStarted reports whether the command reported that it started.
func (p *backgroundStatusPipe) hasStarted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started
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

// reportStartedWithoutProcess tells the foreground process that the command
// has started, and that this process exits without waiting for it.
func reportStartedWithoutProcess() {
	backgroundStatus.send(backgroundStatusMessage{Started: true, NoProcess: true})
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
			if noForwardFlag {
				fmt.Fprintf(out, "Forward: dagger --session %s up --detach\n", sessionID)
			}
			fmt.Fprintf(out, "Attach: dagger sessions attach %s\n", sessionID)
			fmt.Fprintf(out, "Stop: dagger sessions stop %s\n", sessionID)
			if msg.NoProcess {
				fmt.Fprintln(out, "No local process is running.")
			} else {
				fmt.Fprintf(out, "Log: %s\n", logPath)
			}
			return nil
		}
	}
	return fmt.Errorf("background command exited before it started\nLog: %s", logPath)
}

// runsWithoutHost reports whether a call that returns returnType, and whose
// chain and arguments do not use the host, needs nothing from this machine
// once it has started, so it can run with no local process. The rule is
// conservative: a call it keeps here still works, with a process.
func runsWithoutHost(returnType *modTypeDef) bool {
	// The workspace and the modules come from http(s) git refs, so they are
	// not read from this machine.
	if !isHTTPGitRef(workspaceRef) || (moduleURL != "" && !isHTTPGitRef(moduleURL)) {
		return false
	}
	// The engine reads the caller's .env through the host for user defaults.
	if hasDotEnv() {
		return false
	}
	// These write to the host or open a shell after the call.
	if outputPath != "" || shellOnError {
		return false
	}
	// After the call, an LLM opens an interactive prompt, and a changeset is
	// applied to the host.
	switch returnType.Name() {
	case LLM, Changeset:
		return false
	}
	return true
}

// hostCoreFields are the core fields a call chain can reach that use the
// host: exports write to it, and up forwards its ports.
var hostCoreFields = map[string][]string{
	Container: {"export", "exportImage", "up"},
	Directory: {"export"},
	File:      {"export"},
	Changeset: {"export"},
	Workspace: {"export"},
	Service:   {"up"},
}

// hostDependentValue reports whether a function argument's value is read
// from this machine.
func hostDependentValue(v pflag.Value) bool {
	switch v := v.(type) {
	case interface{ items() []DaggerValue }:
		for _, item := range v.items() {
			if hostDependentValue(item) {
				return true
			}
		}
		return false
	case *secretValue, *socketValue, *portForwardValue, *volumeValue:
		return true
	case *serviceValue:
		// tcp:// and udp:// are host services.
		return strings.Contains(v.address, "://")
	case *directoryValue:
		return !isHTTPGitAddress(v.address)
	case *fileValue:
		return !isHTTPGitAddress(v.address)
	case *workspaceValue:
		return !isHTTPGitAddress(v.address)
	case *gitRepositoryValue:
		return !isHTTPGitAddress(v.address)
	case *gitRefValue:
		return !isHTTPGitAddress(v.address)
	case *moduleValue:
		return !isHTTPGitRef(v.ref)
	case *moduleSourceValue:
		return !isHTTPGitRef(v.ref)
	}
	return false
}

// isHTTPGitRef reports whether a workspace or module ref is an http(s) git
// ref rather than a local path. An SSH ref needs the SSH agent.
func isHTTPGitRef(ref string) bool {
	if !isObviouslyRemoteWorkspaceRef(ref) {
		return false
	}
	u, err := gitutil.ParseURL(ref)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		// A ref with no protocol, such as github.com/org/repo, is cloned over
		// https, unless it is SCP-style host:path, which git clones over SSH.
		host, _, _ := strings.Cut(ref, "/")
		return !strings.Contains(host, ":")
	}
	return err == nil && (u.Scheme == gitutil.HTTPProtocol || u.Scheme == gitutil.HTTPSProtocol)
}

// isHTTPGitAddress reports whether an argument address is an http(s) git
// address, which the engine reads from the remote rather than the host.
func isHTTPGitAddress(address string) bool {
	u, err := gitutil.ParseURL(address)
	return err == nil && (u.Scheme == gitutil.HTTPProtocol || u.Scheme == gitutil.HTTPSProtocol)
}

// hasDotEnv reports whether the current directory or a parent has a .env
// file.
func hasDotEnv() bool {
	dir, err := os.Getwd()
	if err != nil {
		return true
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// runInEngine sends the call's query to run on its own in the engine, which
// writes its result into the session's telemetry, and reports that the
// command started with no process left.
func runInEngine(ctx context.Context, dag *dagger.Client, q *querybuilder.Selection, returnType *modTypeDef) error {
	query, err := q.Build(ctx)
	if err != nil {
		return err
	}
	format := callresult.FormatPlain
	switch {
	case returnType.AsFunctionProvider() != nil:
		format = callresult.FormatID
	case jsonOutput:
		format = callresult.FormatJSON
	}
	err = dag.Do(engine.ContextWithDetachedQuery(ctx, format), &dagger.Request{Query: query}, &dagger.Response{})
	if err != nil {
		return err
	}
	reportStartedWithoutProcess()
	return nil
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
