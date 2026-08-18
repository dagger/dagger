package idtui

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// The client half of roster focus (hack/designs/async-agents.md §5.1): who a
// submitted message goes to, what Ctrl-C preempts, and what a keypress does to
// the roster. The rule under test throughout is that all three follow FOCUS,
// never "whatever turn happens to be running".

// focusShellHandler is a ShellHandler that also implements the routing and
// focus contract the frontend probes for.
type focusShellHandler struct {
	stubShellHandler

	mu         sync.Mutex
	absorb     bool
	serial     bool
	submitted  []string
	interrupts int
	handled    []string
	target     string
	focused    []string
	focusErr   error
	queued     string
}

func (h *focusShellHandler) SubmitToTarget(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.absorb {
		return false
	}
	h.submitted = append(h.submitted, msg)
	return true
}

func (h *focusShellHandler) InterruptTarget() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.interrupts++
	return true
}

func (h *focusShellHandler) Serial(string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.serial
}

func (h *focusShellHandler) TargetAgentID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.target
}

func (h *focusShellHandler) FocusAgent(_ context.Context, agentID, _, encodedID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.focusErr != nil {
		return h.focusErr
	}
	if encodedID == "" {
		panic("focus without a rebuilt handle")
	}
	h.focused = append(h.focused, agentID)
	h.target = agentID
	return nil
}

func (h *focusShellHandler) Handle(_ context.Context, input string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handled = append(h.handled, input)
	return nil
}

func (h *focusShellHandler) QueueMessage(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queued = msg
}

func (h *focusShellHandler) DequeueMessage() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := h.queued
	h.queued = ""
	return msg
}

func (h *focusShellHandler) submittedMessages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.submitted...)
}

func (h *focusShellHandler) handledInput() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.handled...)
}

func (h *focusShellHandler) focusedAgents() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.focused...)
}

func (h *focusShellHandler) interruptCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interrupts
}

// focusTestFrontend brings up a headless shell frontend around handler.
func focusTestFrontend(t *testing.T, db *dagui.DB, handler *focusShellHandler) *frontendPretty {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	term := tuist.NewHeadlessTerminal(120, 10)
	fe := newWithTerminal(io.Discard, db, term)
	fe.setupTUI()
	fe.startShell(context.Background(), handler)
	fe.tui.Step()
	return fe
}

func pressEditlineKey(t *testing.T, fe *frontendPretty, key uv.Key) bool {
	t.Helper()
	return fe.interceptEditlineKey(tuist.Context{}, uv.KeyPressEvent(key))
}

// pressNavKey drives one nav-mode keypress, the way HandleKeyPress does when
// the span tree holds tuist's focus.
func pressNavKey(t *testing.T, fe *frontendPretty, r rune) {
	t.Helper()
	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Code: r, Text: string(r)}))
}

