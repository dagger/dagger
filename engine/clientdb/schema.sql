-- The following schema is derived from the OTLP specification, with an
-- autoincrementing ID so we can trivially maintain order of events without
-- worrying about nanosecond timestamp collisions.
--
-- Note that there will be duplicates for spans as they progress through
-- updates to completion. These tables are append-only.

CREATE TABLE IF NOT EXISTS spans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    trace_state TEXT NOT NULL,
    parent_span_id TEXT,
    flags INTEGER NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    start_time INTEGER NOT NULL, -- Nanoseconds from epoch
    end_time INTEGER, -- Nullable to support started spans
    attributes BLOB, -- JSON encoded []*otlpcommonv1.KeyValue
    dropped_attributes_count INTEGER NOT NULL,
    events BLOB, -- JSON encoded []*otlptracev1.Span_Event
    dropped_events_count INTEGER NOT NULL,
    links BLOB, -- JSON encoded []*otlptracev1.Span_Link
    dropped_links_count INTEGER NOT NULL,
    status_code INTEGER NOT NULL,
    status_message TEXT NOT NULL,
    instrumentation_scope BLOB, -- JSON encoded *otlpcommonv1.InstrumentationScope
    resource BLOB, -- JSON encoded *otlpresourcev1.Resource
    resource_schema_url TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id TEXT,
    span_id TEXT,
    timestamp INTEGER NOT NULL, -- Nanoseconds from epoch
    severity_number INTEGER NOT NULL,
    severity_text TEXT NOT NULL,
    body BLOB, -- *Protobuf* encoded otlpcommon.v1.Any
    attributes BLOB, -- JSON encoded []*otlpcommonv1.KeyValue
    instrumentation_scope BLOB, -- JSON encoded *otlpcommonv1.InstrumentationScope
    resource BLOB, -- JSON encoded *otlpresourcev1.Resource
    resource_schema_url TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    data BLOB -- JSON encoded otlpmetricsv1.ResourceMetrics
) STRICT;
