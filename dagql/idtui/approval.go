package idtui

import (
	"context"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/session/prompt"
)

// Approvals are access requests (a git push with the user's credentials, a
// host secret, ...) an agent's module makes on the user's behalf. Unlike
// prompt forms, which are global, serialized and take keyboard focus the
// moment they arrive, an approval is scoped to the call that asked for it:
//
//   - It renders inline beneath the row of the LLM tool call it came from
//     (else the nearest rendered ancestor of the requesting call), so several
//     can be pending at once, each where its agent is working.
//   - It takes focus with its row, and only with its row: whenever the
//     cursor is on the row it sits under and navigation has the keyboard
//     (stepping there, following output there, or the approval arriving
//     there), it unfolds and its keys answer it, with no extra keystroke.
//     Approvals under other rows stay folded, so nothing elsewhere steals
//     keys, and the prompt keeps the keyboard while it has it (the shell
//     reaches approvals with alt+a). Several under one row are focused in
//     turn, the first first. Taking focus pauses following output, which
//     resumes once it is answered or left.
//   - Its keys answer it straight away: there is no second "enter the
//     prompt" step. The default answer is Deny, and granting takes a choice
//     and a confirm (or y/s/a). The choices are a list: ↑↓ (j/k) move the
//     selection, and only past either end of the list does the cursor leave
//     for the row above or below (or the next approval under the same row)
//     -- so walking the rows downwards passes the row and its approval in
//     one step, landing on (and leaving from) Deny. h/l and ←/→ are
//     swallowed: they would collapse or expand the row underneath, moving
//     the approval away from the cursor.
//   - esc leaves it pending and gives the row its own keys back (enter
//     zooms, l expands, ...): the row's approvals stay folded until the
//     cursor moves to another row and back, or ↓ steps onto them. A search
//     landing on the row leaves them folded the same way, so n and N keep
//     seeking matches.
//   - It can also be jumped to (`a` in nav mode, alt+a at the prompt and
//     in the shell), and gives focus back once answered or left.
//   - Inside the conversation it does not name the agent -- it sits under
//     that agent's own tool call -- only the module and function asking.
//     The agent is named only outside it: the roster, and the cue for an
//     approval that is off screen or has no row.
//   - The agent it blocks is cued on the roster, so the user knows there is
//     something to navigate to.
//
// A request whose span has not arrived (or whose rows are zoomed away) is
// "floating": it renders below the trace, a stop of its own after the last
// row, without focus until stepped or jumped onto, and is re-anchored as soon
// as its span shows up in the rows.

var _ prompt.ApprovalHandler = (*frontendPretty)(nil)

// approvalJumpKey jumps to the next pending approval from the prompt and in
// nav mode; nav mode also takes the bare approvalNavKey -- but only while no
// approval has focus, since on a focused approval `a` answers "always".
const (
	approvalJumpKey = "alt+a"
	approvalNavKey  = "a"
)

type approvalChoice struct {
	key      string
	label    string
	decision prompt.ApprovalDecision
}

type pendingApproval struct {
	req *prompt.ApprovalRequest
	seq uint64

	done     chan struct{}
	decision prompt.ApprovalDecision
	err      error
	finished bool

	choices  []approvalChoice
	selected int

	// anchor is the row the approval renders beneath; zero while floating.
	anchor dagui.SpanID
	// agentID is the runtime handle of the agent the approval blocks, for the
	// roster's cue.
	agentID string

	// armed is set while the approval holds (or is about to hold) keyboard
	// focus: when the cursor is on its row with navigation focused, or the
	// user stepped or jumped onto it. Each arming starts on the default
	// choice.
	armed bool
	// returnToInput records that the user jumped here from the prompt, so
	// answering hands focus back to it rather than leaving them in nav mode;
	// followOutput whether the cursor was following new output then, which
	// it resumes.
	returnToInput bool
	followOutput  bool

	view *ApprovalView
}

