package idtui

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"github.com/vito/go-sse/sse"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// The fetch (hack/designs/resume-from-trace.md §5.1, build order slice 5):
// `dagger agent --trace <id>` streaming a past session's whole trace out of
// Dagger Cloud and into the live frontend's DB.
//
// The Cloud endpoints are UNDEPLOYED, so the fake server below is the entire
// risk of this slice: it is built to the wire shape §5.1 specifies and the
// reference implementation (cmd/dagger/trace.go at 1492469b) reads — GET
// /v1/{traces,logs,metrics}/{trace-id}, each an SSE stream whose events carry
// protojson-encoded OTLP export requests, authenticated with a Basic
// Authorization header. §12's two curls could not be run (no Cloud
// credentials, nothing deployed to curl); when they can be, this fixture is
// where what came back gets encoded.
//
// The payload is the canned capture trace_import_test.go already drives slice
// 4 with, served over the wire instead of handed to the importer directly: the
// same crashed session, with its never-ended spans and its attribute-only,
// empty-bodied state records.

// fetchTraceIDHex is the source trace's ID as it appears in the URL — derived
// from the capture rather than spelled out, so the two cannot drift.
var fetchTraceIDHex = hex.EncodeToString(cannedTraceID(foreignTraceIDByte))

// cannedTokenMetrics is the metric half of the capture: an LLM token gauge
// attributed to the imported agent's loop span. Imported metrics COUNT (§12)
// — cost and token totals accumulate across a resume rather than restarting
// at zero — so the fetch has to carry this stream too, not just spans and
// logs.
//
// It carries NO Resource, which is legal OTLP and one of the two shapes the
// shared re-export path dereferences blindly (§13.4's stopgap guards, upstream
// as dagger/otel-go#17). A gauge, not a sum: otel-go's metricFromPB only
// converts gauges today.
func cannedTokenMetrics(spanID byte) *colmetricspb.ExportMetricsServiceRequest {
	point := &metricspb.NumberDataPoint{
		TimeUnixNano: uint64(time.Unix(foreignTurnEnd, 0).UnixNano()),
		Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 4200},
		Attributes: []*commonpb.KeyValue{
			cannedStringAttr("model", "canned"),
			cannedStringAttr(telemetry.MetricsSpanIDAttr, prettyTestSpanID(spanID).String()),
		},
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: telemetry.LLMInputTokens,
					Data: &metricspb.Metric_Gauge{
						Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{point}},
					},
				}},
			}},
		}},
	}
}

// fakeCloud serves the §5.1 endpoints for one trace ID.
type fakeCloud struct {
	traceID string

	traces  []*coltracepb.ExportTraceServiceRequest
	logs    []*collogspb.ExportLogsServiceRequest
	metrics []*colmetricspb.ExportMetricsServiceRequest

	// eventName is put on every SSE event carrying a payload. The real
	// endpoints leave it EMPTY (verified in trace_live_test.go), which is the
	// default here; a test sets it to prove the client does not read it,
	// since Cloud's vocabulary is not a promise anyone has made.
	eventName string
	// status, when set, is returned instead of a stream for the named kind.
	status map[string]int
	// garbage, when set for a kind, serves one event that is not an OTLP
	// export request.
	garbage map[string]bool

	mu       sync.Mutex
	requests []string // "<method> <path>", in arrival order
	authz    []string // the Authorization header of each request
}

func (f *fakeCloud) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("DAGGER_CLOUD_URL", srv.URL)
}

