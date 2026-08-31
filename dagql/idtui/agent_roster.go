package idtui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// AgentRosterEntry is one agent's line in the roster: its identity, its
// display name, and the lifecycle state the engine last published for it.
type AgentRosterEntry struct {
	// ID is the agent's spawn-minted instance ID — the address a focus
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

// AgentRoster renders a compact list of the session's live agents — a bold
// jump number, display name and lifecycle symbol each, on one line:
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
}

// NewAgentRoster creates a roster strip sourcing its entries from the given
// callback.
func NewAgentRoster(profile termenv.Profile, entries func() []AgentRosterEntry) *AgentRoster {
	return &AgentRoster{profile: profile, entries: entries}
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
		label, color := agentStateDisplay(entry.State)

		// Jump numbers only where a jump key exists (ctrl+1…9 from the
		// prompt, 1…9 in nav mode); beyond that the entry is still listed,
		// just not directly addressable by key -- [/] still walks onto it.
		var number string
		if i < 9 {
			number = out.String(strconv.Itoa(i+1)).Bold().String() + " "
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
			nameStyle = nameStyle.Reverse().Bold()
		case entry.ReadOnly:
			nameStyle = nameStyle.Foreground(termenv.ANSIBrightBlack)
		default:
			nameStyle = nameStyle.Foreground(termenv.ANSIWhite)
		}
		part := number + nameStyle.String()
		if label != "" {
			part += " " + out.String(label).Foreground(color).String()
		}
		parts = append(parts, part)
	}

	line := strings.Join(parts, "  ")
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

// agentStateDisplay maps a lifecycle state to its compact symbol and color.
// WAITING_INPUT keeps its attention label; only it and FAILED are
// attention-grabbing. Everything else stays quiet so the roster does not
// compete with the trace for attention.
func agentStateDisplay(state string) (label string, color termenv.Color) {
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
