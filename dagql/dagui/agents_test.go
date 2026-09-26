package dagui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

// agentLoopSnapshot builds a loop span for an agent instance, carrying the
// identity attributes the engine stamps at span start (post-ProcessAttribute).
// StartTime derives from id so importing in id order matches start order.
func agentLoopSnapshot(id byte, agentID, name string, parent SpanID) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:              spanID(id),
		TraceID:         TraceID{TraceID: trace.TraceID{1}},
		Name:            "agent: " + name,
		StartTime:       start,
		ParentID:        parent,
		Agent:           true,
		AgentID:         agentID,
		AgentName:       name,
		AgentCallDigest: "sha256:" + agentID,
	}
}

// testControl builds a complete control revision for an agent published in
// the given trace, the way the engine emits one on every lifecycle change.
func testControl(traceID byte, handle, name string, revision int64, state string) agentcontrol.Agent {
	return agentcontrol.Agent{
		Key: agentcontrol.Key{
			Namespace: agentcontrol.Namespace{
				Session:     "session",
				Trace:       trace.TraceID{traceID}.String(),
				Incarnation: "incarnation",
			},
			Handle: handle,
		},
		Revision:   revision,
		Name:       name,
		CallDigest: "sha256:" + handle,
		State:      state,
		Digest:     "xxh3:tip",
		Activity:   time.Unix(revision, 0).UTC(),
	}
}

func exportControls(t *testing.T, db *DB, agents ...agentcontrol.Agent) {
	t.Helper()
	records := make([]sdklog.Record, 0, len(agents))
	for _, a := range agents {
		records = append(records, controlRecord(a.Record()))
	}
	if err := (DBLogExporter{db}).Export(context.Background(), records); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, _, err := db.AgentControl(); err != nil {
		t.Fatalf("fixture: control records rejected: %v", err)
	}
}

// TestAgentsRosterIsFlatAndUncontained is the property that separates the
// agent roster from the surfaced-services tree it is modelled on: a worker
// agent spawned inside a chief's tool call sits under a Boundary span, and
// must still be surfaced — that agent is precisely the one the roster exists
// to reveal.
func TestAgentsRosterIsFlatAndUncontained(t *testing.T) {
	const (
		rootID byte = iota + 1
		chiefID
		toolCallID
		workerID
	)
	db := NewDB()
	toolCall := agentTestSpan(toolCallID, "spawn(name:\"scout\")", spanID(chiefID))
	toolCall.Boundary = true
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(chiefID, "agent-chief", "interactive", spanID(rootID)),
		toolCall,
		agentLoopSnapshot(workerID, "agent-scout", "scout", spanID(toolCallID)),
	})

	agents := db.Agents()
	if len(agents) != 2 {
		t.Fatalf("expected chief and worker in the roster, got %d: %v", len(agents), agentNames(agents))
	}
	if agents[0].Name != "interactive" || agents[1].Name != "scout" {
		t.Fatalf("roster out of start order: %v", agentNames(agents))
	}
	if agents[1].CallDigest != "sha256:agent-scout" {
		t.Fatalf("worker lost its call digest, so it would not be addressable: %q", agents[1].CallDigest)
	}
}

// TestAgentsGroupsLoopSpansByInstance covers the reason the roster keys on the
// spawn-minted runtime handle rather than the span: a resume after a failure
// relaunches the loop under a NEW span, and both belong to one agent.
func TestAgentsGroupsLoopSpansByInstance(t *testing.T) {
	const (
		rootID byte = iota + 1
		firstLoopID
		secondLoopID
	)
	db := NewDB()
	first := agentLoopSnapshot(firstLoopID, "agent-a", "worker", spanID(rootID))
	first.EndTime = first.StartTime.Add(time.Second)
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		first,
		agentLoopSnapshot(secondLoopID, "agent-a", "worker", spanID(rootID)),
	})

	agents := db.Agents()
	if len(agents) != 1 {
		t.Fatalf("a relaunched loop should not appear as a second agent: %v", agentNames(agents))
	}
	if len(agents[0].Spans) != 2 {
		t.Fatalf("expected both loop spans on the agent, got %d", len(agents[0].Spans))
	}
	if got := agents[0].Span().ID; got != spanID(secondLoopID) {
		t.Fatalf("Span() should be the newest loop span, got %v", got)
	}
	if !agents[0].Live() {
		t.Fatal("agent whose newest loop span is still running should be live")
	}
}

