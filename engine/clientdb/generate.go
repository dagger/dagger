package clientdb

import "context"

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.26.0 generate

// NOTE: this one is done manually since sqlc doesn't support json_each
const selectLinkedSpans = `-- name: SelectLinkedSpans :many
;

SELECT DISTINCT
  s.trace_id AS trace_id,
  s.span_id AS source_span_id,
  CAST(l.value->>'$.traceId' AS TEXT) AS target_trace_id,
  CAST(l.value->>'$.spanId' AS TEXT) AS target_span_id
FROM
  spans s,
  json_each(s.links) l
WHERE
  target_span_id = ?
`

type SelectLinkedSpansRow struct {
	TraceID       string
	SourceSpanID  string
	TargetTraceID string
	TargetSpanID  string
}

func (q *Queries) SelectLinkedSpans(ctx context.Context, spanID string) ([]SelectLinkedSpansRow, error) {
	rows, err := q.db.QueryContext(ctx, selectLinkedSpans, spanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SelectLinkedSpansRow
	for rows.Next() {
		var i SelectLinkedSpansRow
		if err := rows.Scan(
			&i.TraceID,
			&i.SourceSpanID,
			&i.TargetTraceID,
			&i.TargetSpanID,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
