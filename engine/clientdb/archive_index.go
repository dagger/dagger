package clientdb

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// A zero SDK record has a zero attribute value length limit. Build a template
// through the SDK once, with no truncation, then clone it for strict decoding.
var archiveRecordTemplate = func() sdklog.Record {
	capture := &recordTemplateCapture{}
	provider := sdklog.NewLoggerProvider(sdklog.WithAttributeValueLengthLimit(-1), sdklog.WithAttributeCountLimit(-1), sdklog.WithProcessor(capture))
	provider.Logger("archive.decoder").Emit(context.Background(), log.Record{})
	_ = provider.Shutdown(context.Background())
	return capture.record
}()

type recordTemplateCapture struct{ record sdklog.Record }

func (c *recordTemplateCapture) OnEmit(_ context.Context, r *sdklog.Record) error {
	c.record = r.Clone()
	return nil
}
func (*recordTemplateCapture) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (*recordTemplateCapture) Shutdown(context.Context) error                         { return nil }
func (*recordTemplateCapture) ForceFlush(context.Context) error                       { return nil }

// DecodeLogRecord is strict for restore-critical data; the display conversion
// intentionally skips malformed rows and must not be used for verification.
func DecodeLogRecord(row Log) (sdklog.Record, error) {
	var rec = archiveRecordTemplate.Clone()
	var attrs []*commonpb.KeyValue
	if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
		return rec, err
	}
	seen := map[string]bool{}
	for _, a := range attrs {
		if a != nil && seen[a.Key] {
			return rec, fmt.Errorf("duplicate log attribute %s", a.Key)
		}
		if a != nil {
			seen[a.Key] = true
		}
		if a == nil || a.Value == nil {
			return rec, fmt.Errorf("invalid log attribute")
		}
		rec.AddAttributes(log.KeyValue{Key: a.Key, Value: telemetry.LogValueFromPB(a.Value)})
	}
	var body commonpb.AnyValue
	if err := proto.Unmarshal(row.Body, &body); err != nil {
		return rec, err
	}
	rec.SetBody(telemetry.LogValueFromPB(&body))
	if row.TraceID.Valid {
		id, err := trace.TraceIDFromHex(row.TraceID.String)
		if err != nil {
			return rec, err
		}
		rec.SetTraceID(id)
	}
	return rec, nil
}

// archiveLookup indexes the newest agent-control record per agent and
// subscription edge, and remembers the traces whose control records failed
// to decode. Call payloads are found through callLookup instead.
type archiveLookup struct {
	mu       sync.RWMutex
	index    agentcontrol.Index
	agents   map[agentcontrol.Key]int64
	edges    map[agentcontrol.EdgeKey]int64
	failures map[string]error
}

func newArchiveLookup() *archiveLookup {
	return &archiveLookup{agents: map[agentcontrol.Key]int64{}, edges: map[agentcontrol.EdgeKey]int64{}, failures: map[string]error{}}
}

var controlVersionMarker = []byte(`"` + agentcontrol.VersionAttr + `"`)

func (idx *archiveLookup) add(row Log) { idx.addAll([]Log{row}) }
func (idx *archiveLookup) addAll(rows []Log) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, row := range rows {
		if !bytes.Contains(row.Attributes, controlVersionMarker) {
			continue
		}
		rec, err := DecodeLogRecord(row)
		if err != nil {
			idx.failures[row.TraceID.String] = err
			continue
		}
		if !agentcontrol.IsRecord(rec) {
			continue
		}
		a, s, err := agentcontrol.Decode(rec)
		if err != nil {
			idx.failures[row.TraceID.String] = err
			continue
		}
		changed, err := idx.index.ApplyRecord(rec)
		if err != nil {
			idx.failures[row.TraceID.String] = err
			continue
		}
		if changed {
			if a != nil {
				idx.agents[a.Key] = row.ID
			} else {
				idx.edges[s.EdgeKey] = row.ID
			}
		}
	}
}

