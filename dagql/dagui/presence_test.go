package dagui

import (
	"testing"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func presenceRecord(traceID trace.TraceID, spanID trace.SpanID, attrs ...otellog.KeyValue) sdklog.Record {
	attrs = append(attrs, otellog.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRolePresence))
	return newTestLogRecord(traceID, spanID, "", attrs...)
}

// A presence record says its span has display text, routed like that text
// would be, without being text itself.
func TestLogPresenceFlagsHasLogs(t *testing.T) {
	db := NewDB()
	traceID := trace.TraceID{1}
	span := SpanID{SpanID: trace.SpanID{1}}
	db.PrimarySpan = span

	renderable := db.IngestLogs([]sdklog.Record{presenceRecord(traceID, span.SpanID)})
	if len(renderable) != 0 {
		t.Fatalf("presence rendered as text: %d records", len(renderable))
	}
	if !db.Spans.Map[span].HasLogs {
		t.Fatal("presence did not flag the span as having logs")
	}
	if len(db.PrimaryLogs[span]) != 0 {
		t.Fatal("presence buffered as primary output")
	}
}

func TestLogPresenceRoutesByDigest(t *testing.T) {
	db := NewDB()
	traceID := TraceID{TraceID: trace.TraceID{1}}
	creator := SpanID{SpanID: trace.SpanID{2}}
	outputDig := "xxh3:service-output"
	// Before the creator arrives, the presence waits for it, as its text
	// would.
	db.IngestLogs([]sdklog.Record{presenceRecord(traceID.TraceID, trace.SpanID{9},
		otellog.String(telemetry.DagDigestAttr, outputDig))})
	db.ImportSnapshots([]SpanSnapshot{{
		ID:         creator,
		TraceID:    traceID,
		StartTime:  time.Unix(1, 0),
		EndTime:    time.Unix(2, 0),
		CallDigest: "xxh3:call",
		Output:     outputDig,
	}})
	if !db.Spans.Map[creator].HasLogs {
		t.Fatal("parked presence did not flag its creator")
	}
	if got := db.DrainResolvedLogs(creator); len(got) != 0 {
		t.Fatalf("parked presence resolved as text: %d records", len(got))
	}
	if span := db.Spans.Map[SpanID{SpanID: trace.SpanID{9}}]; span != nil && span.HasLogs {
		t.Fatal("presence flagged the emitting span instead of the creator")
	}
}

func TestPrimaryLogsOnDemand(t *testing.T) {
	db := NewDB()
	span := SpanID{SpanID: trace.SpanID{1}}
	db.PrimarySpan = span
	db.PrimaryLogsOnDemand = true
	renderable := db.IngestLogs([]sdklog.Record{newTestLogRecord(trace.TraceID{1}, span.SpanID, "out")})
	if len(renderable) != 1 {
		t.Fatalf("display text not renderable: %d", len(renderable))
	}
	if len(db.PrimaryLogs[span]) != 0 {
		t.Fatal("primary output buffered while read on demand")
	}
	if !db.Spans.Map[span].HasLogs {
		t.Fatal("display text did not flag its span")
	}
}