// approvalChoices lists the answers in increasing order of trust, Deny last
// (and selected by default, see approvalDefaultChoice). Each label spells out
// how long the grant lasts, so a session grant never reads as a lasting one.
// Always is only offered when the request names a key it can be remembered
// under.
func approvalChoices(req *prompt.ApprovalRequest) []approvalChoice {
	choices := []approvalChoice{
		{"y", "Allow once", prompt.APPROVAL_ALLOW_ONCE},
		{"s", "Allow for this session only", prompt.APPROVAL_ALLOW_SESSION},
	}
	if req.GetPersistentKey() != "" {
		module := req.GetRequester().GetModule()
		if module == "" {
			module = "this module"
		}
		choices = append(choices, approvalChoice{"a", "Always allow " + module, prompt.APPROVAL_ALLOW_FOREVER})
	}
	return append(choices, approvalChoice{"n", "Deny", prompt.APPROVAL_DENY})
}

// approvalDefaultChoice is the index of Deny, the safe default.
func approvalDefaultChoice(choices []approvalChoice) int {
	return len(choices) - 1
}

// HandleApproval implements prompt.ApprovalHandler: it shows the request
// inline under the call that made it and blocks until the user answers or ctx
// is canceled. Several may be pending at once.
func (fe *frontendPretty) HandleApproval(ctx context.Context, req *prompt.ApprovalRequest) (prompt.ApprovalDecision, error) {
	if fe.reportOnly {
		return prompt.APPROVAL_DENY, ErrNonInteractive
	}
	a := &pendingApproval{
		req:     req,
		done:    make(chan struct{}),
		choices: approvalChoices(req),
	}
	a.selected = approvalDefaultChoice(a.choices)
	a.view = &ApprovalView{fe: fe, approval: a}
	fe.dispatch(func() {
		fe.addApproval(a)
	})
	select {
	case <-ctx.Done():
		fe.dispatch(func() {
			fe.finishApproval(a, prompt.APPROVAL_DENY, ctx.Err())
		})
		return prompt.APPROVAL_DENY, ctx.Err()
	case <-a.done:
		return a.decision, a.err
	}
}

// addApproval registers a pending approval. Must run on the UI goroutine.
func (fe *frontendPretty) addApproval(a *pendingApproval) {
	if a.finished {
		return
	}
	fe.approvalSeq++
	a.seq = fe.approvalSeq
	fe.approvals = append(fe.approvals, a)
	fe.resolveApproval(a)
	// Arriving under the cursor's row, it takes focus if the row has it.
	fe.syncApprovalFocus()
	fe.approvalsChanged()
}

// finishApproval answers (or cancels) one approval and tears it down. Late
// and duplicate calls are harmless. Must run on the UI goroutine.
func (fe *frontendPretty) finishApproval(a *pendingApproval, decision prompt.ApprovalDecision, err error) {
	if a == nil || a.finished {
		return
	}
	hadFocus := fe.tui != nil && fe.tui.IsFocused(a.view)
	a.finished = true
	a.decision = decision
	a.err = err
	fe.approvals = slices.DeleteFunc(fe.approvals, func(other *pendingApproval) bool {
		return other == a
	})
	fe.updateApprovalAnchor(a.anchor)
	if hadFocus {
		fe.restoreApprovalFocus(a)
	}
	close(a.done)
	fe.approvalsChanged()
}

// restoreApprovalFocus hands focus back after the approval that held it goes
// away (or is left with esc): to the prompt if the user jumped here from it,
// else to the row it sits under -- or the next approval under that row, once
// this one is answered. One left pending keeps its row's approvals folded
// until the cursor moves off the row.
func (fe *frontendPretty) restoreApprovalFocus(a *pendingApproval) {
	if !a.finished && a.anchor.IsValid() {
		fe.approvalsLeftAt = a.anchor
		fe.updateApprovalViews()
	}
	a.armed = false
	if a.followOutput {
		// The jump only borrowed the cursor: resume following output, or the
		// agent's next lines would be cropped below the approval's row.
		a.followOutput = false
		fe.goEnd()
	}
	if a.returnToInput && fe.textInput != nil {
		a.returnToInput = false
		fe.enterInsertMode()
		return
	}
	a.returnToInput = false
	fe.focusNavigationTarget()
	fe.refreshKeymap()
}

// leaveApproval runs when focus leaves an approval without answering it by a
// route other than esc (tab or i to the prompt, say): the cursor resumes
// following output if the jump interrupted that, and the approval is
// disarmed so it cannot take focus back.
func (fe *frontendPretty) leaveApproval(a *pendingApproval) {
	a.armed = false
	a.returnToInput = false
	if a.followOutput {
		a.followOutput = false
		fe.goEnd()
	}
}

