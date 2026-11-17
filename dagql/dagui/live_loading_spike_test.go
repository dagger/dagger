package dagui

import (
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

// Spike for priority-first live loading (not a feature test): what does the
// DB keep for spans it never receives, when their log-channel metadata still
// reaches it? Today the display-free log push carries presence, progress,
// span-name and call-payload records for every span; a live view that loads
// only some spans would still pay for the rest through these.
func TestSpikeLogMetadataForUnloadedSpans(t *testing.T) {
	db := NewDB()
	traceID := trace.TraceID{1}
	const unloaded = 1000
	var records []sdklog.Record
	for i := range unloaded {
		span := trace.SpanID{byte(i >> 8), byte(i), 1}
		records = append(records,
			// has display text
			presenceRecord(traceID, span),
			// was renamed while live
			newTestLogRecord(traceID, span, "renamed",
				otellog.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)),
		)
		// a digest-routed presence (service stdio) whose creator never
		// arrives
		records = append(records, presenceRecord(traceID, trace.SpanID{byte(i >> 8), byte(i), 2},
			otellog.String(telemetry.DagDigestAttr, "xxh3:unloaded-output-"+string(rune('a'+i%26))+string(rune(i)))))
	}
	db.IngestLogs(records)

	var placeholders int
	for _, span := range db.Spans.Order {
		if !span.Received {
			placeholders++
		}
	}
	// Every unloaded span with records gets a placeholder Span (~1 KiB
	// each), and digest-routed records park until a creator arrives that
	// never will.
	if placeholders != unloaded {
		t.Fatalf("placeholders: got %d, want %d", placeholders, unloaded)
	}
	var parked int
	for _, recs := range db.pendingLogsByOutput {
		parked += len(recs)
	}
	if parked != unloaded {
		t.Fatalf("parked digest-routed records: got %d, want %d", parked, unloaded)
	}
	t.Logf("%d unloaded spans' metadata left %d placeholder spans and %d parked records", unloaded, placeholders, parked)
}
