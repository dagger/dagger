package idtui

import (
	"context"
	"io"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// The per-step UI refresh contract (hack/designs/async-agents.md §5.1
// follow-up): the engine publishes a control revision with a new snapshot
// digest on every commit, and the frontend folds those into step
// notifications for the shell handler -- which is what lets the status line
// and changes preview track a working agent step by step instead of going a
// whole turn stale. The interject hint rides the same signal: a mid-turn
// submit is absorbed at the next step boundary, so the boundary is also when
// the hint retires.

// stepShellHandler is focusShellHandler plus the per-step notification probe.
type stepShellHandler struct {
	focusShellHandler
	steps []string
}

func (h *stepShellHandler) AgentStepped(instanceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps = append(h.steps, instanceID)
}

func (h *stepShellHandler) stepped() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.steps...)
}

// stepTestFrontend is focusTestFrontend for the step-notification handler.
func stepTestFrontend(t *testing.T, db *dagui.DB, handler *stepShellHandler) *frontendPretty {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	term := tuist.NewHeadlessTerminal(120, 10)
	fe := newWithTerminal(io.Discard, db, term)
	fe.setupTUI()
	fe.startShell(context.Background(), handler)
	fe.tui.Step()
	return fe
}

// commitSnapshot publishes an agent's next control revision with a fresh
// conversation snapshot digest, the way the engine reports each commit, then
// runs the ingestion hook the exporters run.
func commitSnapshot(t *testing.T, fe *frontendPretty, handle, digest string) {
	t.Helper()
	publishAgentControl(t, fe.db, handle, func(a *agentcontrol.Agent) { a.Digest = digest })
	fe.notifyAgentSteps()
}

// TestSnapshotCommitNotifiesHandlerOncePerStep: every NEW snapshot digest is
// one step boundary -- notified exactly once, however many export batches
// re-observe it.
func TestSnapshotCommitNotifiesHandlerOncePerStep(t *testing.T) {
	handler := &stepShellHandler{}
	handler.target = "agent-chief"
	fe := stepTestFrontend(t, rosterDB(t), handler)

	require.Empty(t, handler.stepped(), "no commits yet, no notifications")

	commitSnapshot(t, fe, "agent-chief", "xxh3:step-one")
	require.Equal(t, []string{"agent-chief"}, handler.stepped())

	// The same digest observed again is not a new step.
	fe.notifyAgentSteps()
	require.Equal(t, []string{"agent-chief"}, handler.stepped())

	// The next commit is.
	commitSnapshot(t, fe, "agent-chief", "xxh3:step-two")
	require.Equal(t, []string{"agent-chief", "agent-chief"}, handler.stepped())

	// Background agents notify too -- the handler decides whose surfaces to
	// refresh, not the trace.
	commitSnapshot(t, fe, "agent-scout", "xxh3:scout-one")
	require.Equal(t, []string{"agent-chief", "agent-chief", "agent-scout"}, handler.stepped())
}

// TestInterjectShowsQueuedUntilStepBoundary: a message absorbed by an
// in-flight turn is on the record but invisible until the agent's next step
// boundary drains the mailbox -- so the frontend shows it queued until that
// boundary lands, and must NOT offer to "edit" it (it cannot be unsent).
func TestInterjectShowsQueuedUntilStepBoundary(t *testing.T) {
	handler := &stepShellHandler{}
	handler.target = "agent-chief"
	handler.absorb = true
	fe := stepTestFrontend(t, rosterDB(t), handler)

	fe.textInput.SetValue("also check the logs")
	fe.handleInputComplete()

	require.Equal(t, []string{"also check the logs"}, handler.submittedMessages())
	require.Equal(t, "also check the logs", fe.queuedMsgLabel.Message(),
		"a mid-turn submit must not look like the input ate it")
	require.True(t, fe.queuedMsgLabel.Sent())

	// Not recallable: alt+up recalls client-side queued messages, and this
	// one is already on the engine's record.
	require.False(t, pressEditlineKey(t, fe, uv.Key{Code: uv.KeyUp, Mod: uv.ModAlt}))
	require.Empty(t, fe.textInput.Value())

	// The keymap must not advertise editing it either.
	help := navKeyHelp(fe.keys(NewOutput(io.Discard)))
	require.NotContains(t, help, "edit queued")

	// Somebody ELSE's step boundary is not this agent's drain.
	commitSnapshot(t, fe, "agent-scout", "xxh3:scout-step")
	require.Equal(t, "also check the logs", fe.queuedMsgLabel.Message())

	// The focused agent commits: the message is on the record now (mailboxes
	// drain at step boundaries), so the hint retires.
	commitSnapshot(t, fe, "agent-chief", "xxh3:chief-step")
	require.Empty(t, fe.queuedMsgLabel.Message())
}

