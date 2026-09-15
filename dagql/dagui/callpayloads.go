package dagui

import (
	telemetry "github.com/dagger/otel-go"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// IsCallPayloadRecord reports whether record belongs to the call-payload
// channel: its content type declares the body to be an encoded call. This
// deliberately includes malformed records (a payload content type over a
// non-bytes body) so no downstream renderer treats them as log text.
func IsCallPayloadRecord(record sdklog.Record) bool {
	payload := false
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key == telemetry.ContentTypeAttr {
			payload = kv.Value.Kind() == otellog.KindString &&
				kv.Value.AsString() == telemetryattrs.CallPayloadContentType
			return false
		}
		return true
	})
	return payload
}

// ingestCallPayload folds one call-payload log record into db.Calls. It
// reports whether the record belongs to the call-payload channel. Payload
// records are always consumed, including malformed ones, so protobuf bytes
// and tampered data can never fall through to ordinary log rendering.
func (db *DB) ingestCallPayload(record sdklog.Record) bool {
	if !IsCallPayloadRecord(record) {
		return false
	}

	if record.Body().Kind() != otellog.KindBytes {
		slog.Warn("dropping malformed call payload record", "reason", "body must be bytes", "kind", record.Body().Kind())
		return true
	}

	decoded := new(callpbv1.Call)
	if err := proto.Unmarshal(record.Body().AsBytes(), decoded); err != nil {
		slog.Warn("dropping malformed call payload record", "reason", "invalid call payload", "err", err)
		return true
	}
	if decoded.GetDigest() == "" {
		slog.Warn("dropping malformed call payload record", "reason", "missing embedded digest")
		return true
	}
	if !db.addCall(decoded.GetDigest(), decoded) {
		return true
	}

	// Views memoized on db.mutations can go from "chain not rebuildable" to
	// rebuildable on this record alone, so a payload is a DB mutation like a
	// span update.
	db.mutations++
	return true
}

// addCall installs an immutable decoded call and invalidates span views that
// provisionally resolved its digest through an output creator while the exact
// call was absent. Logs and spans are independently batched, so those views may
// already have been rendered by the time this runs.
func (db *DB) addCall(digest string, decoded *callpbv1.Call) bool {
	if _, ok := db.Calls[digest]; ok {
		return false
	}
	db.Calls[digest] = decoded
	for _, span := range db.Intervals[digest] {
		span.callCache = nil
		span.baseCache = nil
	}
	return true
}
