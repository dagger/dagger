package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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

func TestUpsertSpansTreatsZeroParentAsRoot(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "odag.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	traceID := "dddddddddddddddddddddddddddddddd"
	rootID := "eeeeeeeeeeeeeeee"

	_, err = st.UpsertSpans(ctx, "collector", []SpanRecord{
		{
			TraceID:       traceID,
			SpanID:        rootID,
			ParentSpanID:  "0000000000000000",
			Name:          "Query.container",
			StartUnixNano: 10,
			EndUnixNano:   20,
			StatusCode:    "STATUS_CODE_OK",
			DataJSON:      `{"attributes":{"dagger.io/dag.digest":"root"}}`,
		},
	})
	if err != nil {
		t.Fatalf("upsert spans: %v", err)
	}

	var status string
	if err := st.db.QueryRowContext(ctx, `
SELECT status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(&status); err != nil {
		t.Fatalf("query trace status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("expected status=completed for zero-parent root, got %q", status)
	}
}

func TestReconcileTraceStatusesHardTimeoutClosesStaleIngesting(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "odag.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	traceID := "ffffffffffffffffffffffffffffffff"
	spanID := "abababababababab"

	_, err = st.UpsertSpans(ctx, "collector", []SpanRecord{
		{
			TraceID:       traceID,
			SpanID:        spanID,
			ParentSpanID:  "1111111111111111",
			Name:          "Container.from",
			StartUnixNano: 10,
			EndUnixNano:   0,
			StatusCode:    "STATUS_CODE_OK",
			DataJSON:      `{"attributes":{"dagger.io/dag.digest":"child"}}`,
		},
	})
	if err != nil {
		t.Fatalf("upsert spans: %v", err)
	}

	var status string
	if err := st.db.QueryRowContext(ctx, `
SELECT status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(&status); err != nil {
		t.Fatalf("query initial trace status: %v", err)
	}
	if status != "ingesting" {
		t.Fatalf("expected initial status=ingesting, got %q", status)
	}

	old := time.Now().Add(-25 * time.Hour).UnixNano()
	if _, err := st.db.ExecContext(ctx, `
UPDATE spans SET updated_unix_nano = ? WHERE trace_id = ?
`, old, traceID); err != nil {
		t.Fatalf("age span updates: %v", err)
	}

	if err := st.ReconcileTraceStatuses(ctx); err != nil {
		t.Fatalf("reconcile trace statuses: %v", err)
	}

	if err := st.db.QueryRowContext(ctx, `
SELECT status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(&status); err != nil {
		t.Fatalf("query reconciled trace status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("expected stale trace to be completed after reconcile, got %q", status)
	}
}

func TestRebuildDerivedRecomputesTraceSummariesFromSpans(t *testing.T) {
	t.Parallel()

	st, err := Open(filepath.Join(t.TempDir(), "odag.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	traceID := "rebuild-trace"
	rootID := "rebuild-root"
	childID := "rebuild-child"

	if _, err := st.UpsertSpans(ctx, "cloud", []SpanRecord{
		{
			TraceID:         traceID,
			SpanID:          rootID,
			Name:            "Query.container",
			StartUnixNano:   10,
			EndUnixNano:     20,
			StatusCode:      "STATUS_CODE_OK",
			DataJSON:        `{"attributes":{"dagger.io/dag.digest":"root"}}`,
			UpdatedUnixNano: 21,
		},
		{
			TraceID:         traceID,
			SpanID:          childID,
			ParentSpanID:    rootID,
			Name:            "Container.from",
			StartUnixNano:   12,
			EndUnixNano:     18,
			StatusCode:      "STATUS_CODE_OK",
			DataJSON:        `{"attributes":{"dagger.io/dag.digest":"child"}}`,
			UpdatedUnixNano: 19,
		},
	}); err != nil {
		t.Fatalf("upsert spans: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `
UPDATE traces
SET first_seen_unix_nano = 999,
    last_seen_unix_nano = 1000,
    span_count = 999,
    status = 'unknown'
WHERE trace_id = ?
`, traceID); err != nil {
		t.Fatalf("corrupt trace summary: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO traces (trace_id, source_mode, first_seen_unix_nano, last_seen_unix_nano, span_count, status)
VALUES (?, 'collector', 1, 1, 0, 'unknown')
`, "orphan-trace"); err != nil {
		t.Fatalf("insert orphan trace: %v", err)
	}

	summary, err := st.RebuildDerived(ctx)
	if err != nil {
		t.Fatalf("rebuild derived: %v", err)
	}
	if summary.Traces != 1 || summary.Spans != 2 {
		t.Fatalf("unexpected rebuild summary: %+v", summary)
	}

	var sourceMode string
	var firstSeen int64
	var lastSeen int64
	var spanCount int
	var status string
	if err := st.db.QueryRowContext(ctx, `
SELECT source_mode, first_seen_unix_nano, last_seen_unix_nano, span_count, status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(&sourceMode, &firstSeen, &lastSeen, &spanCount, &status); err != nil {
		t.Fatalf("query rebuilt trace: %v", err)
	}
	if sourceMode != "cloud" {
		t.Fatalf("expected source mode to be preserved, got %q", sourceMode)
	}
	if firstSeen != 10 || lastSeen != 20 {
		t.Fatalf("unexpected rebuilt bounds: first=%d last=%d", firstSeen, lastSeen)
	}
	if spanCount != 2 {
		t.Fatalf("expected rebuilt span_count=2, got %d", spanCount)
	}
	if status != "completed" {
		t.Fatalf("expected rebuilt status=completed, got %q", status)
	}

	var traceRows int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traces`).Scan(&traceRows); err != nil {
		t.Fatalf("count rebuilt traces: %v", err)
	}
	if traceRows != 1 {
		t.Fatalf("expected orphan derived rows to be removed, got %d traces", traceRows)
	}
}
