package idtui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// Approvals are scoped to the call that asked for them: they render under its
// LLM tool call, never take focus on arrival, gain it when the user navigates
// onto that row, and give it back once answered.

// Span IDs clear of rosterTrace's, so the shell test can combine the two.
var (
	approvalRootID   = prettyTestSpanID(0x41)
	approvalToolID   = prettyTestSpanID(0x42)
	approvalExecID   = prettyTestSpanID(0x43)
	approvalPushID   = prettyTestSpanID(0x44)
	approvalOtherID  = prettyTestSpanID(0x45)
	approvalTool2ID  = prettyTestSpanID(0x46)
	approvalPush2ID  = prettyTestSpanID(0x47)
	approvalLateID   = prettyTestSpanID(0x48)
	approvalLateTool = prettyTestSpanID(0x49)
)

// approvalTraceSnapshots is a run with two tool calls, each nesting the
// internal execution span and the git push call that asks for approval, and a
// later sibling row the cursor starts on.
func approvalTraceSnapshots() []dagui.SpanSnapshot {
	start := time.Unix(100, 0)
	trace := prettyTestTraceID()
	return []dagui.SpanSnapshot{
		{ID: approvalRootID, TraceID: trace, Name: "dagger call", StartTime: start},
		{ID: approvalToolID, TraceID: trace, ParentID: approvalRootID, Name: "push",
			LLMRole: "assistant", LLMTool: "push", StartTime: start.Add(time.Second)},
		{ID: approvalExecID, TraceID: trace, ParentID: approvalToolID, Name: "push",
			LLMTool: "push", Internal: true, StartTime: start.Add(time.Second)},
		{ID: approvalPushID, TraceID: trace, ParentID: approvalExecID, Name: "GitRepository.push",
			StartTime: start.Add(time.Second)},
		{ID: approvalTool2ID, TraceID: trace, ParentID: approvalRootID, Name: "publish",
			LLMRole: "assistant", LLMTool: "publish", StartTime: start.Add(2 * time.Second)},
		{ID: approvalPush2ID, TraceID: trace, ParentID: approvalTool2ID, Name: "GitRepository.push",
			StartTime: start.Add(2 * time.Second)},
		{ID: approvalOtherID, TraceID: trace, ParentID: approvalRootID, Name: "other",
			LLMRole: "assistant", LLMTool: "other", StartTime: start.Add(3 * time.Second)},
	}
}

// approvalTestFrontend brings up a headless, non-shell frontend in nav mode
// over the approval trace, with the cursor following output onto the last row.
func approvalTestFrontend(t *testing.T) *frontendPretty {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	db.ImportSnapshots(approvalTraceSnapshots())
	db.SetPrimarySpan(approvalRootID)
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
	fe.setupTUI()
	fe.recalculateViewLocked()
	fe.tui.Step()
	require.Equal(t, approvalOtherID, fe.FocusedSpan, "precondition: the cursor follows output to the last row")
	require.True(t, fe.navigationFocused(), "precondition: navigating")
	return fe
}

// pushQuestion is how a pushApproval's question line reads.
const pushQuestion = "◆ Allow push to origin @ main?"

// pushApproval is a git push request shaped like the engine's: the bare
// question for frontends that show the requester themselves, and the full
// prompt naming who asks for yes/no fallbacks.
func pushApproval(spanID dagui.SpanID, persistentKey string) *prompt.ApprovalRequest {
	return &prompt.ApprovalRequest{
		Kind:     "git-push",
		Prompt:   `Allow push to origin @ main (requested by agent "chief", module "contributor", function "Contributor.push")?`,
		Question: "Allow push to origin @ main?",
		Subject:  "origin @ main",
		Requester: &prompt.ApprovalRequester{
			AgentName:   "chief",
			AgentHandle: "agent-chief",
			Module:      "contributor",
			Function:    "Contributor.push",
		},
		TraceId:       prettyTestTraceID().String(),
		SpanId:        spanID.String(),
		PersistentKey: persistentKey,
	}
}

