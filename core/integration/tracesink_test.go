package core

import (
	"io"
	"net"
	"net/http"
	"sync"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
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
// capture of a real session can be served back as a replay.
type agentTraceSink struct {
	mu     sync.Mutex
	db     *dagui.DB
	logExp sdklog.Exporter
	base   string

	traces []*coltracepb.ExportTraceServiceRequest
	logs   []*collogspb.ExportLogsServiceRequest
}

func newAgentTraceSink(t *testctx.T) *agentTraceSink {
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

// read runs fn against the DB with ingest held off.
func (sink *agentTraceSink) read(fn func(db *dagui.DB)) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	fn(sink.db)
}
