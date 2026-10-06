package dagui

import (
	"sort"
	"testing"
)

// rootSubscription is the filter a remote frontend that only subscribed to
// the trace root passes to UpdatedSnapshots.
func rootSubscription(root SpanID) map[SpanID]bool {
	return map[SpanID]bool{{}: true, root: true}
}

// forwardedNames returns the names of the forwarded spans, failing on
// duplicates: each span should be sent at most once per batch.
func forwardedNames(t *testing.T, db *DB, snapshots []SpanSnapshot) []string {
	t.Helper()
	seen := map[SpanID]bool{}
	var names []string
	for _, snapshot := range snapshots {
		if seen[snapshot.ID] {
			t.Errorf("span %q forwarded more than once in one batch", db.Spans.Map[snapshot.ID].Name)
		}
		seen[snapshot.ID] = true
		names = append(names, db.Spans.Map[snapshot.ID].Name)
	}
	sort.Strings(names)
	return names
}

func assertForwarded(t *testing.T, names []string, want []string, notWant []string) {
	t.Helper()
	has := map[string]bool{}
	for _, name := range names {
		has[name] = true
	}
	for _, name := range want {
		if !has[name] {
			t.Errorf("expected %q to be forwarded, got %v", name, names)
		}
	}
	for _, name := range notWant {
		if has[name] {
			t.Errorf("expected %q not to be forwarded, got %v", name, names)
		}
	}
}

// TestUpdatedSnapshotsForwardsSurfacedConversation mirrors a multi-agent
// trace viewed remotely: the frontend subscribes only to the root, and the
// agent loop and its messages sit beneath an unsubscribed POST /query. They
// no longer set `reveal`, but dagui surfaces them regardless of depth, so
// they must be forwarded along with the ancestors needed to place them --
// and nothing else beneath POST /query.
func TestUpdatedSnapshotsForwardsSurfacedConversation(t *testing.T) {
	const (
		rootID byte = iota + 1
		queryID
		plumbingID
		loopID
		userID
		assistantID
		toolCallID
		laterID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentTestSpan(queryID, "POST /query", spanID(rootID)),
		agentTestSpan(plumbingID, "plumbing", spanID(queryID)),
		agentLoopSnapshot(loopID, "agent-chief", "chief", spanID(queryID)),
		messageSnapshot(userID, "user message", spanID(loopID), "user"),
		messageSnapshot(assistantID, "assistant message", spanID(loopID), "assistant"),
		agentTestSpan(toolCallID, "tool call plumbing", spanID(assistantID)),
	})

	names := forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"root", "POST /query", "agent: chief", "user message", "assistant message"},
		[]string{"plumbing", "tool call plumbing"},
	)

	// A later message is forwarded without re-sending the ancestors that
	// were already sent. (The loop is sent again: gaining a child updates
	// it, and updates to surfaced spans are forwarded.)
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(laterID, "later message", spanID(loopID), "user"),
	})
	names = forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"later message"},
		[]string{"root", "POST /query", "plumbing", "user message", "assistant message"},
	)
}

// TestUpdatedSnapshotsForwardsBuriedCheck covers a check span buried beneath
// the plumbing of the call that ran it.
func TestUpdatedSnapshotsForwardsBuriedCheck(t *testing.T) {
	const (
		rootID byte = iota + 1
		queryID
		passID
		syncID
		checkID
		execID
	)
	check := agentTestSpan(checkID, "lint", spanID(syncID))
	check.CheckName = "lint"
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentTestSpan(queryID, "POST /query", spanID(rootID)),
		agentTestSpan(passID, "Check.pass", spanID(queryID)),
		agentTestSpan(syncID, "Check.sync", spanID(passID)),
		check,
		agentTestSpan(execID, "withExec", spanID(checkID)),
	})

	names := forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"root", "POST /query", "Check.pass", "Check.sync", "lint"},
		[]string{"withExec"},
	)
}

// TestUpdatedSnapshotsForwardsLateAncestors covers a surfaced span that
// arrives before its ancestors (e.g. spans exported as they end): the
// ancestors can't be sent with it, so they're sent once they arrive.
func TestUpdatedSnapshotsForwardsLateAncestors(t *testing.T) {
	const (
		rootID byte = iota + 1
		queryID
		passID
		syncID
		checkID
		siblingID
	)
	check := agentTestSpan(checkID, "lint", spanID(syncID))
	check.CheckName = "lint"
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{check})

	names := forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	if len(names) != 1 || names[0] != "lint" {
		// in particular, no stub for the parent
		t.Errorf("expected only the check to be forwarded, got %q", names)
	}

	// Check.sync arrives, naming a parent that hasn't arrived yet either.
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(syncID, "Check.sync", spanID(passID)),
	})
	names = forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names, []string{"Check.sync"}, nil)

	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentTestSpan(queryID, "POST /query", spanID(rootID)),
		agentTestSpan(passID, "Check.pass", spanID(queryID)),
		agentTestSpan(siblingID, "unrelated", spanID(passID)),
	})
	names = forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"root", "POST /query", "Check.pass"},
		[]string{"unrelated"},
	)
}