// repairFocus recovers when a focus scope restores to nothing, or to an
// approval answered (or canceled) while something else held focus on top of
// it: navigation takes focus again rather than leaving keys unrouted.
func (fe *frontendPretty) repairFocus() {
	if fe.tui == nil {
		return
	}
	switch f := fe.tui.Focused().(type) {
	case nil:
		fe.focusNavigationTarget()
	case *ApprovalView:
		if f.approval.finished {
			fe.focusNavigationTarget()
		}
	}
}

// approvalsChanged re-renders everything an approval's arrival or departure
// shows up in: the floating list, the roster cue and the keymap.
func (fe *frontendPretty) approvalsChanged() {
	fe.approvalsGen++
	fe.updateAgentRoster()
	fe.refreshKeymap()
	fe.Update()
}

func (fe *frontendPretty) updateApprovalAnchor(anchor dagui.SpanID) {
	if !anchor.IsValid() {
		fe.Update()
		return
	}
	if st := fe.spanTrees[anchor]; st != nil {
		st.Update()
	}
}

// resolveApprovals re-anchors every pending approval after the rows change:
// its span may have arrived, or a zoom or collapse may have moved the nearest
// rendered ancestor.
func (fe *frontendPretty) resolveApprovals() {
	changed := false
	for _, a := range fe.approvals {
		if fe.resolveApproval(a) {
			changed = true
		}
	}
	if changed {
		fe.approvalsGen++
		fe.updateAgentRoster()
	}
	fe.syncApprovalFocus()
}

// resolveApproval recomputes one approval's anchor and agent, reporting
// whether either changed.
func (fe *frontendPretty) resolveApproval(a *pendingApproval) bool {
	span := fe.approvalSpan(a.req)
	anchor := fe.approvalAnchor(span)
	agentID := a.req.GetRequester().GetAgentHandle()
	if agentID == "" {
		for s := span; s != nil; s = s.ParentSpan {
			if s.Agent && s.AgentID != "" {
				agentID = s.AgentID
				break
			}
		}
	}
	changed := false
	if anchor != a.anchor {
		old := a.anchor
		a.anchor = anchor
		a.view.Update() // its hint depends on having a row
		fe.updateApprovalAnchor(old)
		fe.updateApprovalAnchor(anchor)
		changed = true
		if a.armed && anchor.IsValid() {
			// The user is on this approval (typically jumped to while it
			// floated, before its span arrived): the cursor follows it to its
			// row, so it keeps focus rather than the next key landing on
			// whatever row the cursor was left on.
			if row := fe.rows.BySpan[anchor]; row != nil {
				fe.autoFocus = false
				fe.focus(row)
			}
		}
	}
	if agentID != a.agentID {
		a.agentID = agentID
		changed = true
	}
	return changed
}

// approvalSpan is the span of the call that requested approval, if it has
// arrived.
func (fe *frontendPretty) approvalSpan(req *prompt.ApprovalRequest) *dagui.Span {
	if fe.db == nil || req.GetSpanId() == "" {
		return nil
	}
	id, err := trace.SpanIDFromHex(req.GetSpanId())
	if err != nil {
		return nil
	}
	return fe.db.Spans.Map[dagui.SpanID{SpanID: id}]
}

// approvalAnchor finds the row an approval renders beneath: the deepest
// rendered span on the path to the requesting call -- the call's own row when
// it is visible, else the nearest ancestor that is, which for a collapsed
// tool call is the tool call itself. Anchoring as deep as possible keeps the
// approval next to the work it blocks, which is near the newest output, rather
// than above every row an expanded tool call has produced since. Zero means
// there is no such row: the approval floats.
func (fe *frontendPretty) approvalAnchor(span *dagui.Span) dagui.SpanID {
	if span == nil || fe.rows == nil {
		return dagui.SpanID{}
	}
	for s := span; s != nil; s = s.ParentSpan {
		if fe.rows.BySpan[s.ID] != nil && fe.spanTrees[s.ID] != nil {
			return s.ID
		}
	}
	return dagui.SpanID{}
}

// approvalsAt returns the pending approvals anchored beneath a row, oldest
// first.
func (fe *frontendPretty) approvalsAt(spanID dagui.SpanID) []*pendingApproval {
	var at []*pendingApproval
	for _, a := range fe.approvals {
		if a.anchor == spanID {
			at = append(at, a)
		}
	}
	return at
}

