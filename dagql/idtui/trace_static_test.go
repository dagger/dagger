package idtui

import (
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
)

// staticSource is an in-memory traceSource for offline smoke tests: a fixed span
// tree (priority set + children indexed by parent hex) and per-span logs, no
// Cloud connection.
type staticSource struct {
	id       string
	priority []dagui.SpanSnapshot
	childOf  map[string][]dagui.SpanSnapshot
	own      map[string][]cloud.LogMessage
	roll     map[string][]cloud.LogMessage
}

func (s *staticSource) traceID() string                   { return s.id }
func (s *staticSource) loadInitial() []dagui.SpanSnapshot { return s.priority }
func (s *staticSource) children(id dagui.SpanID) []dagui.SpanSnapshot {
	return s.childOf[id.String()]
}
func (s *staticSource) logs(id dagui.SpanID, descendants bool) []cloud.LogMessage {
	if descendants {
		return s.roll[id.String()]
	}
	return s.own[id.String()]
}

// TestTraceSessionLazyFetch drives the headless session over an in-memory tree
// and checks the fetch side-channel: a failing test case is a lazy child of the
// root, so until the root is expanded and the case renders, its (rolled-up) logs
// are never fetched. This is the always-on smoke test for the harness.
func TestTraceSessionLazyFetch(t *testing.T) {
	start := time.Unix(100, 0)
	rootID := prettyTestSpanID(1)
	caseID := prettyTestSpanID(2)
	traceID := prettyTestTraceID()

	src := &staticSource{
		id: traceID.String(),
		priority: []dagui.SpanSnapshot{{
			ID:         rootID,
			TraceID:    traceID,
			Name:       "run tests",
			StartTime:  start,
			EndTime:    start.Add(2 * time.Second),
			Final:      true,
			ChildCount: 1, // signals there's a lazily-loadable child
		}},
		childOf: map[string][]dagui.SpanSnapshot{
			rootID.String(): {{
				ID:           caseID,
				TraceID:      traceID,
				Name:         "unit failure",
				StartTime:    start.Add(time.Second),
				EndTime:      start.Add(2 * time.Second),
				ParentID:     rootID,
				TestCaseName: "unit failure",
				TestStatus:   dagui.TestStatusFailure,
				Final:        true,
			}},
		},
		// A failing leaf test rolls up its descendants (descendants=true).
		roll: map[string][]cloud.LogMessage{
			caseID.String(): {
				{Body: "=== RUN unit failure\n"},
				{Body: "    assertion failed: boom\n"},
				{Body: "--- FAIL: unit failure\n"},
			},
		},
	}

	sess := newTraceSession(t, src, nil)

	// Before expanding, the case isn't loaded, nothing rendered it, so its logs
	// were never fetched — the over-fetch we eliminated.
	if sess.Network().fetchedLog(caseID) {
		t.Fatalf("failing case logs fetched before render; requests=%v", sess.Network().logRequests)
	}

	// Expand the focused root with the real "right" key: the lazy case loads, the
	// TESTS summary renders it, and only THEN are its rolled-up logs fetched.
	out := sess.Press("right")
	if !strings.Contains(out, "unit failure") {
		t.Fatalf("expanded view missing the failing test:\n%s", out)
	}
	if !sess.Network().fetchedLog(caseID) {
		t.Fatalf("failing case logs not fetched after render; requests=%v", sess.Network().logRequests)
	}

	logs := sess.Network().op(opSpanLogs)
	if logs.Requests == 0 || logs.Bytes == 0 {
		t.Fatalf("expected a non-empty GetSpanLogs fetch, got %+v", logs)
	}
	t.Logf("fetched logs: %d requests, %d records, %d bytes", logs.Requests, logs.Records, logs.Bytes)
}
