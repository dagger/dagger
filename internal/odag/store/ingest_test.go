package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUpsertSpansAndTraceSummary(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "odag.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	traceID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rootID := "bbbbbbbbbbbbbbbb"
	childID := "cccccccccccccccc"

	firstBatch := []SpanRecord{
		{
			TraceID:         traceID,
			SpanID:          rootID,
			Name:            "Query.demo",
			StartUnixNano:   10,
			EndUnixNano:     30,
			StatusCode:      "STATUS_CODE_OK",
			StatusMessage:   "",
			DataJSON:        `{"attributes":{"dagger.io/dag.digest":"root"}}`,
			UpdatedUnixNano: 31,
		},
		{
			TraceID:         traceID,
			SpanID:          childID,
			ParentSpanID:    rootID,
			Name:            "Container.from",
			StartUnixNano:   12,
			EndUnixNano:     22,
			StatusCode:      "STATUS_CODE_OK",
			StatusMessage:   "",
			DataJSON:        `{"attributes":{"dagger.io/dag.digest":"child"}}`,
			UpdatedUnixNano: 32,
		},
	}

	summary, err := st.UpsertSpans(ctx, "collector", firstBatch)
	if err != nil {
		t.Fatalf("upsert first batch: %v", err)
	}
	if summary.Traces != 1 || summary.Spans != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	var spanCount int
	var status string
	if err := st.db.QueryRowContext(ctx, `
SELECT span_count, status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(&spanCount, &status); err != nil {
		t.Fatalf("query trace summary: %v", err)
	}
	if spanCount != 2 {
		t.Fatalf("expected span_count=2, got %d", spanCount)
	}
	if status != "completed" {
		t.Fatalf("expected status=completed, got %q", status)
	}

	secondBatch := []SpanRecord{
		{
			TraceID:         traceID,
			SpanID:          rootID,
			Name:            "Query.demo",
			StartUnixNano:   10,
			EndUnixNano:     35,
			StatusCode:      "STATUS_CODE_ERROR",
			StatusMessage:   "boom",
			DataJSON:        `{"attributes":{"dagger.io/dag.digest":"root"}}`,
			UpdatedUnixNano: 36,
		},
	}

	summary, err = st.UpsertSpans(ctx, "collector", secondBatch)
	if err != nil {
		t.Fatalf("upsert second batch: %v", err)
	}
	if summary.Traces != 1 || summary.Spans != 1 {
		t.Fatalf("unexpected second summary: %+v", summary)
	}

	if err := st.db.QueryRowContext(ctx, `
SELECT span_count, status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(&spanCount, &status); err != nil {
		t.Fatalf("query trace summary after update: %v", err)
	}
	if spanCount != 2 {
		t.Fatalf("expected span_count to remain 2, got %d", spanCount)
	}
	if status != "failed" {
		t.Fatalf("expected status=failed, got %q", status)
	}

	var rootStatus string
	var rootMessage string
	if err := st.db.QueryRowContext(ctx, `
SELECT status_code, status_message
FROM spans
WHERE trace_id = ? AND span_id = ?
`, traceID, rootID).Scan(&rootStatus, &rootMessage); err != nil {
		t.Fatalf("query root span: %v", err)
	}
	if rootStatus != "STATUS_CODE_ERROR" {
		t.Fatalf("expected root status to be updated, got %q", rootStatus)
	}
	if rootMessage != "boom" {
		t.Fatalf("expected root status message to be updated, got %q", rootMessage)
	}
}
