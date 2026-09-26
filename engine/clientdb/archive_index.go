package clientdb

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
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

type archiveLookup struct {
	mu       sync.RWMutex
	index    agentcontrol.Index
	agents   map[agentcontrol.Key]int64
	edges    map[agentcontrol.EdgeKey]int64
	calls    map[string]map[string]int64 // trace -> digest -> first row
	titles   map[string]archiveTitle     // trace -> latest session title
	failures map[string]error
}

// archiveTitle is the latest span-name record seen for a trace. The session
// title is published that way (see telemetryattrs.LogRoleSpanName), and a
// reset or branch republishes it, so the highest row wins.
type archiveTitle struct {
	row   int64
	title string
}

// maxIndexedTitleBytes bounds the memory one trace's title can hold. Archive
// titles are sanitized and cut much shorter when they reach a manifest.
const maxIndexedTitleBytes = 4 << 10

func newArchiveLookup() *archiveLookup {
	return &archiveLookup{agents: map[agentcontrol.Key]int64{}, edges: map[agentcontrol.EdgeKey]int64{}, calls: map[string]map[string]int64{}, titles: map[string]archiveTitle{}, failures: map[string]error{}}
}
func (idx *archiveLookup) add(row Log) { idx.addAll([]Log{row}) }
func (idx *archiveLookup) addAll(rows []Log) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, row := range rows {
		if bytes.Contains(row.Attributes, []byte(telemetryattrs.LogRoleAttr)) {
			idx.addTitle(row)
		}
		if !bytes.Contains(row.Attributes, []byte(agentcontrol.VersionAttr)) && !bytes.Contains(row.Attributes, []byte(telemetryattrs.CallPayloadContentType)) {
			continue
		}
		rec, err := DecodeLogRecord(row)
		if err != nil {
			idx.failures[row.TraceID.String] = err
			continue
		}
		if agentcontrol.IsRecord(rec) {
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
			continue
		}
		payload := false
		rec.WalkAttributes(func(a log.KeyValue) bool {
			if a.Key == telemetry.ContentTypeAttr && a.Value.AsString() == telemetryattrs.CallPayloadContentType {
				payload = true
			}
			return true
		})
		if !payload {
			continue
		}
		var call callpbv1.Call
		if rec.Body().Kind() != log.KindBytes {
			idx.failures[row.TraceID.String] = fmt.Errorf("call body is not bytes")
			continue
		}
		if err := proto.Unmarshal(rec.Body().AsBytes(), &call); err != nil {
			idx.failures[row.TraceID.String] = fmt.Errorf("invalid call payload: %w", err)
			continue
		}
		if call.Digest == "" {
			idx.failures[row.TraceID.String] = fmt.Errorf("call payload has no digest")
			continue
		}
		calls := idx.calls[row.TraceID.String]
		if calls == nil {
			calls = map[string]int64{}
			idx.calls[row.TraceID.String] = calls
		}
		if calls[call.Digest] == 0 {
			calls[call.Digest] = row.ID
		}
	}
}

// addTitle indexes a span-name record. Titles are advisory: a malformed record
// is ignored rather than recorded as a restore-critical failure.
func (idx *archiveLookup) addTitle(row Log) {
	if !row.TraceID.Valid || row.TraceID.String == "" {
		return
	}
	if cur, ok := idx.titles[row.TraceID.String]; ok && cur.row > row.ID {
		return
	}
	rec, err := DecodeLogRecord(row)
	if err != nil {
		return
	}
	spanName := false
	rec.WalkAttributes(func(a log.KeyValue) bool {
		if a.Key != telemetryattrs.LogRoleAttr {
			return true
		}
		spanName = a.Value.Kind() == log.KindString && a.Value.AsString() == telemetryattrs.LogRoleSpanName
		return false
	})
	if !spanName || rec.Body().Kind() != log.KindString {
		return
	}
	title := rec.Body().AsString()
	if strings.TrimSpace(title) == "" {
		return
	}
	if len(title) > maxIndexedTitleBytes {
		title = strings.Clone(title[:maxIndexedTitleBytes])
	}
	idx.titles[row.TraceID.String] = archiveTitle{row: row.ID, title: title}
}

// Title returns the latest session title recorded for a trace, unsanitized,
// or "" when none was recorded. Only records attributed to traceID count.
func (s *DB) Title(traceID string) string {
	idx := s.archiveIdx
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.titles[traceID].title
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

func (s *DB) CallPayload(ctx context.Context, traceID, digest string, through int64) (Log, error) {
	idx := s.archiveIdx
	idx.mu.RLock()
	id := idx.calls[traceID][digest]
	idx.mu.RUnlock()
	if id == 0 || id > through {
		return Log{}, fmt.Errorf("missing persisted call %s at cut %d", digest, through)
	}
	row, ok, err := s.logs.readID(ctx, id)
	if err != nil {
		return Log{}, err
	}
	if !ok {
		return Log{}, fmt.Errorf("missing call row %d", id)
	}
	return row, nil
}