type approvalResult struct {
	decision prompt.ApprovalDecision
	err      error
}

// startApproval asks for approval the way the prompt attachable does, on its
// own goroutine, and steps the UI until the request is pending.
func startApproval(t *testing.T, fe *frontendPretty, ctx context.Context, req *prompt.ApprovalRequest) (*pendingApproval, <-chan approvalResult) {
	t.Helper()
	results := make(chan approvalResult, 1)
	go func() {
		decision, err := fe.HandleApproval(ctx, req)
		results <- approvalResult{decision, err}
	}()
	var pending *pendingApproval
	require.Eventually(t, func() bool {
		fe.tui.Step()
		for _, a := range fe.approvals {
			if a.req == req {
				pending = a
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "the approval should become pending")
	return pending, results
}

func awaitApproval(t *testing.T, fe *frontendPretty, results <-chan approvalResult) approvalResult {
	t.Helper()
	var res approvalResult
	require.Eventually(t, func() bool {
		fe.tui.Step()
		select {
		case res = <-results:
			return true
		default:
			return false
		}
	}, 5*time.Second, 5*time.Millisecond, "HandleApproval should return")
	return res
}

func pressKeys(fe *frontendPretty, keys ...string) []string {
	for _, k := range keys {
		fe.tui.Inject(tuist.ParseKey(k))
	}
	return fe.tui.Step()
}

func frameText(lines []string) string {
	return ansi.Strip(strings.Join(lines, "\n"))
}

func lineIndex(t *testing.T, lines []string, substr string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), substr) {
			return i
		}
	}
	t.Fatalf("no line contains %q in:\n%s", substr, frameText(lines))
	return -1
}

func TestApprovalAttachesUnderToolCall(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))

	require.Equal(t, approvalToolID, a.anchor,
		"the approval renders under the tool call display row, past the internal exec span")

	frame := fe.tui.Step()
	tool := lineIndex(t, frame, "Push")
	header := lineIndex(t, frame, "◆ Allow push to origin @ main?")
	require.Less(t, tool, header, "the approval sits beneath its tool call")
	require.Less(t, header, lineIndex(t, frame, "Publish"), "and before the next row")
	require.Contains(t, frame[header+1], "contributor · ↓ to answer",
		"unfocused, it folds to two lines: the question, then the module and how to answer")
	require.Equal(t, header+2, lineIndex(t, frame, "Publish"))
	text := frameText(frame)
	require.NotContains(t, text, "chief", "under the agent's own tool call, the agent is not named")
	require.NotContains(t, text, "requested by", "the question does not name who asks")
	require.NotContains(t, text, "Deny", "the choices only show once focused")

	// Focused, it lists the choices one per line with their keys.
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))
	frame = fe.tui.Step()
	header = lineIndex(t, frame, "◆ Allow push to origin @ main?")
	require.Contains(t, frame[header+1], "contributor · Contributor.push")
	require.Contains(t, frame[header+3], "y  Allow once")
	require.Contains(t, frame[header+4], "s  Allow for this session")
	require.Contains(t, frame[header+5], "▶ n  Deny", "Deny is selected by default")
	require.Contains(t, frame[header+6], "↑↓ choose · enter confirm · esc leave")

	// A request without the bare question falls back to the full prompt.
	req := pushApproval(approvalPush2ID, "")
	req.Question = ""
	b, _ := startApproval(t, fe, t.Context(), req)
	require.Contains(t, strings.Join(fe.approvalLines(b, false, 0), "\n"), "(requested by agent")
}

