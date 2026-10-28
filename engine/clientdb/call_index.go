package clientdb

import (
	"bytes"
	"errors"
	"log/slog"
	"sync"

	telemetry "github.com/dagger/otel-go"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// callLookup indexes the call frames a client's store holds, by call digest.
//
// A frame reaches a client over one of two transports (core/telemetry.go,
// core/dag_call_telemetry.go): a recording span carries its own frame as the
// dagger.io/dag.call attribute, and every frame in the call's closure that
// no span delivered rides a call-payload log record. Rebuilding a call's ID
// (dagui.DB.CallIDForDigest) needs the frame for every digest the chain
// references, so a scoped load has to find each one by digest without
// scanning either stream -- which is what this index answers: the newest
// span row carrying a digest's frame, and the log row carrying its payload.
//
// Both maps are keyed by the digest the producer files the frame under. The
// span side reads it from the dagger.io/dag.digest attribute with a raw byte
// scan of the encoded attributes (no decode); the log side decodes the
// payload record's body, which is small and already identified by its content
// type. The cost is one map entry per distinct call, the same order as the
// span index's per-span maps.
type callLookup struct {
	mu sync.RWMutex
	// spanRow maps a call digest to the newest span row whose attributes
	// carry the call's frame (dagger.io/dag.call).
	spanRow map[string]int64
	// logRow maps a call digest to the log row carrying its payload record.
	// A digest is published at most once per delivery domain, so first wins.
	logRow map[string]int64
}

func newCallLookup() *callLookup {
	return &callLookup{
		spanRow: make(map[string]int64),
		logRow:  make(map[string]int64),
	}
}

// Marker byte strings for the raw-attribute prefilters below. The attributes
// column is a binary KeyValue list (MarshalProtoList), so an attribute's
// presence implies its key (or string value) bytes appear verbatim. A match
// can also come from other bytes, so each prefilter is confirmed by decoding.
var (
	dagCallAttrMarker        = []byte(telemetry.DagCallAttr)
	callPayloadContentMarker = []byte(telemetryattrs.CallPayloadContentType)
)

func (l *callLookup) addSpan(row Span) {
	digest, ok := spanCallDigest(row)
	if !ok {
		return
	}
	l.mu.Lock()
	l.spanRow[digest] = row.ID
	l.mu.Unlock()
}

func (l *callLookup) addSpans(rows []Span) {
	l.mu.Lock()
	for _, row := range rows {
		if digest, ok := spanCallDigest(row); ok {
			l.spanRow[digest] = row.ID
		}
	}
	l.mu.Unlock()
}

// spanCallDigest returns the call digest of a span row that carries its
// frame: it must have both the digest attribute and the frame attribute, as
// a span with a digest but no frame (an older engine, or a frame that failed
// to encode) cannot serve a rebuild.
func spanCallDigest(row Span) (string, bool) {
	if !bytes.Contains(row.Attributes, dagCallAttrMarker) {
		return "", false
	}
	return listStringAttr(row.Attributes, telemetry.DagDigestAttr)
}

// listStringAttr returns the string value of the attribute named key in a
// binary KeyValue list (MarshalProtoList), reading the wire format without
// decoding the list. It reports false when the attribute is absent or its
// value isn't a string.
func listStringAttr(attrs []byte, key string) (string, bool) {
	for len(attrs) > 0 {
		kv, n := protowire.ConsumeBytes(attrs)
		if n < 0 {
			return "", false
		}
		attrs = attrs[n:]
		k, v, ok := wireKeyValue(kv)
		if !ok {
			return "", false
		}
		if string(k) != key {
			continue
		}
		return wireStringValue(v)
	}
	return "", false
}

// KeyValue and AnyValue field numbers, from the OTLP common proto.
const (
	keyValueKeyField    protowire.Number = 1
	keyValueValueField  protowire.Number = 2
	anyValueStringField protowire.Number = 1
)

// wireKeyValue splits an encoded KeyValue into its key and encoded value.
func wireKeyValue(kv []byte) (key, value []byte, ok bool) {
	for len(kv) > 0 {
		num, typ, n := protowire.ConsumeTag(kv)
		if n < 0 {
			return nil, nil, false
		}
		kv = kv[n:]
		if typ == protowire.BytesType && (num == keyValueKeyField || num == keyValueValueField) {
			b, n := protowire.ConsumeBytes(kv)
			if n < 0 {
				return nil, nil, false
			}
			if num == keyValueKeyField {
				key = b
			} else {
				value = b
			}
			kv = kv[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(num, typ, kv)
		if n < 0 {
			return nil, nil, false
		}
		kv = kv[n:]
	}
	return key, value, true
}

// wireStringValue returns the string an encoded AnyValue holds, if it holds
// one.
func wireStringValue(v []byte) (string, bool) {
	for len(v) > 0 {
		num, typ, n := protowire.ConsumeTag(v)
		if n < 0 {
			return "", false
		}
		v = v[n:]
		if num == anyValueStringField && typ == protowire.BytesType {
			b, n := protowire.ConsumeBytes(v)
			if n < 0 {
				return "", false
			}
			return string(b), true
		}
		n = protowire.ConsumeFieldValue(num, typ, v)
		if n < 0 {
			return "", false
		}
		v = v[n:]
	}
	return "", false
}

func (l *callLookup) addLog(row Log) {
	digest, ok := logCallDigest(row)
	if !ok {
		return
	}
	l.mu.Lock()
	if _, seen := l.logRow[digest]; !seen {
		l.logRow[digest] = row.ID
	}
	l.mu.Unlock()
}

func (l *callLookup) addLogs(rows []Log) {
	l.mu.Lock()
	for _, row := range rows {
		digest, ok := logCallDigest(row)
		if !ok {
			continue
		}
		if _, seen := l.logRow[digest]; !seen {
			l.logRow[digest] = row.ID
		}
	}
	l.mu.Unlock()
}

// logCallDigest returns the embedded digest of a call-payload log record.
// The content type reserves the record (dagui.IsCallPayloadRecord); the
// digest is the one the producer stamped into the payload, used verbatim so
// the index never re-derives a digest under this engine's scheme.
func logCallDigest(row Log) (string, bool) {
	if !bytes.Contains(row.Attributes, callPayloadContentMarker) {
		return "", false
	}
	payload, err := CallPayloadBody(row)
	if err != nil {
		slog.Warn("skipping malformed call payload record", "log", row.ID, "err", err)
		return "", false
	}
	return payload.GetDigest(), payload.GetDigest() != ""
}

// CallPayloadBody decodes the call frame a call-payload log record carries.
func CallPayloadBody(row Log) (*callpbv1.Call, error) {
	var body otlpcommonv1.AnyValue
	if err := proto.Unmarshal(row.Body, &body); err != nil {
		return nil, err
	}
	raw, ok := body.GetValue().(*otlpcommonv1.AnyValue_BytesValue)
	if !ok {
		return nil, errors.New("call payload body is not bytes")
	}
	frame := new(callpbv1.Call)
	if err := proto.Unmarshal(raw.BytesValue, frame); err != nil {
		return nil, err
	}
	return frame, nil
}

// rows returns the indexed span row and log row for a digest; either may be
// 0 when that transport never delivered the frame.
func (l *callLookup) rows(digest string) (spanRow, logRow int64) {
	l.mu.RLock()
	spanRow, logRow = l.spanRow[digest], l.logRow[digest]
	l.mu.RUnlock()
	return spanRow, logRow
}

// digests snapshots every call digest either transport delivered a frame for.
func (l *callLookup) digests() map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]struct{}, len(l.spanRow)+len(l.logRow))
	for digest := range l.spanRow {
		out[digest] = struct{}{}
	}
	for digest := range l.logRow {
		out[digest] = struct{}{}
	}
	return out
}