func (f *fakeCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.authz = append(f.authz, r.Header.Get("Authorization"))
	f.mu.Unlock()

	kind, traceID, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	if !ok || traceID != f.traceID {
		http.Error(w, "no such trace", http.StatusNotFound)
		return
	}
	if status := f.status[kind]; status != 0 {
		http.Error(w, "cloud is having a moment", status)
		return
	}

	var payloads []proto.Message
	switch kind {
	case "traces":
		for _, req := range f.traces {
			payloads = append(payloads, req)
		}
	case "logs":
		for _, req := range f.logs {
			payloads = append(payloads, req)
		}
	case "metrics":
		for _, req := range f.metrics {
			payloads = append(payloads, req)
		}
	default:
		http.Error(w, "no such stream", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	// The real endpoints open with a named, data-less preamble event, and
	// send their payloads as UNNAMED events — both verified against
	// api.dagger.cloud (see trace_live_test.go and §13.5). Modelling the
	// preamble here keeps the client's "an event with no data is not a
	// payload" path under test, since it is the first thing Cloud sends.
	_ = sse.Event{Name: "connected"}.Write(w)
	if flusher != nil {
		flusher.Flush()
	}

	if f.garbage[kind] {
		_ = sse.Event{Name: f.eventName, Data: []byte(`{"this":"is not an OTLP export request"}`)}.Write(w)
		return
	}

	for _, payload := range payloads {
		data, err := protojson.Marshal(payload)
		if err != nil {
			panic(err)
		}
		if err := (sse.Event{Name: f.eventName, Data: data}).Write(w); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (f *fakeCloud) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeCloud) authHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.authz...)
}

// cannedCloud serves the capture slice 4 drives the importer with: the crashed
// session's spans, its agent-state records, and its token metrics.
func cannedCloud(withWorker bool) *fakeCloud {
	states := []cannedStateRecord{{span: foreignLoopSpanID, state: "RUNNING"}}
	if withWorker {
		states = append(states, cannedStateRecord{
			span: foreignWorkerSpanID, state: "RUNNING", emptyBody: true,
		})
	}
	return &fakeCloud{
		traceID: fetchTraceIDHex,
		traces:  []*coltracepb.ExportTraceServiceRequest{foreignSessionTrace(withWorker)},
		logs:    []*collogspb.ExportLogsServiceRequest{cannedAgentStateLogs(foreignTraceIDByte, states...)},
		metrics: []*colmetricspb.ExportMetricsServiceRequest{cannedTokenMetrics(foreignLoopSpanID)},
	}
}

// The importer slice 4 landed is the sink the fetch is written for; the
// interface exists so internal/cloud can stay transport-only.
var _ cloud.TraceImportSink = (*enginetel.TraceImporter)(nil)

// recordingSink wraps the real importer and records callbacks so tests can
// assert that Seal happens once after the final span import without imposing
// an order on the concurrently fetched streams.
type recordingSink struct {
	inner cloud.TraceImportSink
	calls []string
}

func (s *recordingSink) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	s.calls = append(s.calls, "spans")
	return s.inner.ImportSpans(ctx, req)
}

func (s *recordingSink) ImportLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	s.calls = append(s.calls, "logs")
	return s.inner.ImportLogs(ctx, req)
}

func (s *recordingSink) ImportMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	s.calls = append(s.calls, "metrics")
	return s.inner.ImportMetrics(ctx, req)
}

func (s *recordingSink) Seal(ctx context.Context) error {
	s.calls = append(s.calls, "seal")
	return s.inner.Seal(ctx)
}

// fetchToken is the DAGGER_CLOUD_TOKEN the tests authenticate with.
const fetchToken = "canned-cloud-token"

func fetchClient(t *testing.T) *cloud.OTLPClient {
	t.Helper()
	t.Setenv("DAGGER_CLOUD_TOKEN", fetchToken)
	cloudAuth, err := auth.GetCloudAuth(t.Context())
	require.NoError(t, err)
	client, err := cloud.NewOTLPClient(t.Context(), cloudAuth)
	require.NoError(t, err)
	return client
}

// fetchIntoDB is the whole slice, end to end: a live session already
// publishing into its own DB, a fake Cloud serving the source trace, and a
// real fetch folding the second into the first through the slice-4 importer.
func fetchIntoDB(t *testing.T, srv *fakeCloud) (*dagui.DB, *recordingSink) {
	t.Helper()
	db, sink := fetchTargets(t)
	srv.start(t)
	require.NoError(t, fetchClient(t).FetchTrace(t.Context(), srv.traceID, sink))
	return db, sink
}

func fetchTargets(t *testing.T) (*dagui.DB, *recordingSink) {
	t.Helper()
	db := dagui.NewDB()
	// The live session is already publishing by the time the fetch runs: its
	// root is the DB's root and its primary span.
	require.NoError(t, db.ExportSpans(context.Background(),
		telemetry.SpansFromPB(liveSessionTrace().GetResourceSpans())))
	return db, &recordingSink{inner: enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})}
}

