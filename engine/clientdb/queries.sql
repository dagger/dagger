-- name: InsertSpan :one
INSERT INTO spans (
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
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
) RETURNING id;

-- name: InsertLog :one
INSERT INTO logs (
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
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
) RETURNING id;

-- name: InsertMetric :one
INSERT INTO metrics (
    data
) VALUES (
    ?
) RETURNING id;

-- name: SelectSpansSince :many
SELECT * FROM spans WHERE id > ? ORDER BY id ASC LIMIT ?;

-- name: SelectLogsSince :many
SELECT * FROM logs WHERE id > ? ORDER BY id ASC LIMIT ?;

-- name: SelectMetricsSince :many
SELECT * FROM metrics WHERE id > ? ORDER BY id ASC LIMIT ?;