func TestApprovalDoesNotTakeFocusOnArrival(t *testing.T) {
	fe := approvalTestFrontend(t)
	before := fe.tui.Focused()
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	fe.tui.Step()
	require.Same(t, before, fe.tui.Focused(), "an arriving approval must not move focus")
	require.False(t, fe.tui.IsFocused(a.view))

	// Even with the cursor sitting on its row when it arrives -- moved there by
	// a re-render rather than by the user -- it waits to be navigated to.
	fe.autoFocus = false
	fe.focus(fe.rows.BySpan[approvalTool2ID])
	b, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPush2ID, ""))
	fe.recalculateViewLocked()
	fe.tui.Step()
	require.Equal(t, approvalTool2ID, b.anchor)
	require.False(t, fe.tui.IsFocused(b.view), "an approval appearing under the cursor must not take focus")
	require.True(t, fe.navigationFocused())
}

func TestApprovalTakesFocusOnNavigation(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))

	// The approval is a stop of its own, just below its tool call:
	// other -> publish -> the approval.
	pressKeys(fe, "k", "k")
	require.Equal(t, approvalToolID, fe.FocusedSpan, "the cursor sits on the approval's tool call")
	require.True(t, fe.tui.IsFocused(a.view), "navigating onto the approval focuses it")
	require.Contains(t, frameText(fe.tui.Step()), "enter confirm", "a focused approval says how to answer")

	// Moving away hands focus back to navigation without answering: Deny is
	// selected on arrival, the bottom of the list, so ↓ leaves at once.
	pressKeys(fe, "j")
	require.Equal(t, approvalTool2ID, fe.FocusedSpan)
	require.False(t, fe.tui.IsFocused(a.view))
	require.Same(t, fe.spanTrees[approvalTool2ID], fe.tui.Focused())
	require.False(t, a.finished)

	// ↑ walks the choices, and past the first one leaves for the tool call row.
	pressKeys(fe, "k")
	require.True(t, fe.tui.IsFocused(a.view))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view), "still choosing")
	require.Equal(t, 0, a.selected)
	pressKeys(fe, "k")
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused(), "the tool call row is its own stop")
	pressKeys(fe, "j")
	require.True(t, fe.tui.IsFocused(a.view))
	require.Equal(t, approvalDefaultChoice(a.choices), a.selected, "each visit starts on Deny")

	// Answer, and focus returns to the row.
	pressKeys(fe, "y")
	res := awaitApproval(t, fe, results)
	require.NoError(t, res.err)
	require.Equal(t, prompt.APPROVAL_ALLOW_ONCE, res.decision)
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused(),
		"focus returns to the row the approval sat under")
	require.Empty(t, fe.approvals)
	require.NotContains(t, frameText(fe.tui.Step()), "Allow push to origin")
}

// The tool call row keeps its own keys: enter there zooms, as everywhere,
// rather than answering the approval beneath it with its default.
func TestApprovalLeavesRowKeysAlone(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	pressKeys(fe, "k", "k", "k", "k", "k")
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused())

	pressKeys(fe, "enter")
	require.False(t, a.finished, "enter on the row must not answer the approval")
	require.Equal(t, approvalToolID, fe.ZoomedSpan, "enter on the row zooms")
}

func TestApprovalEscLeavesItPending(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))

	pressKeys(fe, "esc")
	require.False(t, a.finished, "esc leaves the approval unanswered")
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused(), "and focus back on the row")
}

func TestApprovalAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want prompt.ApprovalDecision
	}{
		{"default is deny", []string{"enter"}, prompt.APPROVAL_DENY},
		{"once", []string{"y"}, prompt.APPROVAL_ALLOW_ONCE},
		{"session", []string{"s"}, prompt.APPROVAL_ALLOW_SESSION},
		{"always", []string{"a"}, prompt.APPROVAL_ALLOW_FOREVER},
		{"deny", []string{"n"}, prompt.APPROVAL_DENY},
		{"choose always", []string{"k", "enter"}, prompt.APPROVAL_ALLOW_FOREVER},
		{"choose session", []string{"up", "up", "enter"}, prompt.APPROVAL_ALLOW_SESSION},
		{"choose once", []string{"k", "k", "k", "enter"}, prompt.APPROVAL_ALLOW_ONCE},
		{"back to deny", []string{"k", "j", "enter"}, prompt.APPROVAL_DENY},
		{"sideways keys do nothing", []string{"l", "right", "h", "left", "enter"}, prompt.APPROVAL_DENY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe := approvalTestFrontend(t)
			a, results := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, "git-push:github.com/dagger/dagger"))
			pressKeys(fe, "k", "k")
			require.True(t, fe.tui.IsFocused(a.view))
			require.Contains(t, frameText(fe.tui.Step()), "a  Always allow contributor")
			pressKeys(fe, tc.keys...)
			res := awaitApproval(t, fe, results)
			require.NoError(t, res.err)
			require.Equal(t, tc.want, res.decision)
		})
	}
}

func TestApprovalAlwaysNeedsPersistentKey(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	require.Len(t, a.choices, 3)

	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))
	require.NotContains(t, frameText(fe.tui.Step()), "Always allow",
		"always is not offered without a key to remember it under")
	pressKeys(fe, "a")
	require.False(t, a.finished, "the always key answers nothing when always is not offered")
	require.True(t, fe.tui.IsFocused(a.view), "nor does it fall through to jump elsewhere")

	// The widest choice on offer is the session.
	pressKeys(fe, "k", "enter")
	res := awaitApproval(t, fe, results)
	require.Equal(t, prompt.APPROVAL_ALLOW_SESSION, res.decision)
}

func TestApprovalDangerHighlighted(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	fe := approvalTestFrontend(t)
	fe.profile = termenv.ANSI
	req := pushApproval(approvalPushID, "")
	req.Kind = "git-force-push"
	req.Prompt = `Allow force-push to origin @ main (requested by agent "chief", module "contributor", function "Contributor.push")?`
	req.Question = "Allow force-push to origin @ main?"
	req.Danger = true
	a, _ := startApproval(t, fe, t.Context(), req)
	lines := fe.approvalLines(a, false, 120)
	var question string
	for _, line := range lines {
		if strings.Contains(ansi.Strip(line), "force-push") {
			question = line
		}
	}
	require.NotEmpty(t, question)
	require.Contains(t, question, "\x1b[", "the question is styled")
	require.Contains(t, question, "31", "the destructive phrase is red")
}

func TestApprovalCancellation(t *testing.T) {
	fe := approvalTestFrontend(t)
	ctx, cancel := context.WithCancel(t.Context())
	a, results := startApproval(t, fe, ctx, pushApproval(approvalPushID, ""))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))

	cancel()
	res := awaitApproval(t, fe, results)
	require.ErrorIs(t, res.err, context.Canceled)
	require.Eventually(t, func() bool {
		fe.tui.Step()
		return len(fe.approvals) == 0
	}, 5*time.Second, 5*time.Millisecond)
	require.True(t, a.finished)
	require.NotContains(t, frameText(fe.tui.Step()), pushQuestion, "a canceled approval disappears")
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused(), "focus returns to the row")
}

func TestConcurrentApprovals(t *testing.T) {
	fe := approvalTestFrontend(t)
	first, firstResult := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	second, secondResult := startApproval(t, fe, t.Context(), pushApproval(approvalPush2ID, ""))
	require.Equal(t, approvalToolID, first.anchor)
	require.Equal(t, approvalTool2ID, second.anchor)
	frame := fe.tui.Step()
	require.Equal(t, 2, strings.Count(frameText(frame), pushQuestion))

	// Answer the second (nearer) one first; the first stays pending.
	pressKeys(fe, "k")
	require.True(t, fe.tui.IsFocused(second.view))
	// On a focused approval `a` answers, so alt+a is the way to the next one.
	pressKeys(fe, "alt+a")
	require.True(t, fe.tui.IsFocused(first.view))
	pressKeys(fe, "alt+a")
	require.True(t, fe.tui.IsFocused(second.view))
	pressKeys(fe, "s")
	res := awaitApproval(t, fe, secondResult)
	require.Equal(t, prompt.APPROVAL_ALLOW_SESSION, res.decision)
	require.False(t, first.finished)
	require.Len(t, fe.approvals, 1)

	// `a` jumps to the remaining one.
	pressKeys(fe, "a")
	require.Equal(t, approvalToolID, fe.FocusedSpan)
	require.True(t, fe.tui.IsFocused(first.view))
	pressKeys(fe, "n")
	res = awaitApproval(t, fe, firstResult)
	require.Equal(t, prompt.APPROVAL_DENY, res.decision)
	require.Empty(t, fe.approvals)
}

