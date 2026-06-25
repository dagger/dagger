package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/slog"
	"github.com/vito/go-sse/sse"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// GraphQL operations for streaming trace data from Dagger Cloud.
// These mirror the subscriptions used by the Cloud UI (dagger.io).

const spanPropsFragment = `
fragment SpanProps on Span {
	id
	traceId
	traceState
	name
	parentId
	kind
	timestamp
	endTime
	updateTime
	attributes
	status {
		message
		code
	}
	events {
		timestamp
		name
		attributes
	}
	links {
		traceId
		spanId
		traceState
		attributes
	}
	scope {
		name
		version
	}
	hasLogs
	childCount
	partial
}
`

const getSpanUpdatesOperation = `
subscription GetSpanUpdates ($orgID: ID!, $traceID: ID!, $root: Boolean! = true, $before: Time, $after: Time, $listen: [ID!]) {
	spansUpdated(org: $orgID, traceId: $traceID, root: $root, before: $before, after: $after, listen: $listen) {
		... SpanProps
	}
}
` + spanPropsFragment

// getSpanUpdatesIncrementalOperation adds the incremental argument, which forces
// the API to return only priority spans and listened subtrees regardless of
// trace size. It's a separate operation so we can fall back to the plain one
// against an API that predates the argument.
const getSpanUpdatesIncrementalOperation = `
subscription GetSpanUpdates ($orgID: ID!, $traceID: ID!, $root: Boolean! = true, $before: Time, $after: Time, $listen: [ID!], $incremental: Boolean! = false) {
	spansUpdated(org: $orgID, traceId: $traceID, root: $root, before: $before, after: $after, listen: $listen, incremental: $incremental) {
		... SpanProps
	}
}
` + spanPropsFragment

const getSpanLogsOperation = `
subscription GetSpanLogs ($orgID: ID!, $traceID: ID!, $spanID: ID!, $after: Time, $descendants: Boolean!) {
	logsEmitted(org: $orgID, traceId: $traceID, spanId: $spanID, descendants: $descendants, after: $after) {
		spanId
		timestamp
		body
		attributes
	}
}
`

// API response types matching the Cloud GraphQL schema.

type SpanData struct {
	ID         string         `json:"id"`
	TraceID    string         `json:"traceId"`
	TraceState string         `json:"traceState"`
	Name       string         `json:"name"`
	ParentID   *string        `json:"parentId"`
	Kind       string         `json:"kind"`
	Timestamp  time.Time      `json:"timestamp"`
	EndTime    *time.Time     `json:"endTime"`
	UpdateTime time.Time      `json:"updateTime"`
	Attributes map[string]any `json:"attributes"`
	Status     SpanStatus     `json:"status"`
	Events     []SpanEvent    `json:"events"`
	Links      []SpanLink     `json:"links"`
	Scope      SpanScope      `json:"scope"`
	HasLogs    bool           `json:"hasLogs"`
	ChildCount int            `json:"childCount"`
	Partial    bool           `json:"partial"`
}

type SpanStatus struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type SpanEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	Name       string         `json:"name"`
	Attributes map[string]any `json:"attributes"`
}

type SpanLink struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	TraceState string         `json:"traceState"`
	Attributes map[string]any `json:"attributes"`
}

type SpanScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type LogMessage struct {
	SpanID     *string        `json:"spanId"`
	Timestamp  time.Time      `json:"timestamp"`
	Body       string         `json:"body"`
	Attributes map[string]any `json:"attributes"`
}


// SSE GraphQL response envelope.

type graphqlSSEResponse[T any] struct {
	Data T `json:"data"`
}

type spansUpdatedResponse struct {
	SpansUpdated []SpanData `json:"spansUpdated"`
}

type logsEmittedResponse struct {
	LogsEmitted []LogMessage `json:"logsEmitted"`
}


// graphqlStatusError is returned when the GraphQL endpoint responds with a
// non-200 status (e.g. 422 for a validation error). It carries the body so
// callers can detect specific failures, such as an unsupported argument.
type graphqlStatusError struct {
	op     string
	status int
	body   string
}

func (e *graphqlStatusError) Error() string {
	return fmt.Sprintf("%s: %d: %s", e.op, e.status, e.body)
}

// graphqlErrorsError carries GraphQL errors delivered in-band over the SSE
// stream (a 200 response with a top-level "errors" array and null data).
type graphqlErrorsError struct {
	op   string
	body string
}

func (e *graphqlErrorsError) Error() string {
	return fmt.Sprintf("%s: %s", e.op, e.body)
}

