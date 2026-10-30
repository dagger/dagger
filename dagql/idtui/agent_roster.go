package idtui

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// AgentRosterEntry is one agent's line in the roster: its identity, its
// display name, and the lifecycle state the engine last published for it.
type AgentRosterEntry struct {
	// ID is the agent's spawn-minted runtime handle — the address a focus
	// request names. It is never the display name, which carries no identity.
	ID   string
	Name string
	// State is the lifecycle state the engine last published.
	State string
	// Focused marks the entry the prompt currently addresses.
	Focused bool
	// ReadOnly marks an agent this client cannot address: the engine never
	// advertised a call digest for it, or the handle failed to rebuild from
	// the trace. Such an entry can be watched, not spoken to, and says so.
	ReadOnly bool
	// Waiting and Failed count the agents beneath this one, with no tab of
	// their own, that are WAITING_INPUT or FAILED: the strip only shows part
	// of a deep roster, and what needs the user must not be in the part it
	// leaves out, so it surfaces on the nearest tab above.
	Waiting, Failed int
}

// AgentRosterView is what the strip shows of the roster: the tabs, in tree
// order, and how much of the rest it folded away.
type AgentRosterView struct {
	// Tabs are the entries with a tab of their own. Jump numbers address
	// them, in this order.
	Tabs []AgentRosterEntry
	// Done counts the STOPPED agents that would have had a tab, folded into
	// one trailing chip instead: finished work stays on the record without
	// costing a tab apiece.
	Done int
	// Total counts every agent on the roster, tabs or not -- all of them
	// reachable by [/].
	Total int
}

// AgentRoster renders a compact list of the session's live agents as tabs — a
// faint jump number, display name and lifecycle symbol each, padded by a cell
// either side — on one line, the focused tab filled like the prompt card and
// edged by its border, the card's bottom edge opening above it:
//
//	1 agent ▶  2 scout ○ +1 needs you  3 docs ▶  +4 done
//
// The roster is embedded at the left of the prompt's status line. It is always
// visible once an agent has been published: besides being a switcher, it is the
// prompt's state indicator.
// Focus moves only by a keypress (ctrl+1…9, alt+l or alt+[/] from the prompt;
// 1…9, ` or [/] in nav mode), never by an event: an agent that needs the user
// advertises attention on its entry and waits. Nothing here may steal focus.
type AgentRoster struct {
	tuist.Compo

	profile termenv.Profile
	// view is consulted on every render, so the strip tracks live state
	// without the frontend having to push updates into it (same pattern as
	// StatusLine.liveStats).
	view func() AgentRosterView
	// tabColors, when set, returns the focused entry's tab colors: the prompt
	// card's shade as its fill and the card's border as its side edges, so
	// the tab reads as part of the prompt it addresses -- the card's bottom
	// edge opens above it (see PromptFrame.SetTabSource). A nil fill falls
	// back to reverse video; a nil border leaves the sides as plain padding.
	tabColors func() (fill, border color.Color)
}

// Glyphs for the focused tab's sides: eighth blocks drawn flush against the
// cell's outer edge, continuing the card's border down around the tab.
const (
	rosterTabLeftEdge  = "▏"
	rosterTabRightEdge = "▕"
)

// NewAgentRoster creates a roster strip sourcing what it shows from the given
// callback.
func NewAgentRoster(profile termenv.Profile, view func() AgentRosterView) *AgentRoster {
	return &AgentRoster{profile: profile, view: view}
}

// SetTabColorSource sets where the focused tab's colors come from (see
// AgentRoster.tabColors). It is read at render time; whoever changes the
// colors re-renders the roster's host.
func (r *AgentRoster) SetTabColorSource(colors func() (fill, border color.Color)) {
	r.tabColors = colors
	r.Update()
}

// View returns what the roster currently shows, or nothing when there is no
// source.
func (r *AgentRoster) View() AgentRosterView {
	if r.view == nil {
		return AgentRosterView{}
	}
	return r.view()
}

// Entries returns the roster's current tabs -- the entries jump numbers
// address.
func (r *AgentRoster) Entries() []AgentRosterEntry {
	return r.View().Tabs
}