// approvalAgents is the set of agents blocked on a pending approval.
func (fe *frontendPretty) approvalAgents() map[string]bool {
	if len(fe.approvals) == 0 {
		return nil
	}
	agents := map[string]bool{}
	for _, a := range fe.approvals {
		if a.agentID != "" {
			agents[a.agentID] = true
		}
	}
	return agents
}

// focusableApproval is the approval that should hold keyboard focus while
// navigating, if any: an armed one under the cursor's row, else an armed
// floating one the user stepped or jumped to, else the first one under the
// cursor's row, unless the user left that row's approvals. Only the last is
// not armed yet: navigationTarget arms it.
func (fe *frontendPretty) focusableApproval() *pendingApproval {
	var floating, first *pendingApproval
	for _, a := range fe.approvals {
		if a.anchor.IsValid() {
			if a.anchor != fe.FocusedSpan {
				continue
			}
			if a.armed {
				return a
			}
			if first == nil {
				first = a
			}
		} else if a.armed && floating == nil {
			floating = a
		}
	}
	if floating != nil {
		return floating
	}
	if fe.FocusedSpan == fe.approvalsLeftAt {
		return nil
	}
	return first
}

// armApproval readies an approval to take keyboard focus: on its default
// choice, and pausing following output, so new rows cannot move the cursor
// off it while it is being answered. Answering or leaving it resumes
// following.
func (fe *frontendPretty) armApproval(a *pendingApproval) {
	a.armed = true
	a.selected = approvalDefaultChoice(a.choices)
	if fe.autoFocus {
		fe.autoFocus = false
		a.followOutput = true
	}
	a.view.Update()
}

// disarmApproval drops an approval's hold on focus, and the way back it
// recorded.
func (fe *frontendPretty) disarmApproval(a *pendingApproval) {
	a.armed = false
	a.returnToInput = false
	a.followOutput = false
}

// updateApprovalViews re-renders every pending approval: a folded one's hint
// depends on where the cursor and focus are.
func (fe *frontendPretty) updateApprovalViews() {
	for _, a := range fe.approvals {
		a.view.Update()
	}
}

// focusedApproval is the approval that holds keyboard focus, if any.
func (fe *frontendPretty) focusedApproval() *pendingApproval {
	if fe.tui == nil {
		return nil
	}
	if v, ok := fe.tui.Focused().(*ApprovalView); ok && !v.approval.finished {
		return v.approval
	}
	return nil
}

// disarmApprovals drops the arming of every approval the cursor's row is not
// the row of, or whose row was left. Floating approvals stay armed unless
// includeFloating: only the user's own navigation leaves those.
func (fe *frontendPretty) disarmApprovals(cursor dagui.SpanID, includeFloating bool) {
	for _, a := range fe.approvals {
		if !a.armed {
			continue
		}
		if a.anchor.IsValid() {
			if a.anchor != cursor || a.anchor == fe.approvalsLeftAt {
				fe.disarmApproval(a)
			}
		} else if includeFloating {
			fe.disarmApproval(a)
		}
	}
}

// approvalSlot is one place of row navigation while approvals are pending:
// a row, or an approval (just after the row it sits under, or after the last
// row while it floats). A row and the first approval under it are one stop:
// landing on the row focuses it, unless the row's approvals were left.
type approvalSlot struct {
	row      *dagui.TraceRow
	approval *pendingApproval
}

func (fe *frontendPretty) approvalSlots() []approvalSlot {
	slots := make([]approvalSlot, 0, len(fe.rows.Order)+len(fe.approvals))
	for _, row := range fe.rows.Order {
		slots = append(slots, approvalSlot{row: row})
		for _, a := range fe.approvalsAt(row.Span.ID) {
			slots = append(slots, approvalSlot{approval: a})
		}
	}
	for _, a := range fe.approvals {
		if !a.anchor.IsValid() {
			slots = append(slots, approvalSlot{approval: a})
		}
	}
	return slots
}

