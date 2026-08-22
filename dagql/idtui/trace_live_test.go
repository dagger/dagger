package idtui

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// The fetch against the REAL Dagger Cloud endpoints, which is where
// hack/designs/resume-from-trace.md §12's two Cloud-side assumptions stop
// being assumptions:
//
//   - that raw byte-bodied call payload records survive the round trip;
//   - that attribute-only agent state and snapshot records survive too.
//
// Neither is checkable against the fake server in trace_fetch_test.go, which
// answers a different question (does the client speak the protocol). This one
// answers "does Cloud give back what the engine put in", and it needs Cloud.
//
// Skipped unless DAGGER_CLOUD_TRACE names a trace to fetch:
//
//	go test ./dagql/idtui -run TestLiveCloud -v \
//	  -exec 'env DAGGER_CLOUD_TRACE=<trace-id>'
//
// Any real trace works; one from a session with agents exercises more of the
// record shapes.

func liveCloudTraceID(t *testing.T) string {
	t.Helper()
	traceID := os.Getenv("DAGGER_CLOUD_TRACE")
	if traceID == "" {
		t.Skip("DAGGER_CLOUD_TRACE not set")
	}
	return traceID
}

// recordShapes counts the log-record shapes a live fetch actually carries,
// inspecting the protobuf on its way to the importer. The empty-bodied count
// is §12's first assumption, stated as a number.
type recordShapes struct {
	inner cloud.TraceImportSink

	spans      int
	logRecords int
	noBody     int // Body absent entirely — the shape that used to panic
	emptyBody  int // Body present, empty string — what the engine emits
	bytesBody  int // raw protobuf call payloads
	textBody   int // Body with text — ordinary log output
	attrOnly   int // no body text, but attributes: agent resume facts
}

func (s *recordShapes) ImportSpans(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) error {
	for _, resourceSpans := range req.GetResourceSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			s.spans += len(scopeSpans.GetSpans())
		}
	}
	return s.inner.ImportSpans(ctx, req)
}

func (s *recordShapes) ImportLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest) error {
	for _, resourceLogs := range req.GetResourceLogs() {
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			for _, record := range scopeLogs.GetLogRecords() {
				s.logRecords++
				body := record.GetBody()
				switch {
				case body == nil:
					s.noBody++
				case len(body.GetBytesValue()) > 0:
					s.bytesBody++
				case body.GetStringValue() == "":
					s.emptyBody++
				default:
					s.textBody++
				}
				if body != nil && len(body.GetBytesValue()) == 0 && body.GetStringValue() == "" && len(record.GetAttributes()) > 0 {
					s.attrOnly++
				}
			}
		}
	}
	return s.inner.ImportLogs(ctx, req)
}

func (s *recordShapes) ImportMetrics(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return s.inner.ImportMetrics(ctx, req)
}

func (s *recordShapes) Seal(ctx context.Context) error { return s.inner.Seal(ctx) }

// TestLiveCloudFetchRoundTrip fetches a real trace through the real client and
// measures what came back.
func TestLiveCloudFetchRoundTrip(t *testing.T) {
	traceID := liveCloudTraceID(t)
	ctx := t.Context()

	cloudAuth, err := auth.GetCloudAuth(ctx)
	require.NoError(t, err)
	if cloudAuth == nil || cloudAuth.Token == nil {
		t.Skip("no Dagger Cloud credential; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
	}
	client, err := cloud.NewOTLPClient(ctx, cloudAuth)
	require.NoError(t, err)

	db := dagui.NewDB()
	sink := &recordShapes{inner: enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})}

	require.NoError(t, client.FetchTrace(ctx, traceID, sink))

	t.Logf("spans=%d logRecords=%d (noBody=%d emptyBody=%d bytesBody=%d textBody=%d attrOnly=%d) calls=%d",
		sink.spans, sink.logRecords, sink.noBody, sink.emptyBody, sink.bytesBody, sink.textBody,
		sink.attrOnly, len(db.Calls))
	t.Log(client.StatsSummary())

	require.NotZero(t, sink.spans, "the trace stream returned no spans")

	// Call payloads ride raw byte bodies on their dedicated log scope. Count
	// both the wire shape and the decoded calls to prove the Cloud round trip
	// preserved the consumer channel.
	require.NotZero(t, sink.bytesBody,
		"no byte-bodied log record came back; call payloads did not survive the round trip")
	require.NotEmpty(t, db.Calls,
		"no call payloads reached the DB; span-free ID rebuild has nothing to work from")
}

// TestLiveCloudPreservesCallPayloads verifies the second assumption where it
// bites: every decoded payload must rebuild to the address computed from its
// raw body.
func TestLiveCloudPreservesCallPayloads(t *testing.T) {
	traceID := liveCloudTraceID(t)
	ctx := t.Context()

	cloudAuth, err := auth.GetCloudAuth(ctx)
	require.NoError(t, err)
	if cloudAuth == nil || cloudAuth.Token == nil {
		t.Skip("no Dagger Cloud credential; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
	}
	client, err := cloud.NewOTLPClient(ctx, cloudAuth)
	require.NoError(t, err)

	db := dagui.NewDB()
	require.NoError(t, client.FetchTrace(ctx, traceID, enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})))

	var checked, mismatched int
	var firstUnrebuildable error
	for digest := range db.Calls {
		id, err := db.CallIDForDigest(digest)
		if err != nil {
			// A chain whose ancestor frames did not reach this client is a
			// DIFFERENT failure (§9's first row) and not what this test is
			// about; count it out rather than calling it a mismatch. It is
			// worth reporting, though — §5.3.3 fails a restore on exactly
			// this, so how common it is on a real trace is a fact slice 6
			// wants.
			if firstUnrebuildable == nil {
				firstUnrebuildable = err
			}
			continue
		}
		checked++
		rebuilt := id.Digest()
		if rebuilt.String() != digest {
			mismatched++
			t.Errorf("call payload %s rebuilt to %s: Cloud did not return it byte-identical",
				digest, rebuilt)
		}
	}

	t.Logf("calls: %d total, %d rebuilt, %d mismatched", len(db.Calls), checked, mismatched)
	if firstUnrebuildable != nil {
		t.Logf("first payload that did not rebuild: %v", firstUnrebuildable)
	}
	require.NotZero(t, checked, "no call payload rebuilt; the assertion below proved nothing")
}
