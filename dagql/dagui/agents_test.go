package dagui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

// newTestAgentStateRecord builds the state record the engine emits on each
// transition of an agent's projected state. waitingOn and stopReason ride
// every record, empty when they do not apply, exactly as the engine emits
// them — that is what lets a consumer folding latest-wins clear a stale one.
func newTestAgentStateRecord(span SpanID, state, waitingOn, stopReason string) sdklog.Record {
	return newTestLogRecord(trace.TraceID{1}, span.SpanID, "",
		otellog.String(telemetryattrs.AgentStateAttr, state),
		otellog.String(telemetryattrs.AgentWaitingOnAttr, waitingOn),
		otellog.String(telemetryattrs.AgentStopReasonAttr, stopReason),
	)
}

// newTestAgentSnapshotRecord builds the record the engine emits on every
// commit of an agent's conversation: the resume anchor, on its own record.
func newTestAgentSnapshotRecord(span SpanID, digest string) sdklog.Record {
	return newTestLogRecord(trace.TraceID{1}, span.SpanID, "",
		otellog.String(telemetryattrs.AgentSnapshotDigestAttr, digest),
	)
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
// spawn-minted instance ID rather than the span: a resume after a failure
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

// TestIngestAgentStateFoldsLatestWins covers the state channel: records are
// consumed as data rather than rendered as log text, the newest record wins,
// and an emptied waitingOn clears a question that has since been answered.
func TestIngestAgentStateFoldsLatestWins(t *testing.T) {
	const (
		rootID byte = iota + 1
		loopID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(loopID, "agent-a", "worker", spanID(rootID)),
	})

	ctx := context.Background()
	exp := DBLogExporter{db}
	if err := exp.Export(ctx, []sdklog.Record{
		newTestAgentStateRecord(spanID(loopID), "RUNNING", "", ""),
		newTestAgentStateRecord(spanID(loopID), "WAITING_INPUT", "ok to delete testdata/legacy?", ""),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	agent := db.Agents()[0]
	if agent.State != "WAITING_INPUT" || agent.WaitingOn != "ok to delete testdata/legacy?" {
		t.Fatalf("expected the parked question to be folded in, got %q / %q", agent.State, agent.WaitingOn)
	}
	if db.Spans.Map[spanID(loopID)].HasLogs {
		t.Fatal("state records are data, not log text: they must not flag the span as having logs")
	}

	// The agent is answered and resumes: the stale question must not linger.
	if err := exp.Export(ctx, []sdklog.Record{
		newTestAgentStateRecord(spanID(loopID), "RUNNING", "", ""),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	agent = db.Agents()[0]
	if agent.State != "RUNNING" || agent.WaitingOn != "" {
		t.Fatalf("expected the answered question to clear, got %q / %q", agent.State, agent.WaitingOn)
	}
}

// TestAgentStateInvalidatesRosterMemo guards the memoization: Agents() caches
// per DB mutation, so a state record that did not count as a mutation would
// leave every reader of the roster looking at a frozen state.
func TestAgentStateInvalidatesRosterMemo(t *testing.T) {
	const (
		rootID byte = iota + 1
		loopID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(loopID, "agent-a", "worker", spanID(rootID)),
	})

	// Prime the memo before the state arrives.
	if got := db.Agents()[0].State; got != "" {
		t.Fatalf("expected no state before any record, got %q", got)
	}

	if err := (DBLogExporter{db}).Export(context.Background(), []sdklog.Record{
		newTestAgentStateRecord(spanID(loopID), "PAUSED", "", ""),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	if got := db.Agents()[0].State; got != "PAUSED" {
		t.Fatalf("roster served a stale memo: expected PAUSED, got %q", got)
	}
}

// TestIngestAgentSnapshotFoldsLatestWins covers the resume anchor: the digest
// of the agent's last committed conversation arrives on its own record, on
// every commit, and the newest one wins. A consumer that folded only the
// first would re-hydrate a restored agent at the start of its life.
func TestIngestAgentSnapshotFoldsLatestWins(t *testing.T) {
	const (
		rootID byte = iota + 1
		loopID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(loopID, "agent-a", "worker", spanID(rootID)),
	})

	// Prime the memo before any record arrives, so a snapshot record that
	// failed to count as a mutation would serve a stale roster below.
	if got := db.Agents()[0].SnapshotDigest; got != "" {
		t.Fatalf("expected no snapshot digest before any record, got %q", got)
	}

	ctx := context.Background()
	exp := DBLogExporter{db}
	if err := exp.Export(ctx, []sdklog.Record{
		newTestAgentSnapshotRecord(spanID(loopID), "xxh3:seed"),
		newTestAgentSnapshotRecord(spanID(loopID), "xxh3:afterstep"),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	agent := db.Agents()[0]
	if agent.SnapshotDigest != "xxh3:afterstep" {
		t.Fatalf("expected the newest committed conversation, got %q", agent.SnapshotDigest)
	}
	if db.Spans.Map[spanID(loopID)].HasLogs {
		t.Fatal("snapshot records are data, not log text: they must not flag the span as having logs")
	}
}

// TestIngestAgentStopReason covers the fact that decides whether a trace can
// be restored at all: session teardown stops every agent, so a STOPPED record
// alone cannot tell a dismissal from a clean exit. The reason rides the
// terminal record, and — like the parked question — an empty one on a later
// record clears it rather than lingering.
func TestIngestAgentStopReason(t *testing.T) {
	const (
		rootID byte = iota + 1
		loopID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(loopID, "agent-a", "worker", spanID(rootID)),
	})

	ctx := context.Background()
	exp := DBLogExporter{db}
	if err := exp.Export(ctx, []sdklog.Record{
		newTestAgentStateRecord(spanID(loopID), "RUNNING", "", ""),
		newTestAgentStateRecord(spanID(loopID), "STOPPED", "", "SESSION"),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	agent := db.Agents()[0]
	if agent.State != "STOPPED" || agent.StopReason != "SESSION" {
		t.Fatalf("expected a teardown stop, got %q / %q", agent.State, agent.StopReason)
	}

	// A resumed FAILED agent that fails again must not keep reporting the
	// reason of a stop that no longer applies.
	if err := exp.Export(ctx, []sdklog.Record{
		newTestAgentStateRecord(spanID(loopID), "FAILED", "", ""),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}
	agent = db.Agents()[0]
	if agent.State != "FAILED" || agent.StopReason != "" {
		t.Fatalf("expected the stop reason to clear, got %q / %q", agent.State, agent.StopReason)
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
	sourceRetryLoopID
	sourceToolCallID
	sourceWorkerLoopID
)

const (
	// sourceTrace is the imported trace's ID; the live one is trace 1, which
	// is what agentTestSpan and agentLoopSnapshot stamp.
	sourceTrace byte = 2

	scoutAgentID = "agent-scout"
)

// inTrace re-stamps a snapshot as belonging to another trace. Imported spans
// differ from live ones in nothing else — a re-hydrated agent republishes the
// same identity, state and snapshot facts into the new trace — so the trace
// ID is the only thing that can distinguish them.
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
// live root span, plus an imported trace with one agent's loop span in it.
// The records land on that loop span, as the engine emits them.
func sourceAgentDB(t *testing.T, records ...sdklog.Record) *DB {
	t.Helper()
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		liveRootSnapshot(),
		sourceRootSnapshot(),
		inTrace(agentLoopSnapshot(sourceLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace),
	})
	exportAgentRecords(t, db, records...)
	return db
}

func exportAgentRecords(t *testing.T, db *DB, records ...sdklog.Record) {
	t.Helper()
	if len(records) == 0 {
		return
	}
	if err := (DBLogExporter{db}).Export(context.Background(), records); err != nil {
		t.Fatalf("export: %v", err)
	}
}

// scoutState builds a state record on the source agent's loop span.
func scoutState(state, waitingOn, stopReason string) sdklog.Record {
	return newTestAgentStateRecord(spanID(sourceLoopID), state, waitingOn, stopReason)
}

// scoutSnapshot builds a resume-anchor record on the source agent's loop span.
func scoutSnapshot(digest string) sdklog.Record {
	return newTestAgentSnapshotRecord(spanID(sourceLoopID), digest)
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
		name   string
		states []sdklog.Record
		want   string
	}{
		{
			// Stop is terminal on purpose: the snapshot stays readable (that
			// is what makes a dismissed worker's WIP harvestable) but the
			// agent does not come back to life.
			name:   "a stop somebody asked for restores as a sealed tombstone",
			states: []sdklog.Record{scoutState("RUNNING", "", ""), scoutState("STOPPED", "", "EXPLICIT")},
			want:   "STOPPED",
		},
		{
			// The stop teardown performs says nothing about what the user
			// wanted, so it is not what gets restored.
			name:   "a teardown stop restores the state held before it",
			states: []sdklog.Record{scoutState("PAUSED", "", ""), scoutState("STOPPED", "", "SESSION")},
			want:   "PAUSED",
		},
		{
			// A FAILED tombstone that teardown sealed is still a failure a
			// resume can retry.
			name:   "a teardown stop over a failure restores as failed",
			states: []sdklog.Record{scoutState("FAILED", "", ""), scoutState("STOPPED", "", "SESSION")},
			want:   "FAILED",
		},
		{
			name:   "a failure restores as failed, so resume retries it",
			states: []sdklog.Record{scoutState("RUNNING", "", ""), scoutState("FAILED", "", "")},
			want:   "FAILED",
		},
		{
			name:   "a paused agent restores paused",
			states: []sdklog.Record{scoutState("PAUSED", "", "")},
			want:   "PAUSED",
		},
		{
			name:   "an idle agent restores idle",
			states: []sdklog.Record{scoutState("IDLE", "", "")},
			want:   "IDLE",
		},
		{
			// The one deliberate deviation from "exactly as it was", and it
			// is forced: the loop died with its session, so a roster that
			// redisplays it as running is lying. Its pending input re-steps
			// when the agent is next prompted.
			name:   "a running agent restores idle: the loop died with its session",
			states: []sdklog.Record{scoutState("IDLE", "", ""), scoutState("RUNNING", "", "")},
			want:   "IDLE",
		},
		{
			// Same reason: nothing is left to answer the question.
			name:   "an agent parked on a question restores idle",
			states: []sdklog.Record{scoutState("WAITING_INPUT", "ok to delete testdata/legacy?", "")},
			want:   "IDLE",
		},
		{
			// The client crashed before any terminal record was exported (or
			// before any record at all was): absence reads as a crash, which
			// restores the same way RUNNING does.
			name:   "an agent with no state record at all restores idle",
			states: nil,
			want:   "IDLE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := sourceAgentDB(t, append(tc.states, scoutSnapshot("xxh3:tip"))...)
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
		})
	}
}

// TestRestorePlanSplitsDismissalFromTeardown is the split §4.4's stop reason
// exists for, seen from the client: session close stops every surviving
// runtime, so a dismissed worker and a merely torn-down one publish identical
// STOPPED records. Restoring both as live reverses the dismissal; restoring
// neither restores nothing at all from a cleanly closed session.
func TestRestorePlanSplitsDismissalFromTeardown(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		liveRootSnapshot(),
		sourceRootSnapshot(),
		inTrace(agentLoopSnapshot(sourceLoopID, "agent-dismissed", "scout", spanID(sourceRootID)), sourceTrace),
		inTrace(agentLoopSnapshot(sourceWorkerLoopID, "agent-survivor", "tests", spanID(sourceRootID)), sourceTrace),
	})
	exportAgentRecords(t, db,
		newTestAgentSnapshotRecord(spanID(sourceLoopID), "xxh3:dismissed"),
		newTestAgentStateRecord(spanID(sourceLoopID), "STOPPED", "", "EXPLICIT"),
		newTestAgentSnapshotRecord(spanID(sourceWorkerLoopID), "xxh3:survivor"),
		newTestAgentStateRecord(spanID(sourceWorkerLoopID), "IDLE", "", ""),
		// ...and then the session exits, which stops the survivor too.
		newTestAgentStateRecord(spanID(sourceWorkerLoopID), "STOPPED", "", "SESSION"),
	)

	plan := db.RestorePlan()
	if len(plan) != 2 {
		t.Fatalf("expected both agents in the plan, got %d: %+v", len(plan), plan)
	}
	dismissed, survivor := plan[0], plan[1]
	if dismissed.ID != "agent-dismissed" || survivor.ID != "agent-survivor" {
		t.Fatalf("plan out of roster order: %+v", plan)
	}
	if dismissed.State != "STOPPED" {
		t.Errorf("a dismissal must survive the restore, got %q", dismissed.State)
	}
	if survivor.State != "IDLE" {
		t.Errorf("a clean exit must not read as a dismissal, got %q", survivor.State)
	}
}

// TestRestorePlanLooksPastTheTeardownRecord covers the one thing latest-wins
// ingestion cannot answer: the teardown record overwrites the state the user
// actually left the agent in, so restoring it needs the record HISTORY.
func TestRestorePlanLooksPastTheTeardownRecord(t *testing.T) {
	db := sourceAgentDB(t,
		scoutState("IDLE", "", ""),
		scoutState("RUNNING", "", ""),
		scoutState("PAUSED", "", ""),
		scoutSnapshot("xxh3:tip"),
		scoutState("STOPPED", "", "SESSION"),
	)

	// The roster keeps reporting what the trace last said; only the restore
	// projection looks behind it.
	if got := db.Agents()[0].State; got != "STOPPED" {
		t.Fatalf("roster state = %q, want the trace's last word, STOPPED", got)
	}
	if entry := onlyRestore(t, db.RestorePlan()); entry.State != "PAUSED" {
		t.Fatalf("restore state = %q, want the state held before teardown, PAUSED", entry.State)
	}
}

// TestRestorePlanFoldsARelaunchedLoop covers an agent with two loop spans (a
// resume retry): the roster already unions them, and the restore must read
// the NEWEST loop's outcome rather than the failure it recovered from.
func TestRestorePlanFoldsARelaunchedLoop(t *testing.T) {
	first := inTrace(agentLoopSnapshot(sourceLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace)
	first.EndTime = first.StartTime.Add(time.Second)
	first.Status = sdktrace.Status{Code: codes.Error, Description: "provider refused the request"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		liveRootSnapshot(),
		sourceRootSnapshot(),
		first,
		inTrace(agentLoopSnapshot(sourceRetryLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace),
	})
	exportAgentRecords(t, db,
		scoutState("FAILED", "", ""),
		scoutSnapshot("xxh3:beforeretry"),
		// The retry relaunches the loop under a fresh span, and republishes
		// the conversation it picks back up.
		newTestAgentSnapshotRecord(spanID(sourceRetryLoopID), "xxh3:tip"),
		newTestAgentStateRecord(spanID(sourceRetryLoopID), "IDLE", "", ""),
	)

	entry := onlyRestore(t, db.RestorePlan())
	if entry.State != "IDLE" {
		t.Errorf("restore state = %q, want the newest loop's outcome, IDLE", entry.State)
	}
	if entry.SnapshotDigest != "xxh3:tip" {
		t.Errorf("restore anchored on a superseded conversation: %q", entry.SnapshotDigest)
	}
	if entry.Error != "" {
		t.Errorf("the failure the retry recovered from must not ride the restored entry: %q", entry.Error)
	}
}

// TestRestorePlanCarriesTheLoopError covers the other half of FAILED: the
// error is the loop span's status description, and rehydrate needs it to
// project FAILED at all (an empty one would project STOPPED, foreclosing the
// very retry the restore was asking for).
func TestRestorePlanCarriesTheLoopError(t *testing.T) {
	loop := inTrace(agentLoopSnapshot(sourceLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace)
	loop.EndTime = loop.StartTime.Add(time.Second)
	loop.Status = sdktrace.Status{Code: codes.Error, Description: "provider refused the request"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{liveRootSnapshot(), sourceRootSnapshot(), loop})
	exportAgentRecords(t, db, scoutState("FAILED", "", ""), scoutSnapshot("xxh3:tip"))

	entry := onlyRestore(t, db.RestorePlan())
	if entry.State != "FAILED" {
		t.Fatalf("restore state = %q, want FAILED", entry.State)
	}
	if entry.Error != "provider refused the request" {
		t.Errorf("restore lost the loop error: %q", entry.Error)
	}
}

// TestRestorePlanRefusesAnAgentWithNoAnchor covers §3.2's refusal: without a
// snapshot digest there is nothing to re-hydrate FROM, and the alternatives
// are all guesses — the seed would restore an amnesiac twin under the
// agent's own ID.
func TestRestorePlanRefusesAnAgentWithNoAnchor(t *testing.T) {
	db := sourceAgentDB(t, scoutState("IDLE", "", ""))

	entry := onlyRestore(t, db.RestorePlan())
	if entry.Err == nil {
		t.Fatal("an agent with no resume anchor must fail the restore rather than being rebuilt from its seed")
	}
	if !strings.Contains(entry.Err.Error(), "scout") {
		t.Errorf("refusal does not name the agent: %v", entry.Err)
	}
}

// TestRestorePlanRefusesAReasonlessStop covers §4.4's refusal, which is the
// same shape: guessing EXPLICIT loses a whole session, guessing SESSION
// resurrects every dismissal, so a STOPPED record with no reason is a trace
// this client cannot restore.
func TestRestorePlanRefusesAReasonlessStop(t *testing.T) {
	db := sourceAgentDB(t,
		scoutState("RUNNING", "", ""),
		scoutState("STOPPED", "", ""),
		scoutSnapshot("xxh3:tip"),
	)

	entry := onlyRestore(t, db.RestorePlan())
	if entry.Err == nil {
		t.Fatal("a STOPPED record with no reason must fail the restore rather than being guessed at")
	}
	if !strings.Contains(entry.Err.Error(), "scout") {
		t.Errorf("refusal does not name the agent: %v", entry.Err)
	}
	// The entry still carries what it could resolve, so a caller can name the
	// agent and (with --partial) skip just this one.
	if entry.ID != scoutAgentID || entry.SnapshotDigest != "xxh3:tip" {
		t.Errorf("refused entry dropped the facts it did have: %+v", entry)
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
	exportAgentRecords(t, db,
		newTestAgentStateRecord(spanID(liveLoopID), "IDLE", "", ""),
		newTestAgentSnapshotRecord(spanID(liveLoopID), "xxh3:live"),
	)

	if len(db.Agents()) != 1 {
		t.Fatal("fixture: the live agent should be on the roster")
	}
	if plan := db.RestorePlan(); len(plan) != 0 {
		t.Fatalf("an agent of the live session has nothing to restore: %+v", plan)
	}
}

// TestRestorePlanIsANoOpOnceRestored is the same property after a restore has
// run, and it is the sharp case: a re-hydrated agent republishes its
// identity, its state and its snapshot into the NEW trace (§4.5), so its
// roster entry looks exactly like a source-trace one. What distinguishes it
// is that some of its spans are the live session's — which is also precisely
// the condition under which re-hydrating it again would error.
func TestRestorePlanIsANoOpOnceRestored(t *testing.T) {
	db := sourceAgentDB(t, scoutState("PAUSED", "", ""), scoutSnapshot("xxh3:tip"))
	if entry := onlyRestore(t, db.RestorePlan()); entry.State != "PAUSED" {
		t.Fatalf("fixture: expected a restorable paused agent, got %q", entry.State)
	}

	// The restore runs: rehydrate publishes an identity span in the live
	// trace, under the same instance ID, carrying the same facts.
	db.ImportSnapshots([]SpanSnapshot{
		agentLoopSnapshot(liveLoopID, scoutAgentID, "scout", spanID(liveRootID)),
	})
	exportAgentRecords(t, db,
		newTestAgentStateRecord(spanID(liveLoopID), "PAUSED", "", ""),
		newTestAgentSnapshotRecord(spanID(liveLoopID), "xxh3:tip"),
	)

	if len(db.Agents()) != 1 {
		t.Fatal("fixture: the restored agent's two lives should fold into one roster entry")
	}
	if plan := db.RestorePlan(); len(plan) != 0 {
		t.Fatalf("restoring twice must be a no-op, not a second re-hydration: %+v", plan)
	}
}

// TestRestorePlanReportsTheEnclosingAgent covers §3.1(c)'s input: a worker's
// loop span is started under its chief's tool-call span, so nesting is
// readable from the DB — and the agent with no agent above it is the one to
// focus.
func TestRestorePlanReportsTheEnclosingAgent(t *testing.T) {
	toolCall := inTrace(agentTestSpan(sourceToolCallID, `spawn(name: "scout")`, spanID(sourceLoopID)), sourceTrace)
	toolCall.Boundary = true

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		liveRootSnapshot(),
		sourceRootSnapshot(),
		inTrace(agentLoopSnapshot(sourceLoopID, "agent-chief", "interactive", spanID(sourceRootID)), sourceTrace),
		toolCall,
		inTrace(agentLoopSnapshot(sourceWorkerLoopID, scoutAgentID, "scout", spanID(sourceToolCallID)), sourceTrace),
	})
	exportAgentRecords(t, db,
		newTestAgentSnapshotRecord(spanID(sourceLoopID), "xxh3:chief"),
		newTestAgentSnapshotRecord(spanID(sourceWorkerLoopID), "xxh3:scout"),
	)

	plan := db.RestorePlan()
	if len(plan) != 2 {
		t.Fatalf("expected chief and worker in the plan, got %d: %+v", len(plan), plan)
	}
	chief, worker := plan[0], plan[1]
	if chief.ParentAgentID != "" {
		t.Errorf("the chief has no agent above it, got %q", chief.ParentAgentID)
	}
	if worker.ParentAgentID != "agent-chief" {
		t.Errorf("worker's enclosing agent = %q, want agent-chief", worker.ParentAgentID)
	}
}

// TestRestorePlanTimestampsTheLastActivity covers the fact the other half of
// §3.1c's focus rule is made of: with several top-level agents the CLI focuses
// the most recently ACTIVE one, and roster order cannot answer that — it
// orders by when each agent first APPEARED. The two disagree in the ordinary
// case, which is why the fact is projected rather than inferred from the
// plan's order: a session's own conversation is the first agent to appear and
// usually the last to speak.
func TestRestorePlanTimestampsTheLastActivity(t *testing.T) {
	// The chief appears first and speaks last; the scout is spawned later and
	// finishes long before the session ends.
	chief := inTrace(agentLoopSnapshot(sourceLoopID, "agent-chief", "interactive", spanID(sourceRootID)), sourceTrace)
	chief.EndTime = chief.StartTime.Add(time.Hour)
	scout := inTrace(agentLoopSnapshot(sourceWorkerLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace)
	scout.EndTime = scout.StartTime.Add(time.Minute)

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{liveRootSnapshot(), sourceRootSnapshot(), chief, scout})
	exportAgentRecords(t, db,
		newTestAgentSnapshotRecord(spanID(sourceLoopID), "xxh3:chief"),
		newTestAgentSnapshotRecord(spanID(sourceWorkerLoopID), "xxh3:scout"),
	)

	plan := db.RestorePlan()
	if len(plan) != 2 {
		t.Fatalf("expected chief and scout in the plan, got %d: %+v", len(plan), plan)
	}
	planned, spoke := plan[0], plan[1]
	if planned.Name != "interactive" || spoke.Name != "scout" {
		t.Fatalf("plan order is by first appearance: %q then %q", planned.Name, spoke.Name)
	}
	if !planned.LastActivity.After(spoke.LastActivity) {
		t.Errorf("chief last active %v, scout %v: the agent that spoke last must sort last, "+
			"or focus follows the order agents appeared in", planned.LastActivity, spoke.LastActivity)
	}
	if !planned.LastActivity.Equal(chief.EndTime) {
		t.Errorf("last activity = %v, want the newest loop span's end (%v)",
			planned.LastActivity, chief.EndTime)
	}
}

// TestRestorePlanTimesAnUnendedLoopFromItsStart: a crashed session's loop
// spans are all sealed to one shared bound at import (§5.1.2), so ends alone
// would tie every agent of that session together. The start time is what is
// left to order them by, and it is the honest answer — nothing recorded when
// the work actually stopped.
func TestRestorePlanTimesAnUnendedLoopFromItsStart(t *testing.T) {
	loop := inTrace(agentLoopSnapshot(sourceLoopID, scoutAgentID, "scout", spanID(sourceRootID)), sourceTrace)
	loop.EndTime = time.Time{}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{liveRootSnapshot(), sourceRootSnapshot(), loop})
	exportAgentRecords(t, db, scoutSnapshot("xxh3:tip"))

	entry := onlyRestore(t, db.RestorePlan())
	if !entry.LastActivity.Equal(loop.StartTime) {
		t.Errorf("last activity = %v, want the loop's start (%v): a span that never ended "+
			"says nothing else about when its agent last did anything",
			entry.LastActivity, loop.StartTime)
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
