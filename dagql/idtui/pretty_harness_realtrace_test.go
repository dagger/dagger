package idtui

import (
	"os"
	"strings"
	"testing"
)

// TestTraceSessionRealTraceLazyFetch replays a recorded real trace and drives it
// like an interactive session, asserting the render-driven fetch behaviour on
// real data: the view loads incrementally and fetches only the logs it renders,
// never every span that has logs.
//
// The fixture is large, so it isn't committed; record one with
// TestRecordTraceFixture (see its doc). The test skips when absent.
func TestTraceSessionRealTraceLazyFetch(t *testing.T) {
	const path = "testdata/traces/call_loadfail.json"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no fixture at %s (record one with TestRecordTraceFixture)", path)
	}
	fix := LoadTraceFixture(t, path)

	spansWithLogs := 0
	for _, fl := range fix.Logs {
		if len(fl.Own) > 0 || len(fl.Roll) > 0 {
			spansWithLogs++
		}
	}
	if spansWithLogs < 2 {
		t.Skipf("fixture has too few log spans (%d) to demonstrate lazy fetch", spansWithLogs)
	}

	sess := newTraceSession(t, fix, nil)

	// Fresh view: only the priority spans are loaded, the root shows collapsed,
	// and only the handful of logs it surfaces were fetched -- not all of them.
	collapsed := sess.Render()
	if !strings.Contains(collapsed, "ERROR") {
		t.Fatalf("fresh render missing the failure:\n%s", collapsed)
	}
	freshFetched := len(sess.Network().logRequests)
	freshBytes := sess.Network().op(opSpanLogs).Bytes
	if freshFetched >= spansWithLogs {
		t.Fatalf("eager fetch: pulled logs for all %d log-spans on a fresh render", spansWithLogs)
	}

	// Drive it: expand the focused root with the real "right" key. Children load
	// lazily and render, which may pull a few more logs -- but still only what's
	// now on screen, not the whole trace.
	expanded := sess.Press("right")
	if len(expanded) <= len(collapsed) {
		t.Fatalf("expanding the root revealed nothing new:\n%s", expanded)
	}
	grownFetched := len(sess.Network().logRequests)
	if grownFetched >= spansWithLogs {
		t.Fatalf("eager fetch after expand: pulled all %d log-spans", spansWithLogs)
	}

	t.Logf("fresh: %d/%d log-spans (%d bytes); after expanding root: %d/%d log-spans (%d bytes)",
		freshFetched, spansWithLogs, freshBytes,
		grownFetched, spansWithLogs, sess.Network().op(opSpanLogs).Bytes)
}
