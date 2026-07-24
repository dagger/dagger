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
