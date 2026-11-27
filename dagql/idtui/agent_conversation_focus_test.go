package idtui

import (
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The live tree follows FOCUS (hack/designs/async-agents.md §5.1): switching
// agents on the roster strip switches the transcript rendered above it. The
// scoping rides the conversation-promotion axis rather than the zoom axis, so
// these also pin that focus never disturbs where the user had navigated to.

// focusConversationDB is rosterDB with a session root above the agents, one
// turn spoken by each, and a terminal error message on the failed scout, so a
// render has both ordinary conversation and failure content to scope.
func focusConversationDB(t *testing.T) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	calls, snapshots := rosterTrace()
	for digest, call := range calls {
		db.Calls[digest] = call
	}

	// A session root above both agents: the promotion host is the trace root,
	// and without this it would be whichever agent's loop span arrived first.
	root := dagui.SpanSnapshot{
		ID:        prettyTestSpanID(90),
		TraceID:   prettyTestTraceID(),
		Name:      "session",
		StartTime: time.Unix(99, 0),
	}
	for i := range snapshots {
		if !snapshots[i].ParentID.IsValid() {
			snapshots[i].ParentID = root.ID
		}
	}

	say := func(id byte, name string, parent dagui.SpanID, at int64) dagui.SpanSnapshot {
		return dagui.SpanSnapshot{
			ID:        prettyTestSpanID(id),
			TraceID:   prettyTestTraceID(),
			Name:      name,
			StartTime: time.Unix(at, 0),
			ParentID:  parent,
			LLMRole:   "assistant",
		}
	}
	snapshots = append(snapshots,
		// rosterTraceFor lays the chief's loop span at 1 and the scout's at 3.
		say(91, "chief-said", prettyTestSpanID(1), 101),
		say(92, "scout-said", prettyTestSpanID(3), 102),
		dagui.SpanSnapshot{
			ID:        prettyTestSpanID(93),
			TraceID:   prettyTestTraceID(),
			Name:      "agent failure",
			Message:   "received",
			StartTime: time.Unix(103, 0),
			EndTime:   time.Unix(104, 0),
			ParentID:  prettyTestSpanID(3),
			LLMRole:   "assistant",
			Status: sdktrace.Status{
				Code:        codes.Error,
				Description: "context limit reached",
			},
		},
	)
	// The terminal message and lifecycle record describe the same scout
	// failure. The message stays in its conversation after the loop ends.
	snapshots[2].Status = sdktrace.Status{Code: codes.Error, Description: "context limit reached"}

	db.ImportSnapshots(append([]dagui.SpanSnapshot{root}, snapshots...))
	publishAgentControl(t, db, "agent-scout", func(a *agentcontrol.Agent) {
		a.State, a.Failure = "FAILED", "context limit reached"
	})
	return db
}

// revealedNames is what the live tree would surface at the top level: the
// names of the spans promotion wired into the host.
func revealedNames(t *testing.T, fe *frontendPretty) map[string]bool {
	t.Helper()
	host := fe.db.RootSpan
	require.NotNil(t, host, "no trace root to promote onto")
	names := map[string]bool{}
	for span := range host.RevealedSpans.Iter() {
		names[span.Name] = true
	}
	return names
}

// TestLiveTreeFollowsFocusedAgent is the switcher's missing half: the strip
// moved the prompt, and now it moves the transcript above it too. The
// withdrawal is the load-bearing part -- promotion only ever ADDS into the
// host's revealed set, so a switch that failed to retract would render both
// agents' turns at once rather than switching between them.
func TestLiveTreeFollowsFocusedAgent(t *testing.T) {
	runFocusTest(t, func(t *testing.T) {
		handler := &focusShellHandler{}
		fe := focusTestFrontend(t, focusConversationDB(t), handler)

		// Unfocused: the whole trace, exactly as before the roster existed.
		fe.recalculateViewLocked()
		require.Equal(t, map[string]bool{
			"chief-said": true, "scout-said": true, "agent failure": true,
		}, revealedNames(t, fe), "with no agent focused the tree is the whole session")

		// Focus the scout (nav mode's jump key, per §5.1).
		pressNavKey(t, fe, '2')
		awaitFocus(t, fe, handler, "agent-scout")
		fe.recalculateViewLocked()
		require.Equal(t, map[string]bool{
			"scout-said": true, "agent failure": true,
		}, revealedNames(t, fe),
			"focusing the failed scout keeps its permanent error and retracts the previous scope")

		// And back: switching again must not leave the scout's turn behind.
		pressNavKey(t, fe, '1')
		awaitFocus(t, fe, handler, "agent-scout", "agent-chief")
		fe.recalculateViewLocked()
		require.Equal(t, map[string]bool{"chief-said": true}, revealedNames(t, fe),
			"switching back scopes to the chief alone")
	})
}

