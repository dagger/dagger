package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// TestAttributionHeader pins the deterministic, model-facing header forms
// (hack/designs/agent-messaging.md §4.1). Determinism is load-bearing:
// replay recordings compare wire text byte for byte, which is why headers
// carry per-runtime ordinals rather than minted message IDs.
func TestAttributionHeader(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", (*LLMMessageOrigin)(nil).AttributionHeader())
	require.Equal(t, "", (&LLMMessageOrigin{Kind: LLMMessageOriginUser}).AttributionHeader())
	require.Equal(t,
		`[message #3 from agent "scout"]`,
		(&LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "scout", Ref: "#3"}).AttributionHeader())
	require.Equal(t,
		`[reply from agent "chief" to your message #2]`,
		(&LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "chief", Ref: "#5", ReplyTo: "#2"}).AttributionHeader())
	require.Equal(t,
		`[event from agent "scout"]`,
		(&LLMMessageOrigin{Kind: LLMMessageOriginEvent, AgentName: "scout"}).AttributionHeader())
	require.Equal(t,
		`[reply to your message #4]`,
		(&LLMMessageOrigin{Kind: LLMMessageOriginUser, ReplyTo: "#4"}).AttributionHeader())
}

// TestRenderMessagesForModel covers the request-build-time render: the
// stored history keeps clean text plus structured origin, and only the wire
// form carries the header.
func TestRenderMessagesForModel(t *testing.T) {
	t.Parallel()

	plain := &LLMMessage{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hello"}},
	}
	attributed := &LLMMessage{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "what branch?"}},
		Origin:  &LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "scout", Ref: "#1"},
	}
	assistant := &LLMMessage{
		Role:    LLMMessageRoleAssistant,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "on it"}},
	}

	rendered := renderMessagesForModel([]*LLMMessage{plain, attributed, assistant})
	require.Len(t, rendered, 3)
	require.Same(t, plain, rendered[0], "messages without headers pass through untouched")
	require.Same(t, assistant, rendered[2])
	require.Equal(t,
		"[message #1 from agent \"scout\"]\n\nwhat branch?",
		rendered[1].TextContent())
	require.Equal(t, "what branch?", attributed.TextContent(),
		"the stored message must keep its clean text")

	// A history with no attributed messages is returned as-is, not copied.
	untouched := []*LLMMessage{plain, assistant}
	require.Equal(t, untouched, renderMessagesForModel(untouched))
}

func TestOriginOmittedFromChain(t *testing.T) {
	t.Parallel()

	// The unmarked common case: a plain user prompt.
	require.True(t, originOmittedFromChain(&LLMMessageOrigin{Kind: LLMMessageOriginUser}, "self"))
	// A plain self-send: an agent steering itself.
	require.True(t, originOmittedFromChain(&LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentID: "self"}, "self"))
	// Another agent's message records.
	require.False(t, originOmittedFromChain(&LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentID: "other"}, "self"))
	// A reply marker always records, whoever sent it.
	require.False(t, originOmittedFromChain(&LLMMessageOrigin{Kind: LLMMessageOriginUser, ReplyTo: "#2"}, "self"))
	// Events always record.
	require.False(t, originOmittedFromChain(&LLMMessageOrigin{Kind: LLMMessageOriginEvent, AgentID: "other"}, "self"))
}

// twoAgentRegistry builds a registry holding runtimes for two named agents
// and returns caller contexts for each — the harness for waits-for guard
// tests.
func twoAgentRegistry(t *testing.T) (ars *AgentRuntimes, rtA, rtB *AgentRuntime, ctxA, ctxB context.Context) {
	t.Helper()
	base := context.Background()
	ctxA = testAgentContext(t, base, "agent-a", "chief")
	ctxB = testAgentContext(t, base, "agent-b", "scout")
	agentA, ok := AgentFromContext(ctxA)
	require.True(t, ok)
	agentB, ok := AgentFromContext(ctxB)
	require.True(t, ok)

	ars = NewAgentRuntimes()
	var err error
	rtA, err = ars.GetOrCreate(base, agentA)
	require.NoError(t, err)
	rtB, err = ars.GetOrCreate(base, agentB)
	require.NoError(t, err)
	return ars, rtA, rtB, ctxA, ctxB
}

