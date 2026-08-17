package dagui

import (
	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// The client half of the call-payload side channel (see the
// dagger.io/dag.call.payload.* block in engine/telemetryattrs).
//
// Rebuilding a call ID (Span.CallID → extractIntoDAG → DB.Call) needs a
// payload for every frame the chain references, and the span channel can only
// ever carry payloads for frames that got a span. The engine publishes the
// rest over the log stream; this is where they land, in the same
// CallPayloads map the span attribute feeds, so nothing downstream has to
// know which channel a payload arrived on.

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
	if _, ok := db.CallPayloads[digest]; ok {
		// Already have it, from either channel. Payloads are keyed by their
		// own digest, so a second copy carries nothing new.
		return true
	}
	db.CallPayloads[digest] = payload
	// Views memoized on db.mutations (and the per-span call caches keyed off
	// them) can go from "chain not rebuildable" to rebuildable on this record
	// alone, so a payload is a DB mutation like a span update.
	db.mutations++
	return true
}