// TestFetchStreamsTheWholeTraceIntoTheLiveDB is §5.1's promise on the wire:
// all three streams, unfiltered, into the DB the live session is already
// using — which is what makes the restored session the old session's TUI plus
// a live prompt rather than a private reconstruction of its conversation.
func TestFetchStreamsTheWholeTraceIntoTheLiveDB(t *testing.T) {
	srv := cannedCloud(true)
	db, _ := fetchIntoDB(t, srv)

	require.ElementsMatch(t, []string{
		"GET /v1/traces/" + fetchTraceIDHex,
		"GET /v1/logs/" + fetchTraceIDHex,
		"GET /v1/metrics/" + fetchTraceIDHex,
	}, srv.paths(), "the fetch must pull all three streams of the trace")

	// Spans: the source session's tree, beside the live one.
	turn := db.Spans.Map[prettyTestSpanID(foreignTurnSpanID)]
	require.NotNil(t, turn, "the imported turn never reached the DB")
	require.Equal(t, "imported turn", turn.Name)
	require.NotNil(t, db.Spans.Map[prettyTestSpanID(liveTurnSpanID)],
		"the fetch disturbed the live session's spans")

	// Logs: the agent-state records are attribute-only, and one of them
	// arrives with no body at all — the shape §12 flags as unverified and
	// §13.4's stopgap guards keep from panicking.
	scout := importedAgent(t, db, "scout")
	require.Equal(t, "RUNNING", scout.State, "the state records did not survive the fetch")

	// Metrics: attributed to the imported loop span, so the resumed session's
	// token totals continue rather than restarting at zero.
	points := db.MetricsBySpan[prettyTestSpanID(foreignLoopSpanID)][telemetry.LLMInputTokens]
	require.Len(t, points, 1, "the imported token metrics never reached the DB")
	require.Equal(t, int64(4200), points[0].Value)
}

// TestFetchSealsOnceAfterTheSpanStream pins the importer's lifecycle rule:
// streams may interleave, but Seal happens exactly once after every stream
// callback, including the final span import, has returned. Sealing early — or
// letting a span arrive after it — leaves the span with a stale end time,
// because the seal's fallback bound is "the newest timestamp seen", which only
// holds for one bounded fetch.
func TestFetchSealsOnceAfterTheSpanStream(t *testing.T) {
	srv := cannedCloud(true)
	// Multiple events make "after the span stream" stronger than merely after
	// the first callback.
	srv.traces = append(srv.traces, foreignSessionTrace(true))
	db, sink := fetchIntoDB(t, srv)

	var spans, seals, lastSpan, sealAt int
	lastSpan, sealAt = -1, -1
	for i, call := range sink.calls {
		switch call {
		case "spans":
			spans++
			lastSpan = i
		case "seal":
			seals++
			sealAt = i
		}
	}
	require.Equal(t, len(srv.traces), spans)
	require.Equal(t, 1, seals, "the fetch must seal exactly once")
	require.Greater(t, sealAt, lastSpan, "Seal ran before all span imports stopped")
	require.Equal(t, len(sink.calls)-1, sealAt, "Seal ran before another stream callback")

	// The seal's effect, stated where a user would see it: the crashed
	// session's never-ended spans stop rendering as live work.
	sealedAt := time.Unix(foreignSealedAt, 0)
	for _, id := range []byte{foreignRootSpanID, foreignLoopSpanID, foreignWorkerSpanID} {
		span := db.Spans.Map[prettyTestSpanID(id)]
		require.NotNil(t, span, "span %d missing from the DB", id)
		require.False(t, span.IsRunning(), "%q still renders as live work", span.Name)
		require.True(t, span.Canceled, "%q was not sealed Canceled", span.Name)
		require.True(t, span.LeftRunning, "%q was not sealed LeftRunning", span.Name)
		require.True(t, span.EndTime.Equal(sealedAt),
			"%q sealed at %v, want the newest timestamp the capture carries (%v)",
			span.Name, span.EndTime, sealedAt)
	}
	require.False(t, importedAgent(t, db, "scout").Live(),
		"an agent whose session died must not report as live")

	// ...and the live session, which the fetch never touched, keeps running.
	require.True(t, db.Spans.Map[prettyTestSpanID(liveRootSpanID)].IsRunning(),
		"the fetch sealed the live trace too")
	require.Equal(t, prettyTestSpanID(liveRootSpanID), db.PrimarySpan,
		"the fetch repointed the primary span")
}

