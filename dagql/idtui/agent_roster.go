package idtui

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
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
	// WaitingOn is what the agent is parked on when State is WAITING_INPUT.
	WaitingOn string
	// Focused marks the entry the prompt currently addresses.
	Focused bool
	// ReadOnly marks an agent this client cannot address: the engine never
	// advertised a call digest for it, or the handle failed to rebuild from
	// the trace. Such an entry can be watched, not spoken to, and says so.
	ReadOnly bool
}

// AgentRoster renders a compact list of the session's live agents as tabs — a
// faint jump number, display name and lifecycle symbol each, padded by a cell
// either side — on one line, the focused tab filled like the prompt card:
//
//	1 agent ▶  2 scout ○  3 docs ▶  4 tests needs you
//
// The roster is embedded at the left of the prompt's status line. It is always
// visible once an agent has been published: besides being a switcher, it is the
// prompt's state indicator.
// Focus moves only by a keypress (ctrl+1…9 or alt+l from the prompt; 1…9, `
// or [/] in nav mode), never by an event: an agent that needs the user
// advertises attention on its entry and waits. Nothing here may steal focus.
type AgentRoster struct {
	tuist.Compo

	profile termenv.Profile
	// entries is consulted on every render, so the strip tracks live state
	// without the frontend having to push updates into it (same pattern as
	// StatusLine.liveStats).
	entries func() []AgentRosterEntry
	// background, when set, returns the fill for the focused entry's tab: the
	// prompt card's shade, so the tab reads as part of the prompt it
	// addresses. Nil (or a nil color) falls back to reverse video.
	background func() color.Color
}

// NewAgentRoster creates a roster strip sourcing its entries from the given
// callback.
func NewAgentRoster(profile termenv.Profile, entries func() []AgentRosterEntry) *AgentRoster {
	return &AgentRoster{profile: profile, entries: entries}
}

// SetBackgroundSource sets where the focused tab's fill comes from (see
// AgentRoster.background). It is read at render time; whoever changes the
// color re-renders the roster's host.
func (r *AgentRoster) SetBackgroundSource(background func() color.Color) {
	r.background = background
	r.Update()
}

// Entries returns the roster's current entries, or nil when there is no
// source.
func (r *AgentRoster) Entries() []AgentRosterEntry {
	if r.entries == nil {
		return nil
	}
	return r.entries()
}

// Visible reports whether the roster renders anything.
func (r *AgentRoster) Visible() bool {
	return len(r.Entries()) > 0
}

// Switchable reports whether roster focus shortcuts should be advertised and
// claimed. A single entry is useful as a state display, but not as a switcher.
func (r *AgentRoster) Switchable() bool {
	return len(r.Entries()) > 1
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
	if !r.Visible() {
		return ""
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(r.profile))
	entries := r.Entries()
	parts := make([]string, 0, len(entries))
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
		part := " " + number + nameStyle.String()
		if label != "" {
			part += " " + out.String(label).Foreground(labelColor).String()
		}
		part += " "
		if entry.Focused {
			part = r.focusTab(part)
		}
		parts = append(parts, part)
	}

	// The tabs' own padding separates them.
	line := strings.Join(parts, "")
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

// focusTab marks the focused entry's tab, padding included: filled with the
// prompt card's shade when one is known, else reverse video. The fill is
// applied per cell so the segments' own styling (faint number, colored symbol)
// survives inside it. A leading reset drops the status line's dim foreground,
// so the tab reads at full contrast.
func (r *AgentRoster) focusTab(part string) string {
	if r.profile == termenv.Ascii {
		return part
	}
	var bg color.Color
	if r.background != nil {
		bg = r.background()
	}
	return ansi.ResetStyle + restyleCells(part, func(style *cellbuf.Style) {
		if bg != nil {
			style.Bg = bg
		} else {
			style.Reverse(true)
		}
	})
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
		// No state record seen yet: the agent is published but its runtime
		// has not reported in. Render it as present-but-unknown rather than
		// guessing a state.
		return "", termenv.ANSIBrightBlack
	}
}
