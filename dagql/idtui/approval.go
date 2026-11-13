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
//   - It never takes focus on its own. Navigation treats it as one more row
//     right below its tool call: the user steps onto it (↓ from the tool call,
//     ↑ from the row after) or jumps to it (`a` in nav mode, alt+a at the
//     prompt), and it gives focus back once answered or left with esc. The
//     tool call row itself keeps its usual keys, so enter there still zooms
//     rather than answering.
//   - Once on an approval, its keys answer it straight away: there is no
//     second "enter the prompt" step. Stepping onto it is already deliberate
//     (it is its own stop, visibly focused), the default answer is Deny, and
//     granting takes a choice and a confirm (or y/s/f). Nav keys that mean
//     something else on a row (h/l, enter, n) steer the approval instead;
//     j/k and esc still leave it.
//   - The agent it blocks is cued on the roster, so the user knows there is
//     something to navigate to.
//
// A request whose span has not arrived (or whose rows are zoomed away) is
// "floating": it renders below the trace, reachable after the last row and
// still without focus, and is re-anchored as soon as its span shows up in the
// rows.

var _ prompt.ApprovalHandler = (*frontendPretty)(nil)

// approvalJumpKey jumps to the next pending approval from the prompt (and in
// nav mode, for muscle memory); nav mode also takes the bare approvalNavKey.
const (
	approvalJumpKey = "alt+a"
	approvalNavKey  = "a"
)

type approvalChoice struct {
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

	// armed is set when the user navigates to the approval. Only an armed
	// approval may hold keyboard focus, so one that appears under the cursor
	// (or that the cursor follows new output onto) never steals keys.
	armed bool
	// returnToInput records that the user jumped here from the prompt, so
	// answering hands focus back to it rather than leaving them in nav mode;
	// followOutput whether the cursor was following new output then, which
	// it resumes.
	returnToInput bool
	followOutput  bool

	view *ApprovalView
}

