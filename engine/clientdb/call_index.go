package clientdb

import (
	"bytes"
	"errors"
	"log/slog"
	"sync"

	telemetry "github.com/dagger/otel-go"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
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

// Marker byte strings for the raw-attribute scans below. The attributes
// column is a protojson-encoded KeyValue list, so an attribute's presence
// implies its quoted key (or value) appears verbatim.
var (
	dagDigestAttrMarker      = []byte(`"` + telemetry.DagDigestAttr + `"`)
	dagCallAttrMarker        = []byte(`"` + telemetry.DagCallAttr + `"`)
	callPayloadContentMarker = []byte(`"` + telemetryattrs.CallPayloadContentType + `"`)
	stringValueMarker        = []byte(`"stringValue"`)
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
	return protoJSONStringAttr(row.Attributes, dagDigestAttrMarker)
}

// protoJSONStringAttr extracts the string value of the attribute whose quoted
// key is marker from a protojson-encoded KeyValue list, without decoding the
// list. It expects the OTLP shape {"key": K, "value": {"stringValue": V}}
// with the key before the value, and tolerates protojson's deliberately
// unstable whitespace. A marker match that isn't in key position (the same
// bytes inside some other attribute's value) is skipped. Values with escape
// sequences are refused: this is for digests, which have none.
func protoJSONStringAttr(attrs, marker []byte) (string, bool) {
	for from := 0; from < len(attrs); {
		at := bytes.Index(attrs[from:], marker)
		if at < 0 {
			return "", false
		}
		at += from
		from = at + 1
		if !precededByKey(attrs[:at]) {
			continue
		}
		rest := attrs[at+len(marker):]
		at = bytes.Index(rest, stringValueMarker)
		if at < 0 {
			return "", false
		}
		rest = rest[at+len(stringValueMarker):]
		// Skip the colon and whitespace to the opening quote.
		open := bytes.IndexByte(rest, '"')
		if open < 0 {
			return "", false
		}
		for _, c := range rest[:open] {
			switch c {
			case ':', ' ', '\t', '\n', '\r':
			default:
				return "", false
			}
		}
		rest = rest[open+1:]
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			return "", false
		}
		value := rest[:end]
		if bytes.IndexByte(value, '\\') >= 0 {
			return "", false
		}
		return string(value), true
	}
	return "", false
}

var keyMarker = []byte(`"key"`)

// precededByKey reports whether prefix ends with `"key":` modulo whitespace,
// i.e. whether the bytes that follow it are an attribute's key.
func precededByKey(prefix []byte) bool {
	prefix = bytes.TrimRight(prefix, " \t\n\r")
	if len(prefix) == 0 || prefix[len(prefix)-1] != ':' {
		return false
	}
	prefix = bytes.TrimRight(prefix[:len(prefix)-1], " \t\n\r")
	return bytes.HasSuffix(prefix, keyMarker)
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
