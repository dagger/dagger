package dagui

import (
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// ingestCallPayload folds one reserved call-payload log record into db.Calls.
// It reports whether the record belongs to the call-payload channel. Reserved
// records are always consumed, including malformed ones, so protobuf bytes and
// tampered data can never fall through to ordinary log rendering.
func (db *DB) ingestCallPayload(record sdklog.Record) bool {
	reservedByScope := record.InstrumentationScope().Name == telemetryattrs.CallPayloadInstrumentationScope
	markerPresent := false
	markerValid := true
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key == telemetryattrs.DagCallPayloadAttr {
			markerPresent = true
			markerValid = markerValid && kv.Value.Kind() == otellog.KindBool && kv.Value.AsBool()
		}
		return true
	})

	if !markerPresent && !reservedByScope {
		return false
	}

	if !markerPresent || !markerValid {
		slog.Warn("dropping malformed call payload record", "reason", "marker must be boolean true")
		return true
	}
	if !reservedByScope {
		slog.Warn("dropping malformed call payload record", "reason", "wrong instrumentation scope", "scope", record.InstrumentationScope().Name)
		return true
	}
	if record.Body().Kind() != otellog.KindBytes {
		slog.Warn("dropping malformed call payload record", "reason", "body must be bytes", "kind", record.Body().Kind())
		return true
	}

	decoded, dgst, err := call.DecodeCallPayload(record.Body().AsBytes())
	if err != nil {
		slog.Warn("dropping malformed call payload record", "reason", "invalid call payload", "err", err)
		return true
	}
	if !db.addCall(dgst.String(), decoded) {
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
