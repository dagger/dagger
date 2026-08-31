package idtui

import (
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
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
	snapshots[2].AgentState = "FAILED"
	snapshots[2].Status = sdktrace.Status{Code: codes.Error, Description: "context limit reached"}

	db.ImportSnapshots(append([]dagui.SpanSnapshot{root}, snapshots...))
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
	handler := &focusShellHandler{}
	fe := focusTestFrontend(t, focusConversationDB(t), handler)

	// Unfocused: the whole trace, exactly as before the roster existed.
	fe.recalculateViewLocked()
	require.Equal(t, map[string]bool{
		"chief-said": true, "scout-said": true, "agent failure": true,
	}, revealedNames(t, fe), "with no agent focused the tree is the whole session")

	// Focus the scout (nav mode's jump key, per §5.1).
	pressNavKey(t, fe, '2')
	awaitFocus(t, handler, "agent-scout")
	fe.tui.Step() // drain the dispatch that settles focus
	fe.recalculateViewLocked()
	require.Equal(t, map[string]bool{
		"scout-said": true, "agent failure": true,
	}, revealedNames(t, fe),
		"focusing the failed scout keeps its permanent error and retracts the previous scope")

	// And back: switching again must not leave the scout's turn behind.
	pressNavKey(t, fe, '1')
	awaitFocus(t, handler, "agent-scout", "agent-chief")
	fe.tui.Step()
	fe.recalculateViewLocked()
	require.Equal(t, map[string]bool{"chief-said": true}, revealedNames(t, fe),
		"switching back scopes to the chief alone")
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
	handler := &focusShellHandler{}
	fe := focusTestFrontend(t, focusConversationDB(t), handler)
	fe.recalculateViewLocked()

	zoomed := fe.ZoomedSpan
	pressNavKey(t, fe, '2')
	awaitFocus(t, handler, "agent-scout")
	fe.recalculateViewLocked()

	require.Equal(t, zoomed, fe.ZoomedSpan, "focusing an agent must not move the zoom")
}

// TestFocusedAgentWithNothingSaidKeepsSession covers the freshly-spawned case:
// an agent that has not surfaced a turn yet would otherwise promote an empty
// set onto a Passthrough host, i.e. blank the screen. Falling back to the
// session is the honest reading of "no transcript yet".
func TestFocusedAgentWithNothingSaidKeepsSession(t *testing.T) {
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
	awaitFocus(t, handler, "agent-scout")
	fe.recalculateViewLocked()

	require.Equal(t, map[string]bool{"chief-said": true}, revealedNames(t, fe),
		"an agent with nothing surfaced yet shows the session, not a blank tree")
}
