package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestSkippedModuleSpans(t *testing.T) {
	db := NewDB()
	if db.HasGenerateReport() {
		t.Fatal("empty db reports a generate report")
	}

	start := time.Unix(1, 0)
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:        SpanID{SpanID: trace.SpanID{1}},
			TraceID:   TraceID{TraceID: trace.TraceID{1}},
			Name:      "generate",
			StartTime: start,
			EndTime:   start.Add(time.Second),
		},
		{
			ID:              SpanID{SpanID: trace.SpanID{2}},
			TraceID:         TraceID{TraceID: trace.TraceID{1}},
			Name:            "bad",
			ParentID:        SpanID{SpanID: trace.SpanID{1}},
			StartTime:       start,
			EndTime:         start.Add(time.Second),
			GenerateSkipped: true,
			Status:          sdktrace.Status{Code: codes.Error, Description: "boom"},
		},
	})

	if !db.HasGenerateReport() {
		t.Fatal("HasGenerateReport = false, want true")
	}
	spans := db.SkippedModuleSpans()
	if len(spans) != 1 || spans[0].Name != "bad" {
		t.Fatalf("SkippedModuleSpans = %v, want one span named bad", spans)
	}
}