// TestInterjectHintClearsOnFocusSwitch: the hint describes a message sent to
// the agent being LEFT; keeping it pinned above another agent's prompt would
// attribute the queue to the wrong conversation.
func TestInterjectHintClearsOnFocusSwitch(t *testing.T) {
	handler := &stepShellHandler{}
	handler.target = "agent-chief"
	handler.absorb = true
	fe := stepTestFrontend(t, rosterDB(t), handler)

	fe.textInput.SetValue("one more thing")
	fe.handleInputComplete()
	require.Equal(t, "one more thing", fe.queuedMsgLabel.Message())

	require.True(t, pressEditlineKey(t, fe, uv.Key{Code: '2', Mod: uv.ModCtrl}))
	require.Empty(t, fe.queuedMsgLabel.Message())
}

// TestRosterKeepsStoppedAgentsReachable: a stopped agent never silently
// vanishes. STOPPED means something real -- a dismissed worker, whose
// snapshot can still be read (it merely errors on send) -- so the strip folds
// it into a "+N done" chip rather than spending a tab on it, but it stays
// counted there, the cycle still walks onto it, and focusing it brings its
// tab back. (Hiding stopped agents outright was tried, and reverted: see
// hack/designs/resume-from-trace.md §6.)
func TestRosterKeepsStoppedAgentsReachable(t *testing.T) {
	handler := &focusShellHandler{target: "agent-chief"}
	db := rosterDB(t)
	fe := focusTestFrontend(t, db, handler)
	require.Len(t, fe.agentRosterEntries(), 2)

	// The scout's runtime stops (dismissed, or replaced by a successor).
	publishAgentControl(t, db, "agent-scout", func(a *agentcontrol.Agent) {
		a.State, a.StopReason = "STOPPED", "EXPLICIT"
	})
	fe.updateAgentRoster()

	// Folded, not gone: the chip counts it, and the roster still holds it.
	view := fe.agentRosterView()
	require.Len(t, view.Tabs, 1)
	require.Equal(t, "chief", view.Tabs[0].Name)
	require.Equal(t, 1, view.Done)
	order := fe.agentRosterOrder()
	require.Len(t, order, 2)
	require.Equal(t, "scout", order[1].Name)
	require.Equal(t, "STOPPED", order[1].State)
	frame := strings.Join(fe.tui.Step(), "\n")
	require.Contains(t, frame, "+1 done")
	require.True(t, fe.agentRoster.Switchable(), "a folded agent is still somewhere to switch to")

	// The cycle walks onto it, and focus gives it its tab back.
	fe.enterNavMode()
	pressNavKey(t, fe, ']')
	require.Equal(t, "scout", focusedRosterName(t, fe))
	view = fe.agentRosterView()
	require.Len(t, view.Tabs, 2)
	require.Zero(t, view.Done)
	fe.updateAgentRoster()
	frame = strings.Join(fe.tui.Step(), "\n")
	require.Contains(t, frame, "scout")
}
