package idtui

import (
	"context"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/session/prompt"
)

// Asks live where approvals do: inline under the call that asked, reached by
// navigation, answered with keys of their own.

func testAsk(spanID dagui.SpanID, input any) *prompt.AskRequest {
	req := &prompt.AskRequest{
		Kind:    "test",
		Title:   "Deploy to production?",
		Message: "This **ships** the build.",
		Requester: &prompt.ApprovalRequester{
			AgentName:   "chief",
			AgentHandle: "agent-chief",
			Module:      "deployer",
			Function:    "Deployer.ship",
		},
		TraceId: prettyTestTraceID().String(),
		SpanId:  spanID.String(),
	}
	switch in := input.(type) {
	case *prompt.AskBoolean:
		req.Input = &prompt.AskRequest_Boolean{Boolean: in}
	case *prompt.AskText:
		req.Input = &prompt.AskRequest_Text{Text: in}
	case *prompt.AskSelect:
		req.Input = &prompt.AskRequest_Select{Select: in}
	case *prompt.AskSecret:
		req.Input = &prompt.AskRequest_Secret{Secret: in}
	}
	return req
}

type askResult struct {
	resp *prompt.AskResponse
	err  error
}

func startAsk(t *testing.T, fe *frontendPretty, ctx context.Context, req *prompt.AskRequest) (*pendingApproval, <-chan askResult) {
	t.Helper()
	results := make(chan askResult, 1)
	go func() {
		resp, err := fe.HandleAsk(ctx, req)
		results <- askResult{resp, err}
	}()
	var pending *pendingApproval
	require.Eventually(t, func() bool {
		fe.tui.Step()
		for _, a := range fe.approvals {
			if a.ask == req {
				pending = a
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "the ask should become pending")
	return pending, results
}

func awaitAsk(t *testing.T, fe *frontendPretty, results <-chan askResult) askResult {
	t.Helper()
	var res askResult
	require.Eventually(t, func() bool {
		fe.tui.Step()
		select {
		case res = <-results:
			return true
		default:
			return false
		}
	}, 5*time.Second, 5*time.Millisecond, "HandleAsk should return")
	return res
}

// typeText types s one key at a time, as a user would.
func typeText(fe *frontendPretty, s string) []string {
	for _, r := range s {
		fe.tui.Inject(uv.KeyPressEvent{Code: r, Text: string(r)})
	}
	return fe.tui.Step()
}

func TestAskAttachesUnderToolCall(t *testing.T) {
	fe := approvalTestFrontend(t)
	before := fe.tui.Focused()
	a, results := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskBoolean{}))
	require.Equal(t, approvalToolID, a.anchor)
	require.Same(t, before, fe.tui.Focused(), "an arriving ask must not move focus")

	frame := fe.tui.Step()
	header := lineIndex(t, frame, "◆ Deploy to production?")
	require.Less(t, lineIndex(t, frame, "Push"), header, "the ask sits beneath its tool call")
	require.Contains(t, frame[header+1], "deployer · ↓ to answer", "unfolded only when focused")
	require.Equal(t, header+2, lineIndex(t, frame, "Publish"))
	require.NotContains(t, frameText(frame), "ships", "the message only shows once focused")

	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view), "navigating onto the ask focuses it")
	frame = fe.tui.Step()
	header = lineIndex(t, frame, "◆ Deploy to production?")
	require.Contains(t, frame[header+1], "deployer · Deployer.ship")
	require.Contains(t, frameText(frame), "This ships the build.", "the message renders as Markdown")
	require.Contains(t, frameText(frame), "y  Yes")
	require.Contains(t, frameText(frame), "▶ n  No", "the default is selected")
	require.Contains(t, frameText(frame), "enter confirm")

	pressKeys(fe, "y")
	res := awaitAsk(t, fe, results)
	require.NoError(t, res.err)
	require.Equal(t, &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: true}}, res.resp)
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused(), "focus returns to the row")
	require.Empty(t, fe.approvals)
}

func TestAskBooleanAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  bool
		keys []string
		want bool
	}{
		{"default no", false, []string{"enter"}, false},
		{"default yes", true, []string{"enter"}, true},
		{"yes", false, []string{"y"}, true},
		{"no", true, []string{"n"}, false},
		{"choose yes", false, []string{"k", "enter"}, true},
		{"sideways keys do nothing", false, []string{"l", "h", "a", "enter"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe := approvalTestFrontend(t)
			a, results := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskBoolean{Default: tc.def}))
			pressKeys(fe, "a")
			require.True(t, fe.tui.IsFocused(a.view))
			pressKeys(fe, tc.keys...)
			res := awaitAsk(t, fe, results)
			require.NoError(t, res.err)
			require.Equal(t, tc.want, res.resp.GetBoolean())
		})
	}
}

func TestAskSelect(t *testing.T) {
	sel := &prompt.AskSelect{
		Choices:       []*prompt.SelectChoice{{Id: "include", Label: "Include"}, {Id: "drop", Label: "Drop"}, {Id: "cancel", Label: "Cancel"}},
		DefaultChoice: "cancel",
	}
	for _, tc := range []struct {
		name string
		keys []string
		want string
	}{
		{"default", []string{"enter"}, "cancel"},
		{"by number", []string{"2"}, "drop"},
		{"by moving", []string{"k", "k", "enter"}, "include"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe := approvalTestFrontend(t)
			a, results := startAsk(t, fe, t.Context(), testAsk(approvalPushID, sel))
			pressKeys(fe, "a")
			require.True(t, fe.tui.IsFocused(a.view))
			text := frameText(fe.tui.Step())
			require.Contains(t, text, "1  Include")
			require.Contains(t, text, "▶ 3  Cancel")
			pressKeys(fe, tc.keys...)
			res := awaitAsk(t, fe, results)
			require.NoError(t, res.err)
			require.Equal(t, tc.want, res.resp.GetChoice())
		})
	}
}