// parseGraphQLErrors returns a graphqlErrorsError if the SSE payload carries
// top-level GraphQL errors, or nil if it's a normal data payload.
func parseGraphQLErrors(op string, data []byte) error {
	var probe struct {
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil
	}
	if len(probe.Errors) == 0 {
		return nil
	}
	return &graphqlErrorsError{op: op, body: string(data)}
}

// isUnsupportedArgError reports whether err is a GraphQL validation failure for
// an unknown argument, which is how an older API rejects the incremental
// argument -- whether returned as an HTTP 422 or in-band over the SSE stream.
func isUnsupportedArgError(err error) bool {
	var se *graphqlStatusError
	if errors.As(err, &se) {
		return se.status == http.StatusUnprocessableEntity &&
			strings.Contains(se.body, "incremental")
	}
	var ge *graphqlErrorsError
	if errors.As(err, &ge) {
		return strings.Contains(ge.body, "GRAPHQL_VALIDATION_FAILED") &&
			strings.Contains(ge.body, "incremental")
	}
	return false
}

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	OpName    string         `json:"operationName"`
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// SpanStreamOpts selects which spans the GetSpanUpdates subscription returns.
// It mirrors the variables the Cloud web UI drives (cloud/app_server.go):
//   - Root: stream the trace's priority spans (roots, revealed spans, checks,
//     tests) plus their ancestors. The server marks spans Partial when the tree
//     is incomplete, signalling that deeper spans must be fetched lazily.
//   - Listen: also stream the subtrees of these span IDs (used to fetch a span's
//     children on demand when the user expands it).
//   - After/Before: restrict updates to a time window, used to resubscribe for
//     live updates (After) or backfill historical children (Before).
type SpanStreamOpts struct {
	Root   bool
	Listen []string
	After  *time.Time
	Before *time.Time
	// Incremental forces priority-only + listened-subtree loading regardless of
	// trace size. Falls back to a full fetch against an API that predates the
	// argument.
	Incremental bool
}

// StreamSpans streams a trace's priority (root) spans. It's a convenience
// wrapper over StreamSpansWith and skips empty "caught up" batches.
func (c *Client) StreamSpans(
	ctx context.Context,
	orgID string,
	traceID string,
	handler func([]SpanData),
) error {
	return c.StreamSpansWith(ctx, orgID, traceID, SpanStreamOpts{Root: true}, func(spans []SpanData) {
		if len(spans) == 0 {
			return
		}
		handler(spans)
	})
}

// StreamSpansWith streams span data with explicit subscription options. Unlike
// StreamSpans it passes through empty batches: the server emits an empty batch
// once it has caught up to the trace's current state, which callers doing
// incremental loading use as a "done with this pass" signal.
func (c *Client) StreamSpansWith(
	ctx context.Context,
	orgID string,
	traceID string,
	opts SpanStreamOpts,
	handler func([]SpanData),
) error {
	vars := map[string]any{
		"orgID":   orgID,
		"traceID": traceID,
		"root":    opts.Root,
		"after":   opts.After,
		"before":  opts.Before,
		"listen":  opts.Listen,
	}
	cb := func(data []byte) error {
		var resp graphqlSSEResponse[spansUpdatedResponse]
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("unmarshal span updates: %w", err)
		}
		c.stats.addRecords("GetSpanUpdates", len(resp.Data.SpansUpdated))
		handler(resp.Data.SpansUpdated)
		return nil
	}

	query := getSpanUpdatesOperation
	if opts.Incremental {
		query = getSpanUpdatesIncrementalOperation
		vars["incremental"] = true
	}

	err := c.streamGraphQL(ctx, &graphqlRequest{
		OpName:    "GetSpanUpdates",
		Query:     query,
		Variables: vars,
	}, cb)

	// Fall back to a full (non-incremental) fetch against an API that predates
	// the incremental argument. The validation error arrives before any data, so
	// retrying can't double-deliver spans.
	if err != nil && opts.Incremental && isUnsupportedArgError(err) {
		slog.Warn("cloud API does not support incremental span loading; fetching full trace")
		delete(vars, "incremental")
		return c.streamGraphQL(ctx, &graphqlRequest{
			OpName:    "GetSpanUpdates",
			Query:     getSpanUpdatesOperation,
			Variables: vars,
		}, cb)
	}
	return err
}