// awaitFocus waits for the handler to have been asked to focus exactly the
// given agents, in order -- focus is retargeted on the shell goroutine.
func awaitFocus(t *testing.T, handler *focusShellHandler, want ...string) {
	t.Helper()
	require.Eventually(t, func() bool {
		focused := handler.focusedAgents()
		if len(focused) != len(want) {
			return false
		}
		for i, id := range want {
			if focused[i] != id {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond,
		"waiting for focus %v, got %v", want, handler.focusedAgents())
}

// focusedRosterName is the agent the STRIP says is focused -- the client's
// belief, which moves on the keypress rather than when the handler catches up.
// It is what the user reads, and what the next roster key steps from.
func focusedRosterName(t *testing.T, fe *frontendPretty) string {
	t.Helper()
	for _, entry := range fe.agentRosterEntries() {
		if entry.Focused {
			return entry.Name
		}
	}
	return ""
}

// TestSubmitAsksTheTargetFirst covers the routing order: the focused
// conversation's own in-flight turn absorbs the message, and only when there
// is nothing to absorb it does the frontend open a new turn.
func TestSubmitAsksTheTargetFirst(t *testing.T) {
	handler := &focusShellHandler{absorb: true}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	fe.textInput.SetValue("steer the focused agent")
	fe.handleInputComplete()

	require.Equal(t, []string{"steer the focused agent"}, handler.submittedMessages())
	require.Empty(t, handler.handledInput(), "an absorbed message must not open a turn")

	// With nothing to absorb it, the same message opens a turn instead.
	handler.mu.Lock()
	handler.absorb = false
	handler.mu.Unlock()

	fe.textInput.SetValue("open a new turn")
	fe.handleInputComplete()
	require.Eventually(t, func() bool {
		handled := handler.handledInput()
		return len(handled) == 1 && handled[0] == "open a new turn"
	}, 5*time.Second, 10*time.Millisecond)
}

// TestMessageQueuesOnlyBehindASerialTurn: a prompt turn no longer blocks the
// client, so only the handler's single interpreter -- a shell command or a
// /command -- makes a submission wait. Before the split ANY running turn did,
// which with a roster meant you could not speak to a second agent at all.
func TestMessageQueuesOnlyBehindASerialTurn(t *testing.T) {
	handler := &focusShellHandler{serial: true}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	// A serial turn is in flight.
	fe.serialRunning = true
	fe.textInput.SetValue("wait your turn")
	fe.handleInputComplete()

	require.Empty(t, handler.handledInput())
	require.Equal(t, "wait your turn", fe.queuedMsgLabel.Message())

	// Nothing serial running: the message opens its own turn immediately,
	// even though another (prompt) turn may still be live.
	fe.serialRunning = false
	fe.turnsRunning = 1
	fe.textInput.SetValue("straight through")
	fe.handleInputComplete()
	require.Eventually(t, func() bool {
		handled := handler.handledInput()
		return len(handled) == 1 && handled[0] == "straight through"
	}, 5*time.Second, 10*time.Millisecond)
}

// TestCtrlCPreemptsTheFocusedAgent: Ctrl-C is an explicit interrupt addressed
// at the focused agent's runtime, not a cancel re-pointed at whichever turn
// holds the client. A serial turn is the exception -- that turn IS the client.
// In either case, queued input is abandoned with the interrupted work.
func TestCtrlCPreemptsTheFocusedAgent(t *testing.T) {
	handler := &focusShellHandler{}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	var canceled int
	fe.shellInterrupt = func(error) { canceled++ }

	fe.setInterjectHint("stale agent follow-up")
	pressEditlineKey(t, fe, uv.Key{Code: 'c', Mod: uv.ModCtrl})
	require.Equal(t, 1, handler.interruptCount())
	require.Zero(t, canceled, "the focused agent is interrupted server-side")
	require.Empty(t, fe.queuedMsgLabel.Message(), "Ctrl-C must retire the sent interject hint")

	// A shell command owns the client, so Ctrl-C cancels it as before and
	// drops the client-side message waiting behind it.
	fe.serialRunning = true
	fe.setQueuedMessage("stale serial follow-up")
	pressEditlineKey(t, fe, uv.Key{Code: 'c', Mod: uv.ModCtrl})
	require.Equal(t, 1, handler.interruptCount(),
		"no agent is interrupted while a shell command runs")
	require.Equal(t, 1, canceled)
	require.Empty(t, fe.queuedMsgLabel.Message())
	require.Empty(t, handler.DequeueMessage(), "Ctrl-C must drain the handler's queue")
}

// rosterDB builds a trace with two agents, each with a loop span carrying its
// identity plus the (internal) call span whose payload a client rebuilds the
// agent's handle from.
func rosterDB(t *testing.T) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	calls, snapshots := rosterTrace()
	for digest, call := range calls {
		db.Calls[digest] = call
	}
	db.ImportSnapshots(snapshots)
	return db
}

// singleAgentDB builds a visible but non-switchable roster fixture.
func singleAgentDB(t *testing.T) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	calls, snapshots := rosterTraceFor("chief")
	for digest, call := range calls {
		db.Calls[digest] = call
	}
	db.ImportSnapshots(snapshots)
	return db
}

// threeAgentDB is rosterDB with one more agent, so a cycle has somewhere to
// wrap around from.
func threeAgentDB(t *testing.T) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	calls, snapshots := rosterTraceFor("chief", "scout", "docs")
	for digest, call := range calls {
		db.Calls[digest] = call
	}
	db.ImportSnapshots(snapshots)
	return db
}

