package idtui

import (
	"strings"
	"testing"

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