func TestAskText(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskText{Default: "v1"}))
	pressKeys(fe, "a")
	require.True(t, fe.tui.IsFocused(a.view))
	require.Contains(t, frameText(fe.tui.Step()), "▶ v1", "the input starts with the default")

	// Letters that navigate elsewhere are text here.
	frame := typeText(fe, ".2 jka")
	require.Contains(t, frameText(frame), "▶ v1.2 jka")
	require.True(t, fe.tui.IsFocused(a.view))
	pressKeys(fe, "backspace", "backspace")
	require.Contains(t, frameText(fe.tui.Step()), "▶ v1.2 j")
	pressKeys(fe, "alt+enter")
	require.NotContains(t, string(a.input), "\n", "a one-line ask takes no new line")
	fe.tui.Inject(uv.PasteEvent{Content: "x\ny"})
	fe.tui.Step()
	require.Equal(t, "v1.2 jx y", string(a.input), "a paste is flattened to one line")
	pressKeys(fe, "enter")
	res := awaitAsk(t, fe, results)
	require.NoError(t, res.err)
	require.Equal(t, "v1.2 jx y", res.resp.GetText())
}

func TestAskMultilineText(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskText{Multiline: true}))
	pressKeys(fe, "a")
	typeText(fe, "one")
	pressKeys(fe, "alt+enter")
	typeText(fe, "two")
	pressKeys(fe, "ctrl+j")
	frame := fe.tui.Step()
	require.Equal(t, "one\ntwo\n", string(a.input))
	require.Less(t, lineIndex(t, frame, "▶ one"), lineIndex(t, frame, "  two"))
	require.Contains(t, frameText(frame), "alt+enter new line")
	pressKeys(fe, "backspace", "enter")
	res := awaitAsk(t, fe, results)
	require.NoError(t, res.err)
	require.Equal(t, "one\ntwo", res.resp.GetText())
}

func TestAskSecretIsMasked(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, results := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskSecret{}))
	pressKeys(fe, "a")
	require.True(t, fe.tui.IsFocused(a.view))
	frame := typeText(fe, "hunter2")
	text := frameText(frame)
	require.NotContains(t, text, "hunter2", "a secret is never shown")
	require.Contains(t, text, "▶ "+strings.Repeat("•", 7))
	require.NotEqual(t, "2", fe.pressedKey, "a secret's keys are never echoed to the keymap")
	fe.tui.Inject(uv.PasteEvent{Content: "!"})
	fe.tui.Step()
	pressKeys(fe, "enter")
	res := awaitAsk(t, fe, results)
	require.NoError(t, res.err)
	require.Equal(t, []byte("hunter2!"), res.resp.GetSecret())
	require.Nil(t, a.input, "the typed secret is dropped once answered")
}

func TestAskTextLeaves(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskText{}))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))
	typeText(fe, "draft")
	pressKeys(fe, "esc")
	require.False(t, a.finished, "esc leaves the ask unanswered")
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused())

	// ↓ walks back onto it, ↓ again leaves for the next row (j would be
	// text), keeping the draft.
	pressKeys(fe, "j")
	require.True(t, fe.tui.IsFocused(a.view))
	pressKeys(fe, "down")
	require.Equal(t, approvalTool2ID, fe.FocusedSpan)
	require.Equal(t, "draft", string(a.input))
}

func TestAskCancellation(t *testing.T) {
	fe := approvalTestFrontend(t)
	ctx, cancel := context.WithCancel(t.Context())
	a, results := startAsk(t, fe, ctx, testAsk(approvalPushID, &prompt.AskSecret{}))
	pressKeys(fe, "k", "k")
	require.True(t, fe.tui.IsFocused(a.view))
	cancel()
	res := awaitAsk(t, fe, results)
	require.ErrorIs(t, res.err, context.Canceled)
	require.Nil(t, res.resp)
	require.Eventually(t, func() bool {
		fe.tui.Step()
		return len(fe.approvals) == 0
	}, 5*time.Second, 5*time.Millisecond)
	require.Same(t, fe.spanTrees[approvalToolID], fe.tui.Focused(), "focus returns to the row")
}

func TestAskBesideApproval(t *testing.T) {
	fe := approvalTestFrontend(t)
	approval, approvalResults := startApproval(t, fe, t.Context(), pushApproval(approvalPushID, ""))
	ask, askResults := startAsk(t, fe, t.Context(), testAsk(approvalPush2ID, &prompt.AskBoolean{}))
	frame := frameText(fe.tui.Step())
	require.Contains(t, frame, pushQuestion)
	require.Contains(t, frame, "◆ Deploy to production?")

	pressKeys(fe, "k")
	require.True(t, fe.tui.IsFocused(ask.view), "the nearer one is a stop below its row")
	pressKeys(fe, "y")
	require.True(t, awaitAsk(t, fe, askResults).resp.GetBoolean())
	pressKeys(fe, "a")
	require.True(t, fe.tui.IsFocused(approval.view))
	pressKeys(fe, "n")
	require.Equal(t, prompt.APPROVAL_DENY, awaitApproval(t, fe, approvalResults).decision)
}

func TestAskOffscreenCue(t *testing.T) {
	fe := approvalTestFrontend(t)
	a, _ := startAsk(t, fe, t.Context(), testAsk(approvalPushID, &prompt.AskBoolean{}))
	require.Contains(t, ansiStrip(fe.approvalCueLine(a, "↑")), "chief is waiting for an answer ↑")
}

func ansiStrip(s string) string { return frameText([]string{s}) }