// rosterTrace is the raw material of rosterDB: the call payloads a client
// ingests, and the spans that carry them.
func rosterTrace() (map[string]*callpbv1.Call, []dagui.SpanSnapshot) {
	return rosterTraceFor("chief", "scout")
}

// rosterTraceFor is rosterTrace for a roster of a given size, in the order
// the agents were born -- which is the order the strip numbers them in.
func rosterTraceFor(names ...string) (map[string]*callpbv1.Call, []dagui.SpanSnapshot) {
	start := time.Unix(100, 0)
	traceID := prettyTestTraceID()
	calls := map[string]*callpbv1.Call{}
	var snapshots []dagui.SpanSnapshot

	for i, name := range names {
		id := "agent-" + name
		digest := "sha256:" + name
		calls[digest] = &callpbv1.Call{
			Digest: digest,
			Field:  "agent",
			Type:   &callpbv1.Type{NamedType: "Agent"},
		}
		snapshots = append(snapshots,
			dagui.SpanSnapshot{
				ID:        prettyTestSpanID(byte(2*i + 1)),
				TraceID:   traceID,
				Name:      "agent: " + name,
				StartTime: start.Add(time.Duration(i) * time.Second),
				Agent:     true,
				AgentID:   id,
				AgentName: name,
				// The identity the loop span publishes, including the digest
				// of the call that produced the agent value.
				AgentCallDigest: digest,
				AgentState:      "IDLE",
			},
			dagui.SpanSnapshot{
				ID:         prettyTestSpanID(byte(2*i + 2)),
				TraceID:    traceID,
				Name:       "agent(id:)",
				StartTime:  start.Add(time.Duration(i) * time.Second),
				CallDigest: digest,
			},
		)
	}
	return calls, snapshots
}

// TestFocusKeyRetargetsAndKeepsDrafts covers the switcher: a numbered jump
// retargets the prompt through a handle rebuilt from the trace, the
// half-typed line is parked against the agent being left, and the
// last-focused toggle brings it back.
func TestFocusKeyRetargetsAndKeepsDrafts(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, rosterDB(t), handler)

	entries := fe.agentRosterEntries()
	require.Len(t, entries, 2)
	require.True(t, entries[0].Focused, "the session's own agent starts focused")
	require.False(t, entries[1].Focused)

	// Half a sentence to the chief, then jump to the scout.
	fe.textInput.SetValue("half a thought")
	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModAlt}))
	require.Eventually(t, func() bool {
		focused := handler.focusedAgents()
		return len(focused) == 1 && focused[0] == "agent-scout"
	}, 5*time.Second, 10*time.Millisecond)
	fe.tui.Step()

	require.Equal(t, "", fe.textInput.Value(), "the scout has no draft yet")
	entries = fe.agentRosterEntries()
	require.True(t, entries[1].Focused, "focus follows the handler's target")

	// Type at the scout, then toggle back to the chief: each draft returns to
	// the agent it was meant for.
	fe.textInput.SetValue("for the scout")
	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: 'l', Mod: uv.ModAlt}))
	require.Eventually(t, func() bool {
		focused := handler.focusedAgents()
		return len(focused) == 2 && focused[1] == "agent-chief"
	}, 5*time.Second, 10*time.Millisecond)
	fe.tui.Step()
	require.Equal(t, "half a thought", fe.textInput.Value())

	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModAlt}))
	require.Eventually(t, func() bool {
		return len(handler.focusedAgents()) == 3
	}, 5*time.Second, 10*time.Millisecond)
	fe.tui.Step()
	require.Equal(t, "for the scout", fe.textInput.Value())
}

