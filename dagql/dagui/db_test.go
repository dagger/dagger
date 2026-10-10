package dagui

import (
	"sort"
	"testing"
	"time"
	"unsafe"
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

// Spans with the same name share one copy of it.
func TestSpanNamesInterned(t *testing.T) {
	db := NewDB()
	name := func() string { return string([]byte("Container.withExec")) }
	db.ImportSnapshots([]SpanSnapshot{
		{ID: spanID(1), Name: name()},
		{ID: spanID(2), Name: name()},
	})
	a, b := db.Spans.Map[spanID(1)].Name, db.Spans.Map[spanID(2)].Name
	if a != "Container.withExec" || b != a {
		t.Fatalf("names = %q, %q", a, b)
	}
	if unsafe.StringData(a) != unsafe.StringData(b) {
		t.Fatal("spans with the same name should share it")
	}
}

// db.Intervals keeps one span per call digest and start time: re-exports of
// a span replace it, while another run of the same call adds to it.
func TestIntervalsOneSpanPerStartTime(t *testing.T) {
	start := time.Unix(100, 0)
	snap := func(id byte, start time.Time, done bool) SpanSnapshot {
		s := SpanSnapshot{ID: spanID(id), Name: "call", CallDigest: "xxh3:call", StartTime: start}
		if done {
			s.EndTime = start.Add(time.Second)
		}
		return s
	}
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{snap(1, start, false)})
	db.ImportSnapshots([]SpanSnapshot{snap(1, start, true)})
	if got := db.Intervals["xxh3:call"]; len(got) != 1 || got[0] != db.Spans.Map[spanID(1)] {
		t.Fatalf("re-export should replace the span, got %v", got)
	}
	db.ImportSnapshots([]SpanSnapshot{snap(2, start.Add(time.Minute), true)})
	if got := db.Intervals["xxh3:call"]; len(got) != 2 {
		t.Fatalf("a second run should add a span, got %d", len(got))
	}
	if got := db.MostInterestingSpan("xxh3:call"); got != db.Spans.Map[spanID(1)] {
		t.Fatalf("most interesting span = %v, want the earliest", got)
	}
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

// TestUpdatedSnapshotsForwardsBuriedService covers a service instance span
// buried beneath plumbing. The instance is passthrough, so its children must
// not be forwarded through it just because it's surfaced.
func TestUpdatedSnapshotsForwardsBuriedService(t *testing.T) {
	const (
		rootID byte = iota + 1
		queryID
		startID
		innerStartID
		instanceID
		childID
		grandchildID
	)
	innerStart := agentTestSpan(innerStartID, "service.start", spanID(startID))
	innerStart.Passthrough = true
	instance := agentTestSpan(instanceID, "exec redis-server", spanID(innerStartID))
	instance.Service = true
	instance.ServiceName = "redis"
	instance.Passthrough = true
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentTestSpan(queryID, "POST /query", spanID(rootID)),
		agentTestSpan(startID, "Service.start", spanID(queryID)),
		innerStart,
		instance,
		agentTestSpan(childID, "service child", spanID(instanceID)),
		agentTestSpan(grandchildID, "service grandchild", spanID(childID)),
	})

	names := forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"root", "POST /query", "Service.start", "service.start", "exec redis-server"},
		[]string{"service child", "service grandchild"},
	)
}

// TestUpdatedSnapshotsForwardsServiceDisplay covers `dagger up`'s
// per-service display spans, which carry a service name but no instance mark.
func TestUpdatedSnapshotsForwardsServiceDisplay(t *testing.T) {
	const (
		rootID byte = iota + 1
		queryID
		upID
		displayID
		readyID
	)
	display := agentTestSpan(displayID, "web:8080", spanID(upID))
	display.ServiceName = "web"
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentTestSpan(queryID, "POST /query", spanID(rootID)),
		agentTestSpan(upID, "ModTreeNode.up", spanID(queryID)),
		display,
		agentTestSpan(readyID, "ready http://web:8080", spanID(displayID)),
	})

	names := forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"root", "POST /query", "ModTreeNode.up", "web:8080"},
		[]string{"ready http://web:8080"},
	)
}

// TestUpdatedSnapshotsSkipsInternalService asserts internal service spans,
// which dagui doesn't surface, aren't forwarded either.
func TestUpdatedSnapshotsSkipsInternalService(t *testing.T) {
	const (
		rootID byte = iota + 1
		queryID
		plumbingID
		instanceID
		displayID
	)
	instance := agentTestSpan(instanceID, "internal instance", spanID(plumbingID))
	instance.Service = true
	instance.ServiceName = "internal"
	instance.Internal = true
	display := agentTestSpan(displayID, "internal display", spanID(plumbingID))
	display.ServiceName = "internal"
	display.Internal = true
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentTestSpan(queryID, "POST /query", spanID(rootID)),
		agentTestSpan(plumbingID, "plumbing", spanID(queryID)),
		instance,
		display,
	})

	names := forwardedNames(t, db, db.UpdatedSnapshots(rootSubscription(spanID(rootID))))
	assertForwarded(t, names,
		[]string{"root", "POST /query"},
		[]string{"plumbing", "internal instance", "internal display"},
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
