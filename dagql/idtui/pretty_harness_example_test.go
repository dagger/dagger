package idtui

import (
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
)

// TestTraceSessionLazyFetch drives the interactive frontend as a black box from
// a hand-built fixture and asserts on the fetch side-channel — the thing that
// previously needed the live CLI's --debug. A failing test case is a lazy child
// of the root: until the root is expanded and the case actually renders, its
// (rolled-up) logs are never fetched.
func TestTraceSessionLazyFetch(t *testing.T) {
	start := time.Unix(100, 0)
	rootID := prettyTestSpanID(1)
	caseID := prettyTestSpanID(2)
	traceID := prettyTestTraceID()

	fix := &TraceFixture{
		TraceID: traceID.String(),
		// Only the root is in the priority set; the failing case is lazy.
		Priority: []string{rootID.String()},
		Spans: []dagui.SpanSnapshot{
			{
				ID:         rootID,
				TraceID:    traceID,
				Name:       "run tests",
				StartTime:  start,
				EndTime:    start.Add(2 * time.Second),
				Final:      true,
				ChildCount: 1, // signals there's a lazily-loadable child
			},
			{
				ID:           caseID,
				TraceID:      traceID,
				Name:         "unit failure",
				StartTime:    start.Add(time.Second),
				EndTime:      start.Add(2 * time.Second),
				ParentID:     rootID,
				TestCaseName: "unit failure",
				TestStatus:   dagui.TestStatusFailure,
				Final:        true,
			},
		},
		Logs: map[string]FixtureLogs{
			// A failing leaf test rolls up its descendants (descendants=true).
			caseID.String(): {Roll: []cloud.LogMessage{
				{Body: "=== RUN unit failure\n"},
				{Body: "    assertion failed: boom\n"},
				{Body: "--- FAIL: unit failure\n"},
			}},
		},
	}

	sess := newTraceSession(t, fix, nil)

	// Before expanding, the case isn't loaded, so nothing has rendered it and
	// its logs were never fetched -- this is the over-fetch we eliminated.
	if sess.Network().fetchedLog(caseID) {
		t.Fatalf("failing case logs fetched before it was rendered; requests=%v", sess.Network().logRequests)
	}

	// Expand the focused root with the real "right" key: the lazy case loads,
	// the TESTS summary renders it, and only THEN are its rolled-up logs fetched.
	out := sess.Press("right")
	if !strings.Contains(out, "unit failure") {
		t.Fatalf("expanded view missing the failing test:\n%s", out)
	}
	if !sess.Network().fetchedLog(caseID) {
		t.Fatalf("failing case logs not fetched after rendering it; requests=%v", sess.Network().logRequests)
	}

	logs := sess.Network().op(opSpanLogs)
	if logs.Requests == 0 || logs.Bytes == 0 {
		t.Fatalf("expected a non-empty GetSpanLogs fetch, got %+v", logs)
	}
	t.Logf("fetched logs: %d requests, %d records, %d bytes", logs.Requests, logs.Records, logs.Bytes)
}