// TestAgentControlInvalidatesRosterMemo guards the memoization: Agents()
// caches per DB mutation, so a control record that did not count as a
// mutation would leave every reader of the roster looking at a frozen state.
func TestAgentControlInvalidatesRosterMemo(t *testing.T) {
	const (
		rootID byte = iota + 1
		loopID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(loopID, "agent-a", "worker", spanID(rootID)),
	})

	// Prime the memo before any control record arrives.
	if got := db.Agents()[0].State; got != "" {
		t.Fatalf("expected no state before any control record, got %q", got)
	}

	exportControls(t, db, testControl(1, "agent-a", "worker", 1, "PAUSED"))

	agent := db.Agents()[0]
	if agent.State != "PAUSED" || agent.SnapshotDigest != "xxh3:tip" {
		t.Fatalf("roster served a stale memo: got %q / %q", agent.State, agent.SnapshotDigest)
	}
	if db.Spans.Map[spanID(loopID)].HasLogs {
		t.Fatal("control records are data, not log text: they must not flag a span as having logs")
	}
}

// Restore-plan fixtures. A resuming client holds TWO traces in one DB: the
// live session's own — the one it is publishing into, rooted at db.RootSpan —
// and the source trace it imported to restore from. Telling them apart is
// half of what RestorePlan does, so every fixture below stands up both.
const (
	liveRootID byte = iota + 1
	liveLoopID
	sourceRootID
	sourceLoopID
)

const (
	// liveTrace is what agentTestSpan and agentLoopSnapshot stamp;
	// sourceTrace is the imported trace's ID.
	liveTrace   byte = 1
	sourceTrace byte = 2

	scoutAgentID = "agent-scout"
)

// inTrace re-stamps a snapshot as belonging to another trace. Imported spans
// differ from live ones in nothing else — a re-hydrated agent republishes the
// same identity into the new trace — so the trace ID is the only thing that
// can distinguish them.
func inTrace(snapshot SpanSnapshot, traceID byte) SpanSnapshot {
	snapshot.TraceID = TraceID{TraceID: trace.TraceID{traceID}}
	return snapshot
}

// liveRootSnapshot is the live CLI's own root span: parentless and still
// running, so the DB takes it as db.RootSpan (which the fetch must leave
// alone, design §5.1.1) and every imported span is foreign relative to it.
func liveRootSnapshot() SpanSnapshot {
	root := agentTestSpan(liveRootID, "dagger agent --trace", SpanID{})
	root.EndTime = time.Time{}
	return root
}

// sourceRootSnapshot is the imported trace's root: a second parentless span,
// which is exactly what an import produces.
func sourceRootSnapshot() SpanSnapshot {
	return inTrace(agentTestSpan(sourceRootID, "dagger agent", SpanID{}), sourceTrace)
}

// sourceAgentDB stands up what a client holds just after the fetch: its own
// live root span, plus an imported trace with one agent's loop span in it and
// the given control revisions.
func sourceAgentDB(t *testing.T, controls ...agentcontrol.Agent) *DB {
	t.Helper()
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		liveRootSnapshot(),
		sourceRootSnapshot(),
		inTrace(agentLoopSnapshot(sourceLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace),
	})
	if len(controls) > 0 {
		exportControls(t, db, controls...)
	}
	return db
}

func onlyRestore(t *testing.T, plan []AgentRestore) AgentRestore {
	t.Helper()
	if len(plan) != 1 {
		t.Fatalf("expected one entry in the restore plan, got %d: %+v", len(plan), plan)
	}
	return plan[0]
}

// TestRestorePlanStateMapping is design §3.1's table, which exists because
// "restore them in the state they were in" is not a lookup: the trace's last
// word on an agent is often a stop that the session's own teardown performed,
// and a loop that was running when its session died cannot be restored as
// running at all.
func TestRestorePlanStateMapping(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		state, stopReason, preTeardown string
		want                           string
	}{
		{
			// Stop is terminal on purpose: the snapshot stays readable (that
			// is what makes a dismissed worker's WIP harvestable) but the
			// agent does not come back to life.
			name:  "a stop somebody asked for restores as a sealed tombstone",
			state: "STOPPED", stopReason: "EXPLICIT",
			want: "STOPPED",
		},
		{
			// The stop teardown performs says nothing about what the user
			// wanted, so it is not what gets restored.
			name:  "a teardown stop restores the state held before it",
			state: "STOPPED", stopReason: "SESSION", preTeardown: "PAUSED",
			want: "PAUSED",
		},
		{
			name:  "a teardown stop over a running loop restores idle",
			state: "STOPPED", stopReason: "SESSION", preTeardown: "RUNNING",
			want: "IDLE",
		},
		{
			name:  "a paused agent restores paused",
			state: "PAUSED",
			want:  "PAUSED",
		},
		{
			name:  "an idle agent restores idle",
			state: "IDLE",
			want:  "IDLE",
		},
		{
			// The one deliberate deviation from "exactly as it was", and it
			// is forced: the loop died with its session, so a roster that
			// redisplays it as running is lying. Its pending input re-steps
			// when the agent is next prompted.
			name:  "a running agent restores idle: the loop died with its session",
			state: "RUNNING",
			want:  "IDLE",
		},
		{
			// Same reason: nothing is left to answer the question.
			name:  "an agent parked on a question restores idle",
			state: "WAITING_INPUT",
			want:  "IDLE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testControl(sourceTrace, scoutAgentID, "scout", 1, tc.state)
			a.StopReason, a.PreTeardownState = tc.stopReason, tc.preTeardown
			db := sourceAgentDB(t, a)
			entry := onlyRestore(t, db.RestorePlan())
			if entry.Err != nil {
				t.Fatalf("entry refused: %v", entry.Err)
			}
			if entry.State != tc.want {
				t.Errorf("restore state = %q, want %q", entry.State, tc.want)
			}
			if entry.ID != scoutAgentID || entry.Name != "scout" {
				t.Errorf("entry lost the identity it re-hydrates under: %q / %q", entry.ID, entry.Name)
			}
			if entry.SnapshotDigest != "xxh3:tip" {
				t.Errorf("entry lost its resume anchor: %q", entry.SnapshotDigest)
			}
			// The roster keeps reporting what the trace last said; only the
			// restore projection maps it.
			if got := db.Agents()[0].State; got != tc.state {
				t.Errorf("roster state = %q, want the trace's last word, %q", got, tc.state)
			}
		})
	}
}

