package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/internal/buildkit/identity"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// runWithPrivateTraceSession re-execs just this test when engine-dev supplies a
// shared nested session. Inherited SDK session variables can bypass the explicit
// from-source runner; they also prevent changing workdir or installing a trace
// sink. The child uses the explicitly configured runner and CLI, with its own
// session, without mutating the parallel test process's environment. Callers
// must return when it returns true. Removing the session variables also prevents
// recursive re-exec.
func runWithPrivateTraceSession(ctx context.Context, t *testctx.T) bool {
	t.Helper()
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); !nested {
		return false
	}
	require.NotEmpty(t, os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST"))
	require.NotEmpty(t, os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"))
	binary, err := os.Executable()
	require.NoError(t, err)
	parts := strings.Split(t.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run="+strings.Join(parts, "/"), "-test.v", "-test.count=1")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "DAGGER_SESSION_PORT=") && !strings.HasPrefix(env, "DAGGER_SESSION_TOKEN=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	out, err := cmd.CombinedOutput()
	t.Logf("private-session test:\n%s", out)
	require.NoError(t, err)
	return true
}

// agentTraceSink is the consumer half of a trace-driven client, stood up
// in-process: an OTLP endpoint the session's CLI forwards engine telemetry
// to, folded into the same dagui.DB a frontend builds its view from. Tests
// that need to observe what the engine actually published (call payloads,
// spans, log records) use it in place of a canned approximation.
//
// A frontend owns its DB single-threaded, so ingest (HTTP handler
// goroutines) and the test's reads are serialized on one mutex rather than
// the DB being made concurrent.
//
// It also KEEPS every export request it was handed, in arrival order, so a
// capture of a real session can be served back as a recording.
type agentTraceSink struct {
	mu     sync.Mutex
	db     *dagui.DB
	logExp sdklog.Exporter
	base   string
	conn   engineconn.EngineConn
	// changed is closed and replaced after every ingested export, waking
	// observers blocked in restorableCapture.
	changed chan struct{}

	traces []*coltracepb.ExportTraceServiceRequest
	logs   []*collogspb.ExportLogsServiceRequest
}

func newAgentTraceSink(t testing.TB) *agentTraceSink {
	t.Helper()
	db := dagui.NewDB()
	sink := &agentTraceSink{db: db, logExp: db.LogExporter(), changed: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", sink.tracesHandler)
	mux.HandleFunc("POST /v1/logs", sink.logsHandler)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go srv.Serve(l) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	sink.base = "http://" + l.Addr().String()
	return sink
}

// clientOpts points the CLI session this client spawns at the sink. LIVE is
// what makes a still-running agent's loop span arrive at all: without it the
// CLI only forwards spans once they have ended.
func (sink *agentTraceSink) clientOpts() []dagger.ClientOpt {
	return []dagger.ClientOpt{
		dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", sink.base+"/v1/traces"),
		dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", sink.base+"/v1/logs"),
		dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_TRACES_LIVE", "1"),
	}
}

func (sink *agentTraceSink) tracesHandler(w http.ResponseWriter, r *http.Request) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sink.traces = append(sink.traces, &req)
	if err := sink.db.ExportSpans(r.Context(), telemetry.SpansFromPB(req.ResourceSpans)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sink.notifyLocked()
	w.WriteHeader(http.StatusCreated)
}

func (sink *agentTraceSink) logsHandler(w http.ResponseWriter, r *http.Request) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sink.logs = append(sink.logs, &req)
	if err := telemetry.ReexportLogsFromPB(r.Context(), sink.logExp, &req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sink.notifyLocked()
	w.WriteHeader(http.StatusCreated)
}

// connectWithTrace creates a source session whose agent control records and
// payloads are observable by tests. The resulting recipes come from committed
// agent telemetry, never from an engine-local LLM handle. It explicitly starts
// the from-source CLI even when the test process is nested, without changing
// the process-global session environment.
func connectWithTrace(ctx context.Context, t *testctx.T, configs ...engineconn.Config) (*dagger.Client, *agentTraceSink) {
	t.Helper()
	require.LessOrEqual(t, len(configs), 1)
	var cfg engineconn.Config
	if len(configs) == 1 {
		cfg = configs[0]
	}
	sink := newAgentTraceSink(t)
	cfg.UnsetEnv = append(slices.Clone(cfg.UnsetEnv), "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN")
	cfg.ExtraEnv = append(slices.Clone(cfg.ExtraEnv),
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+sink.base+"/v1/traces",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+sink.base+"/v1/logs",
		"OTEL_EXPORTER_OTLP_TRACES_LIVE=1",
	)
	conn, found, err := engineconn.FromLocalCLI(ctx, &cfg)
	require.NoError(t, err)
	require.True(t, found, "set _EXPERIMENTAL_DAGGER_CLI_BIN to the from-source CLI")
	sink.conn = conn
	return connect(ctx, t, dagger.WithConn(conn)), sink
}

// captureLLMRecipe seeds an inert agent with the given conversation and waits for
// its committed anchor's entire payload closure to cross the telemetry wire. The
// temporary agent never starts a loop. Returning an encoded recipe rather than
// its local seed ID allows the caller to close the source and use a fresh client.
// This is capture evidence, not archive finalization evidence.
func (sink *agentTraceSink) captureLLMRecipe(ctx context.Context, t *testctx.T, c *dagger.Client, llm *dagger.LLM) (dagger.ID, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	seed, err := llm.ID(ctx)
	if err != nil {
		return "", err
	}
	name := "recipe-capture-" + identity.NewID()
	_, err = rehydrateAgent(ctx, c, string(seed), name, name, "IDLE", "")
	if err != nil {
		return "", err
	}
	return sink.committedRecipe(ctx, name)
}

// captureShellRecipe captures a nested shell's committed conversation after its
// client exits. The outer session's telemetry carries the nested control records.
func (sink *agentTraceSink) captureShellRecipe(ctx context.Context, t *testctx.T, base *dagger.Container, selection string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	name := "shell-recipe-" + identity.NewID()
	_, err := base.With(daggerShell(selection + " | spawn --handle " + name + " --name " + name)).Sync(ctx)
	if err != nil {
		return "", err
	}
	id, err := sink.committedRecipe(ctx, name)
	return string(id), err
}

// committedRecipe also serves containerized shell fixtures: they spawn a named
// inert agent instead of invoking a serialization API, then the enclosing client
// observes that agent's committed recipe in forwarded telemetry.
func (sink *agentTraceSink) committedRecipe(ctx context.Context, handle string) (dagger.ID, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var source agentcontrol.Key
	var lastErr error
	for {
		var encoded string
		var invalid error
		sink.read(func(db *dagui.DB) {
			agents, _, err := db.AgentControl()
			if err != nil {
				invalid = err
				return
			}
			var selected *agentcontrol.Agent
			for _, agent := range agents {
				if agent.Handle != handle {
					continue
				}
				if selected != nil || (source.Handle != "" && source != agent.Key) {
					invalid = fmt.Errorf("capture handle %q appeared in multiple source namespaces", handle)
					return
				}
				source = agent.Key
				selected = &agent
			}
			if selected == nil {
				return
			}
			if _, err := selected.RestoreState(); err != nil {
				invalid = err
				return
			}
			id, err := db.CallIDForDigest(selected.Digest)
			if err != nil {
				lastErr = err
				return
			}
			encoded, lastErr = id.Encode()
		})
		if invalid != nil {
			return "", invalid
		}
		if encoded != "" {
			return dagger.ID(encoded), nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("capture committed recipe %q: %w", handle, errors.Join(ctx.Err(), lastErr))
		case <-ticker.C:
		}
	}
}

// read runs fn against the DB with ingest held off.
func (sink *agentTraceSink) read(fn func(db *dagui.DB)) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	fn(sink.db)
}

// capture returns the OTLP export requests the session forwarded, in arrival
// order — the raw material a fake Cloud serves back.
func (sink *agentTraceSink) capture() ([]*coltracepb.ExportTraceServiceRequest, []*collogspb.ExportLogsServiceRequest) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return slices.Clone(sink.traces), slices.Clone(sink.logs)
}

// notifyLocked wakes every observer after either telemetry channel is
// ingested. Reading the predicate and this channel under mu prevents lost
// wakeups.
func (sink *agentTraceSink) notifyLocked() {
	close(sink.changed)
	sink.changed = make(chan struct{})
}

// restorableTraceCapture is a received prefix of the trace that was verified
// restorable, and that same prefix's export requests.
type restorableTraceCapture struct {
	nodes    map[string]*dagui.AgentNode
	traceIDs map[string]string
	traces   []*coltracepb.ExportTraceServiceRequest
	logs     []*collogspb.ExportLogsServiceRequest
}

// errAgentCaptureFailed is terminal: waiting longer cannot make an agent
// whose producer reported a capture error restorable.
var errAgentCaptureFailed = errors.New("agent capture failed")

// restorableCaptureLocked validates the current received prefix and captures
// that same prefix: the roster size, each agent's required state, and that
// each agent's CURRENT anchor rebuilds its full recipe closure. Checking and
// capturing separately would let a newer revision (a turn's final commit, a
// dismissal) move an anchor to frames still in flight between the two.
func (sink *agentTraceSink) restorableCaptureLocked(count int, states map[string]string) (restorableTraceCapture, error) {
	agents := sink.db.Agents()
	if len(agents) != count {
		return restorableTraceCapture{}, fmt.Errorf("want %d agents, received %d", count, len(agents))
	}
	nodes := make(map[string]*dagui.AgentNode, count)
	traceIDs := make(map[string]string, count)
	for _, agent := range agents {
		if agent.Control != nil && agent.Control.CaptureError != "" {
			return restorableTraceCapture{}, fmt.Errorf("%w: agent %q: %s", errAgentCaptureFailed, agent.Name, agent.Control.CaptureError)
		}
		if agent.Control == nil || agent.CallDigest == "" || agent.SnapshotDigest == "" {
			return restorableTraceCapture{}, fmt.Errorf("agent %q lacks a control record, call digest or snapshot digest", agent.Name)
		}
		if state, ok := states[agent.Name]; ok && agent.State != state {
			return restorableTraceCapture{}, fmt.Errorf("agent %q state %q, want %q", agent.Name, agent.State, state)
		}
		if _, err := sink.db.CallIDForDigest(agent.SnapshotDigest); err != nil {
			return restorableTraceCapture{}, fmt.Errorf("agent %q anchor %s: %w", agent.Name, agent.SnapshotDigest, err)
		}
		nodes[agent.Name] = agent
		traceIDs[agent.Name] = agent.Control.Trace
	}
	for name := range states {
		if _, ok := nodes[name]; !ok {
			return restorableTraceCapture{}, fmt.Errorf("required agent %q not in trace", name)
		}
	}
	return restorableTraceCapture{
		nodes:    nodes,
		traceIDs: traceIDs,
		traces:   slices.Clone(sink.traces),
		logs:     slices.Clone(sink.logs),
	}, nil
}

// restorableCapture blocks until the received prefix is restorable, then
// returns it. It waits on the sink's change notification, not a poll.
func (sink *agentTraceSink) restorableCapture(ctx context.Context, count int, states map[string]string) (restorableTraceCapture, error) {
	for {
		if err := ctx.Err(); err != nil {
			return restorableTraceCapture{}, err
		}
		sink.mu.Lock()
		captured, err := sink.restorableCaptureLocked(count, states)
		changed := sink.changed
		sink.mu.Unlock()
		if err == nil {
			return captured, nil
		}
		if errors.Is(err, errAgentCaptureFailed) {
			return restorableTraceCapture{}, err
		}
		select {
		case <-ctx.Done():
			return restorableTraceCapture{}, fmt.Errorf("waiting for restorable trace: %w: %w", ctx.Err(), err)
		case <-changed:
		}
	}
}

// awaitRestorableCapture is restorableCapture bounded to a minute, failing
// the test if the trace never becomes restorable. states names the lifecycle
// state an agent's latest record must show; agents it omits may be in any.
func (sink *agentTraceSink) awaitRestorableCapture(ctx context.Context, t testing.TB, count int, states map[string]string) restorableTraceCapture {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	captured, err := sink.restorableCapture(ctx, count, states)
	require.NoError(t, err)
	return captured
}
