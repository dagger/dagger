package dagui

import (
	"testing"
	"time"
)

func TestFrontendOptsShowsCompletedCheckSpans(t *testing.T) {
	now := time.Now()
	opts := FrontendOpts{
		GCThreshold: time.Second,
		Verbosity:   HideCompletedVerbosity,
	}
	db := NewDB()

	regularSpan := &Span{SpanSnapshot: SpanSnapshot{
		ID:        testID(1),
		Name:      "regular",
		StartTime: now.Add(-11 * time.Second),
		EndTime:   now.Add(-10 * time.Second),
	}}
	if opts.ShouldShow(db, regularSpan) {
		t.Fatal("expected old completed non-check span to be hidden")
	}

	checkSpan := &Span{SpanSnapshot: SpanSnapshot{
		ID:          testID(2),
		Name:        "passing-check",
		StartTime:   now.Add(-11 * time.Second),
		EndTime:     now.Add(-10 * time.Second),
		CheckName:   "passing-check",
		CheckPassed: true,
	}}
	if !opts.ShouldShow(db, checkSpan) {
		t.Fatal("expected old completed check span to stay visible")
	}
}

func TestDBHasChecks(t *testing.T) {
	db := NewDB()
	if db.HasChecks() {
		t.Fatal("expected empty DB to have no checks")
	}

	db.ImportSnapshots([]SpanSnapshot{{
		ID:        testID(1),
		Name:      "passing-check",
		StartTime: time.Unix(1, 0),
		EndTime:   time.Unix(2, 0),
		CheckName: "passing-check",
	}})

	if !db.HasChecks() {
		t.Fatal("expected DB to report checks")
	}
}