// TestFetchRestoresTheAgentsScrollback is what the fetch is FOR, asserted
// through the frontend rather than the DB: one roster entry spanning both of
// the agent's lives, and a transcript that opens with the turns the old
// session spoke.
func TestFetchRestoresTheAgentsScrollback(t *testing.T) {
	db, _ := fetchIntoDB(t, cannedCloud(false))

	require.Len(t, db.Agents(), 1, "the restored agent's two lives split the roster")

	handler := &focusShellHandler{target: importChiefAgentID}
	fe := focusTestFrontend(t, db, handler)
	fe.recalculateViewLocked()

	require.Equal(t, map[string]bool{"imported turn": true, "live turn": true},
		revealedNames(t, fe),
		"the fetched transcript must span both of the agent's lives")
}

// TestFetchSendsTheCloudAuthHeader covers the auth path end to end:
// auth.GetCloudAuth reads DAGGER_CLOUD_TOKEN into a Basic credential, and
// auth.GetDaggerCloudAuth renders the header Cloud expects, on every one of
// the three requests.
func TestFetchSendsTheCloudAuthHeader(t *testing.T) {
	srv := cannedCloud(false)
	fetchIntoDB(t, srv)

	want, err := auth.GetDaggerCloudAuth(t.Context(), fetchToken)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(want, "Basic "), "fixture: %q", want)

	headers := srv.authHeaders()
	require.Len(t, headers, 3)
	for i, got := range headers {
		require.Equal(t, want, got, "request %d went out unauthenticated", i)
	}
}

// TestFetchIgnoresTheSSEEventName is the hedge §12's undeployed endpoints
// demand: the event vocabulary is unverified, so the client reads payloads,
// not names. Tightening it to a name nobody has promised would turn a whole
// trace into silence.
func TestFetchIgnoresTheSSEEventName(t *testing.T) {
	srv := cannedCloud(false)
	srv.eventName = "next"
	db, _ := fetchIntoDB(t, srv)

	require.NotNil(t, db.Spans.Map[prettyTestSpanID(foreignTurnSpanID)],
		"a named event was skipped")
}

// TestFetchFailsOnAnUndecodablePayload: a payload this client cannot decode is
// a lost fact — an agent's state record, a call payload, a whole subtree — and
// §12 settled that a trace which cannot be rebuilt fails the restore rather
// than degrading. The reference client warns and carries on, which is right
// for a view and wrong for a restore.
func TestFetchFailsOnAnUndecodablePayload(t *testing.T) {
	srv := cannedCloud(false)
	srv.garbage = map[string]bool{"logs": true}
	_, sink := fetchTargets(t)
	srv.start(t)

	err := fetchClient(t).FetchTrace(t.Context(), srv.traceID, sink)
	require.ErrorContains(t, err, "unmarshal logs")
}

// TestFetchFailsOnAnErrorResponse: an endpoint that is down, or a trace that
// is not there, must fail the command with what the server said — not import
// an empty trace and restore nothing.
func TestFetchFailsOnAnErrorResponse(t *testing.T) {
	srv := cannedCloud(false)
	srv.status = map[string]int{"traces": http.StatusServiceUnavailable}
	_, sink := fetchTargets(t)
	srv.start(t)

	err := fetchClient(t).FetchTrace(t.Context(), srv.traceID, sink)
	require.ErrorContains(t, err, "503")
	require.ErrorContains(t, err, "cloud is having a moment")
}

// TestFetchFailsOnAnUnknownTrace: a trace ID Cloud does not have is a 404, and
// the restore says so rather than opening an empty session.
func TestFetchFailsOnAnUnknownTrace(t *testing.T) {
	srv := cannedCloud(false)
	_, sink := fetchTargets(t)
	srv.start(t)

	err := fetchClient(t).FetchTrace(t.Context(), "0123456789abcdef0123456789abcdef", sink)
	require.ErrorContains(t, err, "404")
}

// TestFetchRequiresAuth: no credential is a clear instruction, not a 401 from
// three round trips later.
func TestFetchRequiresAuth(t *testing.T) {
	_, err := cloud.NewOTLPClient(t.Context(), nil)
	require.ErrorContains(t, err, "not authenticated")
}
