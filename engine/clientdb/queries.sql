-- name: InsertSpan :one
INSERT INTO
  spans (
    trace_id,
    span_id,
    trace_state,
    parent_span_id,
    flags,
    name,
    kind,
    start_time,
    end_time,
    attributes,
    dropped_attributes_count,
    events,
    dropped_events_count,
    links,
    dropped_links_count,
    status_code,
    status_message,
    instrumentation_scope,
    resource,
    resource_schema_url
  )
VALUES
  (
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?,
    ?
  ) RETURNING id;

-- name: InsertLog :one
INSERT INTO
  logs (
    trace_id,
    span_id,
    timestamp,
    severity_number,
    severity_text,
    body,
    attributes,
    instrumentation_scope,
    resource,
    resource_schema_url
  )
VALUES
  (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id;

-- name: InsertMetric :one
INSERT INTO
  metrics (data)
VALUES
  (?) RETURNING id;

-- name: SelectSpansSince :many
SELECT
  *
FROM
  spans
WHERE
  id > ?
ORDER BY
  id ASC
LIMIT
  ?;

-- name: SelectLogsSince :many
SELECT
  *
FROM
  logs
WHERE
  id > ?
ORDER BY
  id ASC
LIMIT
  ?;

-- name: SelectMetricsSince :many
SELECT
  *
FROM
  metrics
WHERE
  id > ?
ORDER BY
  id ASC
LIMIT
  ?;

-- name: SelectLogsTimespan :many
SELECT
  *
FROM
  logs
WHERE
  timestamp > @start
  AND timestamp <= @end
ORDER BY
  timestamp ASC
LIMIT
  ?;

-- name: SelectSpan :one
SELECT
  *
FROM
  spans
WHERE
  trace_id = ?
  AND span_id = ?;

-- name: SelectLogsBeneathSpan :many
WITH RECURSIVE descendant_spans AS (
  SELECT s.span_id
  FROM spans s
  WHERE s.parent_span_id = @span_id
  UNION ALL
  SELECT DISTINCT s.span_id
  FROM spans s
  INNER JOIN descendant_spans ds ON s.parent_span_id = ds.span_id
)
SELECT
  *
FROM
  logs l
WHERE
  l.span_id IN (SELECT span_id FROM descendant_spans)
AND
  l.id > ?
ORDER BY
  l.id ASC
LIMIT
  ?;
;
