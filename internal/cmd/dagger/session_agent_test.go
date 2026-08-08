package daggercmd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

// The two policies this slice is about are policies, not plumbing: WHO a
// submitted message goes to, and WHOSE business it is to stop a runtime. Both
// are exercised here against a fake runtime, with no engine — the point of
// hiding the handle behind agentRuntime (hack/designs/async-agents.md §5.1).

type fakeRuntime struct {
	mu         sync.Mutex
	sent       []string
	interrupts int
	resumes    int
	stops      int
	reseeds    int
	reseedErr  error
	state      dagger.AgentState
	delivered  chan string
}

var _ agentRuntime = (*fakeRuntime)(nil)

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		state:     dagger.AgentStateRunning,
		delivered: make(chan string, 8),
	}
}

func (f *fakeRuntime) SendMessage(_ context.Context, msg string) (agentMessage, error) {
	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	f.delivered <- msg
	return fakeMessage{}, nil
}

func (f *fakeRuntime) Resume(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes++
	return nil
}

func (f *fakeRuntime) Interrupt(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupts++
	return nil
}

func (f *fakeRuntime) WaitFor(context.Context, dagger.AgentState) error { return nil }

func (f *fakeRuntime) State(context.Context) (dagger.AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *fakeRuntime) setState(state dagger.AgentState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *fakeRuntime) SnapshotID(context.Context) (dagger.ID, error) { return "", nil }

func (f *fakeRuntime) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	return nil
}

func (f *fakeRuntime) Reseed(context.Context, *dagger.LLM) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reseedErr != nil {
		return f.reseedErr
	}
	f.reseeds++
	return nil
}

func (f *fakeRuntime) reseedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reseeds
}

func (f *fakeRuntime) counts() (sent, interrupts, stops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent), f.interrupts, f.stops
}

// awaitSend waits for a message to reach the runtime (Submit is
// fire-and-forget, so the send happens on its own goroutine).
func (f *fakeRuntime) awaitSend(t *testing.T) string {
	t.Helper()
	select {
	case msg := <-f.delivered:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the message to reach the runtime")
		return ""
	}
}