// stepApproval moves the cursor by one stop when approvals are pending.
// Reports whether it handled the move; with no approvals pending, plain row
// navigation applies.
//
// A row is entered at its first approval, from above and below alike. From
// a focused approval, ↑ past its first choice skips its own row -- the row
// and its first approval are one stop -- for the approval before it under
// the same row, else the row above; ↓ past its last choice goes on to the
// next approval under the row, else the row below. From a row whose
// approvals were left, ↓ steps onto the first of them.
func (fe *frontendPretty) stepApproval(delta int) bool {
	if len(fe.approvals) == 0 || fe.rows == nil {
		return false
	}
	slots := fe.approvalSlots()
	cur := -1
	focused := fe.focusedApproval()
	if focused != nil {
		cur = slices.IndexFunc(slots, func(s approvalSlot) bool { return s.approval == focused })
	} else if fe.FocusedSpan.IsValid() {
		cur = slices.IndexFunc(slots, func(s approvalSlot) bool {
			return s.row != nil && s.row.Span.ID == fe.FocusedSpan
		})
	}
	if cur < 0 {
		return false
	}
	next := cur + delta
	if focused != nil && focused.anchor.IsValid() && next >= 0 &&
		slots[next].row != nil && slots[next].row.Span.ID == focused.anchor {
		next += delta
	}
	if next < 0 || next >= len(slots) {
		return true
	}
	s := slots[next]
	switch {
	case s.row != nil:
		fe.manualFocus(s.row)
	case s.approval.anchor.IsValid() && s.approval.anchor != fe.FocusedSpan:
		// Up into another row from below: the row is entered at its first
		// approval, as from above.
		if row := fe.rows.BySpan[s.approval.anchor]; row != nil {
			fe.manualFocus(row)
		}
	default:
		fe.focusApproval(s.approval, false)
	}
	return true
}

// focusApproval puts the cursor on an approval and gives it keyboard focus.
// fromInput records that the user came from the prompt, to return there.
func (fe *frontendPretty) focusApproval(a *pendingApproval, fromInput bool) {
	fe.autoFocus = false
	for _, other := range fe.approvals {
		fe.disarmApproval(other)
	}
	// Going to it deliberately unfolds its row's approvals again.
	fe.approvalsLeftAt = dagui.SpanID{}
	// Every visit starts on Deny: the safe default, and the bottom of the
	// list, so walking the rows downwards passes it in one step.
	fe.armApproval(a)
	a.returnToInput = fromInput
	fe.updateApprovalViews()
	if a.anchor.IsValid() {
		if row := fe.rows.BySpan[a.anchor]; row != nil {
			fe.focus(row)
		}
	}
	if fe.searchQuery != "" {
		fe.searchIdx = -1
	}
	fe.tui.SetFocus(a.view)
	fe.syncHardwareCursor()
	fe.refreshKeymap()
	fe.Update()
}

// syncApprovalFocus moves navigation's keyboard focus where it belongs after
// approvals come, go or move: off an approval view that should no longer hold
// it (re-anchored away from the cursor, or disarmed), and onto one that
// arrived under the cursor's row. Focus held elsewhere (the prompt, a form,
// a search) is left alone.
func (fe *frontendPretty) syncApprovalFocus() {
	if fe.tui == nil || !fe.navigationFocused() {
		return
	}
	if target := fe.navigationTarget(); target != fe.tui.Focused() {
		fe.tui.SetFocus(target)
		fe.syncHardwareCursor()
		fe.refreshKeymap()
	}
}

// jumpToApproval moves the cursor to the next pending approval (cycling in
// arrival order) and gives it focus. Reports whether there was one.
func (fe *frontendPretty) jumpToApproval() bool {
	if len(fe.approvals) == 0 || fe.tui == nil {
		return false
	}
	fromInput := fe.inputFocused()
	followOutput := fe.autoFocus
	next := fe.approvals[0]
	if cur := fe.focusedApproval(); cur != nil {
		if i := slices.Index(fe.approvals, cur); i >= 0 {
			next = fe.approvals[(i+1)%len(fe.approvals)]
		}
		// Moving between approvals keeps the original way back.
		fromInput = fromInput || cur.returnToInput
		followOutput = followOutput || cur.followOutput
	}
	if !next.anchor.IsValid() && fe.zoomedAwayFrom(next) {
		// The rows are zoomed away from the requesting call: unzoom to give
		// it a row to sit under. Zoomed into its own call, it already shows
		// in the zoomed view, so the user's view is kept.
		fe.ZoomedSpan = fe.db.PrimarySpan
		fe.renderVersion++
		fe.recalculateViewLocked()
	}
	fe.focusApproval(next, fromInput)
	next.followOutput = followOutput
	return true
}