// Visible reports whether the roster renders anything.
func (r *AgentRoster) Visible() bool {
	view := r.View()
	return len(view.Tabs) > 0 || view.Done > 0
}

// Switchable reports whether roster focus shortcuts should be advertised and
// claimed. A single agent is useful as a state display, but not as a
// switcher. Agents without a tab count: [/] reaches them.
func (r *AgentRoster) Switchable() bool {
	view := r.View()
	return max(len(view.Tabs), view.Total) > 1
}

// Height is the roster's standalone line count.
func (r *AgentRoster) Height() int {
	if !r.Visible() {
		return 0
	}
	return 1
}

func (r *AgentRoster) Render(ctx tuist.Context) {
	if line := r.Line(ctx.Width); line != "" {
		ctx.Lines(line)
	}
}

// Line renders the roster as a single line, truncated to width when positive.
// The status line places it before the context meter.
func (r *AgentRoster) Line(width int) string {
	line, _, _ := r.layout(width)
	return line
}

// FocusedTab reports the columns [start, end) the focused entry's tab spans
// in Line(width), clipped to what survives truncation. ok is false when no
// entry is focused or its tab is truncated away entirely.
func (r *AgentRoster) FocusedTab(width int) (start, end int, ok bool) {
	_, start, end = r.layout(width)
	return start, end, start < end
}

// layout renders the roster line along with the focused tab's column span.
func (r *AgentRoster) layout(width int) (line string, tabStart, tabEnd int) {
	if !r.Visible() {
		return "", 0, 0
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(r.profile))
	view := r.View()
	entries := view.Tabs
	parts := make([]string, 0, len(entries)+1)
	col := 0
	for i, entry := range entries {
		label, labelColor := agentStateDisplay(entry.State)

		// Jump numbers only where a jump key exists (ctrl+1…9 from the
		// prompt, 1…9 in nav mode); beyond that the entry is still listed,
		// just not directly addressable by key -- [/] still walks onto it.
		// The number is a quiet key hint, the same faint color focused or not.
		var number string
		if i < 9 {
			number = out.String(strconv.Itoa(i+1)).Foreground(termenv.ANSIBrightBlack).String() + " "
		}

		name := entry.Name
		if name == "interactive" {
			name = "agent"
		}
		if entry.ReadOnly {
			// Watch-only: the client holds no handle for it, so retain one
			// quiet mark rather than implying it can be addressed.
			name += "·"
		}

		nameStyle := out.String(name)
		switch {
		case entry.Focused:
			nameStyle = nameStyle.Bold()
		case entry.ReadOnly:
			nameStyle = nameStyle.Foreground(termenv.ANSIBrightBlack)
		default:
			// The terminal's own foreground, dimmed: the focused tab shows the
			// same color at full strength (ANSI white can outshine it).
			nameStyle = nameStyle.Faint()
		}
		// Each entry is a tab with a cell of padding either side, which the
		// focused tab's fill covers too.
		content := number + nameStyle.String()
		if label != "" {
			content += " " + out.String(label).Foreground(labelColor).String()
		}
		// Attention from beneath: the label's own words and colors, counted,
		// so it reads as "this many more like that, under here".
		if entry.Waiting > 0 {
			content += " " + out.String("+"+strconv.Itoa(entry.Waiting)+" needs you").Foreground(termenv.ANSIYellow).String()
		}
		if entry.Failed > 0 {
			content += " " + out.String("+"+strconv.Itoa(entry.Failed)+" "+IconFailure).Foreground(termenv.ANSIRed).String()
		}
		var part string
		if entry.Focused {
			// The strip leads the line, so a tab at column 0 sits flush with
			// the screen edge, where the card above draws no left border.
			part = r.focusTab(content, col > 0)
		} else {
			part = " " + content + " "
		}
		partWidth := ansi.StringWidth(part)
		if entry.Focused {
			tabStart, tabEnd = col, col+partWidth
		}
		col += partWidth
		parts = append(parts, part)
	}
	if view.Done > 0 {
		// Quiet, like an unfocused STOPPED tab: no number, since a jump
		// key would name a whole group. [/] walks onto its members.
		part := " " + out.String("+"+strconv.Itoa(view.Done)+" done").Foreground(termenv.ANSIBrightBlack).String() + " "
		col += ansi.StringWidth(part)
		parts = append(parts, part)
	}

	// The tabs' own padding separates them.
	line = strings.Join(parts, "")
	if width > 0 && col > width {
		line = ansi.Truncate(line, width, "…")
		// Only the cells before the ellipsis survive.
		tabEnd = min(tabEnd, width-1)
		tabStart = min(tabStart, tabEnd)
	}
	return line, tabStart, tabEnd
}