// StreamLogs streams log messages for a span from Dagger Cloud's GraphQL API.
// When descendants is true, logs from the span's whole subtree are included;
// when false, only the span's own logs are returned.
func (c *Client) StreamLogs(
	ctx context.Context,
	orgID string,
	traceID string,
	spanID string,
	descendants bool,
	handler func([]LogMessage),
) error {
	return c.streamGraphQL(ctx, &graphqlRequest{
		OpName: "GetSpanLogs",
		Query:  getSpanLogsOperation,
		Variables: map[string]any{
			"orgID":       orgID,
			"traceID":     traceID,
			"spanID":      spanID,
			"descendants": descendants,
			"after":       nil,
		},
	}, func(data []byte) error {
		var resp graphqlSSEResponse[logsEmittedResponse]
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("unmarshal logs: %w", err)
		}
		logs := resp.Data.LogsEmitted
		if len(logs) == 0 {
			return nil
		}
		c.stats.addRecords("GetSpanLogs", len(logs))
		handler(logs)
		return nil
	})
}

// streamGraphQL connects to the Cloud GraphQL SSE endpoint and streams
// subscription results. It POSTs a GraphQL request and reads SSE events.
// Events with type "next" contain data; "complete" signals end of stream.
func (c *Client) streamGraphQL(ctx context.Context, gqlReq *graphqlRequest, cb func([]byte) error) error {
	body, err := json.Marshal(gqlReq)
	if err != nil {
		return fmt.Errorf("marshal graphql request: %w", err)
	}

	c.stats.addRequest(gqlReq.OpName)

	endpoint := c.u.JoinPath("/query").String()
	slog.Debug("connecting to cloud GraphQL SSE", "url", endpoint, "op", gqlReq.OpName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.h.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", gqlReq.OpName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return &graphqlStatusError{op: gqlReq.OpName, status: resp.StatusCode, body: string(respBody)}
	}

	slog.Debug("connected to cloud GraphQL SSE", "op", gqlReq.OpName)

	reader := sse.NewReadCloser(resp.Body)
	defer reader.Close()

	for {
		event, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				slog.Debug("cloud GraphQL SSE stream ended", "op", gqlReq.OpName, "err", err)
				return nil
			}
			return fmt.Errorf("read SSE event from %s: %w", gqlReq.OpName, err)
		}

		slog.Debug("received SSE event", "op", gqlReq.OpName, "type", event.Name, "dataLen", len(event.Data))

		switch event.Name {
		case "complete":
			slog.Debug("cloud GraphQL SSE stream completed", "op", gqlReq.OpName)
			return nil
		case "next":
			if len(event.Data) == 0 {
				continue
			}
			c.stats.addEvent(gqlReq.OpName, len(event.Data))
			// A subscription can deliver GraphQL errors (e.g. a validation error)
			// in-band as a 200 "next" event with a top-level "errors" array and
			// null data. Surface them instead of silently yielding no results.
			if gqlErr := parseGraphQLErrors(gqlReq.OpName, event.Data); gqlErr != nil {
				return gqlErr
			}
			if err := cb(event.Data); err != nil {
				slog.Warn("error processing SSE event", "op", gqlReq.OpName, "err", err)
			}
		default:
			// "connected" or other events; ignore
		}
	}
}

// SpansToPB converts Cloud API SpanData into OTLP ResourceSpans proto,
// suitable for feeding through telemetry.SpansFromPB and into a SpanExporter.
func SpansToPB(spans []SpanData) []*tracepb.ResourceSpans {
	if len(spans) == 0 {
		return nil
	}

	pbSpans := make([]*tracepb.Span, 0, len(spans))
	for i := range spans {
		pbSpans = append(pbSpans, spanDataToPB(&spans[i]))
	}

	return []*tracepb.ResourceSpans{
		{
			Resource: &resourcepb.Resource{},
			ScopeSpans: []*tracepb.ScopeSpans{
				{
					Spans: pbSpans,
				},
			},
		},
	}
}

