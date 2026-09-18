package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/muesli/termenv"
)

func TestCacheReportRendersExactHitRate(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	snapshots := []dagui.SpanSnapshot{{
		ID: rootID, TraceID: prettyTestTraceID(), Name: "call", StartTime: start, EndTime: start.Add(time.Second), Final: true,
	}}
	add := func(id byte, outcome, route string) {
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID: prettyTestSpanID(id), TraceID: prettyTestTraceID(), ParentID: rootID,
			StartTime: start, EndTime: start.Add(time.Second), Final: true,
			CacheContract: telemetryattrs.CacheContractV1, CacheOutcome: outcome, CacheHitRoute: route,
		})
	}
	add(2, telemetryattrs.CacheOutcomeHit, telemetryattrs.CacheHitRouteRecipe)
	add(3, telemetryattrs.CacheOutcomeHit, telemetryattrs.CacheHitRouteStructural)
	add(4, telemetryattrs.CacheOutcomeExecuted, "")
	add(5, telemetryattrs.CacheOutcomeJoined, "")
	add(6, telemetryattrs.CacheOutcomeUncached, "")
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()
	got := strings.Join(fe.cacheReport(false), "\n")
	if want := "Cache hits 2/4 (50%)"; got != want {
		t.Fatalf("cache report = %q, want %q", got, want)
	}

	fe.profile = termenv.ANSI
	got = strings.Join(fe.cacheReport(false), "\n")
	if !strings.Contains(got, "\x1b[92m") {
		t.Fatalf("cache report is not light green: %q", got)
	}
}

func TestCacheReportAbsentWithoutEvidence(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	db.ImportSnapshots([]dagui.SpanSnapshot{{ID: rootID, TraceID: prettyTestTraceID(), Final: true}})
	db.SetPrimarySpan(rootID)
	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()
	if got := fe.cacheReport(false); got != nil {
		t.Fatalf("cache report = %v, want nil", got)
	}
}

func TestFinalRenderIncludesCacheReport(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "call", StartTime: start, EndTime: start.Add(time.Second), Final: true},
		{
			ID: prettyTestSpanID(2), TraceID: prettyTestTraceID(), ParentID: rootID,
			StartTime: start, EndTime: start.Add(time.Second), Final: true,
			CacheContract: telemetryattrs.CacheContractV1,
			CacheOutcome:  telemetryattrs.CacheOutcomeHit,
			CacheHitRoute: telemetryattrs.CacheHitRouteRecipe,
		},
	})
	db.SetPrimarySpan(rootID)
	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "Cache hits 1/1 (100%)") {
		t.Fatalf("final render missing cache report:\n%s", got)
	}
}
