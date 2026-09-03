package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// TestStrictSubtreeExcludesCauseLinkedSpans covers the scoped-report
// invariant at its source: a span that is only reachable through a CAUSE link
// (here a resumed output, whose creator lives outside the zoomed subtree) must
// not be walked into a strictly-scoped view, even though the unscoped walk
// deliberately renders it inline.
func TestStrictSubtreeExcludesCauseLinkedSpans(t *testing.T) {
	db := NewDB()

	traceID := TraceID{TraceID: trace.TraceID{1}}
	rootID := SpanID{SpanID: trace.SpanID{1}}
	toolID := SpanID{SpanID: trace.SpanID{2}}
	innerID := SpanID{SpanID: trace.SpanID{3}}
	foreignID := SpanID{SpanID: trace.SpanID{4}}
	outputDig := "xxh3:foreign-output"

	db.ImportSnapshots([]SpanSnapshot{
		{
			ID: rootID, TraceID: traceID, Name: "run",
			StartTime: time.Unix(1, 0), EndTime: time.Unix(9, 0),
		},
		{
			ID: toolID, TraceID: traceID, ParentID: rootID, Name: "tool call",
			StartTime: time.Unix(2, 0), EndTime: time.Unix(8, 0),
		},
		{
			// Real child of the tool call, but resumed from an output produced
			// outside it: the DB hangs it off that creator too, and the
			// unscoped walk renders the creator inline as its cause.
			ID: innerID, TraceID: traceID, ParentID: toolID, Name: "inner work",
			StartTime: time.Unix(3, 0), EndTime: time.Unix(4, 0),
			ResumeOutput: outputDig,
		},
		{
			ID: foreignID, TraceID: traceID, ParentID: rootID, Name: "foreign work",
			StartTime: time.Unix(5, 0), EndTime: time.Unix(6, 0),
			CallDigest: "xxh3:foreign", Output: outputDig,
		},
	})

	loose := db.RowsView(FrontendOpts{ZoomedSpan: toolID, Verbosity: ShowCompletedVerbosity})
	if loose.BySpan[foreignID] == nil {
		t.Fatal("fixture is wrong: the unscoped walk should inline the cause-linked span")
	}

	strict := db.RowsView(FrontendOpts{
		ZoomedSpan:    toolID,
		Verbosity:     ShowCompletedVerbosity,
		StrictSubtree: true,
	})
	if strict.BySpan[foreignID] != nil {
		t.Fatal("strictly-scoped view included a span outside the zoomed subtree")
	}
	if strict.BySpan[innerID] == nil {
		t.Fatal("strictly-scoped view dropped a real descendant")
	}
}