func spanDataToPB(s *SpanData) *tracepb.Span {
	span := &tracepb.Span{
		TraceId:           hexToBytes(s.TraceID),
		SpanId:            hexToBytes(s.ID),
		TraceState:        s.TraceState,
		Name:              s.Name,
		Kind:              spanKindFromString(s.Kind),
		StartTimeUnixNano: uint64(s.Timestamp.UnixNano()),
		Status: &tracepb.Status{
			Code:    tracepb.Status_StatusCode(tracepb.Status_StatusCode_value[s.Status.Code]),
			Message: s.Status.Message,
		},
	}

	if s.ParentID != nil {
		span.ParentSpanId = hexToBytes(*s.ParentID)
	}
	if s.EndTime != nil {
		span.EndTimeUnixNano = uint64(s.EndTime.UnixNano())
	}

	// Convert attributes
	for k, v := range s.Attributes {
		span.Attributes = append(span.Attributes, &commonpb.KeyValue{
			Key:   k,
			Value: anyToOTLPValue(v),
		})
	}

	// Convert events
	for _, e := range s.Events {
		event := &tracepb.Span_Event{
			TimeUnixNano: uint64(e.Timestamp.UnixNano()),
			Name:         e.Name,
		}
		for k, v := range e.Attributes {
			event.Attributes = append(event.Attributes, &commonpb.KeyValue{
				Key:   k,
				Value: anyToOTLPValue(v),
			})
		}
		span.Events = append(span.Events, event)
	}

	// Convert links
	for _, l := range s.Links {
		link := &tracepb.Span_Link{
			TraceId:    hexToBytes(l.TraceID),
			SpanId:     hexToBytes(l.SpanID),
			TraceState: l.TraceState,
		}
		for k, v := range l.Attributes {
			link.Attributes = append(link.Attributes, &commonpb.KeyValue{
				Key:   k,
				Value: anyToOTLPValue(v),
			})
		}
		span.Links = append(span.Links, link)
	}

	return span
}

func anyToOTLPValue(v any) *commonpb.AnyValue {
	switch val := v.(type) {
	case string:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: val}}
	case bool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: val}}
	case float64:
		// JSON numbers decode as float64
		if val == float64(int64(val)) {
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(val)}}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: val}}
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: i}}
		}
		if f, err := val.Float64(); err == nil {
			return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: f}}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: val.String()}}
	case []any:
		values := make([]*commonpb.AnyValue, len(val))
		for i, elem := range val {
			values[i] = anyToOTLPValue(elem)
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{
			ArrayValue: &commonpb.ArrayValue{Values: values},
		}}
	default:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("%v", v)}}
	}
}

func hexToBytes(hex string) []byte {
	if hex == "" {
		return nil
	}
	b := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		b[i/2] = hexByte(hex[i])<<4 | hexByte(hex[i+1])
	}
	return b
}

func hexByte(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0
	}
}

func spanKindFromString(kind string) tracepb.Span_SpanKind {
	switch kind {
	case "SPAN_KIND_INTERNAL":
		return tracepb.Span_SPAN_KIND_INTERNAL
	case "SPAN_KIND_SERVER":
		return tracepb.Span_SPAN_KIND_SERVER
	case "SPAN_KIND_CLIENT":
		return tracepb.Span_SPAN_KIND_CLIENT
	case "SPAN_KIND_PRODUCER":
		return tracepb.Span_SPAN_KIND_PRODUCER
	case "SPAN_KIND_CONSUMER":
		return tracepb.Span_SPAN_KIND_CONSUMER
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

// LogMessagesToRecords converts Cloud API LogMessage values into OTel SDK
// log records suitable for feeding into a LogExporter.
func LogMessagesToRecords(traceID string, msgs []LogMessage) []sdklog.Record {
	tid, _ := trace.TraceIDFromHex(traceID)
	records := make([]sdklog.Record, 0, len(msgs))
	for _, msg := range msgs {
		var rec sdklog.Record
		rec.SetTimestamp(msg.Timestamp)
		rec.SetBody(log.StringValue(msg.Body))
		rec.SetTraceID(tid)
		if msg.SpanID != nil {
			sid, _ := trace.SpanIDFromHex(*msg.SpanID)
			rec.SetSpanID(sid)
		}
		for k, v := range msg.Attributes {
			rec.AddAttributes(log.KeyValue{
				Key:   k,
				Value: anyToLogValue(v),
			})
		}
		records = append(records, rec)
	}
	return records
}

func anyToLogValue(v any) log.Value {
	switch val := v.(type) {
	case string:
		return log.StringValue(val)
	case bool:
		return log.BoolValue(val)
	case float64:
		if val == float64(int64(val)) {
			return log.Int64Value(int64(val))
		}
		return log.Float64Value(val)
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return log.Int64Value(i)
		}
		if f, err := val.Float64(); err == nil {
			return log.Float64Value(f)
		}
		return log.StringValue(val.String())
	case []any:
		values := make([]log.Value, len(val))
		for i, elem := range val {
			values[i] = anyToLogValue(elem)
		}
		return log.SliceValue(values...)
	default:
		return log.StringValue(fmt.Sprintf("%v", v))
	}
}