// TestUnaddressableAgentIsReadOnly: an agent whose handle cannot be rebuilt
// from this client's trace can be watched, not spoken to. The failure must be
// loud and the entry marked, rather than a focus that silently goes nowhere.
func TestUnaddressableAgentIsReadOnly(t *testing.T) {
	db := rosterDB(t)
	// Drop the scout's call payload: the frame never reached this client.
	delete(db.Calls, "sha256:scout")

	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, db, handler)

	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModAlt}))
	require.Empty(t, handler.focusedAgents(),
		"focus must not move to an agent with no handle")
	require.Error(t, fe.promptErr)

	entries := fe.agentRosterEntries()
	require.True(t, entries[1].ReadOnly)
	require.True(t, entries[0].Focused, "focus stays where it was")

	// And the strip says so, rather than rendering a normal-looking entry.
	// (Going through updateAgentRoster is the real path: the strip's content
	// comes from the trace, so something has to push it.)
	fe.updateAgentRoster()
	frame := strings.Join(fe.tui.Step(), "\n")
	require.Contains(t, frame, "scout·")
}

// TestNavDigitFailedFocusStaysInNav: a nav-mode digit that names an agent
// whose handle cannot be rebuilt reports the error and STAYS in nav mode.
// Handing the prompt back on the error path would type the user's next nav
// keys into the draft ("draft text that should survive323").
func TestNavDigitFailedFocusStaysInNav(t *testing.T) {
	db := rosterDB(t)
	delete(db.Calls, "sha256:scout")
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, db, handler)

	fe.enterNavMode(false)
	require.False(t, fe.editlineFocused)

	pressNavKey(t, fe, '2')
	require.Empty(t, handler.focusedAgents(), "focus must not move")
	require.Error(t, fe.promptErr, "the failure must be loud")
	require.False(t, fe.editlineFocused,
		"a failed focus must not hand the prompt back: the user's fingers are on nav keys")
}

// TestFocusRetriesAfterPayloadArrives: an entry marked unaddressable by a
// failed rebuild is a cached verdict, not a permanent one — payloads keep
// arriving as the trace streams, so naming the entry again retries, and a
// success clears both the mark and the stale error line.
func TestFocusRetriesAfterPayloadArrives(t *testing.T) {
	db := rosterDB(t)
	scoutCall := db.Calls["sha256:scout"]
	delete(db.Calls, "sha256:scout")
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, db, handler)

	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModAlt}))
	require.Empty(t, handler.focusedAgents())
	require.Error(t, fe.promptErr)
	require.True(t, fe.agentRosterEntries()[1].ReadOnly)

	// The missing payload lands late; the next explicit focus retries the
	// rebuild instead of refusing off the cached mark.
	db.Calls["sha256:scout"] = scoutCall
	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModAlt}))
	awaitFocus(t, handler, "agent-scout")
	fe.tui.Step()
	require.NoError(t, fe.promptErr, "a successful focus clears the stale error")
	require.False(t, fe.agentRosterEntries()[1].ReadOnly, "and the read-only mark with it")
}

// TestTurnEndFlipYieldsToActiveNavigation: the turn-end auto-flip to insert
// mode is a convenience for an idle user. Under active navigation it races
// the user's fingers — nav '2', 'i', arrow-key history recall land as
// literal prompt text — so a recent nav keypress suppresses it.
func TestTurnEndFlipYieldsToActiveNavigation(t *testing.T) {
	handler := &focusShellHandler{}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	// The submit-style flip: nav mode entered automatically.
	fe.enterNavMode(true)
	require.False(t, fe.editlineFocused)

	// Keys are being pressed right now: the turn's end must not yank the
	// keyboard into insert mode under them.
	fe.navKeyAt = time.Now()
	fe.handleShellDone(nil, false)
	require.False(t, fe.editlineFocused, "flip suppressed under active navigation")

	// Quiet keyboard: the flip lands as before.
	fe.navKeyAt = time.Now().Add(-2 * autoModeSwitchDebounce)
	fe.handleShellDone(nil, false)
	require.True(t, fe.editlineFocused, "an idle user still gets the prompt back")
}

