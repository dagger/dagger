package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SpanRecord struct {
	TraceID         string
	SpanID          string
	ParentSpanID    string
	Name            string
	StartUnixNano   int64
	EndUnixNano     int64
	StatusCode      string
	StatusMessage   string
	DataJSON        string
	UpdatedUnixNano int64
}

type IngestSummary struct {
	Traces int
	Spans  int
}

const (
	traceStatusCloseGracePeriod = 30 * time.Second
	traceStatusHardStaleTimeout = 24 * time.Hour
)

func (s *Store) UpsertSpans(ctx context.Context, sourceMode string, spans []SpanRecord) (IngestSummary, error) {
	if len(spans) == 0 {
		return IngestSummary{}, nil
	}
	if sourceMode == "" {
		sourceMode = "collector"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	traceStmt, err := tx.PrepareContext(ctx, `
INSERT INTO traces (trace_id, source_mode, first_seen_unix_nano, last_seen_unix_nano, span_count, status)
VALUES (?, ?, ?, ?, 0, 'ingesting')
ON CONFLICT(trace_id) DO UPDATE SET
  source_mode = excluded.source_mode,
  first_seen_unix_nano = MIN(traces.first_seen_unix_nano, excluded.first_seen_unix_nano),
  last_seen_unix_nano = MAX(traces.last_seen_unix_nano, excluded.last_seen_unix_nano)
`)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("prepare trace upsert: %w", err)
	}
	defer traceStmt.Close()

	spanStmt, err := tx.PrepareContext(ctx, `
INSERT INTO spans (
  trace_id, span_id, parent_span_id, name,
  start_unix_nano, end_unix_nano,
  status_code, status_message,
  updated_unix_nano, data_json
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(trace_id, span_id) DO UPDATE SET
  parent_span_id = excluded.parent_span_id,
  name = excluded.name,
  start_unix_nano = excluded.start_unix_nano,
  end_unix_nano = excluded.end_unix_nano,
  status_code = excluded.status_code,
  status_message = excluded.status_message,
  updated_unix_nano = excluded.updated_unix_nano,
  data_json = excluded.data_json
`)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("prepare span upsert: %w", err)
	}
	defer spanStmt.Close()

	touched := make(map[string]struct{}, len(spans))
	now := time.Now().UnixNano()
	for _, span := range spans {
		if span.TraceID == "" {
			return IngestSummary{}, fmt.Errorf("span has empty trace id")
		}
		if span.SpanID == "" {
			return IngestSummary{}, fmt.Errorf("span has empty span id (trace=%s)", span.TraceID)
		}

		firstSeen := span.StartUnixNano
		if firstSeen == 0 {
			firstSeen = now
		}
		lastSeen := span.EndUnixNano
		if lastSeen == 0 {
			lastSeen = span.StartUnixNano
		}
		if lastSeen == 0 {
			lastSeen = now
		}

		if _, err := traceStmt.ExecContext(ctx,
			span.TraceID,
			sourceMode,
			firstSeen,
			lastSeen,
		); err != nil {
			return IngestSummary{}, fmt.Errorf("upsert trace %s: %w", span.TraceID, err)
		}

		updatedUnixNano := span.UpdatedUnixNano
		if updatedUnixNano == 0 {
			updatedUnixNano = now
		}

		if _, err := spanStmt.ExecContext(ctx,
			span.TraceID,
			span.SpanID,
			nullString(span.ParentSpanID),
			span.Name,
			span.StartUnixNano,
			span.EndUnixNano,
			span.StatusCode,
			span.StatusMessage,
			updatedUnixNano,
			span.DataJSON,
		); err != nil {
			return IngestSummary{}, fmt.Errorf("upsert span %s/%s: %w", span.TraceID, span.SpanID, err)
		}

		touched[span.TraceID] = struct{}{}
	}

	nowUnixNano := time.Now().UnixNano()
	const summarizeTraceSQL = `
UPDATE traces
SET
  first_seen_unix_nano = COALESCE((SELECT MIN(start_unix_nano) FROM spans WHERE trace_id = ?), first_seen_unix_nano),
  last_seen_unix_nano = COALESCE((SELECT MAX(CASE WHEN end_unix_nano > 0 THEN end_unix_nano ELSE start_unix_nano END) FROM spans WHERE trace_id = ?), last_seen_unix_nano),
  span_count = COALESCE((SELECT COUNT(*) FROM spans WHERE trace_id = ?), span_count),
  status = CASE
    WHEN EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = ?
        AND (` + traceRootPredicateSQL + `)
        AND end_unix_nano > 0
        AND status_code = 'STATUS_CODE_ERROR'
    ) THEN 'failed'
    WHEN EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = ?
        AND (` + traceRootPredicateSQL + `)
        AND end_unix_nano > 0
    ) THEN 'completed'
    WHEN NOT EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = ?
        AND end_unix_nano = 0
    ) AND EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = ?
        AND end_unix_nano > 0
    ) AND (? - COALESCE((SELECT MAX(updated_unix_nano) FROM spans WHERE trace_id = ?), 0)) >= ? THEN
      CASE
        WHEN EXISTS(
          SELECT 1 FROM spans
          WHERE trace_id = ?
            AND end_unix_nano > 0
            AND status_code = 'STATUS_CODE_ERROR'
        ) THEN 'failed'
        ELSE 'completed'
      END
    WHEN EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = ?
    ) AND (? - COALESCE((SELECT MAX(updated_unix_nano) FROM spans WHERE trace_id = ?), 0)) >= ? THEN
      CASE
        WHEN EXISTS(
          SELECT 1 FROM spans
          WHERE trace_id = ?
            AND end_unix_nano > 0
            AND status_code = 'STATUS_CODE_ERROR'
        ) THEN 'failed'
        ELSE 'completed'
      END
    ELSE 'ingesting'
  END
WHERE trace_id = ?
`
	for traceID := range touched {
		if _, err := tx.ExecContext(ctx, summarizeTraceSQL,
			traceID, traceID, traceID, traceID, traceID,
			traceID, traceID, nowUnixNano, traceID, int64(traceStatusCloseGracePeriod), traceID,
			traceID, nowUnixNano, traceID, int64(traceStatusHardStaleTimeout), traceID,
			traceID,
		); err != nil {
			return IngestSummary{}, fmt.Errorf("summarize trace %s: %w", traceID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return IngestSummary{}, fmt.Errorf("commit tx: %w", err)
	}
	committed = true

	return IngestSummary{
		Traces: len(touched),
		Spans:  len(spans),
	}, nil
}

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{
		String: v,
		Valid:  true,
	}
}

