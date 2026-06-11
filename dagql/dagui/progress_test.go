package dagui

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func newTestProgressRecord(traceID trace.TraceID, spanID trace.SpanID, item string, current, total int64) sdklog.Record {
	return newTestLogRecord(traceID, spanID, "",
		otellog.String(telemetryattrs.ProgressItemAttr, item),
		otellog.Int64(telemetryattrs.ProgressCurrentAttr, current),
		otellog.Int64(telemetryattrs.ProgressTotalAttr, total),
		otellog.String(telemetryattrs.ProgressUnitAttr, "bytes"),
	)
}

func TestIngestProgressLogs(t *testing.T) {
	db := NewDB()

	traceID := TraceID{TraceID: trace.TraceID{1}}
	fromID := SpanID{SpanID: trace.SpanID{1}}
	pullID := SpanID{SpanID: trace.SpanID{2}}

	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:        fromID,
			TraceID:   traceID,
			Name:      "Container.from",
			StartTime: time.Unix(1, 0),
		},
		{
			ID:        pullID,
			TraceID:   traceID,
			ParentID:  fromID,
			Name:      "pulling docker.io/library/nginx",
			StartTime: time.Unix(1, 0),
		},
	})

	err := db.LogExporter().Export(context.Background(), []sdklog.Record{
		newTestProgressRecord(traceID.TraceID, pullID.SpanID, "sha256:layer1", 0, 100),
		newTestProgressRecord(traceID.TraceID, pullID.SpanID, "sha256:layer2", 25, 200),
		newTestProgressRecord(traceID.TraceID, pullID.SpanID, "sha256:layer1", 50, 100),
	})
	if err != nil {
		t.Fatalf("export: %s", err)
	}

	pull := db.Spans.Map[pullID]
	if pull.Progress == nil {
		t.Fatal("expected progress state on pulling span")
	}
	if len(pull.Progress.Order) != 2 {
		t.Fatalf("expected 2 progress items, got %d", len(pull.Progress.Order))
	}
	layer1 := pull.Progress.Order[0]
	if layer1.Name != "sha256:layer1" || layer1.Current != 50 || layer1.Total != 100 {
		t.Fatalf("unexpected first item state: %+v", layer1)
	}
	if layer1.Complete() {
		t.Fatal("layer1 should not be complete at 50/100")
	}
	if layer1.Unit != "bytes" {
		t.Fatalf("expected bytes unit, got %q", layer1.Unit)
	}
	current, total := pull.Progress.Totals()
	if current != 75 || total != 300 {
		t.Fatalf("unexpected totals: %d/%d", current, total)
	}

	// progress records are data, not log text
	if pull.HasLogs {
		t.Fatal("progress records should not mark the span as having logs")
	}

	// progress surfaces on ancestors for collapsed/hidden rendering
	from := db.Spans.Map[fromID]
	if _, ok := from.ProgressSpans.Map[pullID]; !ok {
		t.Fatal("expected pulling span in ancestor's ProgressSpans")
	}

	// completion converges
	err = db.LogExporter().Export(context.Background(), []sdklog.Record{
		newTestProgressRecord(traceID.TraceID, pullID.SpanID, "sha256:layer1", 100, 100),
		newTestProgressRecord(traceID.TraceID, pullID.SpanID, "sha256:layer2", 200, 200),
	})
	if err != nil {
		t.Fatalf("export: %s", err)
	}
	for _, item := range pull.Progress.Order {
		if !item.Complete() {
			t.Fatalf("expected %s to be complete, got %d/%d", item.Name, item.Current, item.Total)
		}
	}

	// ordinary logs still flow as text
	err = db.LogExporter().Export(context.Background(), []sdklog.Record{
		newTestLogRecord(traceID.TraceID, pullID.SpanID, "hello"),
	})
	if err != nil {
		t.Fatalf("export: %s", err)
	}
	if !pull.HasLogs {
		t.Fatal("expected ordinary log to mark the span as having logs")
	}
}

func TestIngestProgressLogsBeforeSpans(t *testing.T) {
	db := NewDB()

	traceID := TraceID{TraceID: trace.TraceID{2}}
	exportID := SpanID{SpanID: trace.SpanID{1}}
	downloadID := SpanID{SpanID: trace.SpanID{2}}

	// progress arrives before any span data: nothing to walk yet
	err := db.LogExporter().Export(context.Background(), []sdklog.Record{
		newTestProgressRecord(traceID.TraceID, downloadID.SpanID, "bytes", 50, 100),
	})
	if err != nil {
		t.Fatalf("export: %s", err)
	}

	// the progress-carrying span arrives, linking to a parent that hasn't
	// arrived yet either
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:        downloadID,
			TraceID:   traceID,
			ParentID:  exportID,
			Name:      "downloading /out",
			StartTime: time.Unix(1, 0),
		},
	})
	export := db.Spans.Map[exportID]
	if _, ok := export.ProgressSpans.Map[downloadID]; !ok {
		t.Fatal("expected late-arriving span's progress to register in its parent")
	}

	// the parent arrives last, linking further up; the registration must
	// propagate to the new ancestor too
	rootID := SpanID{SpanID: trace.SpanID{3}}
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:        exportID,
			TraceID:   traceID,
			ParentID:  rootID,
			Name:      "export directory",
			StartTime: time.Unix(1, 0),
		},
	})
	root := db.Spans.Map[rootID]
	if _, ok := root.ProgressSpans.Map[downloadID]; !ok {
		t.Fatal("expected progress registration to propagate to late-arriving ancestors")
	}
}