// topLevelNames is what the live tree actually renders at its top level: the
// spans of the rows view's body, by name.
func topLevelNames(fe *frontendPretty) map[string]bool {
	names := map[string]bool{}
	for _, tree := range fe.rowsView.Body {
		names[tree.Span.Name] = true
	}
	return names
}

// TestRevealedAgentWorkFollowsFocus covers the revealed spans that are not
// messages: an agent's /compact (stamped by the CLI with the agent it acted
// on, since it runs outside any loop span) and work revealed inside a loop.
// Reveal bubbling carries both to the host whoever they belong to, so unlike
// the promoted messages they used to render in every agent's transcript.
// They belong to their own agent's alone -- a worker's to the agent whose loop
// it runs beneath, too -- and a span of no agent shows in all.
func TestRevealedAgentWorkFollowsFocus(t *testing.T) {
	runFocusTest(t, func(t *testing.T) {
		db := focusConversationDB(t)
		reveal := func(id byte, name string, parent dagui.SpanID, owner string, at int64) dagui.SpanSnapshot {
			return dagui.SpanSnapshot{
				ID:         prettyTestSpanID(id),
				TraceID:    prettyTestTraceID(),
				Name:       name,
				StartTime:  time.Unix(at, 0),
				ParentID:   parent,
				Reveal:     true,
				AgentOwner: owner,
			}
		}
		db.ImportSnapshots([]dagui.SpanSnapshot{
			reveal(94, "/compact", prettyTestSpanID(90), "agent-scout", 105),
			reveal(95, "start mcp server", prettyTestSpanID(1), "", 106),
			reveal(96, "session step", prettyTestSpanID(90), "", 107),
			// A worker the scout spawned, beneath the scout's loop.
			{
				ID:        prettyTestSpanID(97),
				TraceID:   prettyTestTraceID(),
				Name:      "agent: worker",
				StartTime: time.Unix(108, 0),
				ParentID:  prettyTestSpanID(3),
				Agent:     true,
				AgentID:   "agent-worker",
				AgentName: "worker",
			},
			reveal(98, "worker mcp server", prettyTestSpanID(97), "", 109),
			// A helper the chief spawned, and the helper's own /compact: the
			// stamp hangs off the session, so only the helper's loop says it
			// runs beneath the chief.
			{
				ID:        prettyTestSpanID(99),
				TraceID:   prettyTestTraceID(),
				Name:      "agent: helper",
				StartTime: time.Unix(110, 0),
				ParentID:  prettyTestSpanID(1),
				Agent:     true,
				AgentID:   "agent-helper",
				AgentName: "helper",
			},
			reveal(100, "/compact helper", prettyTestSpanID(90), "agent-helper", 111),
		})
		db.SetPrimarySpan(prettyTestSpanID(90))

		handler := &focusShellHandler{}
		fe := focusTestFrontend(t, db, handler)
		// Focus by the handler's settled target, the source of truth: the
		// roster's jump keys number the nested agents too.
		focus := func(agentID string) {
			handler.mu.Lock()
			handler.target = agentID
			handler.mu.Unlock()
			fe.updateAgentRoster()
			fe.recalculateViewLocked()
		}

		fe.recalculateViewLocked()
		require.Equal(t, map[string]bool{
			"chief-said": true, "scout-said": true, "agent failure": true,
			"/compact": true, "start mcp server": true, "session step": true,
			"worker mcp server": true, "/compact helper": true,
		}, topLevelNames(fe), "with no agent focused the tree is the whole session")

		focus("agent-scout")
		require.Equal(t, map[string]bool{
			"scout-said": true, "agent failure": true,
			"/compact": true, "session step": true, "worker mcp server": true,
		}, topLevelNames(fe), "the scout's tree keeps its own and its worker's work, not the chief's")

		focus("agent-chief")
		require.Equal(t, map[string]bool{
			"chief-said": true, "start mcp server": true, "session step": true,
			"/compact helper": true,
		}, topLevelNames(fe), "the chief's tree keeps its helper's /compact, but not the scout's work")
	})
}