// ControlRows selects only final received projections. Verification compares
// them with a separately supplied, quiesced producer witness.
func (s *DB) ControlRows(ctx context.Context, traceID string, through int64, want agentcontrol.Expectation) ([]Log, error) {
	idx := s.archiveIdx
	idx.mu.RLock()
	if err := idx.failures[traceID]; err != nil {
		idx.mu.RUnlock()
		return nil, err
	}
	var ids []int64
	for key, id := range idx.agents {
		if key.Trace == traceID {
			ids = append(ids, id)
		}
	}
	for key, id := range idx.edges {
		if key.Trace == traceID {
			ids = append(ids, id)
		}
	}
	idx.mu.RUnlock()
	slices.Sort(ids)
	var folded agentcontrol.Index
	var rows []Log
	for _, id := range ids {
		if id > through {
			return nil, fmt.Errorf("control row %d is beyond fixed cut", id)
		}
		row, ok, err := s.logs.readID(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("missing control row %d", id)
		}
		rec, err := DecodeLogRecord(row)
		if err != nil {
			return nil, err
		}
		if _, err = folded.ApplyRecord(rec); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := folded.Verify(want); err != nil {
		return nil, err
	}
	return rows, nil
}

// CallPayload returns a call-payload log row carrying digest's frame, as long
// as it lies within the cut and belongs to traceID.
//
// A frame reaches the store on one of two carriers (see callLookup): a
// call-payload log record, or — for a call with a recording span of its own —
// the span's dagger.io/dag.call attribute, which is then the frame's only
// delivery. The log row is preferred. A frame that only rode its span is
// returned as a payload row synthesized from the span row, in exactly the
// shape the producer emits (core/dag_call_telemetry.go), so bootstrap
// packing and every reader stay oblivious to the carrier. A synthesized row
// has no log row ID (0).
//
// The span side is indexed by its newest snapshot. Every snapshot of a call
// span carries the same frame, and a sealed cut is taken after the session's
// telemetry shut down, so the newest snapshot lies within it.
func (s *DB) CallPayload(ctx context.Context, traceID, digest string, cut HighWater) (Log, error) {
	spanID, logID := s.callIdx.rows(digest)
	var errs error
	if logID != 0 && logID <= cut.Logs {
		row, err := s.callPayloadLog(ctx, traceID, digest, logID)
		if err == nil {
			return row, nil
		}
		errs = err
	}
	if spanID != 0 && spanID <= cut.Spans {
		row, err := s.callPayloadFromSpan(ctx, traceID, digest, spanID)
		if err == nil {
			return row, nil
		}
		errs = errors.Join(errs, err)
	}
	if errs == nil {
		errs = fmt.Errorf("missing persisted call %s at cut (spans %d, logs %d)", digest, cut.Spans, cut.Logs)
	}
	return Log{}, errs
}

func (s *DB) callPayloadLog(ctx context.Context, traceID, digest string, id int64) (Log, error) {
	row, ok, err := s.logs.readID(ctx, id)
	if err != nil {
		return Log{}, err
	}
	if !ok {
		return Log{}, fmt.Errorf("missing call row %d", id)
	}
	if row.TraceID.String != traceID {
		return Log{}, fmt.Errorf("persisted call %s is in trace %s, not %s", digest, row.TraceID.String, traceID)
	}
	return row, nil
}

func (s *DB) callPayloadFromSpan(ctx context.Context, traceID, digest string, id int64) (Log, error) {
	row, ok, err := s.spans.readID(ctx, id)
	if err != nil {
		return Log{}, err
	}
	if !ok {
		return Log{}, fmt.Errorf("missing call span row %d", id)
	}
	if row.TraceID != traceID {
		return Log{}, fmt.Errorf("persisted call span %s is in trace %s, not %s", digest, row.TraceID, traceID)
	}
	return SpanCallPayloadLog(row, digest)
}

// SpanCallPayloadLog synthesizes the call-payload log row for the frame a call
// span row carries as dagger.io/dag.call: a bytes body holding the encoded
// call, tagged with the call payload content type and digest, attributed to
// the span. The frame must carry digest.
func SpanCallPayloadLog(row Span, digest string) (Log, error) {
	var attrs []*commonpb.KeyValue
	if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
		return Log{}, fmt.Errorf("decode call span %s attributes: %w", digest, err)
	}
	var encoded string
	for _, attr := range attrs {
		if attr.GetKey() == telemetry.DagCallAttr {
			encoded = attr.GetValue().GetStringValue()
		}
	}
	if encoded == "" {
		return Log{}, fmt.Errorf("call span %s carries no call payload", digest)
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Log{}, fmt.Errorf("decode call span %s payload: %w", digest, err)
	}
	var frame callpbv1.Call
	if err := proto.Unmarshal(payload, &frame); err != nil {
		return Log{}, fmt.Errorf("decode call span %s payload: %w", digest, err)
	}
	if frame.GetDigest() != digest {
		return Log{}, fmt.Errorf("call span %s carries the frame of %q", digest, frame.GetDigest())
	}
	body, err := proto.Marshal(&commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: payload}})
	if err != nil {
		return Log{}, err
	}
	logAttrs, err := MarshalProtoJSONs([]*commonpb.KeyValue{
		{Key: telemetry.ContentTypeAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: telemetryattrs.CallPayloadContentType}}},
		{Key: telemetryattrs.CallPayloadDigestAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: digest}}},
	})
	if err != nil {
		return Log{}, err
	}
	return Log{
		TraceID:              sql.NullString{String: row.TraceID, Valid: row.TraceID != ""},
		SpanID:               sql.NullString{String: row.SpanID, Valid: row.SpanID != ""},
		Timestamp:            row.StartTime,
		Body:                 body,
		Attributes:           logAttrs,
		InstrumentationScope: row.InstrumentationScope,
		Resource:             row.Resource,
		ResourceSchemaURL:    row.ResourceSchemaURL,
	}, nil
}