// TestRosterRendersWhenAgentsAppear: the strip's content comes from the trace
// rather than from a setter, so a component that is never marked dirty renders
// once -- empty -- and never again. The trace push is what makes it visible at
// all.
func TestRosterRendersWhenAgentsAppear(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, dagui.NewDB(), handler)

	frame := strings.Join(fe.tui.Step(), "\n")
	require.NotContains(t, frame, "chief", "a session with no agents has no strip")

	// The agents arrive on the trace, exactly as the exporters deliver them.
	calls, snapshots := rosterTrace()
	for digest, call := range calls {
		fe.db.Calls[digest] = call
	}
	fe.db.ImportSnapshots(snapshots)
	fe.updateAgentRoster()

	frame = strings.Join(fe.tui.Step(), "\n")
	require.Contains(t, frame, "1 chief")
	require.Contains(t, frame, "2 scout")
}

// TestRosterStaysVisibleInNavMode is the precondition for nav mode's roster
// keys: the digits address entries by their position on the strip, so a strip
// that vanished on esc would leave the user aiming at something they cannot
// see.
func TestRosterStaysVisibleInNavMode(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, rosterDB(t), handler)
	fe.updateAgentRoster()

	fe.enterNavMode(false)
	frame := ansi.Strip(strings.Join(fe.tui.Step(), "\n"))
	require.Contains(t, frame, "i input mode", "sanity: this is nav mode's frame")
	require.Contains(t, frame, "1 chief")
	require.Contains(t, frame, "2 scout")
}

// TestNavDigitFocusesAndReturnsToPrompt covers the binding that always
// arrives: alt+<digit> is contested all the way up the stack (editors,
// browsers, terminal emulators), so nav mode offers the same jump on the bare
// digit. Focusing is a prelude to typing at the agent, so it hands the prompt
// back -- which is also what makes the per-agent draft worth keeping.
func TestNavDigitFocusesAndReturnsToPrompt(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, rosterDB(t), handler)

	fe.textInput.SetValue("half a thought")
	fe.enterNavMode(false)
	require.False(t, fe.editlineFocused)

	pressNavKey(t, fe, '2')
	awaitFocus(t, handler, "agent-scout")
	fe.tui.Step()

	require.True(t, fe.editlineFocused, "a roster key means 'go talk to that agent'")
	require.Equal(t, "", fe.textInput.Value(), "the scout has no draft yet")

	// The drafts follow the agents exactly as they do in prompt mode.
	fe.textInput.SetValue("for the scout")
	fe.enterNavMode(false)
	pressNavKey(t, fe, '1')
	awaitFocus(t, handler, "agent-scout", "agent-chief")
	fe.tui.Step()
	require.True(t, fe.editlineFocused)
	require.Equal(t, "half a thought", fe.textInput.Value())
}

// TestNavDigitWithoutAnEntryIsUnclaimed: nav mode's digits are unmodified
// keys, so they may only speak for the roster when there is a roster on
// screen to speak for. A digit past the end of the strip -- or any digit at
// all in a single-agent session, where the roster is visible but cannot
// switch -- must pass through untouched rather than silently dropping the user
// at a prompt.
func TestNavDigitWithoutAnEntryIsUnclaimed(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, rosterDB(t), handler)
	fe.enterNavMode(false)

	pressNavKey(t, fe, '3')
	require.Empty(t, handler.focusedAgents())
	require.False(t, fe.editlineFocused, "a digit naming nobody must not switch modes")

	solo := focusTestFrontend(t, singleAgentDB(t), &focusShellHandler{target: "agent-chief"})
	soloFrame := ansi.Strip(strings.Join(solo.tui.Step(), "\n"))
	require.Contains(t, soloFrame, "1 chief", "single-agent roster should remain visible")
	require.NotContains(t, soloFrame, "1…9 focus agent", "single-agent roster must not advertise switching")
	solo.enterNavMode(false)
	pressNavKey(t, solo, '1')
	require.False(t, solo.editlineFocused, "single-agent roster must not claim digit bindings")
}

