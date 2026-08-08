package dagui

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	otellog "go.opentelemetry.io/otel/log"
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

// newTestAgentStateRecord builds the state record the engine emits on each
// transition of an agent's projected state.
func newTestAgentStateRecord(span SpanID, state, waitingOn string) sdklog.Record {
	return newTestLogRecord(trace.TraceID{1}, span.SpanID, "",
		otellog.String(telemetryattrs.AgentStateAttr, state),
		otellog.String(telemetryattrs.AgentWaitingOnAttr, waitingOn),
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
		newTestAgentStateRecord(spanID(loopID), "RUNNING", ""),
		newTestAgentStateRecord(spanID(loopID), "WAITING_INPUT", "ok to delete testdata/legacy?"),
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
		newTestAgentStateRecord(spanID(loopID), "RUNNING", ""),
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
		newTestAgentStateRecord(spanID(loopID), "PAUSED", ""),
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	if got := db.Agents()[0].State; got != "PAUSED" {
		t.Fatalf("roster served a stale memo: expected PAUSED, got %q", got)
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
