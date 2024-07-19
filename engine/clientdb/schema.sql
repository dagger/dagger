-- The following schema is derived from the OTLP specification, with an
-- autoincrementing ID so we can trivially maintain order of events without
-- worrying about nanosecond timestamp collisions.
--
-- All JSONB fields are the JSON representation of the corresponding OTLP
-- protobuf type.
--
-- Note that there will be duplicates for spans as they progress through
-- updates to completion. These tables are append-only.

CREATE TABLE spans (
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
    attributes JSONB, -- JSONB to store span attributes
    dropped_attributes_count INTEGER NOT NULL,
    events JSONB, -- JSONB to store span events
    dropped_events_count INTEGER NOT NULL,
    links JSONB, -- JSONB to store span links
    dropped_links_count INTEGER NOT NULL,
    status_code INTEGER NOT NULL,
    status_message TEXT NOT NULL,
    instrumentation_scope JSONB, -- JSONB to store instrumentation scope
    resource JSONB -- JSONB to store resource attributes
);

CREATE TABLE logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id TEXT,
    span_id TEXT,
    timestamp INTEGER NOT NULL, -- Nanoseconds from epoch
    severity INTEGER NOT NULL,
    body JSONB, -- JSONB encoded otlpcommon.v1.Any
    attributes JSONB -- JSONB encoded otlpcommon.v1.Key
);

CREATE TABLE metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    unit TEXT,
    type TEXT,
    timestamp INTEGER NOT NULL, -- Nanoseconds from epoch
    data_points JSONB -- JSONB to store metric data points
);
