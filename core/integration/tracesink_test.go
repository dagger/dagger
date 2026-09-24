package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
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
	mu      sync.Mutex
	db      *dagui.DB
	logExp  sdklog.Exporter
	base    string
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

// notifyLocked wakes every observer after either telemetry channel is ingested.
// Reading the predicate and this channel under mu prevents lost wakeups.
func (sink *agentTraceSink) notifyLocked() {
	close(sink.changed)
	sink.changed = make(chan struct{})
}

type restorableTraceCapture struct {
	traceIDs map[string]string
	traces   []*coltracepb.ExportTraceServiceRequest
	logs     []*collogspb.ExportLogsServiceRequest
}

// restorableCaptureLocked validates the current received prefix and captures
// that same prefix. AgentNode projections are replaced on every DB mutation;
// copy the trace IDs here as their underlying Span pointers remain live.
func (sink *agentTraceSink) restorableCaptureLocked(count int, states map[string]string) (restorableTraceCapture, error) {
	agents := sink.db.Agents()
	if len(agents) != count {
		return restorableTraceCapture{}, fmt.Errorf("want %d agents, received %d", count, len(agents))
	}
	traceIDs := make(map[string]string, count)
	for _, agent := range agents {
		if agent.CallDigest == "" || agent.SnapshotDigest == "" {
			return restorableTraceCapture{}, fmt.Errorf("agent %q lacks call or snapshot digest", agent.Name)
		}
		if state, ok := states[agent.Name]; ok && agent.State != state {
			return restorableTraceCapture{}, fmt.Errorf("agent %q state %q, want %q", agent.Name, agent.State, state)
		}
		if _, err := sink.db.CallIDForDigest(agent.SnapshotDigest); err != nil {
			return restorableTraceCapture{}, fmt.Errorf("agent %q anchor %s: %w", agent.Name, agent.SnapshotDigest, err)
		}
		traceIDs[agent.Name] = agent.Span().TraceID.String()
	}
	for name := range states {
		if _, ok := traceIDs[name]; !ok {
			return restorableTraceCapture{}, fmt.Errorf("required agent %q not in trace", name)
		}
	}
	return restorableTraceCapture{
		traceIDs: traceIDs,
		traces:   slices.Clone(sink.traces),
		logs:     slices.Clone(sink.logs),
	}, nil
}

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
		select {
		case <-ctx.Done():
			return restorableTraceCapture{}, fmt.Errorf("waiting for restorable trace: %w: %w", ctx.Err(), err)
		case <-changed:
		}
	}
}

func (sink *agentTraceSink) awaitRestorableCapture(ctx context.Context, t testing.TB, count int, states map[string]string) restorableTraceCapture {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	captured, err := sink.restorableCapture(ctx, count, states)
	require.NoError(t, err)
	return captured
}
