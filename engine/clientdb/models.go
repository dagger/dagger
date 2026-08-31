package clientdb

import "database/sql"

type Log struct {
	ID                   int64
	TraceID              sql.NullString
	SpanID               sql.NullString
	Timestamp            int64
	SeverityNumber       int64
	SeverityText         string
	Body                 []byte
	Attributes           []byte
	InstrumentationScope []byte
	Resource             []byte
	ResourceSchemaURL    string
}

type Metric struct {
	ID   int64
	Data []byte
}

type Span struct {
	ID                     int64
	TraceID                string
	SpanID                 string
	TraceState             string
	ParentSpanID           sql.NullString
	Flags                  int64
	Name                   string
	Kind                   string
	StartTime              int64
	EndTime                sql.NullInt64
	Attributes             []byte
	DroppedAttributesCount int64
	Events                 []byte
	DroppedEventsCount     int64
	Links                  []byte
	DroppedLinksCount      int64
	StatusCode             int64
	StatusMessage          string
	InstrumentationScope   []byte
	Resource               []byte
	ResourceSchemaURL      string
}

type SelectLogsBeneathSpanParams struct {
	SpanID sql.NullString
	ID     int64
	Limit  int64
}

type SelectLogsSinceParams struct {
	ID    int64
	Limit int64
}

type SelectMetricsSinceParams struct {
	ID    int64
	Limit int64
}

type SelectSpanParams struct {
	TraceID string
	SpanID  string
}

type SelectSpansSinceParams struct {
	ID    int64
	Limit int64
}

// HighWater is a fixed inclusive cursor for each telemetry stream.
// A zero cursor denotes an empty stream.
type HighWater struct {
	Spans   int64
	Logs    int64
	Metrics int64
}

type SelectSpansRangeParams struct {
	AfterID   int64
	ThroughID int64
	Limit     int64
}

type SelectLogsRangeParams struct {
	AfterID   int64
	ThroughID int64
	Limit     int64
}

type SelectMetricsRangeParams struct {
	AfterID   int64
	ThroughID int64
	Limit     int64
}