func TestApprovalFloatsUntilItsSpanArrives(t *testing.T) {
	fe := approvalTestFrontend(t)
	before := fe.tui.Focused()
	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalLateID, ""))
	require.False(t, a.anchor.IsValid(), "an unknown span leaves the approval floating")
	frame := fe.tui.Step()
	require.Contains(t, frameText(frame), pushQuestion, "a floating approval still shows")
	require.Same(t, before, fe.tui.Focused(), "and still does not take focus")

	// The span arrives under a new tool call: the approval moves there.
	start := time.Unix(100, 0).Add(4 * time.Second)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: approvalLateTool, TraceID: prettyTestTraceID(), ParentID: approvalRootID, Name: "late",
			LLMRole: "assistant", LLMTool: "late", StartTime: start},
		{ID: approvalLateID, TraceID: prettyTestTraceID(), ParentID: approvalLateTool, Name: "GitRepository.push",
			StartTime: start},
	})
	fe.recalculateViewLocked()
	frame = fe.tui.Step()
	require.Equal(t, approvalLateTool, a.anchor)
	require.Less(t, lineIndex(t, frame, "Late"), lineIndex(t, frame, pushQuestion))

	pressKeys(fe, "a", "y")
	res := awaitApproval(t, fe, results)
	require.Equal(t, prompt.APPROVAL_ALLOW_ONCE, res.decision)
}

func TestFloatingApprovalJump(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalLateID, ""))
	pressKeys(fe, "a")
	require.True(t, fe.tui.IsFocused(a.view), "the jump key focuses a floating approval")
	pressKeys(fe, "s")
	res := awaitApproval(t, fe, results)
	require.Equal(t, prompt.APPROVAL_ALLOW_SESSION, res.decision)
	require.True(t, fe.navigationFocused(), "focus returns to navigation")
}

// A jump only borrows the cursor: answering resumes following output when it
// was following, so new rows are not cropped below the approval's row.
func TestApprovalJumpResumesFollowingOutput(t *testing.T) {
	fe := approvalTestFrontend(t)
	require.True(t, fe.autoFocus, "precondition: following output")
	_, results := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	pressKeys(fe, "a", "y")
	awaitApproval(t, fe, results)
	require.True(t, fe.autoFocus)

	start := time.Unix(100, 0).Add(4 * time.Second)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: approvalLateTool, TraceID: prettyTestTraceID(), ParentID: approvalRootID, Name: "late",
			LLMRole: "assistant", LLMTool: "late", StartTime: start},
	})
	fe.recalculateViewLocked()
	require.Equal(t, approvalLateTool, fe.FocusedSpan, "the cursor follows new output again")
}

// A search jump moves the cursor off a floating approval, which must then
// stop taking keys: `n` means "next match" again, not "deny".
func TestSearchLeavesFloatingApproval(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalLateID, ""))
	pressKeys(fe, "a")
	require.True(t, fe.tui.IsFocused(a.view))

	pressKeys(fe, "/", "P", "u", "s", "h", "enter")
	require.Equal(t, approvalToolID, fe.FocusedSpan, "the search moved the cursor")
	require.False(t, fe.tui.IsFocused(a.view), "the approval no longer has focus")
	pressKeys(fe, "n")
	require.False(t, a.finished, "n searches rather than denying")
}