// TestNavCycleWalksTheRoster covers [/]: one step per press, wrapping at both
// ends, WITHOUT leaving nav mode. A cycle is a survey verb -- you tap it until
// you land on the one you want -- so the presses here are consecutive, with no
// mode switch in between. That is the shape that catches both ways this can
// break: a cycle that returned to the prompt would type its own second press
// into the input, and one that counted from the handler's settled target would
// step to the same agent three times, since focus is retargeted asynchronously.
func TestNavCycleWalksTheRoster(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, threeAgentDB(t), handler)
	fe.enterNavMode(false)

	// Three taps in a row: chief -> scout -> docs -> back to chief.
	for _, want := range []string{"scout", "docs", "chief"} {
		pressNavKey(t, fe, ']')
		require.Equal(t, want, focusedRosterName(t, fe))
		require.False(t, fe.editlineFocused,
			"a cycle key must stay in nav mode, or its next press types itself")
	}

	// And backwards, off the front of the list.
	pressNavKey(t, fe, '[')
	require.Equal(t, "docs", focusedRosterName(t, fe))
	require.False(t, fe.editlineFocused)
}

// TestNavCycleCoalescesRequests: only one focus request is allowed out at a
// time, so a burst of taps asks the handler to attach to where the user landed
// rather than to every agent walked past -- and never twice to the same one,
// which is what a cycle counting from the stale target would do.
func TestNavCycleCoalescesRequests(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, threeAgentDB(t), handler)
	fe.enterNavMode(false)

	pressNavKey(t, fe, ']')
	pressNavKey(t, fe, ']')
	pressNavKey(t, fe, ']')
	require.Equal(t, "chief", focusedRosterName(t, fe), "the walk wrapped all the way round")

	// The first request goes out on the press; the rest ride on the client's
	// belief until it lands, and then one catch-up request is sent.
	require.Eventually(t, func() bool {
		return len(handler.focusedAgents()) >= 1
	}, 5*time.Second, 10*time.Millisecond)
	fe.tui.Step()
	require.Eventually(t, func() bool {
		focused := handler.focusedAgents()
		return len(focused) > 0 && focused[len(focused)-1] == "agent-chief"
	}, 5*time.Second, 10*time.Millisecond)
	fe.tui.Step()

	focused := handler.focusedAgents()
	require.Less(t, len(focused), 3, "taps behind an in-flight request coalesce")
	for i := 1; i < len(focused); i++ {
		require.NotEqual(t, focused[i-1], focused[i],
			"the same agent must never be requested twice in a row")
	}
}

// TestNavCycleWithNobodyToCycleTo: with one addressable agent the cycle has
// nowhere to go, and must say so rather than consuming the key to mime a
// switch that never happened.
func TestNavCycleWithNobodyToCycleTo(t *testing.T) {
	// One agent: the roster is visible as state, but cycle keys are not bound.
	solo := focusTestFrontend(t, singleAgentDB(t), &focusShellHandler{target: "agent-chief"})
	solo.enterNavMode(false)
	pressNavKey(t, solo, ']')
	pressNavKey(t, solo, '[')
	require.False(t, solo.editlineFocused, "no strip, no cycle bindings")
	require.False(t, solo.navCycleAgent(1), "the binding reports the no-op")
	require.False(t, solo.navCycleAgent(-1))

	// Two agents, one of them watch-only: the strip is up, but there is still
	// only one agent focus can move to -- itself.
	db := rosterDB(t)
	delete(db.Calls, "sha256:scout")
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, db, handler)
	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModAlt}))
	require.Error(t, fe.promptErr, "naming a read-only entry reports why")

	fe.setPromptError(nil)
	fe.enterNavMode(false)
	pressNavKey(t, fe, ']')
	require.Empty(t, handler.focusedAgents())
	require.False(t, fe.editlineFocused)
	// A cycle names nobody in particular, so it steps over the entry it
	// cannot address instead of answering with an error about an agent the
	// user never asked for.
	require.NoError(t, fe.promptErr)
}

