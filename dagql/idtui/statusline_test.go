package idtui

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// TestRenderContextBar locks in the eighth-cell fill math for the status-line
// context gauge. Colours are stripped (Ascii profile) so the assertions read
// the raw block characters.
func TestRenderContextBar(t *testing.T) {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(termenv.Ascii))

	for _, tc := range []struct {
		name    string
		percent float64
		want    string
	}{
		{"empty", 0, "░░░░░░░░░░"},
		{"quarter", 25, "██▌░░░░░░░"},
		{"half", 50, "█████░░░░░"},
		{"near-full", 90, "█████████░"},
		{"full", 100, "██████████"},
		{"overflow clamps to full", 150, "██████████"},
		{"negative clamps to empty", -1, "░░░░░░░░░░"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderContextBar(out, tc.percent)
			if got != tc.want {
				t.Errorf("renderContextBar(%.0f) = %q, want %q", tc.percent, got, tc.want)
			}
		})
	}
}

// TestStatusLineOmitsSyntheticWorkingIndicator keeps agent lifecycle state
// sourced exclusively from the roster.
func TestStatusLineOmitsSyntheticWorkingIndicator(t *testing.T) {
	sl := &StatusLine{profile: termenv.Ascii}
	sl.data = StatusLineData{Model: "claude-opus-4-6", InputTokens: 100}

	term := tuist.NewHeadlessTerminal(80, 1)
	tui := tuist.New(term)
	tui.AddChild(sl)
	tui.RenderOnce()
	line := strings.Join(tui.Frame(), "\n")
	if strings.Contains(line, "working") || strings.Contains(line, DotFilled) {
		t.Fatalf("agent state must come from the roster: %q", line)
	}
}

// TestStatusLineRendersReorientedLayout locks in the bottom-bar grouping: the
// roster and context meter stay at the left, while usage, cost, subscription,
// and model form one right-aligned group in that order.
func TestStatusLineRendersReorientedLayout(t *testing.T) {
	roster := NewAgentRoster(termenv.Ascii, func() []AgentRosterEntry {
		return []AgentRosterEntry{{Name: "interactive", State: "IDLE"}}
	})
	sl := &StatusLine{
		profile: termenv.Ascii,
		roster:  roster,
		data: StatusLineData{
			Model:             "gpt-5.6-sol",
			SubscriptionLabel: "ChatGPT",
			InputTokens:       551,
			OutputTokens:      11,
			CacheReads:        12000,
			TotalCost:         0.009,
			ContextPercent:    1.3,
			ContextWindow:     1100000,
			AutoCompact:       true,
		},
	}

	const width = 140
	term := tuist.NewHeadlessTerminal(width, 1)
	tui := tuist.New(term)
	tui.AddChild(sl)
	tui.RenderOnce()
	line := strings.TrimRight(strings.Join(tui.Frame(), "\n"), " ")

	left := "1 agent ○ ▏░░░░░░░░░ 1.3%/1.1M (auto)"
	if !strings.HasPrefix(line, left) {
		t.Fatalf("expected roster and context at the left, got:\n%q", line)
	}
	right := "↑551 ↓11 R12k $0.009 (ChatGPT) gpt-5.6-sol"
	if !strings.HasSuffix(line, right) {
		t.Fatalf("expected stats and model grouped at the right, got:\n%q", line)
	}
	if gap := strings.TrimPrefix(strings.TrimSuffix(line, right), left); len(gap) < 2 || strings.TrimSpace(gap) != "" {
		t.Fatalf("expected blank alignment padding between left and right groups, got %q", gap)
	}
}

// TestStatusLineRendersRosterWithoutModel keeps the roster visible during the
// startup window before model metadata reaches the frontend.
func TestStatusLineRendersRosterWithoutModel(t *testing.T) {
	roster := NewAgentRoster(termenv.Ascii, func() []AgentRosterEntry {
		return []AgentRosterEntry{{Name: "interactive", State: "RUNNING"}}
	})
	sl := &StatusLine{profile: termenv.Ascii, roster: roster}

	term := tuist.NewHeadlessTerminal(80, 1)
	tui := tuist.New(term)
	tui.AddChild(sl)
	tui.RenderOnce()
	line := strings.Join(tui.Frame(), "\n")
	if !strings.Contains(line, "1 agent ▶") {
		t.Fatalf("expected roster before model selection, got %q", line)
	}
}

// TestStatusLineRendersContextBar verifies the gauge is wired into the status
// line next to the percentage text once context usage is known.
func TestStatusLineRendersContextBar(t *testing.T) {
	sl := &StatusLine{profile: termenv.Ascii}
	sl.data = StatusLineData{
		Model:          "claude-opus-4-6",
		ContextWindow:  200000,
		ContextPercent: 50,
	}

	term := tuist.NewHeadlessTerminal(80, 1)
	tui := tuist.New(term)
	tui.AddChild(sl)
	tui.RenderOnce()

	line := strings.Join(tui.Frame(), "\n")
	if !strings.Contains(line, "50.0%/200k") {
		t.Fatalf("expected context percentage in status line, got:\n%q", line)
	}
	// A half-full gauge precedes the text: five filled cells then five empty.
	if !strings.Contains(line, "█████░░░░░ 50.0%/200k") {
		t.Fatalf("expected half-full context gauge before the text, got:\n%q", line)
	}
}

// TestStatusLineOmitsContextBarWhenUnknown verifies no gauge is drawn when the
// usage is unknown (negative percent), leaving just the "?/<window>" text.
func TestStatusLineOmitsContextBarWhenUnknown(t *testing.T) {
	sl := &StatusLine{profile: termenv.Ascii}
	sl.data = StatusLineData{
		Model:          "claude-opus-4-6",
		ContextWindow:  200000,
		ContextPercent: -1,
	}

	term := tuist.NewHeadlessTerminal(80, 1)
	tui := tuist.New(term)
	tui.AddChild(sl)
	tui.RenderOnce()

	line := strings.Join(tui.Frame(), "\n")
	if !strings.Contains(line, "?/200k") {
		t.Fatalf("expected unknown-context text, got:\n%q", line)
	}
	if strings.ContainsAny(line, "█░") {
		t.Fatalf("expected no gauge when usage is unknown, got:\n%q", line)
	}
}

// TestStatusLineSeededFromResume reproduces the resume ordering: LoadSession
// pushes the restored conversation's stats via SetStatusLine before the
// interactive shell (and thus the status line component) is created. The
// frontend must retain that data and seed the new status line with it, so a
// resumed conversation shows its token/context stats immediately instead of
// waiting for the user to send a message and generate fresh live metrics.
func TestStatusLineSeededFromResume(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	term := tuist.NewHeadlessTerminal(120, 10)
	fe := newWithTerminal(io.Discard, dagui.NewDB(), term)
	// Bring up the TUI without the event loop, then drive it via Step.
	fe.setupTUI()

	// Resume order: stats arrive before the status line exists.
	fe.SetStatusLine(StatusLineData{
		Model:          "claude-opus-4-8",
		InputTokens:    12000,
		OutputTokens:   3400,
		ContextWindow:  200000,
		ContextPercent: 50,
	})
	// Flush the dispatched update; the status line doesn't exist yet, so this
	// only records the data for later.
	_ = fe.tui.Step()

	// The shell — and its status line — start after the resume.
	fe.startShell(context.Background(), stubShellHandler{})

	frame := strings.Join(fe.tui.Step(), "\n")
	for _, want := range []string{"claude-opus-4-8", "50.0%/200k", "↑12k", "↓3.4k"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("resumed status line missing %q:\n%s", want, frame)
		}
	}
}