// awaitNoSend asserts nothing reaches the runtime.
func (f *fakeRuntime) awaitNoSend(t *testing.T) {
	t.Helper()
	select {
	case msg := <-f.delivered:
		t.Fatalf("unexpected message delivered: %q", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

type fakeMessage struct{}

func (fakeMessage) Delivery(context.Context) (dagger.AgentMessageDelivery, error) {
	return dagger.AgentMessageDeliverySteered, nil
}

func (fakeMessage) Await(context.Context) (string, error) { return "", nil }

// testSession builds a session with no dagger client or frontend: enough for
// the routing and ownership policies, which must not need either.
func testSession(t *testing.T, names ...string) (*LLMSession, []*sessionAgent) {
	t.Helper()
	s := &LLMSession{plumbingCtx: context.Background()}
	var agents []*sessionAgent
	for _, name := range names {
		a := s.newAgent(name)
		a.bindRuntime(newFakeRuntime(), "agent-"+name, "", true)
		s.agents = append(s.agents, a)
		agents = append(agents, a)
	}
	if len(agents) > 0 {
		s.target = agents[0]
	}
	return s, agents
}

func runtimeOf(t *testing.T, a *sessionAgent) *fakeRuntime {
	t.Helper()
	rt, ok := a.runtime().(*fakeRuntime)
	require.True(t, ok, "conversation has no fake runtime bound")
	return rt
}

// TestSubmitGoesToTheFocusedAgentNotTheBusyOne is the routing rule the whole
// split exists for: a message follows FOCUS. Before the split the frontend
// handed a submitted message to whichever agent owned the in-flight turn,
// which with a roster is routinely somebody else's conversation.
func TestSubmitGoesToTheFocusedAgentNotTheBusyOne(t *testing.T) {
	s, agents := testSession(t, "chief", "scout")
	chief, scout := agents[0], agents[1]

	// The scout is mid-turn; the chief (focused) is not.
	scout.beginTurn(func(error) {})

	// Nothing absorbs it: the focused conversation has no open turn, so the
	// caller must open one rather than steer the busy agent.
	require.False(t, s.SubmitToTarget("for the chief"))
	runtimeOf(t, scout).awaitNoSend(t)

	// Focus the scout: now its open turn absorbs the message.
	s.SetTarget(scout)
	require.True(t, s.SubmitToTarget("for the scout"))
	require.Equal(t, "for the scout", runtimeOf(t, scout).awaitSend(t))

	sent, _, _ := runtimeOf(t, chief).counts()
	require.Zero(t, sent, "the chief must not have received anything")
}

// TestSubmitBuffersWhileTheTurnIsStillOpening: sending the prompt takes
// engine round-trips, and a message typed during that window must join the
// turn being opened rather than open a rival one -- a rival submit would
// re-run reference attachment and auto-compaction, either of which can
// replace the LLM wholesale and stop the runtime mid-turn.
func TestSubmitBuffersWhileTheTurnIsStillOpening(t *testing.T) {
	s, _ := testSession(t)
	opening := s.newAgent("fresh")
	s.agents = append(s.agents, opening)
	s.SetTarget(opening)

	// No turn at all: nothing absorbs the message.
	require.False(t, s.SubmitToTarget("nobody home"))

	// A turn is opening, but has no runtime yet: accepted and buffered.
	opening.beginTurn(func(error) {})
	require.True(t, s.SubmitToTarget("typed while spawning"))

	// The submit that opened the turn flushes the buffer once its runtime
	// exists, so the message lands on the record behind the prompt.
	rt := newFakeRuntime()
	opening.bindRuntime(rt, "agent-fresh", "", true)
	opening.flushPending(rt)
	require.Equal(t, "typed while spawning", rt.awaitSend(t))
}

// TestSubmitNeedsATurn: with no turn open the message is not absorbed, so the
// caller opens one.
func TestSubmitNeedsATurn(t *testing.T) {
	s, agents := testSession(t, "chief")
	chief := agents[0]

	require.False(t, s.SubmitToTarget("no turn open"))

	chief.beginTurn(func(error) {})
	require.True(t, s.SubmitToTarget("absorbed"))
	require.Equal(t, "absorbed", runtimeOf(t, chief).awaitSend(t))
}

// TestInterruptTargetPreemptsTheFocusedAgent locks in Ctrl-C's addressing:
// it names the focused agent's runtime rather than re-pointing a cancel at
// whichever turn holds the client. An agent that is running but is not the
// one blocking the client used to be uninterruptible from the client at all.
func TestInterruptTargetPreemptsTheFocusedAgent(t *testing.T) {
	s, agents := testSession(t, "chief", "scout")
	chief, scout := agents[0], agents[1]

	// The scout holds the only in-flight turn, but the chief has focus.
	scout.beginTurn(func(error) {})

	require.True(t, s.InterruptTarget())
	require.Eventually(t, func() bool {
		_, interrupts, _ := runtimeOf(t, chief).counts()
		return interrupts == 1
	}, 5*time.Second, 10*time.Millisecond, "the focused agent's runtime must be interrupted")

	_, scoutInterrupts, _ := runtimeOf(t, scout).counts()
	require.Zero(t, scoutInterrupts, "the busy agent must be left alone")
}

// TestInterruptSkipsAnIdleRuntime: interrupt on an idle agent is equivalent to
// pause, so Ctrl-C on a quiet prompt -- which is mostly how it is used, to
// clear a half-typed line -- must not park the agent.
func TestInterruptSkipsAnIdleRuntime(t *testing.T) {
	s, agents := testSession(t, "chief")
	chief := agents[0]
	rt := runtimeOf(t, chief)
	rt.setState(dagger.AgentStateIdle)

	require.True(t, s.InterruptTarget())
	require.Never(t, func() bool {
		_, interrupts, _ := rt.counts()
		return interrupts > 0
	}, 200*time.Millisecond, 10*time.Millisecond)

	// Once it is genuinely working, the same keypress preempts it.
	rt.setState(dagger.AgentStateRunning)
	require.True(t, s.InterruptTarget())
	require.Eventually(t, func() bool {
		_, interrupts, _ := rt.counts()
		return interrupts == 1
	}, 5*time.Second, 10*time.Millisecond)
}

// TestInterruptTargetCancelsItsOwnTurn: with a turn in flight on the focused
// conversation, cancelling that turn's await is the interrupt -- WithPrompt
// then issues the server-side preempt and re-roots on the kept prefix, so
// interrupting twice would be wrong.
func TestInterruptTargetCancelsItsOwnTurn(t *testing.T) {
	s, agents := testSession(t, "chief")
	chief := agents[0]

	var canceled error
	chief.beginTurn(func(cause error) { canceled = cause })

	require.True(t, s.InterruptTarget())
	require.True(t, errors.Is(canceled, errAgentInterrupted))

	_, interrupts, _ := runtimeOf(t, chief).counts()
	require.Zero(t, interrupts, "the turn's own path issues the server-side interrupt")
}

// TestInterruptDropsMessagesBufferedDuringTurnOpen covers the pre-runtime queue:
// a follow-up accepted while spawn is opening must not race flushPending after
// Ctrl-C and become stale input on the newly created runtime.
func TestInterruptDropsMessagesBufferedDuringTurnOpen(t *testing.T) {
	s, _ := testSession(t)
	opening := s.newAgent("opening")
	s.agents = append(s.agents, opening)
	s.SetTarget(opening)

	var canceled error
	opening.beginTurn(func(cause error) { canceled = cause })
	require.True(t, s.SubmitToTarget("queued while spawning"))

	require.True(t, s.InterruptTarget())
	require.ErrorIs(t, canceled, errAgentInterrupted)

	// Even if the spawn finishes after Ctrl-C, flushPending has nothing stale
	// left to deliver.
	rt := newFakeRuntime()
	opening.bindRuntime(rt, "agent-opening", "", true)
	opening.flushPending(rt)
	rt.awaitNoSend(t)
}

// TestRewindWaitsForTheCanceledTurnAndPreservesFocus verifies the client seam
// around Agent.reseed: edit cancels only the focused turn, waits for its cleanup
// barrier, then reseeds the same runtime. A background agent is untouched.
func TestRewindWaitsForTheCanceledTurnAndPreservesFocus(t *testing.T) {
	s, agents := testSession(t, "chief", "scout")
	chief, scout := agents[0], agents[1]
	chiefRT, scoutRT := runtimeOf(t, chief), runtimeOf(t, scout)

	// Model WithPrompt's ordering: its save is scheduled before endTurn. Hold
	// that save open so the test proves rewind drains it as well as the turn.
	saveStarted := make(chan struct{})
	saveRelease := make(chan struct{})
	s.onStep = func(*sessionAgent) {
		close(saveStarted)
		<-saveRelease
	}

	var canceled error
	chief.beginTurn(func(cause error) { canceled = cause })
	s.stepped(chief)
	<-saveStarted
	done := make(chan error, 1)
	go func() {
		done <- chief.rewindRuntime(context.Background(), nil)
	}()

	require.Eventually(t, func() bool {
		return errors.Is(canceled, errAgentRewound)
	}, time.Second, 10*time.Millisecond)
	require.Zero(t, chiefRT.reseedCount(), "reseed must wait for old turn cleanup")

	chief.endTurn()
	select {
	case err := <-done:
		t.Fatalf("rewind completed before the abandoned save drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(saveRelease)
	require.NoError(t, <-done)
	require.Equal(t, 1, chiefRT.reseedCount())
	require.Same(t, chiefRT, chief.runtime(), "rewind preserves instance identity")

	_, scoutInterrupts, scoutStops := scoutRT.counts()
	require.Zero(t, scoutInterrupts)
	require.Zero(t, scoutStops)
	require.Zero(t, scoutRT.reseedCount(), "rewind must not mutate another agent")

	// An attached focused agent is also somebody else's runtime. Rewind may
	// interrupt the work the user explicitly targeted, but branches locally by
	// detaching instead of reseeding that runtime.
	attached := s.newAgent("attached")
	attachedRT := newFakeRuntime()
	attached.bindRuntime(attachedRT, "agent-attached", "encoded", false)
	require.NoError(t, attached.rewindRuntime(context.Background(), nil))
	require.Nil(t, attached.runtime())
	_, attachedInterrupts, _ := attachedRT.counts()
	require.Equal(t, 1, attachedInterrupts)
	require.Zero(t, attachedRT.reseedCount())
}

// TestDropAgentStopsOnlyWhatTheSessionSpawned is the ownership rule: clearing
// a conversation you merely attached to must never stop somebody else's
// worker. dropAgent is the choke point every wholesale LLM replacement goes
// through, so this is the only place that guarantee can be enforced.
func TestDropAgentStopsOnlyWhatTheSessionSpawned(t *testing.T) {
	s, agents := testSession(t, "own")
	own := agents[0]

	attached := s.newAgent("someone-elses")
	attachedRT := newFakeRuntime()
	attached.bindRuntime(attachedRT, "agent-attached", "encoded-handle", false)
	s.agents = append(s.agents, attached)

	attached.dropAgent()
	require.Nil(t, attached.runtime(), "the handle is forgotten either way")
	require.Eventually(t, func() bool {
		_, _, stops := attachedRT.counts()
		return stops == 0
	}, 200*time.Millisecond, 10*time.Millisecond)

	ownRT := runtimeOf(t, own)
	own.dropAgent()
	require.Eventually(t, func() bool {
		_, _, stops := ownRT.counts()
		return stops == 1
	}, 5*time.Second, 10*time.Millisecond, "a runtime this session spawned is stopped")
}

// TestReseedKeepsTheInstance is the continuity rule: a wholesale LLM
// replacement reseeds the conversation's OWN runtime in place -- same
// instance, no stop, no successor -- instead of minting a STOPPED tombstone
// per replacement. The ownership rule carries over unchanged: a runtime the
// session merely attached to is somebody else's agent, so reseeding it is
// refused and the caller falls back to detaching.
func TestReseedKeepsTheInstance(t *testing.T) {
	s, agents := testSession(t, "own")
	own := agents[0]

	// Owned runtime: reseeded in place, still bound, never stopped.
	require.NoError(t, own.reseedAgent(nil))
	ownRT := runtimeOf(t, own)
	require.Equal(t, 1, ownRT.reseedCount())
	_, _, stops := ownRT.counts()
	require.Zero(t, stops, "reseed must not stop the runtime")
	require.NotNil(t, own.runtime(), "the runtime stays bound")

	// Attached runtime: refused. Replacing somebody else's conversation is
	// not this session's call; detaching (the caller's fallback) is the most
	// it may do.
	attached := s.newAgent("someone-elses")
	attachedRT := newFakeRuntime()
	attached.bindRuntime(attachedRT, "agent-attached", "encoded-handle", false)
	require.Error(t, attached.reseedAgent(nil))
	require.Zero(t, attachedRT.reseedCount())
	require.NotNil(t, attached.runtime(), "reseedAgent itself never detaches")

	// A conversation with no runtime has nothing to swap: success, so the
	// caller does NOT drop -- the next prompt submit spawns fresh anyway.
	fresh := s.newAgent("unspawned")
	require.NoError(t, fresh.reseedAgent(nil))
}

// TestFocusResolvesToAnExistingConversation: focusing an agent the session
// already drives -- its own, most importantly -- must retarget it rather than
// attach a second conversation to one runtime. That correlation is exactly
// what the runtime's instance ID buys.
func TestFocusResolvesToAnExistingConversation(t *testing.T) {
	s, agents := testSession(t, "chief", "scout")
	chief, scout := agents[0], agents[1]

	require.Equal(t, chief, s.Target())
	require.Equal(t, "agent-chief", s.TargetAgentID())

	// No encoded handle needed: the session already holds this one.
	require.NoError(t, s.Focus(context.Background(), "agent-scout", "scout", ""))
	require.Equal(t, scout, s.Target())
	require.Equal(t, "agent-scout", s.TargetAgentID())
	require.Len(t, s.agents, 2, "focusing a held agent must not add a conversation")

	// An agent nobody holds, with no rebuilt handle, cannot be attached to --
	// and the error must say so rather than silently focusing nothing.
	err := s.Focus(context.Background(), "agent-ghost", "ghost", "")
	require.ErrorContains(t, err, "not addressable")
	require.Equal(t, scout, s.Target(), "a failed attach leaves focus where it was")
}
