package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

const getSpanUpdatesOperation = `
subscription GetSpanUpdates ($orgID: ID!, $traceID: ID!, $root: Boolean! = true, $before: Time, $after: Time, $listen: [ID!]) {
	spansUpdated(org: $orgID, traceId: $traceID, root: $root, before: $before, after: $after, listen: $listen) {
		... SpanProps
	}
}
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

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	OpName    string         `json:"operationName"`
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// StreamSpans streams span data for a trace from Dagger Cloud's GraphQL API.
// It calls the handler for each batch of spans received.
func (c *Client) StreamSpans(
	ctx context.Context,
	orgID string,
	traceID string,
	handler func([]SpanData),
) error {
	return c.streamGraphQL(ctx, &graphqlRequest{
		OpName: "GetSpanUpdates",
		Query:  getSpanUpdatesOperation,
		Variables: map[string]any{
			"orgID":   orgID,
			"traceID": traceID,
			"root":    true,
			"after":   nil,
			"before":  nil,
			"listen":  nil,
		},
	}, func(data []byte) error {
		var resp graphqlSSEResponse[spansUpdatedResponse]
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("unmarshal span updates: %w", err)
		}
		spans := resp.Data.SpansUpdated
		if len(spans) == 0 {
			return nil
		}
		handler(spans)
		return nil
	})
}

// StreamLogs streams log messages for a trace from Dagger Cloud's GraphQL API.
func (c *Client) StreamLogs(
	ctx context.Context,
	orgID string,
	traceID string,
	spanID string,
	handler func([]LogMessage),
) error {
	return c.streamGraphQL(ctx, &graphqlRequest{
		OpName: "GetSpanLogs",
		Query:  getSpanLogsOperation,
		Variables: map[string]any{
			"orgID":       orgID,
			"traceID":     traceID,
			"spanID":      spanID,
			"descendants": true,
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
		return fmt.Errorf("%s: %s: %s", gqlReq.OpName, resp.Status, string(respBody))
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