// zoomedAwayFrom reports whether the view is zoomed into a subtree that does
// not contain the approval's requesting call. A call that has not arrived yet
// may well be inside the zoom, so it does not count as away.
func (fe *frontendPretty) zoomedAwayFrom(a *pendingApproval) bool {
	if !fe.ZoomedSpan.IsValid() || fe.ZoomedSpan == fe.db.PrimarySpan {
		return false
	}
	span := fe.approvalSpan(a.req)
	if span == nil {
		return false
	}
	for s := span; s != nil; s = s.ParentSpan {
		if s.ID == fe.ZoomedSpan {
			return false
		}
	}
	return true
}

// handleApprovalKey answers or steers the focused approval. Keys it does not
// claim bubble on to navigation, so moving off the row works as usual.
func (fe *frontendPretty) handleApprovalKey(a *pendingApproval, keyStr string) bool {
	if a.finished {
		return false
	}
	pick := func(decision prompt.ApprovalDecision) bool {
		if approvalOffers(a, decision) {
			fe.finishApproval(a, decision, nil)
		}
		// Claimed even when not on offer: an answer key must never fall
		// through to navigation (`a` would jump to another approval).
		return true
	}
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
			return false // past Deny: on to the row below
		}
		a.selected++
		a.view.Update()
		return true
	case "left", "right", "h", "l":
		// Would collapse or expand the row underneath.
		return true
	case "enter":
		fe.finishApproval(a, a.choices[a.selected].decision, nil)
		return true
	case "y":
		return pick(prompt.APPROVAL_ALLOW_ONCE)
	case "s":
		return pick(prompt.APPROVAL_ALLOW_SESSION)
	case "a":
		return pick(prompt.APPROVAL_ALLOW_FOREVER)
	case "n":
		return pick(prompt.APPROVAL_DENY)
	case "esc", "alt+esc":
		// Leave without answering: the approval stays pending, and focus goes
		// back where it came from.
		fe.restoreApprovalFocus(a)
		return true
	}
	return false
}

func (fe *frontendPretty) approvalKeys() []key.Binding {
	a := fe.focusedApproval()
	if a == nil {
		return nil
	}
	always := approvalOffers(a, prompt.APPROVAL_ALLOW_FOREVER)
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑↓", "up", "down", "j", "k"), key.WithHelp("↑↓", "choose")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
		key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "once")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "session")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "always"), KeyEnabled(always)),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "deny")),
		key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "leave")),
		key.NewBinding(key.WithKeys(approvalJumpKey), key.WithHelp(approvalJumpKey, "next approval"),
			KeyEnabled(len(fe.approvals) > 1)),
	}
}

func approvalOffers(a *pendingApproval, decision prompt.ApprovalDecision) bool {
	for _, c := range a.choices {
		if c.decision == decision {
			return true
		}
	}
	return false
}

// renderApprovalCues renders a one-line cue for each anchored approval whose
// lines the last progress render left off screen (scrolled above the window,
// into scrollback, or cropped below it), so a blocked agent is never invisible:
// outside the shell there is no roster to say so.
func (fe *frontendPretty) renderApprovalCues() []string {
	if len(fe.approvals) == 0 || fe.progressTopGaps == nil {
		return nil
	}
	var lines []string
	for _, a := range fe.approvals {
		if !a.anchor.IsValid() {
			continue
		}
		tree := fe.spanTrees[a.anchor]
		if tree == nil {
			continue
		}
		off, ok := tree.approvalLines[a]
		if !ok {
			continue
		}
		top := fe.findSpanLine(a.anchor, fe.progressTopGaps)
		if top < 0 {
			continue
		}
		var arrow string
		switch line := top + off; {
		case line < fe.progressWindow[0]:
			arrow = "↑"
		case line >= fe.progressWindow[1]:
			arrow = "↓"
		default:
			continue
		}
		lines = append(lines, fe.approvalCueLine(a, arrow))
	}
	return lines
}

