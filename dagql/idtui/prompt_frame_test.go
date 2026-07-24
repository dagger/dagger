package idtui

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// TestPromptFrameRendersFramedInput verifies the LLM prompt input is framed by
// a full-width horizontal rule above and below, with the input flush to the
// edge and no background shading.
func TestPromptFrameRendersFramedInput(t *testing.T) {
	const width = 40

	input := tuist.NewTextInput("")
	input.SetValue("hello there")

	frame := NewPromptFrame(input, termenv.ANSI)
	frame.SetEnabled(true)

	term := tuist.NewHeadlessTerminal(width, 6)
	tui := tuist.New(term)
	tui.AddChild(frame)
	tui.RenderOnce()

	lines := tui.Frame()
	joined := strings.Join(lines, "\n")

	var barIdxs []int
	inputIdx := -1
	for i, line := range lines {
		plain := strings.TrimRight(stripANSICodes(line), " ")
		switch {
		case plain != "" && strings.Trim(plain, HorizBar) == "":
			barIdxs = append(barIdxs, i)
		case strings.Contains(plain, "hello there"):
			inputIdx = i
		}
	}

	if len(barIdxs) < 2 {
		t.Fatalf("expected two horizontal rules framing the prompt, got %d:\n%s", len(barIdxs), visibleEscapes(joined))
	}
	if inputIdx == -1 {
		t.Fatalf("prompt input line not found:\n%s", visibleEscapes(joined))
	}
	// The input must sit between the two rules.
	if !(barIdxs[0] < inputIdx && inputIdx < barIdxs[len(barIdxs)-1]) {
		t.Fatalf("input line (%d) is not framed by rules (%v):\n%s", inputIdx, barIdxs, visibleEscapes(joined))
	}

	// Both rules span the full width.
	for _, bi := range []int{barIdxs[0], barIdxs[len(barIdxs)-1]} {
		if got := len([]rune(strings.TrimRight(stripANSICodes(lines[bi]), " "))); got != width {
			t.Fatalf("rule width = %d, want %d:\n%s", got, width, visibleEscapes(joined))
		}
	}

	// The input is flush to the edge (no prompt symbol before it).
	if got := stripANSICodes(lines[inputIdx]); !strings.HasPrefix(got, "hello there") {
		t.Fatalf("input is not flush to the edge, got %q:\n%s", got, visibleEscapes(joined))
	}

	// No background shading (ANSIBrightBlack bg = SGR 100) anywhere in the frame.
	if strings.Contains(joined, "\x1b[100m") {
		t.Fatalf("prompt frame must not render a background:\n%s", visibleEscapes(joined))
	}
}

// TestPromptFrameDisabledRendersBare verifies that when framing is disabled
// (plain shell mode) the input is rendered without any rules.
func TestPromptFrameDisabledRendersBare(t *testing.T) {
	input := tuist.NewTextInput("⋈ ")
	input.SetValue("ls -la")

	frame := NewPromptFrame(input, termenv.ANSI)
	// enabled defaults to false.

	term := tuist.NewHeadlessTerminal(40, 4)
	tui := tuist.New(term)
	tui.AddChild(frame)
	tui.RenderOnce()

	lines := tui.Frame()
	joined := strings.Join(lines, "\n")

	for _, line := range lines {
		trimmed := strings.TrimRight(stripANSICodes(line), " ")
		if trimmed != "" && strings.Trim(trimmed, HorizBar) == "" {
			t.Fatalf("bare (disabled) prompt must not render rules:\n%s", visibleEscapes(joined))
		}
	}
}
