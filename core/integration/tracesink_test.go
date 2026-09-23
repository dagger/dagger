package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
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

	traces []*coltracepb.ExportTraceServiceRequest
	logs   []*collogspb.ExportLogsServiceRequest
}

func newAgentTraceSink(t testing.TB) *agentTraceSink {
	t.Helper()
	db := dagui.NewDB()
	sink := &agentTraceSink{db: db, logExp: db.LogExporter()}

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
	w.WriteHeader(http.StatusCreated)
}

// connectWithTrace creates a source session whose agent control records and
// payloads are observable by tests. The resulting recipes come from committed
// agent telemetry, never from an engine-local LLM handle or a public portable-ID
// API. It explicitly starts the from-source CLI even when the test process is
// nested, without changing the process-global session environment.
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
			if selected.Removed {
				invalid = fmt.Errorf("capture agent %q was removed", handle)
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
