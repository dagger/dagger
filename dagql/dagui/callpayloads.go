package dagui

import (
	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// The client half of the call-payload log transport (see the
// dagger.io/dag.call.payload.* block in engine/telemetryattrs).
//
// Rebuilding a call ID (Span.CallID → extractIntoDAG → DB.Call) needs a
// payload for every frame the chain references. New engines publish the root
// and its closure over the log stream; legacy span attributes feed the same
// CallPayloads map, so nothing downstream has to know which channel carried an
// older payload.

// ingestCallPayload folds a call-payload log record (one carrying
// telemetryattrs.DagCallPayloadAttr) into db.CallPayloads. It reports whether
// the record was a call payload; such records are consumed entirely and must
// not be treated as log text — they are data about a call, not output from
// one.
//
// A payload may arrive before or after the span that references its digest:
// spans and logs are separate pipelines with separate batching and no
// ordering guarantee between them. Both orders work because nothing here
// depends on the span, and DB.Call resolves payloads lazily at read time
// rather than caching their absence.
func (db *DB) ingestCallPayload(record sdklog.Record) bool {
	var digest, payload string
	var sawPayload bool
	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.DagCallPayloadDigestAttr:
			digest = kv.Value.AsString()
		case telemetryattrs.DagCallPayloadAttr:
			payload = kv.Value.AsString()
			sawPayload = true
		}
		return true
	})
	if !sawPayload {
		return false
	}
	if digest == "" || payload == "" {
		// Malformed, but unambiguously a payload record: consume it rather
		// than rendering half a protobuf as log text.
		return true
	}
	if !db.addCallPayload(digest, payload) {
		// Already have it, from either channel. Payloads are keyed by their
		// own digest, so a second copy carries nothing new.
		return true
	}
	// Views memoized on db.mutations can go from "chain not rebuildable" to
	// rebuildable on this record alone, so a payload is a DB mutation like a
	// span update.
	db.mutations++
	return true
}

// addCallPayload installs an immutable exact payload and invalidates any span
// views that provisionally resolved the digest through an output creator while
// the payload was absent. Logs and spans are independently batched, so those
// provisional views may already have been rendered by the time this runs.
func (db *DB) addCallPayload(digest, payload string) bool {
	if _, ok := db.CallPayloads[digest]; ok {
		return false
	}
	db.CallPayloads[digest] = payload
	delete(db.Calls, digest)
	for _, span := range db.Intervals[digest] {
		span.callCache = nil
		span.baseCache = nil
	}
	return true
}
