package idtui

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dagger/dagger/engine/session/prompt"
)

// Asks are questions the engine or a module puts to the user (see
// prompt.AskRequest): a yes/no answer, some text, a choice, or a secret.
// They live where approvals do, and behave the same way (see approval.go):
// inline beneath the call that asked, never taking focus on arrival, reached
// by navigating onto them or jumping with the approval key, answered with
// keys of their own, and cued and notified while they wait.
//
// What differs is the input:
//
//   - A yes/no ask or a selection is a list of choices like an approval's,
//     starting on the request's default: ↑↓ (j/k) move, enter confirms, and
//     y/n (yes/no) or 1-9 (a selection) answer straight away.
//   - A text or secret ask is a one-line editor (several lines when asked
//     for, alt+enter or ctrl+j adding one): what is typed goes in, enter
//     submits, backspace and ctrl+u edit, ↑↓ leave for the rows. A secret
//     shows only a mask, and its keys are never echoed to the keymap.

var _ prompt.AskHandler = (*frontendPretty)(nil)

// HandleAsk implements prompt.AskHandler: it shows the question inline under
// the call that asked and blocks until the user answers or ctx is canceled.
// Several may be pending at once, beside approvals.
func (fe *frontendPretty) HandleAsk(ctx context.Context, req *prompt.AskRequest) (*prompt.AskResponse, error) {
	if fe.reportOnly {
		return nil, ErrNonInteractive
	}
	a := &pendingApproval{
		req:  askHeader(req),
		ask:  req,
		done: make(chan struct{}),
	}
	a.choices, a.defaultChoice = askChoices(req)
	a.selected = a.defaultChoice
	if text := req.GetText(); text != nil {
		a.input = []rune(text.GetDefault())
	}
	a.view = &ApprovalView{fe: fe, approval: a}
	requester := req.GetRequester().GetAgentName()
	if requester == "" {
		requester = req.GetRequester().GetModule()
	}
	fe.notifyPrompt(requester, askQuestion(req))
	if err := fe.awaitApproval(ctx, a); err != nil {
		return nil, err
	}
	return a.answer, nil
}

// askHeader is what an ask shares with an approval, for everything that
// treats the two alike: who asks, and the call it is anchored to.
func askHeader(req *prompt.AskRequest) *prompt.ApprovalRequest {
	question := askQuestion(req)
	return &prompt.ApprovalRequest{
		Kind:      req.GetKind(),
		Prompt:    question,
		Question:  question,
		Requester: req.GetRequester(),
		TraceId:   req.GetTraceId(),
		SpanId:    req.GetSpanId(),
	}
}

// askQuestion is the one line an ask is headed by: its title, else the
// first line of its message.
func askQuestion(req *prompt.AskRequest) string {
	if title := strings.TrimSpace(req.GetTitle()); title != "" {
		return title
	}
	first, _, _ := strings.Cut(strings.TrimSpace(req.GetMessage()), "\n")
	return strings.TrimSpace(first)
}

// askChoices lists a yes/no ask's or a selection's answers, and which one is
// selected by default. Other asks have none.
func askChoices(req *prompt.AskRequest) ([]approvalChoice, int) {
	switch {
	case req.GetBoolean() != nil:
		choices := []approvalChoice{
			{key: "y", label: "Yes", id: "yes"},
			{key: "n", label: "No", id: "no"},
		}
		if req.GetBoolean().GetDefault() {
			return choices, 0
		}
		return choices, 1
	case req.GetSelect() != nil:
		var choices []approvalChoice
		def := 0
		for i, c := range req.GetSelect().GetChoices() {
			k := ""
			if i < 9 {
				k = strconv.Itoa(i + 1)
			}
			choices = append(choices, approvalChoice{key: k, label: c.GetLabel(), id: c.GetId()})
			if c.GetId() == req.GetSelect().GetDefaultChoice() {
				def = i
			}
		}
		return choices, def
	}
	return nil, 0
}

// askTakesText reports whether an ask is answered by typing.
func askTakesText(req *prompt.AskRequest) bool {
	return req.GetText() != nil || req.GetSecret() != nil
}

func askMultiline(req *prompt.AskRequest) bool {
	return req.GetText().GetMultiline() || req.GetSecret().GetMultiline()
}

// waitingFor names what an approval or ask is waiting for, in its cue.
func waitingFor(a *pendingApproval) string {
	if a.ask != nil {
		return "an answer"
	}
	return "approval"
}