// TestAgentWaitGuard covers hack/designs/agent-messaging.md §4.5: self-waits
// are refused unconditionally, a cycle-closing wait is refused with the named
// path, and releasing an edge reopens it.
func TestAgentWaitGuard(t *testing.T) {
	t.Parallel()

	ars, rtA, rtB, ctxA, ctxB := twoAgentRegistry(t)

	// Self-wait: always refused (async-agents §8's hazard, enforced).
	_, err := ars.beginAgentWait(ctxA, rtA, "awaiting the reply to message #1")
	require.ErrorContains(t, err, `agent "chief" cannot block on itself`)

	// A non-agent caller registers nothing and is never refused.
	release, err := ars.beginAgentWait(context.Background(), rtA, "waiting for IDLE")
	require.NoError(t, err)
	release()

	// chief → scout: fine.
	releaseA, err := ars.beginAgentWait(ctxA, rtB, "awaiting the reply to message #1")
	require.NoError(t, err)

	// scout → chief would close the cycle: refused, naming the whole path.
	_, err = ars.beginAgentWait(ctxB, rtA, "awaiting the reply to message #2")
	require.ErrorContains(t, err, "would deadlock")
	require.ErrorContains(t, err, `agent "scout" → agent "chief" (awaiting the reply to message #2)`)
	require.ErrorContains(t, err, `agent "scout" (awaiting the reply to message #1)`)

	// Releasing the chief's wait reopens the edge.
	releaseA()
	releaseB, err := ars.beginAgentWait(ctxB, rtA, "awaiting the reply to message #2")
	require.NoError(t, err)
	releaseB()
	// Release is idempotent.
	releaseB()
}

// TestResolveReply covers §4.2's reply correlation: an explicit reply
// settles the replied-to record in the SENDER's runtime, whichever token
// form names it, and a mistyped ref is refused loudly.
func TestResolveReply(t *testing.T) {
	t.Parallel()

	ars, rtA, _, _, _ := twoAgentRegistry(t) //nolint:dogsled

	// A question from scout sits consumed in chief's runtime, mid-turn.
	msgID, err := rtA.enqueue("what branch?", &LLMMessageOrigin{
		Kind: LLMMessageOriginAgent, AgentID: "agent-b", AgentName: "scout",
	})
	require.NoError(t, err)
	rtA.mu.Lock()
	rec := rtA.messages[msgID]
	rec.consumed = true
	require.Equal(t, "#1", rec.origin.Ref)
	rtA.mu.Unlock()

	// The chief replies: the record resolves with the reply text, so an
	// awaiter of the question gets the direct answer, not the turn end.
	chiefOrigin := &LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentID: "agent-a", AgentName: "chief"}
	ref, err := ars.resolveReply(chiefOrigin, "#1", "main")
	require.NoError(t, err)
	require.Equal(t, "#1", ref)
	rtA.mu.Lock()
	require.True(t, rec.resolved)
	require.Equal(t, "main", rec.reply)
	rtA.mu.Unlock()

	// Token forms: bare ordinal and full message ID both resolve.
	ref, err = ars.resolveReply(chiefOrigin, "1", "again")
	require.NoError(t, err)
	require.Equal(t, "#1", ref)
	ref, err = ars.resolveReply(chiefOrigin, msgID, "again")
	require.NoError(t, err)
	require.Equal(t, "#1", ref)

	// A ref naming nothing is a model mistyping — refused loudly.
	_, err = ars.resolveReply(chiefOrigin, "#7", "answer")
	require.ErrorContains(t, err, `agent "chief" has no message #7 to reply to`)

	// A non-agent sender has no runtime to resolve within: the marker
	// passes through for the recipient's pairing only.
	ref, err = ars.resolveReply(&LLMMessageOrigin{Kind: LLMMessageOriginUser}, "#9", "answer")
	require.NoError(t, err)
	require.Equal(t, "#9", ref)
}

