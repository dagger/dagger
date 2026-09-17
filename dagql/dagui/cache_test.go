package dagui

import (
	"testing"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/trace"
)

func TestCacheStatsScopesAndCountsDecisions(t *testing.T) {
	db := NewDB()
	traceA := TraceID{TraceID: trace.TraceID{1}}
	traceB := TraceID{TraceID: trace.TraceID{2}}
	rootID := SpanID{SpanID: trace.SpanID{1}}
	checkID := SpanID{SpanID: trace.SpanID{2}}
	start := time.Unix(100, 0)
	cache := func(id byte, parent SpanID, traceID TraceID, outcome, route string) SpanSnapshot {
		return SpanSnapshot{
			ID:            SpanID{SpanID: trace.SpanID{id}},
			TraceID:       traceID,
			ParentID:      parent,
			StartTime:     start,
			EndTime:       start.Add(time.Second),
			Final:         true,
			CacheContract: telemetryattrs.CacheContractV1,
			CacheOutcome:  outcome,
			CacheHitRoute: route,
		}
	}
	db.ImportSnapshots([]SpanSnapshot{
		{ID: rootID, TraceID: traceA, Name: "root", StartTime: start, EndTime: start.Add(time.Second), Final: true},
		{ID: checkID, TraceID: traceA, ParentID: rootID, Name: "check", StartTime: start, EndTime: start.Add(time.Second), Final: true},
		cache(3, checkID, traceA, telemetryattrs.CacheOutcomeHit, telemetryattrs.CacheHitRouteStructural),
		cache(4, checkID, traceA, telemetryattrs.CacheOutcomeJoined, ""),
		cache(5, rootID, traceA, telemetryattrs.CacheOutcomeExecuted, ""),
		cache(6, rootID, traceA, telemetryattrs.CacheOutcomeUncached, ""),
		cache(7, SpanID{}, traceB, telemetryattrs.CacheOutcomeHit, telemetryattrs.CacheHitRouteRecipe),
		{ID: SpanID{SpanID: trace.SpanID{8}}, TraceID: traceA, ParentID: rootID, CacheContract: "future", CacheOutcome: "hit", Final: true},
	})
	db.SetPrimarySpan(rootID)

	got := db.CacheStats(nil)
	if got.Hits != 1 || got.Executed != 1 || got.Joined != 1 || got.Uncached != 1 || got.Unsupported != 1 || got.StructuralHits != 1 {
		t.Fatalf("root stats = %+v", got)
	}
	if got.Lookups() != 3 {
		t.Fatalf("root lookups = %d, want 3", got.Lookups())
	}

	got = db.CacheStats(db.Spans.Map[checkID])
	if got.Hits != 1 || got.Joined != 1 || got.Executed != 0 || got.Uncached != 0 || got.Unsupported != 0 {
		t.Fatalf("check stats = %+v", got)
	}
}