// finishAsk answers an ask and tears it down. Must run on the UI goroutine.
func (fe *frontendPretty) finishAsk(a *pendingApproval, answer *prompt.AskResponse) {
	if a.finished {
		return
	}
	a.answer = answer
	fe.finishApproval(a, prompt.APPROVAL_DENY, nil)
}

// pickAskChoice answers a yes/no ask or a selection with its i'th choice.
func (fe *frontendPretty) pickAskChoice(a *pendingApproval, i int) {
	if i < 0 || i >= len(a.choices) {
		return
	}
	c := a.choices[i]
	if a.ask.GetBoolean() != nil {
		fe.finishAsk(a, &prompt.AskResponse{Answer: &prompt.AskResponse_Boolean{Boolean: c.id == "yes"}})
		return
	}
	fe.finishAsk(a, &prompt.AskResponse{Answer: &prompt.AskResponse_Choice{Choice: c.id}})
}

// submitAsk answers a text or secret ask with what was typed.
func (fe *frontendPretty) submitAsk(a *pendingApproval) {
	value := string(a.input)
	if a.ask.GetSecret() != nil {
		fe.finishAsk(a, &prompt.AskResponse{Answer: &prompt.AskResponse_Secret{Secret: []byte(value)}})
	} else {
		fe.finishAsk(a, &prompt.AskResponse{Answer: &prompt.AskResponse_Text{Text: value}})
	}
	a.input = nil
}

// handleAskKey answers or steers a focused yes/no ask or selection. Keys it
// does not claim bubble on to navigation.
func (fe *frontendPretty) handleAskKey(a *pendingApproval, keyStr string) bool {
	switch keyStr {
	case "up", "k":
		if a.selected == 0 {
			return false // past the top: on to the row above
		}
		a.selected--
		a.view.Update()
		return true
	case "down", "j":
		if a.selected == len(a.choices)-1 {
			return false // past the bottom: on to the row below
		}
		a.selected++
		a.view.Update()
		return true
	case "left", "right", "h", "l":
		// Would collapse or expand the row underneath.
		return true
	case "enter":
		fe.pickAskChoice(a, a.selected)
		return true
	case "esc", "alt+esc":
		fe.restoreApprovalFocus(a)
		return true
	case approvalNavKey:
		// Not an answer, but on an ask it never jumps elsewhere either, as
		// on an approval.
		return true
	}
	for i, c := range a.choices {
		if c.key != "" && c.key == keyStr {
			fe.pickAskChoice(a, i)
			return true
		}
	}
	return false
}

// handleAskTextKey edits or submits a focused text or secret ask. Keys it
// does not claim (↑↓, tab, alt+a, ...) bubble on to navigation.
func (fe *frontendPretty) handleAskTextKey(a *pendingApproval, k uv.Key) bool {
	if a.finished {
		return false
	}
	keyStr := k.String()
	switch {
	case keyStr == "esc" || keyStr == "alt+esc":
		fe.restoreApprovalFocus(a)
		fe.recordKeyPress(keyStr)
		return true
	case k.Code == uv.KeyEnter && (k.Mod.Contains(uv.ModShift) || k.Mod.Contains(uv.ModAlt)),
		k.Code == 'j' && k.Mod == uv.ModCtrl:
		if askMultiline(a.ask) {
			a.input = append(a.input, '\n')
			a.view.Update()
		}
		return true
	case k.Code == uv.KeyEnter && k.Mod == 0:
		fe.recordKeyPress(keyStr)
		fe.submitAsk(a)
		return true
	case k.Code == uv.KeyBackspace:
		if len(a.input) > 0 {
			a.input = a.input[:len(a.input)-1]
			a.view.Update()
		}
		return true
	case k.Code == 'u' && k.Mod == uv.ModCtrl:
		a.input = nil
		a.view.Update()
		return true
	case k.Code == uv.KeyLeft, k.Code == uv.KeyRight:
		// No cursor to move: the input only grows at its end. Claimed, or
		// they would collapse or expand the row underneath.
		return true
	case k.Text != "" && !k.Mod.Contains(uv.ModCtrl) && !k.Mod.Contains(uv.ModAlt):
		a.input = append(a.input, []rune(k.Text)...)
		a.view.Update()
		return true
	}
	return false
}

// pasteAsk appends pasted text to a text or secret ask, flattened to one
// line unless it takes several.
func (fe *frontendPretty) pasteAsk(a *pendingApproval, content string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !askMultiline(a.ask) {
		content = strings.ReplaceAll(content, "\n", " ")
	}
	a.input = append(a.input, []rune(content)...)
	a.view.Update()
}