// TestEventDelivery covers §4.3's delivery rules at the runtime level: a
// subscribed transition enqueues an event message with EVENT origin into the
// subscriber's mailbox, the subscribe-time level check fires for an
// already-reached state, and a stopped subscriber drops events rather than
// relaunching.
func TestEventDelivery(t *testing.T) {
	t.Parallel()

	base := context.Background()
	ctxA := testAgentContext(t, base, "agent-a", "chief")
	ctxB := testAgentContext(t, base, "agent-b", "scout")
	agentA, ok := AgentFromContext(ctxA)
	require.True(t, ok)
	agentB, ok := AgentFromContext(ctxB)
	require.True(t, ok)

	ars := NewAgentRuntimes()
	chief, err := ars.GetOrCreate(base, agentA)
	require.NoError(t, err)
	scout, err := ars.GetOrCreate(base, agentB)
	require.NoError(t, err)

	// Subscribe the chief to scout completions. Scout is inert (projects
	// IDLE), so the level check fires immediately — the guard against a
	// fast worker settling before the subscription lands.
	require.NoError(t, ars.Notify(base, agentB, agentA, []AgentState{AgentStateIdle, AgentStateFailed}))
	waitForMailbox(t, chief, 1)
	chief.mu.Lock()
	rec := chief.messages[chief.mailbox[0]]
	require.Equal(t, LLMMessageOriginEvent, rec.origin.Kind)
	require.Equal(t, "agent-b", rec.origin.AgentID)
	require.Equal(t, "scout", rec.origin.AgentName)
	require.Contains(t, rec.text, `Agent "scout" is now idle.`)
	chief.mu.Unlock()

	// An edge into a subscribed state fires once; unsubscribed states are
	// silent. RUNNING (via a fact mutation) is not subscribed; the return
	// to IDLE is. The turn's commit is what makes the idle edge news —
	// idleEventDue is set here exactly as a real drain's commitLast would.
	scout.testTransition(func() { scout.idleEventDue = true; scout.turnOpen = true })
	scout.testTransition(func() { scout.turnOpen = false })
	waitForMailbox(t, chief, 2)

	// An idle edge with NO new committed work — a tombstone relaunch that
	// found nothing to retry, a bare fact toggle — is suppressed: it would
	// re-announce a final reply the subscriber already heard (the stale
	// idle event of the resume-retry flow).
	scout.testTransition(func() { scout.turnOpen = true })
	scout.testTransition(func() { scout.turnOpen = false })
	waitForEventQueueDrained(t, scout)
	chief.mu.Lock()
	require.Len(t, chief.mailbox, 2, "an idle edge without new committed work must not re-announce the old reply")
	chief.mu.Unlock()

	// Self-subscription is refused.
	require.ErrorContains(t, ars.Notify(base, agentB, agentB, []AgentState{AgentStateIdle}),
		`agent "scout" cannot subscribe to its own lifecycle`)

	// A stopped subscriber drops events instead of relaunching: the
	// mailbox count stays put and the tombstone stays STOPPED.
	chief.mu.Lock()
	chief.transitionLocked(func() {
		chief.done = true
		chief.sealed = true
		chief.stopRequested = true
	})
	chief.mu.Unlock()
	scout.testTransition(func() { scout.idleEventDue = true; scout.turnOpen = true })
	scout.testTransition(func() { scout.turnOpen = false })
	waitForEventQueueDrained(t, scout)
	chief.mu.Lock()
	require.Len(t, chief.mailbox, 2, "a stopped subscriber must not receive events")
	require.Equal(t, AgentStateStopped, chief.stateLocked())
	chief.mu.Unlock()
}

