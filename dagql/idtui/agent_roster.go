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

// AgentRoster renders a tmux-style strip of the session's live agents
// directly above the prompt — a jump number, name and state flag each, on one
// line, with the focused entry marked and inverted:
//
//	1:chief* ●run   2:scout ○idle   3:docs ●run   4:tests !needs you
//
// It sits next to the prompt rather than in the sidebar because "who is
// running, and who needs me" is a question asked while typing, and the
// sidebar is a top-right overlay that occludes the tree it summarizes and
// has no selection model to grow into a switcher.
//
// Focus moves only by a keypress (alt+1…9 or alt+l from the prompt; 1…9, `
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

// Visible reports whether the strip renders anything. A session with a
// single agent is the ordinary case and gets no strip: the status line
// already says whether that one agent is working, so a roster of one is
// pure chrome.
func (r *AgentRoster) Visible() bool {
	return len(r.Entries()) > 1
}

// Height is the strip's line count, for the frontend's chrome budgeting.
func (r *AgentRoster) Height() int {
	if !r.Visible() {
		return 0
	}
	return 1
}

func (r *AgentRoster) Render(ctx tuist.Context) {
	if !r.Visible() {
		return
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(r.profile))
	entries := r.Entries()
	parts := make([]string, 0, len(entries))
	for i, entry := range entries {
		glyph, label, color := agentStateDisplay(entry.State)

		// Jump numbers only where a jump key exists (alt+1…9 from the
		// prompt, 1…9 in nav mode); beyond that the entry is still listed,
		// just not directly addressable by key -- [/] still walks onto it.
		var prefix string
		if i < 9 {
			prefix = strconv.Itoa(i+1) + ":"
		}

		name := entry.Name
		switch {
		case entry.Focused:
			// tmux's current-window marker: a textual mark, so focus is
			// legible on a terminal (or in a test) that keeps no styling.
			name += "*"
		case entry.ReadOnly:
			// Watch-only: the client holds no handle for it, so mark it
			// rather than letting it look like something you can talk to.
			name += "·"
		}

		nameStyle := out.String(prefix + name)
		if entry.Focused {
			nameStyle = nameStyle.Reverse().Bold()
		} else if entry.ReadOnly {
			nameStyle = nameStyle.Foreground(termenv.ANSIBrightBlack)
		} else {
			nameStyle = nameStyle.Foreground(termenv.ANSIWhite)
		}
		flag := out.String(glyph + label).Foreground(color).String()
		parts = append(parts, nameStyle.String()+" "+flag)
	}

	line := strings.Join(parts, out.String("   ").String())
	if ctx.Width > 0 {
		line = ansi.Truncate(line, ctx.Width, "…")
	}
	ctx.Lines(line)
}

// agentStateDisplay maps a lifecycle state to its glyph, short label and
// color. Only two states are attention-grabbing — WAITING_INPUT and FAILED,
// the two where the ball is in the user's court — and they are the only ones
// that get a warm color; everything else stays quiet so the strip does not
// compete with the trace for attention.
func agentStateDisplay(state string) (glyph, label string, color termenv.Color) {
	switch state {
	case "WAITING_INPUT":
		return "!", "needs you", termenv.ANSIYellow
	case "FAILED":
		return "✘", "failed", termenv.ANSIRed
	case "RUNNING":
		return DotFilled, "run", termenv.ANSIGreen
	case "PAUSED":
		return "‖", "paused", termenv.ANSIBrightBlack
	case "STOPPED":
		return "✔", "stopped", termenv.ANSIBrightBlack
	case "IDLE":
		return DotEmpty, "idle", termenv.ANSIBrightBlack
	default:
		// No state record seen yet: the agent is published but its runtime
		// has not reported in. Render it as present-but-unknown rather than
		// guessing a state.
		return DotTiny, "", termenv.ANSIBrightBlack
	}
}