func (fe *frontendPretty) askKeys(a *pendingApproval) []key.Binding {
	next := key.NewBinding(key.WithKeys(approvalJumpKey), key.WithHelp(approvalJumpKey, "next question"),
		KeyEnabled(len(fe.approvals) > 1))
	leave := key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "leave"))
	if askTakesText(a.ask) {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "submit")),
			key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"), key.WithHelp("alt+enter", "new line"),
				KeyEnabled(askMultiline(a.ask))),
			leave,
			next,
		}
	}
	binds := []key.Binding{
		key.NewBinding(key.WithKeys("↑↓", "up", "down", "j", "k"), key.WithHelp("↑↓", "choose")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
	}
	if a.ask.GetBoolean() != nil {
		binds = append(binds,
			key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes")),
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "no")))
	}
	return append(binds, leave, next)
}

// askLines renders an ask: its question headed like an approval's, who asks,
// then -- when focused -- the message and the input, or a fold to two lines
// when not.
//
// The title and requester arrive as literal text, and the message as
// Markdown, all escaped by the engine (prompt.AskRequest).
func (fe *frontendPretty) askLines(a *pendingApproval, focused bool, width int) []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	req := a.ask
	faint := func(s string) string { return out.String(s).Faint().String() }
	bold := func(s string) string { return out.String(s).Bold().String() }

	var lines []string
	add := func(line string) {
		if width > 2 {
			line = ansi.Wrap(line, width, "")
		}
		for i, l := range strings.Split(line, "\n") {
			if i > 0 {
				l = "  " + l
			}
			lines = append(lines, l)
		}
	}

	question := askQuestion(req)
	add(out.String(IconApproval).Foreground(termenv.ANSIYellow).Bold().String() + " " + bold(question))

	var agent string
	if !a.anchor.IsValid() {
		agent = req.GetRequester().GetAgentName()
	}
	if !focused {
		add("  " + faint(joinNonEmpty(" · ", agent, approvalModule(a.req), fe.approvalUnfocusedHint(a))))
		return lines
	}
	who := approvalRequester(a.req, false)
	if full := approvalRequester(a.req, true); width <= 0 || ansi.StringWidth(full)+2 <= width {
		who = full
	}
	if who = joinNonEmpty(" · ", agent, who); who != "" {
		add("  " + faint(who))
	}
	if note := prompt.RememberedNote(req); note != "" {
		add("  " + faint(note))
	}

	// The message, unless it is all the header said.
	if msg := strings.TrimSpace(req.GetMessage()); msg != "" && msg != question {
		mdWidth := width - 2
		if mdWidth <= 0 {
			mdWidth = 80
		}
		rendered := strings.TrimSpace((&Markdown{
			Content:        msg,
			Width:          mdWidth,
			TableRuleColor: fe.logs.tableRuleColor,
		}).View())
		for _, l := range strings.Split(rendered, "\n") {
			lines = append(lines, "  "+l)
		}
	}

	lines = append(lines, "")
	if askTakesText(req) {
		value := string(a.input)
		if req.GetSecret() != nil {
			// Only a mask: one bullet per character typed, never the text.
			var masked strings.Builder
			for _, r := range a.input {
				if r == '\n' {
					masked.WriteRune('\n')
				} else {
					masked.WriteString("•")
				}
			}
			value = masked.String()
		}
		cursor := out.String(" ").Reverse().String()
		for i, l := range strings.Split(value, "\n") {
			prefix := "  "
			if i == 0 {
				prefix = bold(CaretRightFilled) + " "
			}
			// Show the end of a line too long for the width, where the
			// typing (and the cursor) is, rather than cropping it off.
			if width > 0 {
				if excess := ansi.StringWidth(l) + 3 - width; excess > 0 {
					l = ansi.TruncateLeft(l, excess+1, "…")
				}
			}
			lines = append(lines, prefix+l)
		}
		lines[len(lines)-1] += cursor
		hint := "enter submit · esc leave"
		if askMultiline(req) {
			hint = "enter submit · alt+enter new line · esc leave"
		}
		add(faint(hint))
		return lines
	}
	for i, c := range a.choices {
		label := c.label
		if c.key != "" {
			label = c.key + "  " + label
		} else {
			label = "   " + label
		}
		if i == a.selected {
			add(bold(CaretRightFilled + " " + label))
		} else {
			add("  " + label)
		}
	}
	add(faint("↑↓ choose · enter confirm · esc leave"))
	return lines
}