// focusTab marks the focused entry's tab around its content, padding
// included: filled with the prompt card's shade when one is known, else
// reverse video. With the card's border known too, the padding cells become
// thin side edges in that color, so the tab hangs off the card above it --
// except the left edge when leftEdge is false (the tab leads the line, and the
// card has no left border to continue), which stays filled padding. The fill
// is applied per cell so the segments' own styling (faint number, colored
// symbol) survives inside it. A leading reset drops the status line's dim
// foreground, so the tab reads at full contrast.
func (r *AgentRoster) focusTab(content string, leftEdge bool) string {
	if r.profile == termenv.Ascii {
		return " " + content + " "
	}
	var bg, border color.Color
	if r.tabColors != nil {
		bg, border = r.tabColors()
	}
	fill := func(s string) string {
		return restyleCells(s, func(style *cellbuf.Style) {
			if bg != nil {
				style.Bg = bg
			} else {
				style.Reverse(true)
			}
		})
	}
	if bg == nil || border == nil {
		return ansi.ResetStyle + fill(" "+content+" ")
	}
	side := func(glyph string) string {
		return restyleCells(glyph, func(style *cellbuf.Style) {
			style.Fg = border
			style.Bg = bg
		})
	}
	left := fill(" ")
	if leftEdge {
		left = side(rosterTabLeftEdge)
	}
	return ansi.ResetStyle + left + fill(content) + side(rosterTabRightEdge)
}

// agentRosterModel is the roster as the frontend navigates it: every agent,
// and what the strip shows of them.
type agentRosterModel struct {
	// order is every agent, depth-first by lineage: each agent followed by
	// its descendants, siblings in the order they appeared. [/] walks it, so
	// nothing the strip leaves out is out of reach.
	order []AgentRosterEntry
	view  AgentRosterView
}

