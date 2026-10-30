package dagui

import (
	"testing"
)

// agentParents maps each agent on the roster to its ParentID.
func agentParents(db *DB) map[string]string {
	parents := map[string]string{}
	for _, node := range db.Agents() {
		parents[node.ID] = node.ParentID
	}
	return parents
}

func requireParents(t *testing.T, db *DB, want map[string]string) {
	t.Helper()
	got := agentParents(db)
	if len(got) != len(want) {
		t.Fatalf("roster = %v, want %v", got, want)
	}
	for id, parent := range want {
		if got[id] != parent {
			t.Errorf("parent of %s = %q, want %q (roster: %v)", id, got[id], parent, got)
		}
	}
}

// TestAgentsLineage covers where an agent's parent comes from: the control
// record's Parent when it names a known agent, else the nearest agent loop
// span above its own, else nobody. The shape is the real one that motivated
// it: a chief's worker runs an integration test whose nested client spawns
// agents the engine records with no parent, though their spans sit under the
// worker -- and some of them publish no loop span at all.
func TestAgentsLineage(t *testing.T) {
	const (
		rootID byte = iota + 1
		chiefID
		spawnID
		workerID
		testRunID
		testAgentID
		reviewerID
		strayID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(chiefID, "chief", "agent", spanID(rootID)),
		agentTestSpan(spawnID, "spawn", spanID(chiefID)),
		agentLoopSnapshot(workerID, "worker", "worker", spanID(spawnID)),
		agentTestSpan(testRunID, "go test", spanID(workerID)),
		agentLoopSnapshot(testAgentID, "test-agent", "flaky", spanID(testRunID)),
		// A reviewer whose loop span the engine nested under the chief, but
		// whose control record names the worker that spawned it.
		agentLoopSnapshot(reviewerID, "reviewer", "finder", spanID(chiefID)),
		agentLoopSnapshot(strayID, "stray", "stray", spanID(rootID)),
	})

	// Before any control record, ancestry is all there is.
	requireParents(t, db, map[string]string{
		"chief": "", "worker": "chief", "test-agent": "worker",
		"reviewer": "chief", "stray": "",
	})

	worker := testControl(1, "worker", "worker", 1, "IDLE")
	worker.Parent = "chief"
	reviewer := testControl(1, "reviewer", "finder", 1, "STOPPED")
	reviewer.Parent, reviewer.StopReason = "worker", "EXPLICIT"
	// The nested client's agents: no parent recorded.
	testAgent := testControl(1, "test-agent", "flaky", 1, "STOPPED")
	testAgent.StopReason = "EXPLICIT"
	controlOnly := testControl(1, "inert", "inert", 1, "STOPPED")
	controlOnly.StopReason = "EXPLICIT"
	// A parent outside the roster is nobody; ancestry still places it.
	stray := testControl(1, "stray", "stray", 1, "IDLE")
	stray.Parent = "gone"
	exportControls(t, db, worker, reviewer, testAgent, controlOnly, stray)

	requireParents(t, db, map[string]string{
		"chief":      "",
		"worker":     "chief",
		"reviewer":   "worker", // the control record outranks span nesting
		"test-agent": "worker", // no recorded parent: span ancestry
		"inert":      "",       // neither: a root
		"stray":      "",
	})
}

// TestAgentsLineageBreaksCycles: control records can describe a loop (each
// revision is valid on its own), and a parent chain that never ends would
// hang anything walking it. The loop is cut at its earliest member.
func TestAgentsLineageBreaksCycles(t *testing.T) {
	const (
		rootID byte = iota + 1
		aID
		bID
		cID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(aID, "a", "a", spanID(rootID)),
		agentLoopSnapshot(bID, "b", "b", spanID(rootID)),
		agentLoopSnapshot(cID, "c", "c", spanID(rootID)),
	})
	a := testControl(1, "a", "a", 1, "IDLE")
	b := testControl(1, "b", "b", 1, "IDLE")
	c := testControl(1, "c", "c", 1, "IDLE")
	a.Parent, b.Parent, c.Parent = "c", "a", "b"
	exportControls(t, db, a, b, c)

	requireParents(t, db, map[string]string{"a": "", "b": "a", "c": "b"})
}

// TestAgentsLineageFollowsLateAncestors: spans arrive in any order, so an
// agent's loop span can land before the span between it and its parent
// agent. The roster must not freeze that agent at the root once the missing
// link arrives -- and must not rebuild on every unrelated span either.
func TestAgentsLineageFollowsLateAncestors(t *testing.T) {
	const (
		rootID byte = iota + 1
		chiefID
		toolCallID
		workerID
		otherID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(chiefID, "chief", "agent", spanID(rootID)),
		// The tool call it was spawned under has not arrived yet.
		agentLoopSnapshot(workerID, "worker", "worker", spanID(toolCallID)),
	})
	requireParents(t, db, map[string]string{"chief": "", "worker": ""})

	before := db.Agents()
	db.ImportSnapshots([]SpanSnapshot{agentTestSpan(otherID, "unrelated", spanID(rootID))})
	if after := db.Agents(); &after[0] != &before[0] {
		t.Fatal("an unrelated span invalidated the roster")
	}

	db.ImportSnapshots([]SpanSnapshot{agentTestSpan(toolCallID, "spawn", spanID(chiefID))})
	requireParents(t, db, map[string]string{"chief": "", "worker": "chief"})
}