func (fe *frontendPretty) approvalCueLine(a *pendingApproval, arrow string) string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	accent := termenv.ANSIYellow
	if a.req.GetDanger() {
		accent = termenv.ANSIRed
	}
	// Outside the conversation, so the agent is named.
	who := a.req.GetRequester().GetAgentName()
	if who == "" {
		who = a.req.GetRequester().GetModule()
	}
	if who == "" {
		who = "an agent"
	}
	jump := approvalNavKey
	if fe.shell != nil {
		jump = approvalJumpKey
	}
	line := "  " + out.String(IconApproval).Foreground(accent).Bold().String() + " " +
		out.String(who+" is waiting for approval "+arrow).Bold().String() +
		out.String(" · "+jump+" to go there").Faint().String()
	if w := fe.contentWidth; w > 0 {
		line = ansi.Truncate(line, w, "…")
	}
	return line
}

// renderFloatingApprovals renders the approvals that have no row to sit under
// yet, separated by blank lines.
func (fe *frontendPretty) renderFloatingApprovals(ctx tuist.Context) []string {
	var lines []string
	for _, a := range fe.approvals {
		if a.anchor.IsValid() {
			continue
		}
		childCtx := ctx
		if ctx.Width > 0 {
			childCtx.Width = max(ctx.Width-2, 1)
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		result := fe.RenderChildResult(childCtx, a.view)
		for _, line := range result.Lines {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

// renderInlineApprovals renders the approvals anchored beneath this row,
// starting at line base of this tree. Only the frontend's own tree hosts
// them: a view mounts in one place.
func (s *SpanTreeView) renderInlineApprovals(ctx tuist.Context, r *renderer, row *dagui.TraceRow, base int) []string {
	s.approvalLines = nil
	if s.finalRender || s.scope != nil || s.fe.spanTrees[s.spanID] != s {
		return nil
	}
	pending := s.fe.approvalsAt(s.spanID)
	if len(pending) == 0 {
		return nil
	}
	prefix := s.inlineReportPrefix(r, row)
	childCtx := ctx
	if ctx.Width > 0 {
		childCtx.Width = max(ctx.Width-ansi.StringWidth(prefix), 1)
	}
	s.approvalLines = make(map[*pendingApproval]int, len(pending))
	var lines []string
	for _, a := range pending {
		s.approvalLines[a] = base + len(lines)
		result := s.RenderChildResult(childCtx, a.view)
		for _, line := range result.Lines {
			lines = append(lines, prefix+line)
		}
	}
	return lines
}

// ApprovalView renders one pending approval and, when focused, answers it.
type ApprovalView struct {
	tuist.Compo
	fe       *frontendPretty
	approval *pendingApproval
	focused  bool
}

var (
	_ tuist.Interactive = (*ApprovalView)(nil)
	_ tuist.Focusable   = (*ApprovalView)(nil)
)

func (v *ApprovalView) SetFocused(_ tuist.Context, focused bool) {
	if v.focused != focused {
		v.focused = focused
		// The others' hints name the jump key only while one has focus.
		v.fe.updateApprovalViews()
		v.Update()
	}
	if !focused && v.fe.tui != nil {
		// Focus is moving away; once it lands, clean up if it left for the
		// prompt or the rows. A form or search opened over the approval
		// returns to it, so it keeps its place.
		fe, a := v.fe, v.approval
		fe.dispatch(func() {
			if a.finished || !a.armed || fe.tui.IsFocused(v) {
				return
			}
			if fe.inputFocused() || fe.navigationFocused() {
				fe.leaveApproval(a)
				fe.refreshKeymap()
				fe.Update()
			}
		})
	}
}

func (v *ApprovalView) HandleKeyPress(_ tuist.Context, ev uv.KeyPressEvent) bool {
	keyStr := uv.Key(ev).String()
	if !v.fe.handleApprovalKey(v.approval, keyStr) {
		return false
	}
	v.fe.recordKeyPress(keyStr)
	v.fe.Update()
	return true
}

func (v *ApprovalView) Render(ctx tuist.Context) {
	ctx.Lines(v.fe.approvalLines(v.approval, v.focused, ctx.Width)...)
}

// approvalLines renders an approval: the question, who asks (module and
// function -- never the agent, whose own tool call it sits under), and either
// the choices when focused or a fold to two lines when not.
//
// Every field arrives as literal display text the engine already escaped
// (prompt.ApprovalRequest), so it renders as-is: escaping or stripping it
// again would mangle what the engine chose to show.
func (fe *frontendPretty) approvalLines(a *pendingApproval, focused bool, width int) []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	req := a.req
	faint := func(s string) string { return out.String(s).Faint().String() }

	var lines []string
	// add wraps a line to width, indenting its continuation lines.
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

	// The question, after a ◆ in the request's accent. The full prompt names
	// who asks again, for yes/no fallbacks; this one is bare. A destructive
	// one carries its danger in red, the way the yes/no fallback flags a
	// force-push.
	question := req.GetQuestion()
	if question == "" {
		question = req.GetPrompt()
	}
	accent := termenv.ANSIYellow
	if req.GetDanger() {
		accent = termenv.ANSIRed
	}
	bold := func(s string) string { return out.String(s).Bold().String() }
	danger := func(s string) string { return out.String(s).Foreground(termenv.ANSIRed).Bold().String() }
	header := out.String(IconApproval).Foreground(accent).Bold().String() + " "
	switch {
	case req.GetDanger() && strings.HasPrefix(question, forcePushAction):
		header += danger(forcePushAction) + bold(strings.TrimPrefix(question, forcePushAction))
	case req.GetDanger() && strings.Contains(question, forcePushPhrase):
		before, after, _ := strings.Cut(question, forcePushPhrase)
		if before != "" {
			header += bold(before)
		}
		header += danger(forcePushPhrase)
		if after != "" {
			header += bold(after)
		}
	default:
		header += bold(question)
	}
	add(header)

	// Who asks, and (unfocused) how to get here.
	var agent string
	if !a.anchor.IsValid() {
		// No row: it renders outside the conversation, so the agent is named.
		agent = req.GetRequester().GetAgentName()
	}
	if !focused {
		add("  " + faint(joinNonEmpty(" · ", agent, approvalModule(req), fe.approvalUnfocusedHint(a))))
		return lines
	}
	who := approvalRequester(req, false)
	if full := approvalRequester(req, true); width <= 0 || ansi.StringWidth(full)+2 <= width {
		who = full
	}
	if who = joinNonEmpty(" · ", agent, who); who != "" {
		add("  " + faint(who))
	}
	if subject := req.GetSubject(); subject != "" && !strings.Contains(question, subject) {
		add("  " + faint(subject))
	}

	// The choices, one per line with its key.
	lines = append(lines, "")
	for i, c := range a.choices {
		if i == a.selected {
			add(bold(CaretRightFilled + " " + c.key + "  " + c.label))
		} else {
			add("  " + bold(c.key) + "  " + c.label)
		}
	}
	add(faint("↑↓ choose · enter confirm · esc leave"))
	return lines
}

// approvalUnfocusedHint says how to reach an approval that does not have
// focus. In the shell that is the jump key, which works from the prompt and
// nav mode alike. Otherwise, under the cursor's row (left with esc, or
// waiting behind another approval there) it is ↓; elsewhere the jump key,
// bare unless an approval has focus and takes `a` as an answer.
func (fe *frontendPretty) approvalUnfocusedHint(a *pendingApproval) string {
	switch {
	case fe.shell != nil:
		return approvalJumpKey + " to answer"
	case a.anchor.IsValid() && a.anchor == fe.FocusedSpan:
		return "↓ to answer"
	case fe.focusedApproval() != nil:
		return approvalJumpKey + " to answer"
	default:
		return approvalNavKey + " to answer"
	}
}

// approvalModule is the requesting module's name, else its ref.
func approvalModule(req *prompt.ApprovalRequest) string {
	r := req.GetRequester()
	if mod := r.GetModule(); mod != "" {
		return mod
	}
	return r.GetModuleRef()
}

// approvalRequester renders who asks inside the conversation: the module and
// function, with the module's ref when withRef. The fields arrive already
// escaped; the agent is not named here, and agent_handle (the engine's raw
// id) is only matched on, never shown.
func approvalRequester(req *prompt.ApprovalRequest, withRef bool) string {
	r := req.GetRequester()
	mod := approvalModule(req)
	if ref := r.GetModuleRef(); withRef && ref != "" && ref != mod {
		mod += " (" + ref + ")"
	}
	return joinNonEmpty(" · ", mod, r.GetFunction())
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