// approvalChoices lists the answers in increasing order of trust: the safe
// answer first (and selected by default), the widest grant last. Forever is
// only offered when the request names a key it can be remembered under.
func approvalChoices(req *prompt.ApprovalRequest) []approvalChoice {
	choices := []approvalChoice{
		{"Deny", prompt.APPROVAL_DENY},
		{"Allow once", prompt.APPROVAL_ALLOW_ONCE},
		{"Allow for this session", prompt.APPROVAL_ALLOW_SESSION},
	}
	if req.GetPersistentKey() != "" {
		choices = append(choices, approvalChoice{"Allow forever", prompt.APPROVAL_ALLOW_FOREVER})
	}
	return choices
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
// else to the row it sits under.
func (fe *frontendPretty) restoreApprovalFocus(a *pendingApproval) {
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
// navigating, if any: an armed one under the cursor, else an armed floating
// one the user jumped to.
func (fe *frontendPretty) focusableApproval() *pendingApproval {
	var floating *pendingApproval
	for _, a := range fe.approvals {
		if !a.armed {
			continue
		}
		if a.anchor.IsValid() {
			if a.anchor == fe.FocusedSpan {
				return a
			}
		} else if floating == nil {
			floating = a
		}
	}
	return floating
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

// disarmApprovals drops the arming of every approval the cursor is no longer
// on. Floating approvals stay armed unless includeFloating: only the user's
// own navigation leaves those.
func (fe *frontendPretty) disarmApprovals(cursor dagui.SpanID, includeFloating bool) {
	for _, a := range fe.approvals {
		if !a.armed {
			continue
		}
		if a.anchor.IsValid() {
			if a.anchor != cursor {
				a.armed = false
				a.returnToInput = false
				a.followOutput = false
			}
		} else if includeFloating {
			a.armed = false
			a.returnToInput = false
			a.followOutput = false
		}
	}
}

// approvalSlot is one stop of row navigation while approvals are pending:
// a row, or an approval (just after the row it sits under, or after the last
// row while it floats).
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

// stepApproval moves the cursor by one stop when approvals are pending,
// treating each as a row below its tool call. Reports whether it handled the
// move; with no approvals pending, plain row navigation applies.
func (fe *frontendPretty) stepApproval(delta int) bool {
	if len(fe.approvals) == 0 || fe.rows == nil {
		return false
	}
	slots := fe.approvalSlots()
	cur := -1
	if a := fe.focusableApproval(); a != nil {
		cur = slices.IndexFunc(slots, func(s approvalSlot) bool { return s.approval == a })
	} else if fe.FocusedSpan.IsValid() {
		cur = slices.IndexFunc(slots, func(s approvalSlot) bool {
			return s.row != nil && s.row.Span.ID == fe.FocusedSpan
		})
	}
	if cur < 0 {
		return false
	}
	next := cur + delta
	if next < 0 || next >= len(slots) {
		return true
	}
	if s := slots[next]; s.approval != nil {
		fe.focusApproval(s.approval, false)
	} else {
		fe.manualFocus(s.row)
	}
	return true
}

// focusApproval puts the cursor on an approval and gives it keyboard focus.
// fromInput records that the user came from the prompt, to return there.
func (fe *frontendPretty) focusApproval(a *pendingApproval, fromInput bool) {
	fe.autoFocus = false
	for _, other := range fe.approvals {
		other.armed = false
		other.returnToInput = false
		other.followOutput = false
	}
	a.armed = true
	a.returnToInput = fromInput
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

// syncApprovalFocus moves focus off an approval view that should no longer
// hold it (re-anchored away from the cursor, or disarmed).
func (fe *frontendPretty) syncApprovalFocus() {
	if fe.tui == nil {
		return
	}
	v, ok := fe.tui.Focused().(*ApprovalView)
	if !ok {
		return
	}
	if target := fe.navigationTarget(); target != v {
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
		for _, c := range a.choices {
			if c.decision == decision {
				fe.finishApproval(a, decision, nil)
				return true
			}
		}
		return false
	}
	switch keyStr {
	case "left", "h":
		if a.selected > 0 {
			a.selected--
			a.view.Update()
		}
		return true
	case "right", "l":
		if a.selected < len(a.choices)-1 {
			a.selected++
			a.view.Update()
		}
		return true
	case "enter":
		fe.finishApproval(a, a.choices[a.selected].decision, nil)
		return true
	case "y", "o":
		return pick(prompt.APPROVAL_ALLOW_ONCE)
	case "s":
		return pick(prompt.APPROVAL_ALLOW_SESSION)
	case "f":
		return pick(prompt.APPROVAL_ALLOW_FOREVER)
	case "n", "d":
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
	_, forever := approvalChoiceIndex(a, prompt.APPROVAL_ALLOW_FOREVER)
	return []key.Binding{
		key.NewBinding(key.WithKeys("←→", "left", "right", "h", "l"), key.WithHelp("←→", "choose")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
		key.NewBinding(key.WithKeys("y", "o"), key.WithHelp("y", "once")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "session")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "forever"), KeyEnabled(forever)),
		key.NewBinding(key.WithKeys("n", "d"), key.WithHelp("n", "deny")),
		key.NewBinding(key.WithKeys("↑↓", "up", "down", "j", "k"), key.WithHelp("↑↓", "move away")),
		key.NewBinding(key.WithKeys("esc", "alt+esc"), key.WithHelp("esc", "leave")),
		key.NewBinding(key.WithKeys(approvalNavKey), key.WithHelp(approvalNavKey, "next approval"),
			KeyEnabled(len(fe.approvals) > 1)),
	}
}

func approvalChoiceIndex(a *pendingApproval, decision prompt.ApprovalDecision) (int, bool) {
	for i, c := range a.choices {
		if c.decision == decision {
			return i, true
		}
	}
	return -1, false
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

// approvalLines renders an approval: who is asking, what for, the choices,
// and how to answer.
func (fe *frontendPretty) approvalLines(a *pendingApproval, focused bool, width int) []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	req := a.req

	// Every field arrives as literal display text the engine already escaped
	// (prompt.ApprovalRequest), so it renders as-is: escaping or stripping it
	// again would mangle what the engine chose to show.

	accent := termenv.ANSIYellow
	if req.GetDanger() {
		accent = termenv.ANSIRed
	}

	var lines []string
	add := func(line string) {
		if width > 0 {
			line = ansi.Wrap(line, width, "")
		}
		lines = append(lines, strings.Split(line, "\n")...)
	}

	// Who is asking.
	header := out.String(IconApproval+" approval needed").Foreground(accent).Bold().String()
	if who := approvalRequester(req); who != "" {
		header += out.String(" · " + who).Faint().String()
	}
	add(header)

	// What for. The header already names who asks, so this is the bare
	// question (the full prompt, which names them again, is for yes/no
	// fallbacks). A destructive one carries its danger in red, the way the
	// yes/no fallback flags a force-push.
	question := req.GetQuestion()
	if question == "" {
		question = req.GetPrompt()
	}
	style := func(s string) string {
		return out.String(s).Foreground(termenv.ANSIYellow).Bold().String()
	}
	danger := func(s string) string {
		return out.String(s).Foreground(termenv.ANSIRed).Bold().String()
	}
	switch {
	case req.GetDanger() && strings.Contains(question, forcePushPhrase):
		before, after, _ := strings.Cut(question, forcePushPhrase)
		var b strings.Builder
		if before != "" {
			b.WriteString(style(before))
		}
		b.WriteString(danger(forcePushPhrase))
		if after != "" {
			b.WriteString(style(after))
		}
		add(b.String())
	case req.GetDanger():
		add(danger(question))
	default:
		add(style(question))
	}
	if subject := req.GetSubject(); subject != "" && !strings.Contains(question, subject) {
		add(out.String("subject: ").Faint().String() + subject)
	}

	// The choices.
	var choices []string
	for i, c := range a.choices {
		label := c.label
		switch {
		case focused && i == a.selected:
			choices = append(choices, out.String(CaretRightFilled+" "+label).Bold().String())
		case !focused && i == a.selected:
			choices = append(choices, "  "+out.String(label).Bold().Faint().String())
		default:
			choices = append(choices, "  "+out.String(label).Faint().String())
		}
	}
	add(strings.Join(choices, "   "))

	// How to answer.
	if focused {
		hint := "←/→ choose · enter confirm · y once · s session"
		if _, ok := approvalChoiceIndex(a, prompt.APPROVAL_ALLOW_FOREVER); ok {
			hint += " · f forever"
		}
		hint += " · n deny · esc leave"
		add(out.String(hint).Faint().String())
	} else {
		add(out.String(fe.approvalUnfocusedHint(a)).Faint().String())
	}
	return lines
}

// approvalUnfocusedHint says how to reach an approval that does not have
// focus. alt+a works from the prompt and nav mode alike; outside the shell
// there is no prompt, so the bare key does.
func (fe *frontendPretty) approvalUnfocusedHint(a *pendingApproval) string {
	jump := approvalNavKey
	if fe.shell != nil {
		jump = approvalJumpKey
	}
	if a.anchor.IsValid() {
		return "select it (↓ from the row above) or press " + jump + " to answer"
	}
	return "press " + jump + " to answer"
}

// approvalRequester renders who is asking: agent, module and function. The
// fields arrive already escaped, and agent_handle (the engine's raw id) is
// only matched on, never shown.
func approvalRequester(req *prompt.ApprovalRequest) string {
	r := req.GetRequester()
	if r == nil {
		return ""
	}
	var parts []string
	if name := r.GetAgentName(); name != "" {
		parts = append(parts, "agent "+name)
	}
	if mod := r.GetModule(); mod != "" {
		if ref := r.GetModuleRef(); ref != "" && ref != mod {
			mod += " (" + ref + ")"
		}
		parts = append(parts, "module "+mod)
	} else if ref := r.GetModuleRef(); ref != "" {
		parts = append(parts, "module "+ref)
	}
	if fn := r.GetFunction(); fn != "" {
		parts = append(parts, fn)
	}
	return strings.Join(parts, " · ")
}
