package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type TraceRecord struct {
	TraceID           string    `json:"traceID"`
	SourceMode        string    `json:"sourceMode"`
	FirstSeenUnixNano int64     `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64     `json:"lastSeenUnixNano"`
	SpanCount         int       `json:"spanCount"`
	Status            string    `json:"status"`
	FirstSeen         time.Time `json:"firstSeen"`
	LastSeen          time.Time `json:"lastSeen"`
}

func (s *Store) ListTraces(ctx context.Context, limit int) ([]TraceRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if err := s.ReconcileTraceStatuses(ctx); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
  trace_id,
  source_mode,
  first_seen_unix_nano,
  last_seen_unix_nano,
  span_count,
  status
FROM traces
ORDER BY last_seen_unix_nano DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	var traces []TraceRecord
	for rows.Next() {
		var rec TraceRecord
		if err := rows.Scan(
			&rec.TraceID,
			&rec.SourceMode,
			&rec.FirstSeenUnixNano,
			&rec.LastSeenUnixNano,
			&rec.SpanCount,
			&rec.Status,
		); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		rec.FirstSeen = time.Unix(0, rec.FirstSeenUnixNano)
		rec.LastSeen = time.Unix(0, rec.LastSeenUnixNano)
		traces = append(traces, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traces: %w", err)
	}

	return traces, nil
}

func (s *Store) GetTrace(ctx context.Context, traceID string) (TraceRecord, error) {
	if err := s.ReconcileTraceStatuses(ctx); err != nil {
		return TraceRecord{}, err
	}
	var rec TraceRecord
	err := s.db.QueryRowContext(ctx, `
SELECT
  trace_id,
  source_mode,
  first_seen_unix_nano,
  last_seen_unix_nano,
  span_count,
  status
FROM traces
WHERE trace_id = ?
`, traceID).Scan(
		&rec.TraceID,
		&rec.SourceMode,
		&rec.FirstSeenUnixNano,
		&rec.LastSeenUnixNano,
		&rec.SpanCount,
		&rec.Status,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return TraceRecord{}, ErrNotFound
		}
		return TraceRecord{}, fmt.Errorf("query trace %s: %w", traceID, err)
	}
	rec.FirstSeen = time.Unix(0, rec.FirstSeenUnixNano)
	rec.LastSeen = time.Unix(0, rec.LastSeenUnixNano)
	return rec, nil
}

func (s *Store) ListTraceSpans(ctx context.Context, traceID string) ([]SpanRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  trace_id,
  span_id,
  COALESCE(parent_span_id, ''),
  name,
  start_unix_nano,
  end_unix_nano,
  status_code,
  status_message,
  data_json,
  updated_unix_nano
FROM spans
WHERE trace_id = ?
ORDER BY end_unix_nano ASC, start_unix_nano ASC, span_id ASC
`, traceID)
	if err != nil {
		return nil, fmt.Errorf("query spans for trace %s: %w", traceID, err)
	}
	defer rows.Close()

	var spans []SpanRecord
	for rows.Next() {
		var span SpanRecord
		if err := rows.Scan(
			&span.TraceID,
			&span.SpanID,
			&span.ParentSpanID,
			&span.Name,
			&span.StartUnixNano,
			&span.EndUnixNano,
			&span.StatusCode,
			&span.StatusMessage,
			&span.DataJSON,
			&span.UpdatedUnixNano,
		); err != nil {
			return nil, fmt.Errorf("scan span: %w", err)
		}
		spans = append(spans, span)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate spans: %w", err)
	}

	return spans, nil
}

func (s *Store) ListTraceIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT trace_id
FROM traces
ORDER BY last_seen_unix_nano DESC, trace_id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("query trace ids: %w", err)
	}
	defer rows.Close()

	traceIDs := make([]string, 0)
	for rows.Next() {
		var traceID string
		if err := rows.Scan(&traceID); err != nil {
			return nil, fmt.Errorf("scan trace id: %w", err)
		}
		traceIDs = append(traceIDs, traceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trace ids: %w", err)
	}

	return traceIDs, nil
}