// Jumping to an approval whose call is inside the zoomed subtree keeps the
// zoom: the approval already shows there.
func TestApprovalJumpKeepsZoomOnItsCall(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	// Up past the publish row and the approval's three choices onto the tool
	// call, then zoom into it.
	pressKeys(fe, "k", "k", "k", "k", "k", "enter")
	require.Equal(t, approvalToolID, fe.ZoomedSpan)

	pressKeys(fe, "a")
	require.Equal(t, approvalToolID, fe.ZoomedSpan, "the zoom is kept")
	require.True(t, fe.tui.IsFocused(a.view))
	require.Contains(t, frameText(fe.tui.Step()), pushQuestion)
}

// An approval whose call has not arrived may yet land inside the zoom, so
// jumping to it keeps the zoom too.
func TestFloatingApprovalJumpKeepsZoom(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalLateID, ""))
	pressKeys(fe, "k", "enter")
	require.Equal(t, approvalTool2ID, fe.ZoomedSpan)

	pressKeys(fe, "a")
	require.Equal(t, approvalTool2ID, fe.ZoomedSpan, "the zoom is kept")
	require.True(t, fe.tui.IsFocused(a.view))
}

// An expanded tool call whose children fill the screen pushes its rows -- and
// so the approval -- off screen while the cursor follows output. The approval
// anchors under the requesting call's own row, and when that row is off
// screen a one-line cue says where it went; the keymap names the jump key
// first, so truncation cannot drop it.
func TestApprovalOffscreenIsCued(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	start := time.Unix(100, 0)
	trace := prettyTestTraceID()
	db := dagui.NewDB()
	snapshots := []dagui.SpanSnapshot{
		{ID: approvalRootID, TraceID: trace, Name: "dagger call", StartTime: start},
		{ID: approvalToolID, TraceID: trace, ParentID: approvalRootID, Name: "push",
			LLMRole: "assistant", LLMTool: "push", StartTime: start.Add(time.Second)},
		{ID: approvalPushID, TraceID: trace, ParentID: approvalToolID, Name: "GitRepository.push",
			StartTime: start.Add(time.Second)},
	}
	for i := range 30 {
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID: prettyTestSpanID(byte(0x80 + i)), TraceID: trace, ParentID: approvalToolID,
			Name: fmt.Sprintf("step%02d", i), StartTime: start.Add(time.Duration(2+i) * time.Second),
		})
	}
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(approvalRootID)
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(100, 12))
	fe.setupTUI()
	fe.setExpanded(approvalToolID, true)
	fe.recalculateViewLocked()
	fe.tui.Step()
	require.Equal(t, prettyTestSpanID(0x80+29), fe.FocusedSpan, "precondition: following output to the last child")

	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	require.Equal(t, approvalPushID, a.anchor, "the approval anchors under the requesting call's own row")

	frame := frameText(fe.tui.Step())
	require.Contains(t, frame, "step29", "the newest output stays on screen")
	require.NotContains(t, frame, "Allow push to origin", "the approval itself scrolled away")
	require.Contains(t, frame, "◆ chief is waiting for approval ↑ · a to go there",
		"a cue names the waiting agent, outside the conversation")
	require.Contains(t, frame, "a answer approval", "the keymap names the jump key")
	require.Equal(t, "a", fe.keys(NewOutput(io.Discard))[0].Help().Key, "first, so truncation keeps it")

	pressKeys(fe, "a")
	frame = frameText(fe.tui.Step())
	require.True(t, fe.tui.IsFocused(a.view))
	require.Contains(t, frame, "Deny", "jumping brings the approval on screen")
	require.NotContains(t, frame, "waiting for approval ↑", "and the cue goes away")
}