// buildAgentRosterModel arranges the session's agents (db.Agents(), whose
// ParentID links always end at a root) for the strip, relative to the focused
// agent. entry projects one agent to its entry, Focused aside.
//
// A deep session -- a chief, its workers, their reviewers, the agents an
// integration test spawned inside one of them -- cannot be one line of tabs,
// so the strip shows the neighborhood of focus:
//
//   - every root agent;
//   - the focused agent, its chain of ancestors, and its direct children:
//     where it sits, and who it is directing.
//
// With nothing focused, every root's direct children stand in for the
// focused agent's: the session's own agent is a root, so this is the same
// view focusing it would give -- the top two levels, a chief and its
// workers -- rather than a strip that changes shape on the first focus.
//
// Within that, STOPPED agents fold into the trailing "+N done" chip: STOPPED
// is terminal, so their tabs would only grow. The focused agent and its
// ancestors never fold, since they are the context the rest is read in.
// Nor does an agent that stops while the user is looking: keep holds the
// agents that had a tab under the current focus, and they keep it until
// focus next moves, so a tab never vanishes -- renumbering the ones after it
// -- between a glance at the strip and the jump key it prompted.
//
// Nothing becomes unreachable: [/] walks order, which holds everybody, and
// whatever [/] lands on is focused and therefore shown. The cycle steps over
// read-only agents, though (until their handle rebuilds; see cycleAgent), so
// a read-only agent's children join the neighborhood with it: they could
// never come into view by focusing it.
//
// What needs the user must not hide in the part left out: an agent with no
// tab that is WAITING_INPUT or FAILED counts on its nearest ancestor that the
// neighborhood includes, which keeps (or regains) its tab to say so.
func buildAgentRosterModel(agents []*dagui.AgentNode, focused string, keep map[string]bool, entry func(*dagui.AgentNode) AgentRosterEntry) agentRosterModel {
	n := len(agents)
	if n == 0 {
		return agentRosterModel{}
	}
	index := make(map[string]int, n)
	for i, agent := range agents {
		index[agent.ID] = i
	}
	parent := make([]int, n)
	children := make([][]int, n)
	var roots []int
	for i, agent := range agents {
		p, ok := index[agent.ParentID]
		if agent.ParentID == "" || !ok || p == i {
			parent[i] = -1
			roots = append(roots, i)
			continue
		}
		parent[i] = p
		children[p] = append(children[p], i)
	}

	// Depth-first, guarded against revisits so a malformed tree still
	// terminates; anything a guard skipped is appended as a root.
	order := make([]int, 0, n)
	visited := make([]bool, n)
	var walk func(int)
	walk = func(i int) {
		if visited[i] {
			return
		}
		visited[i] = true
		order = append(order, i)
		for _, child := range children[i] {
			walk(child)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	for i := range agents {
		walk(i)
	}

	focus := -1
	if i, ok := index[focused]; ok && focused != "" {
		focus = i
	}
	entries := make([]AgentRosterEntry, n)
	for i, agent := range agents {
		entries[i] = entry(agent)
	}

	// The neighborhood of focus, and the ancestors that never fold. A
	// read-only agent cannot be focused -- nothing walks onto it -- so its
	// own children could never come into view by focusing it: they join the
	// neighborhood whenever it does.
	near := make([]bool, n)
	chain := make([]bool, n)
	var include func(int)
	include = func(i int) {
		if near[i] {
			return
		}
		near[i] = true
		if entries[i].ReadOnly {
			for _, child := range children[i] {
				include(child)
			}
		}
	}
	for _, root := range roots {
		include(root)
	}
	if focus >= 0 {
		for i := focus; i >= 0 && !chain[i]; i = parent[i] {
			include(i)
			chain[i] = true
		}
		for _, child := range children[focus] {
			include(child)
		}
	} else {
		for _, root := range roots {
			for _, child := range children[root] {
				include(child)
			}
		}
	}

	// STOPPED agents fold, unless they are the context of focus, or stopped
	// while holding a tab under it.
	tab := make([]bool, n)
	for i, agent := range agents {
		tab[i] = near[i] && (agent.State != "STOPPED" || chain[i] || keep[agent.ID])
	}

	// Attention bubbles to the nearest ancestor in the neighborhood -- every
	// root is in it, so there always is one -- which gets a tab to show it.
	waiting := make([]int, n)
	failed := make([]int, n)
	for i, agent := range agents {
		if tab[i] || (agent.State != "WAITING_INPUT" && agent.State != "FAILED") {
			continue
		}
		at := i
		for !near[at] && parent[at] >= 0 {
			at = parent[at]
		}
		tab[at] = true
		if agent.State == "WAITING_INPUT" {
			waiting[at]++
		} else {
			failed[at]++
		}
	}

	model := agentRosterModel{
		order: make([]AgentRosterEntry, 0, n),
		view:  AgentRosterView{Total: n},
	}
	for _, i := range order {
		e := entries[i]
		e.Focused = i == focus
		e.Waiting, e.Failed = waiting[i], failed[i]
		model.order = append(model.order, e)
		switch {
		case tab[i]:
			model.view.Tabs = append(model.view.Tabs, e)
		case near[i]:
			model.view.Done++
		}
	}
	return model
}

// agentStateDisplay maps a lifecycle state to its compact symbol and color.
// WAITING_INPUT keeps its attention label; only it and FAILED are
// attention-grabbing. Everything else stays quiet so the roster does not
// compete with the trace for attention.
func agentStateDisplay(state string) (label string, labelColor termenv.Color) {
	switch state {
	case "WAITING_INPUT":
		return "needs you", termenv.ANSIYellow
	case "FAILED":
		return IconFailure, termenv.ANSIRed
	case "RUNNING":
		return CaretRightFilled, termenv.ANSIGreen
	case "PAUSED":
		return IconPause, termenv.ANSIBrightBlack
	case "STOPPED":
		return IconStop, termenv.ANSIBrightBlack
	case "IDLE":
		return DotEmpty, termenv.ANSIBrightBlack
	default:
		// No control record seen yet: the agent is published but its runtime
		// has not reported in. Render it as present-but-unknown rather than
		// guessing a state.
		return "", termenv.ANSIBrightBlack
	}
}
