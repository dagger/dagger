package core

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"

	telemetry "github.com/dagger/otel-go"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
)

// largeCaptureFixture persists a CI-shaped trace: one root, a fan of
// test-like children, each with a subtree of nested work, and log records on
// most spans. Sizes default small enough for a quick run; set
// DAGGER_CAPTURE_BENCH_SPANS / _LOGS to scale it to a real CI trace
// (~220k spans, ~410k log records).
func largeCaptureFixture(b *testing.B) (*clientdb.DBs, string) {
	b.Helper()
	spans := envInt("DAGGER_CAPTURE_BENCH_SPANS", 20000)
	logs := envInt("DAGGER_CAPTURE_BENCH_LOGS", 40000)
	const (
		traceID = "000102030405060708090a0b0c0d0e0f"
		fanout  = 200
		depth   = 4
	)
	spanHex := func(i int) string { return fmt.Sprintf("%016x", i+1) }
	root := spanHex(0)
	emptyAttrs, err := clientdb.MarshalProtoJSONs([]*otlpcommonv1.KeyValue(nil))
	if err != nil {
		b.Fatal(err)
	}
	rows := make([]clientdb.Span, 0, spans)
	rows = append(rows, clientdb.Span{TraceID: traceID, SpanID: root, Name: "root", StartTime: 1, EndTime: sql.NullInt64{Int64: 2, Valid: true}, Attributes: emptyAttrs, InstrumentationScope: []byte("{}"), Resource: []byte("{}"), Links: []byte("[]"), Events: []byte("[]")})
	for i := 1; i < spans; i++ {
		// Children 1..fanout hang off the root; every later span hangs off a
		// span up to `depth` levels above it in its test's subtree.
		parent := 0
		if i > fanout {
			parent = i - fanout
			if (i/fanout)%depth == 0 {
				parent = (i % fanout) + 1
			}
		}
		rows = append(rows, clientdb.Span{
			TraceID: traceID, SpanID: spanHex(i), ParentSpanID: validSpanID(spanHex(parent)),
			Name: "span " + strconv.Itoa(i), StartTime: int64(i + 1), EndTime: sql.NullInt64{Int64: int64(i + 2), Valid: true},
			Attributes: emptyAttrs, InstrumentationScope: []byte("{}"), Resource: []byte("{}"), Links: []byte("[]"), Events: []byte("[]"),
		})
	}
	dbs := clientdb.NewDBs(b.TempDir())
	store, err := dbs.Open(b.Context(), "capture-test")
	if err != nil {
		b.Fatal(err)
	}
	for start := 0; start < len(rows); start += 5000 {
		if _, err := store.AppendSpans(rows[start:min(start+5000, len(rows))]); err != nil {
			b.Fatal(err)
		}
	}
	stdout, err := clientdb.MarshalProtoJSONs([]*otlpcommonv1.KeyValue{{
		Key:   telemetry.StdioStreamAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: 1}},
	}})
	if err != nil {
		b.Fatal(err)
	}
	scope, err := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	batch := make([]clientdb.Log, 0, 5000)
	for i := range logs {
		body, err := proto.Marshal(stringLogBody(fmt.Sprintf("log line %d\n", i)))
		if err != nil {
			b.Fatal(err)
		}
		batch = append(batch, clientdb.Log{
			TraceID:              sql.NullString{String: traceID, Valid: true},
			SpanID:               validSpanID(spanHex(i % spans)),
			Body:                 body,
			Attributes:           stdout,
			InstrumentationScope: scope,
			Resource:             []byte("{}"),
		})
		if len(batch) == cap(batch) || i == logs-1 {
			if _, err := store.AppendLogs(batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	// Keep the store open, as the engine does for a live session: each
	// ClientTelemetry call then shares it rather than recovering it from disk.
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Error(err)
		}
	})
	return dbs, root
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func BenchmarkCaptureLogLinesLargeScope(b *testing.B) {
	dbs, root := largeCaptureFixture(b)
	ctx := ContextWithQuery(b.Context(), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}})
	m := newMCP()
	for b.Loop() {
		if _, err := m.captureLogLines(ctx, root, true, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadTraceReportLargeScope(b *testing.B) {
	dbs, root := largeCaptureFixture(b)
	ctx := ContextWithQuery(b.Context(), &Query{Server: &logCaptureTestServer{mockServer: &mockServer{}, dbs: dbs}})
	m := newMCP()
	for b.Loop() {
		if _, err := m.inspectSpanResult(ctx, root, readTraceReportOpts()); err != nil {
			b.Fatal(err)
		}
	}
}