const traceRootPredicateSQL = "parent_span_id IS NULL OR parent_span_id = '' OR TRIM(parent_span_id, '0') = ''"

func NormalizeParentSpanID(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	if strings.Trim(v, "0") == "" {
		return ""
	}
	return v
}

func (s *Store) ReconcileTraceStatuses(ctx context.Context) error {
	nowUnixNano := time.Now().UnixNano()
	const reconcileSQL = `
UPDATE traces
SET
  status = CASE
    WHEN EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = traces.trace_id
        AND (` + traceRootPredicateSQL + `)
        AND end_unix_nano > 0
        AND status_code = 'STATUS_CODE_ERROR'
    ) THEN 'failed'
    WHEN EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = traces.trace_id
        AND (` + traceRootPredicateSQL + `)
        AND end_unix_nano > 0
    ) THEN 'completed'
    WHEN NOT EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = traces.trace_id
        AND end_unix_nano = 0
    ) AND EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = traces.trace_id
        AND end_unix_nano > 0
    ) AND (? - COALESCE((SELECT MAX(updated_unix_nano) FROM spans WHERE trace_id = traces.trace_id), 0)) >= ? THEN
      CASE
        WHEN EXISTS(
          SELECT 1 FROM spans
          WHERE trace_id = traces.trace_id
            AND end_unix_nano > 0
            AND status_code = 'STATUS_CODE_ERROR'
        ) THEN 'failed'
        ELSE 'completed'
      END
    WHEN EXISTS(
      SELECT 1 FROM spans
      WHERE trace_id = traces.trace_id
    ) AND (? - COALESCE((SELECT MAX(updated_unix_nano) FROM spans WHERE trace_id = traces.trace_id), 0)) >= ? THEN
      CASE
        WHEN EXISTS(
          SELECT 1 FROM spans
          WHERE trace_id = traces.trace_id
            AND end_unix_nano > 0
            AND status_code = 'STATUS_CODE_ERROR'
        ) THEN 'failed'
        ELSE 'completed'
      END
    ELSE 'ingesting'
  END
WHERE status = 'ingesting' OR status = 'unknown'
`
	if _, err := s.db.ExecContext(ctx, reconcileSQL,
		nowUnixNano, int64(traceStatusCloseGracePeriod),
		nowUnixNano, int64(traceStatusHardStaleTimeout),
	); err != nil {
		return fmt.Errorf("reconcile trace statuses: %w", err)
	}
	return nil
}