// TestRestorePlanRefusesAnAgentWithoutControl: an agent seen only through its
// loop spans comes from a trace that predates control records, which carries
// no lifecycle state or resume anchor to restore it from.
func TestRestorePlanRefusesAnAgentWithoutControl(t *testing.T) {
	db := sourceAgentDB(t)

	agent := db.Agents()[0]
	if agent.Control != nil || agent.State != "" || agent.SnapshotDigest != "" {
		t.Fatalf("lifecycle must come only from control records, got %+v", agent)
	}
	entry := onlyRestore(t, db.RestorePlan())
	if entry.Err == nil {
		t.Fatal("an agent with no control record must fail the restore rather than being guessed at")
	}
	if !strings.Contains(entry.Err.Error(), "scout") {
		t.Errorf("refusal does not name the agent: %v", entry.Err)
	}
	if entry.ID != scoutAgentID {
		t.Errorf("refused entry dropped the identity it did have: %+v", entry)
	}
}

// TestRestorePlanIgnoresTheLiveSessionsOwnAgents is the property that keeps a
// restore from duplicating what the session already has: an agent spawned in
// THIS session has a runtime entry already, and re-hydrating it is exactly
// what §4.1's existence check refuses.
func TestRestorePlanIgnoresTheLiveSessionsOwnAgents(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		liveRootSnapshot(),
		agentLoopSnapshot(liveLoopID, "agent-live", "interactive", spanID(liveRootID)),
	})
	exportControls(t, db, testControl(liveTrace, "agent-live", "interactive", 1, "IDLE"))

	if len(db.Agents()) != 1 {
		t.Fatal("fixture: the live agent should be on the roster")
	}
	if plan := db.RestorePlan(); len(plan) != 0 {
		t.Fatalf("an agent of the live session has nothing to restore: %+v", plan)
	}
}

// TestRestorePlanIsANoOpOnceRestored is the same property after a restore has
// run, and it is the sharp case: a re-hydrated agent republishes its identity
// and control record into the NEW trace (§4.5), so its roster entry looks
// exactly like a source-trace one. What distinguishes it is that it is
// published in the live session — which is also precisely the condition under
// which re-hydrating it again would error.
func TestRestorePlanIsANoOpOnceRestored(t *testing.T) {
	db := sourceAgentDB(t, testControl(sourceTrace, scoutAgentID, "scout", 1, "PAUSED"))
	if entry := onlyRestore(t, db.RestorePlan()); entry.State != "PAUSED" {
		t.Fatalf("fixture: expected a restorable paused agent, got %q", entry.State)
	}

	// The restore runs: rehydrate publishes an identity span in the live
	// trace, under the same runtime handle, carrying the same facts.
	db.ImportSnapshots([]SpanSnapshot{
		agentLoopSnapshot(liveLoopID, scoutAgentID, "scout", spanID(liveRootID)),
	})
	exportControls(t, db, testControl(liveTrace, scoutAgentID, "scout", 1, "PAUSED"))

	if len(db.Agents()) != 1 {
		t.Fatal("fixture: the restored agent's two lives should fold into one roster entry")
	}
	if plan := db.RestorePlan(); len(plan) != 0 {
		t.Fatalf("restoring twice must be a no-op, not a second re-hydration: %+v", plan)
	}
}

// agentTestSpan builds a plain (non-agent) span.
func agentTestSpan(id byte, name string, parent SpanID) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:        spanID(id),
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      name,
		StartTime: start,
		EndTime:   start.Add(time.Second),
		ParentID:  parent,
	}
}

func agentNames(agents []*AgentNode) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}