// TestDrainWindowProjectsRunning pins the projection across the drain's
// pop-to-commit window: the mailbox empties at the pop but the turn opens
// only after the unlocked withPrompt Select, and the draining fact is what
// keeps the projection from transiently claiming IDLE mid-progress — a lie
// that let waitSettled return a stale reply and Reseed race the drain's
// commit. Pause still outranks it: an interrupt mid-drain parks.
func TestDrainWindowProjectsRunning(t *testing.T) {
	t.Parallel()

	base := context.Background()
	ctx := testAgentContext(t, base, "agent-d", "drainer")
	agent, ok := AgentFromContext(ctx)
	require.True(t, ok)

	ars := NewAgentRuntimes()
	rt, err := ars.GetOrCreate(base, agent)
	require.NoError(t, err)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.Equal(t, AgentStateIdle, rt.stateLocked())
	rt.draining = true
	require.Equal(t, AgentStateRunning, rt.stateLocked(),
		"the pop-to-commit drain window must project RUNNING")
	rt.paused = true
	require.Equal(t, AgentStatePaused, rt.stateLocked(),
		"pause takes priority over the drain window")
}

func waitForMailbox(t *testing.T, rt *AgentRuntime, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return len(rt.mailbox) >= want
	}, 5e9, 1e6, "expected %d mailbox entries", want)
}

func waitForEventQueueDrained(t *testing.T, rt *AgentRuntime) {
	t.Helper()
	require.Eventually(t, func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return len(rt.eventQueue) == 0 && !rt.eventDispatchRunning
	}, 5e9, 1e6, "expected the event queue to drain")
}

// TestEmitUserMessageSpanRecordsOrigin covers the telemetry half of §4.1's
// client-facing promise: a consumed message's recorded origin rides its
// "LLM prompt" span as dagger.io/llm.origin.* attributes, so frontends can
// attribute another agent's words (and render events compactly) for agents
// they merely observe. A plain user prompt stamps nothing, keeping
// pre-provenance spans unchanged.
func TestEmitUserMessageSpanRecordsOrigin(t *testing.T) {
	_, ctx := stateRecorderCtx(t)
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	ctx, parent := tp.Tracer("origin-attrs-test").Start(ctx, "turn")
	defer parent.End()

	emitUserMessageSpan(ctx, &LLMMessage{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "what branch?"}},
		Origin: &LLMMessageOrigin{
			Kind: LLMMessageOriginAgent, AgentID: "agent-b", AgentName: "scout", Ref: "#3",
		},
	}, "")
	emitUserMessageSpan(ctx, &LLMMessage{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hello"}},
	}, "")

	ended := spans.Ended()
	require.Len(t, ended, 2)

	attrsOf := func(span sdktrace.ReadOnlySpan) map[string]string {
		attrs := map[string]string{}
		for _, attr := range span.Attributes() {
			attrs[string(attr.Key)] = attr.Value.AsString()
		}
		return attrs
	}

	attributed := attrsOf(ended[0])
	require.Equal(t, telemetryattrs.LLMMessageOriginKindAgent, attributed[telemetryattrs.LLMMessageOriginKindAttr])
	require.Equal(t, "agent-b", attributed[telemetryattrs.LLMMessageOriginAgentIDAttr])
	require.Equal(t, "scout", attributed[telemetryattrs.LLMMessageOriginAgentNameAttr])
	require.Equal(t, "#3", attributed[telemetryattrs.LLMMessageOriginRefAttr])
	require.NotContains(t, attributed, telemetryattrs.LLMMessageOriginReplyToAttr)

	plain := attrsOf(ended[1])
	require.NotContains(t, plain, telemetryattrs.LLMMessageOriginKindAttr)
}