// TestNavToggleReturnsToLastAgent: nav mode gets the last-focused toggle too,
// or it would be strictly weaker than the prompt at the very thing §5.1 calls
// the common case. Unlike the cycle it names a destination, so it hands the
// prompt back.
func TestNavToggleReturnsToLastAgent(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, rosterDB(t), handler)
	fe.enterNavMode(false)

	// Nothing focused before, so there is nothing to toggle back to yet.
	pressNavKey(t, fe, '`')
	require.Empty(t, handler.focusedAgents())
	require.False(t, fe.editlineFocused, "an unarmed toggle must not switch modes")

	pressNavKey(t, fe, '2')
	awaitFocus(t, handler, "agent-scout")
	fe.tui.Step()

	fe.enterNavMode(false)
	pressNavKey(t, fe, '`')
	awaitFocus(t, handler, "agent-scout", "agent-chief")
	fe.tui.Step()
	require.True(t, fe.editlineFocused, "the toggle names an agent, so it lands at the prompt")
	require.Equal(t, "chief", focusedRosterName(t, fe))
}

// TestNavRosterKeysAreAdvertised: the keys are only useful if the keymap bar
// names them, and only honest if it names them exactly when focus can switch.
func TestNavRosterKeysAreAdvertised(t *testing.T) {
	out := NewOutput(io.Discard)

	solo := focusTestFrontend(t, singleAgentDB(t), &focusShellHandler{target: "agent-chief"})
	solo.enterNavMode(false)
	require.NotContains(t, navKeyHelp(solo.keys(out)), "focus agent")

	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, rosterDB(t), handler)
	fe.enterNavMode(false)
	help := navKeyHelp(fe.keys(out))
	require.Contains(t, help, "1…9 focus agent")
	require.Contains(t, help, "[/] prev/next agent")
	require.NotContains(t, help, "last agent",
		"the toggle greys out until there is somewhere to toggle back to")

	// Once focus has moved, the toggle has a destination and lights up. (The
	// digit landed us at the prompt, so go back to nav mode to read its bar.)
	pressNavKey(t, fe, '2')
	awaitFocus(t, handler, "agent-scout")
	fe.tui.Step()
	fe.enterNavMode(false)
	require.Contains(t, navKeyHelp(fe.keys(out)), "` last agent")

	// With the second entry watch-only there is nobody to cycle to, so the
	// cycle greys out while the numbered jump -- which can still report why
	// that entry is unreachable -- stays.
	readOnly := rosterDB(t)
	delete(readOnly.Calls, "sha256:scout")
	ro := focusTestFrontend(t, readOnly, &focusShellHandler{target: "agent-chief"})
	require.True(t, pressEditlineKey(t, ro, uv.Key{Code: '2', Mod: uv.ModAlt}))
	ro.enterNavMode(false)
	help = navKeyHelp(ro.keys(out))
	require.Contains(t, help, "1…9 focus agent")
	require.NotContains(t, help, "prev/next agent")
}

// navKeyHelp renders the enabled bindings the way the keymap bar does.
func navKeyHelp(binds []key.Binding) string {
	var help []string
	for _, b := range binds {
		if !b.Enabled() {
			continue
		}
		help = append(help, b.Help().Key+" "+b.Help().Desc)
	}
	return strings.Join(help, " | ")
}
