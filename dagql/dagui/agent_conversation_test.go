package dagui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// agentConversationDB builds the shape a staff session produces: a chief agent
// whose conversation contains a tool call (a Boundary message, as tool calls
// are), and a worker agent whose own loop span hangs beneath that call.
//
// It is the containment case the roster exists for -- the worker is buried
// under a Boundary -- so it is also the case any agent-scoped view has to get
// right.
func agentConversationDB(t *testing.T) *DB {
	t.Helper()
	const (
		rootID byte = iota + 1
		chiefLoopID
		chiefSaidID
		toolCallID
		workerLoopID
		workerSaidID
		chiefRepliedID
	)
	toolCall := messageSnapshot(toolCallID, "spawn(name:\"scout\")", spanID(chiefLoopID), "assistant")
	toolCall.Boundary = true

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(chiefLoopID, "agent-chief", "chief", spanID(rootID)),
		messageSnapshot(chiefSaidID, "chief-said", spanID(chiefLoopID), "user"),
		toolCall,
		agentLoopSnapshot(workerLoopID, "agent-scout", "scout", spanID(toolCallID)),
		messageSnapshot(workerSaidID, "worker-said", spanID(workerLoopID), "assistant"),
		messageSnapshot(chiefRepliedID, "chief-replied", spanID(chiefLoopID), "assistant"),
	})
	return db
}

func agentByID(t *testing.T, db *DB, id string) *AgentNode {
	t.Helper()
	for _, agent := range db.Agents() {
		if agent.ID == id {
			return agent
		}
	}
	t.Fatalf("agent %q not in roster", id)
	return nil
}

// TestSurfacedConversationForAgentScopesToOneAgent is the property the roster's
// switcher rests on: focusing a worker shows the worker's transcript and NOT
// its chief's, even though the worker's turns are also reachable from the
// chief's (they nest under the tool call that spawned it).
func TestSurfacedConversationForAgentScopesToOneAgent(t *testing.T) {
	db := agentConversationDB(t)

	worker := surfacedMessageNames(db.SurfacedConversationForAgent(agentByID(t, db, "agent-scout")))
	require.Equal(t, map[string]bool{"worker-said": true}, worker,
		"a focused worker shows its own turns only")

	chief := surfacedMessageNames(db.SurfacedConversationForAgent(agentByID(t, db, "agent-chief")))
	require.True(t, chief["chief-said"] && chief["chief-replied"],
		"the chief's own turns are in its conversation: %v", chief)
	require.True(t, chief["spawn(name:\"scout\")"],
		"the tool call is part of the chief's conversation: %v", chief)
	require.True(t, chief["worker-said"],
		"a sub-agent's turns still roll up beneath the call that spawned them: %v", chief)
}

// TestSurfacedConversationForAgentSpansRelaunch covers the reason this is keyed
// on the AGENT and not on AgentNode.Span(): a resume after a failure relaunches
// the loop under a fresh span, and the turns from before the relaunch belong to
// the same conversation. Scoping to the newest loop span alone would silently
// drop them.
func TestSurfacedConversationForAgentSpansRelaunch(t *testing.T) {
	const (
		rootID byte = iota + 1
		firstLoopID
		beforeID
		secondLoopID
		afterID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		agentTestSpan(rootID, "root", SpanID{}),
		agentLoopSnapshot(firstLoopID, "agent-retry", "retry", spanID(rootID)),
		messageSnapshot(beforeID, "before-failure", spanID(firstLoopID), "assistant"),
		// A resume relaunches the loop: same agent ID, second span.
		agentLoopSnapshot(secondLoopID, "agent-retry", "retry", spanID(rootID)),
		messageSnapshot(afterID, "after-resume", spanID(secondLoopID), "assistant"),
	})

	agent := agentByID(t, db, "agent-retry")
	require.Len(t, agent.Spans, 2, "the agent owns both loop spans")

	roots := db.SurfacedConversationForAgent(agent)
	got := make([]string, len(roots))
	for i, node := range roots {
		got[i] = node.Span.Name
	}
	require.Equal(t, []string{"before-failure", "after-resume"}, got,
		"both loop spans' turns, merged in conversation order")
}

// TestSurfacedConversationForAgentKeepsWholeTraceMemo guards the reason the
// agent view has a memo slot of its own. Both views are consulted on the same
// render -- the live tree scopes to the focused agent while the report stays
// zoom-scoped -- so sharing the single-entry, root-keyed whole-trace memo would
// have them evict each other every frame, and (worse) hand a caller the other
// one's answer.
func TestSurfacedConversationForAgentKeepsWholeTraceMemo(t *testing.T) {
	db := agentConversationDB(t)

	before := surfacedMessageNames(db.SurfacedConversation())
	require.True(t, before["chief-said"] && before["worker-said"],
		"whole trace holds everything: %v", before)

	scoped := surfacedMessageNames(db.SurfacedConversationForAgent(agentByID(t, db, "agent-scout")))
	require.Equal(t, map[string]bool{"worker-said": true}, scoped)

	after := surfacedMessageNames(db.SurfacedConversation())
	require.Equal(t, before, after,
		"the whole-trace view is unchanged by an agent-scoped read")
}

// TestDemoteConversationNodesFromWithdrawsPromotion is what makes switching
// agents possible at all: promotion is an ADD into a set that outlives the
// render, so without an explicit withdrawal the host accumulates every
// transcript it was ever pointed at and the switcher only ever adds.
func TestDemoteConversationNodesFromWithdrawsPromotion(t *testing.T) {
	db := agentConversationDB(t)
	host := db.Spans.Map[spanID(1)]
	require.NotNil(t, host)

	chief := db.SurfacedConversationForAgent(agentByID(t, db, "agent-chief"))
	db.PromoteConversationNodesTo(host, chief)
	require.NotZero(t, len(host.RevealedSpans.Order), "promotion reveals the chief's turns")

	// The nested half: the worker's turn is revealed under the tool call, not
	// under the host, so a withdrawal that only cleared the host would leave it.
	toolCall := db.Spans.Map[spanID(4)]
	require.NotNil(t, toolCall)
	require.NotZero(t, len(toolCall.RevealedSpans.Order),
		"the sub-agent's turn is revealed under the tool call")

	db.DemoteConversationNodesFrom(host, chief)
	require.Zero(t, len(host.RevealedSpans.Order), "withdrawal clears the host")
	require.Zero(t, len(toolCall.RevealedSpans.Order),
		"withdrawal reaches nested reveals too")
}