// In the shell the transcript flows into scrollback; an approval that went
// with it is cued the same way.
func TestApprovalInScrollbackIsCued(t *testing.T) {
	db := rosterDB(t)
	start := time.Unix(100, 0)
	trace := prettyTestTraceID()
	snapshots := approvalTraceSnapshots()
	for i := range 30 {
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID: prettyTestSpanID(byte(0x80 + i)), TraceID: trace, ParentID: approvalRootID,
			Name: fmt.Sprintf("later%02d", i), LLMRole: "assistant", LLMTool: fmt.Sprintf("later%02d", i),
			StartTime: start.Add(time.Duration(10+i) * time.Second),
		})
	}
	db.ImportSnapshots(snapshots)
	fe := focusTestFrontend(t, db, &focusShellHandler{target: "agent-chief"})
	_, _ = startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	frame := frameText(fe.tui.Step())
	require.Contains(t, frame, "◆ chief is waiting for approval ↑ · alt+a to go there", "a cue says an approval waits above")
	require.Contains(t, frame, "alt+a to answer")
}

// In the shell the prompt owns focus: an approval must leave it alone, cue
// the blocked agent on the roster, and hand focus back after a jump.
func TestApprovalInShellKeepsPromptFocus(t *testing.T) {
	db := rosterDB(t)
	db.ImportSnapshots(approvalTraceSnapshots())
	handler := &focusShellHandler{target: "agent-chief"}
	fe := focusTestFrontend(t, db, handler)
	fe.updateAgentRoster()
	require.True(t, fe.inputFocused())

	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	fe.tui.Step()
	require.True(t, fe.inputFocused(), "an approval must not steal the prompt's focus")
	fe.textInput.SetValue("still typing")

	entries := fe.agentRosterEntries()
	require.True(t, entries[0].AwaitingApproval, "the blocked agent is cued on the roster")
	require.False(t, entries[1].AwaitingApproval)
	require.Contains(t, frameText(fe.tui.Step()), "chief "+IconApproval+" approval")
	require.Contains(t, navKeyHelp(fe.keys(NewOutput(io.Discard))), "alt+a answer approval")
	require.Contains(t, frameText(fe.tui.Step()), "alt+a to answer")
	require.True(t, fe.autoFocus, "precondition: following output")
	following := fe.FocusedSpan

	pressKeys(fe, "alt+a")
	require.True(t, fe.tui.IsFocused(a.view), "alt+a jumps to the approval")
	pressKeys(fe, "y")
	res := awaitApproval(t, fe, results)
	require.Equal(t, prompt.APPROVAL_ALLOW_ONCE, res.decision)
	require.True(t, fe.inputFocused(), "answering returns focus to the prompt it was jumped from")
	require.Equal(t, "still typing", fe.textInput.Value())
	require.False(t, fe.agentRosterEntries()[0].AwaitingApproval, "the cue clears once answered")
	require.True(t, fe.autoFocus, "the cursor resumes following output, or new lines crop below it")
	require.Equal(t, following, fe.FocusedSpan)
}

// Leaving a jumped-to approval for the prompt by tab or i -- not esc -- also
// resumes following output and disarms the approval, so it neither keeps the
// cursor parked nor takes focus back.
func TestApprovalLeftForPromptResumesFollowing(t *testing.T) {
	for _, leave := range []string{"tab", "i"} {
		t.Run(leave, func(t *testing.T) {
			db := rosterDB(t)
			db.ImportSnapshots(approvalTraceSnapshots())
			fe := focusTestFrontend(t, db, &focusShellHandler{target: "agent-chief"})
			a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
			require.True(t, fe.autoFocus, "precondition: following output")
			following := fe.FocusedSpan

			pressKeys(fe, "alt+a")
			require.True(t, fe.tui.IsFocused(a.view))
			require.False(t, fe.autoFocus)

			pressKeys(fe, leave)
			fe.tui.Step()
			require.True(t, fe.inputFocused(), "%s goes to the prompt", leave)
			require.False(t, a.finished, "leaving does not answer")
			require.False(t, a.armed, "the approval no longer holds the cursor")
			require.True(t, fe.autoFocus, "the cursor resumes following output")
			require.Equal(t, following, fe.FocusedSpan)
		})
	}
}