// TestFocusedAgentFailureRendersAboveInput verifies the durable failure message
// occupies the conversation stream rather than a transient label: it renders
// before the prompt and remains among the flowing lines used for scrollback.
func TestFocusedAgentFailureRendersAboveInput(t *testing.T) {
	const failureText = "context limit reached"
	handler := &focusShellHandler{target: "agent-scout"}
	fe := focusTestFrontend(t, focusConversationDB(t), handler)

	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(120)
	_, _ = logs.Write([]byte(failureText + "\n"))
	fe.logs.Logs[prettyTestSpanID(93)] = logs
	fe.updateAgentRoster()
	fe.recalculateViewLocked()

	lines := fe.tui.RenderLines()
	failureLine, promptLine := -1, -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, failureText):
			failureLine = i
		case strings.Contains(line, "⋈ "):
			promptLine = i
		}
	}
	require.NotEqual(t, -1, failureLine, "failed agent error is absent:\n%s", strings.Join(lines, "\n"))
	require.Greater(t, promptLine, failureLine,
		"failed agent error must render above the input:\n%s", strings.Join(lines, "\n"))
	require.NoError(t, fe.promptErr,
		"the durable transcript message must not depend on the transient prompt error label")
}

// TestFocusDoesNotDisturbZoom pins the axis choice. Focus could have been
// implemented by writing ZoomedSpan -- it is the existing "show me this
// subtree" mechanism -- but zoom is navigation the user drives with enter/esc,
// so esc would then silently un-follow the agent the prompt still addresses,
// and switching would discard wherever they had navigated to.
func TestFocusDoesNotDisturbZoom(t *testing.T) {
	runFocusTest(t, func(t *testing.T) {
		handler := &focusShellHandler{}
		fe := focusTestFrontend(t, focusConversationDB(t), handler)
		fe.recalculateViewLocked()

		zoomed := fe.ZoomedSpan
		pressNavKey(t, fe, '2')
		awaitFocus(t, fe, handler, "agent-scout")
		fe.recalculateViewLocked()

		require.Equal(t, zoomed, fe.ZoomedSpan, "focusing an agent must not move the zoom")
	})
}

// TestFocusedAgentWithNothingSaidKeepsSession covers the freshly-spawned case:
// an agent that has not surfaced a turn yet would otherwise promote an empty
// set onto a Passthrough host, i.e. blank the screen. Falling back to the
// session is the honest reading of "no transcript yet".
func TestFocusedAgentWithNothingSaidKeepsSession(t *testing.T) {
	runFocusTest(t, func(t *testing.T) {
		db := dagui.NewDB()
		calls, snapshots := rosterTrace()
		for digest, call := range calls {
			db.Calls[digest] = call
		}
		root := dagui.SpanSnapshot{
			ID:        prettyTestSpanID(90),
			TraceID:   prettyTestTraceID(),
			Name:      "session",
			StartTime: time.Unix(99, 0),
		}
		for i := range snapshots {
			if !snapshots[i].ParentID.IsValid() {
				snapshots[i].ParentID = root.ID
			}
		}
		// Only the chief has spoken; the scout was just spawned.
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID:        prettyTestSpanID(91),
			TraceID:   prettyTestTraceID(),
			Name:      "chief-said",
			StartTime: time.Unix(101, 0),
			ParentID:  prettyTestSpanID(1),
			LLMRole:   "assistant",
		})
		db.ImportSnapshots(append([]dagui.SpanSnapshot{root}, snapshots...))

		handler := &focusShellHandler{}
		fe := focusTestFrontend(t, db, handler)

		pressNavKey(t, fe, '2')
		awaitFocus(t, fe, handler, "agent-scout")
		fe.recalculateViewLocked()

		require.Equal(t, map[string]bool{"chief-said": true}, revealedNames(t, fe),
			"an agent with nothing surfaced yet shows the session, not a blank tree")
	})
}
