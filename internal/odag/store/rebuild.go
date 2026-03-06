package store

import (
	"context"
	"fmt"
	"time"
)

type RebuildSummary struct {
	Traces int
	Spans  int
}

func (s *Store) RebuildDerived(ctx context.Context) (RebuildSummary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RebuildSummary{}, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var summary RebuildSummary
	if err := tx.QueryRowContext(ctx, `
SELECT
  COUNT(DISTINCT trace_id),
  COUNT(*)
FROM spans
`).Scan(&summary.Traces, &summary.Spans); err != nil {
		return RebuildSummary{}, fmt.Errorf("count spans: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
CREATE TEMP TABLE trace_source_modes AS
SELECT trace_id, source_mode
FROM traces
`); err != nil {
		return RebuildSummary{}, fmt.Errorf("snapshot trace source modes: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM traces`); err != nil {
		return RebuildSummary{}, fmt.Errorf("delete derived traces: %w", err)
	}

	nowUnixNano := time.Now().UnixNano()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO traces (
  trace_id,
  source_mode,
  first_seen_unix_nano,
  last_seen_unix_nano,
  span_count,
  status
)
WITH span_summary AS (
  SELECT
    spans.trace_id AS trace_id,
    COALESCE(trace_source_modes.source_mode, 'collector') AS source_mode,
    MIN(CASE
      WHEN spans.start_unix_nano > 0 THEN spans.start_unix_nano
      WHEN spans.updated_unix_nano > 0 THEN spans.updated_unix_nano
      ELSE 0
    END) AS first_seen_unix_nano,
    MAX(CASE
      WHEN spans.end_unix_nano > 0 THEN spans.end_unix_nano
      ELSE spans.start_unix_nano
    END) AS last_seen_unix_nano,
    COUNT(*) AS span_count,
    MAX(spans.updated_unix_nano) AS max_updated_unix_nano,
    SUM(CASE
      WHEN (`+traceRootPredicateSQL+`) AND spans.end_unix_nano > 0 AND spans.status_code = 'STATUS_CODE_ERROR' THEN 1
      ELSE 0
    END) AS ended_root_errors,
    SUM(CASE
      WHEN (`+traceRootPredicateSQL+`) AND spans.end_unix_nano > 0 THEN 1
      ELSE 0
    END) AS ended_roots,
    SUM(CASE
      WHEN spans.end_unix_nano = 0 THEN 1
      ELSE 0
    END) AS open_spans,
    SUM(CASE
      WHEN spans.end_unix_nano > 0 THEN 1
      ELSE 0
    END) AS ended_spans,
    SUM(CASE
      WHEN spans.end_unix_nano > 0 AND spans.status_code = 'STATUS_CODE_ERROR' THEN 1
      ELSE 0
    END) AS ended_errors
  FROM spans
  LEFT JOIN trace_source_modes USING (trace_id)
  GROUP BY spans.trace_id, COALESCE(trace_source_modes.source_mode, 'collector')
)
SELECT
  trace_id,
  source_mode,
  first_seen_unix_nano,
  last_seen_unix_nano,
  span_count,
  CASE
    WHEN ended_root_errors > 0 THEN 'failed'
    WHEN ended_roots > 0 THEN 'completed'
    WHEN open_spans = 0 AND ended_spans > 0 AND (? - COALESCE(max_updated_unix_nano, 0)) >= ? THEN
      CASE
        WHEN ended_errors > 0 THEN 'failed'
        ELSE 'completed'
      END
    WHEN span_count > 0 AND (? - COALESCE(max_updated_unix_nano, 0)) >= ? THEN
      CASE
        WHEN ended_errors > 0 THEN 'failed'
        ELSE 'completed'
      END
    ELSE 'ingesting'
  END AS status
FROM span_summary
`, nowUnixNano, int64(traceStatusCloseGracePeriod), nowUnixNano, int64(traceStatusHardStaleTimeout)); err != nil {
		return RebuildSummary{}, fmt.Errorf("rebuild traces: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return RebuildSummary{}, fmt.Errorf("commit tx: %w", err)
	}
	committed = true

	return summary, nil
}