// The prompt's alt+a works in nav mode too, so the hint holds in either mode.
func TestApprovalJumpKeyInShellNavMode(t *testing.T) {
	db := rosterDB(t)
	db.ImportSnapshots(approvalTraceSnapshots())
	fe := focusTestFrontend(t, db, &focusShellHandler{target: "agent-chief"})
	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	fe.enterNavMode()
	pressKeys(fe, "alt+a")
	require.True(t, fe.tui.IsFocused(a.view))
	pressKeys(fe, "esc")
	require.False(t, a.finished)
	require.True(t, fe.navigationFocused(), "esc from a nav-mode jump stays in nav mode")
	pressKeys(fe, "a", "n")
	res := awaitApproval(t, fe, results)
	require.Equal(t, prompt.APPROVAL_DENY, res.decision)
}

// Jumping to a floating approval and then having its span arrive keeps the
// approval focused: the cursor follows it to its new row.
func TestFloatingApprovalKeepsFocusWhenAnchored(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startApproval(t, fe, t.Context(), pushApproval(approvalLateID, ""))
	pressKeys(fe, "a")
	require.True(t, fe.tui.IsFocused(a.view))

	start := time.Unix(100, 0).Add(4 * time.Second)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: approvalLateTool, TraceID: prettyTestTraceID(), ParentID: approvalRootID, Name: "late",
			LLMRole: "assistant", LLMTool: "late", StartTime: start},
		{ID: approvalLateID, TraceID: prettyTestTraceID(), ParentID: approvalLateTool, Name: "GitRepository.push",
			StartTime: start},
	})
	fe.recalculateViewLocked()
	fe.tui.Step()
	require.Equal(t, approvalLateTool, a.anchor)
	require.Equal(t, approvalLateTool, fe.FocusedSpan, "the cursor follows the approval to its row")
	require.True(t, fe.tui.IsFocused(a.view), "the approval keeps focus")

	pressKeys(fe, "y")
	res := awaitApproval(t, fe, results)
	require.Equal(t, prompt.APPROVAL_ALLOW_ONCE, res.decision)
}

// A form opened over a focused approval borrows focus and hands it back: the
// approval keeps its place rather than being left.
func TestFormOverApprovalReturnsToIt(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))

	fe.handlePromptForm(NewForm(huh.NewGroup(huh.NewInput().Title("Something else"))), nil)
	fe.tui.Step()
	require.True(t, fe.formFocused())
	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	awaitFormTeardown(t, fe)
	fe.tui.Step()
	require.True(t, a.armed)
	require.True(t, fe.tui.IsFocused(a.view), "focus returns to the approval")
}

// A form opened over a focused approval that is canceled meanwhile must not
// restore focus to the dead approval when it closes.
func TestFormOverCanceledApprovalRestoresNavigation(t *testing.T) {
	fe := approvalTestFrontend(t)
	ctx, cancel := context.WithCancel(t.Context())
	a, results := startApproval(t, fe, ctx, pushApproval(approvalPushID, ""))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))

	fe.handlePromptForm(NewForm(huh.NewGroup(huh.NewInput().Title("Something else"))), nil)
	fe.tui.Step()
	require.True(t, fe.formFocused())

	cancel()
	awaitApproval(t, fe, results)
	fe.tui.Inject(tuist.ParseKey("ctrl+c"))
	awaitFormTeardown(t, fe)

	require.True(t, fe.navigationFocused(), "navigation takes focus back")
	pressKeys(fe, "j")
	require.Equal(t, approvalTool2ID, fe.FocusedSpan, "and navigation keys work")
}
