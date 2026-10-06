package idtui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"

	"github.com/dagger/dagger/dagql/dagui"
)

func renderSummary(t *testing.T, toolName string, names, values []string) string {
	t.Helper()
	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.ANSI))
	span := &dagui.Span{
		SpanSnapshot: dagui.SpanSnapshot{
			LLMTool:          toolName,
			LLMToolArgNames:  names,
			LLMToolArgValues: values,
		},
	}
	renderToolArgsSummary(out, span)
	return buf.String()
}

func TestRenderToolArgsSummary(t *testing.T) {
	// No args => nothing rendered.
	assert.Empty(t, renderSummary(t, "Read", nil, nil))

	// Unrecognized tool/arg => nothing rendered (caller falls back).
	assert.Empty(t, renderSummary(t, "SomeCustomTool", []string{"foo"}, []string{"bar"}))

	// Path arg rendered in cyan (SGR 36).
	got := renderSummary(t, "Read", []string{"path"}, []string{"main.go"})
	assert.Contains(t, stripANSICodes(got), "main.go")
	assert.Contains(t, got, "\x1b[36m")

	// Desc arg rendered faint (SGR 2).
	got = renderSummary(t, "Grep", []string{"pattern"}, []string{"needle"})
	assert.Contains(t, stripANSICodes(got), "needle")
	assert.Contains(t, got, "\x1b[2m")

	// Content arg rendered faint italic (SGR 2;3).
	got = renderSummary(t, "Bash", []string{"command"}, []string{"echo hi"})
	assert.Contains(t, stripANSICodes(got), "echo hi")
	assert.Contains(t, got, "\x1b[2;3m")

	// Read pagination is labeled so numeric values remain meaningful.
	got = renderSummary(t, "Read",
		[]string{"path", "offset", "limit"},
		[]string{"main.go", "20", "10"})
	assert.Equal(t, " main.go offset=20 limit=10", stripANSICodes(got))

	// Generic argv is already flattened by core and reads like a shell command.
	got = renderSummary(t, "Run", []string{"args"}, []string{"foo bar"})
	assert.Equal(t, " foo bar", stripANSICodes(got))

	// Multi-line content collapses to the first line with an ellipsis.
	got = renderSummary(t, "Bash", []string{"command"}, []string{"echo hi\nrm -rf /"})
	plain := stripANSICodes(got)
	assert.Contains(t, plain, "echo hi …")
	assert.NotContains(t, plain, "rm -rf /")
}

// tabExpandWidth is what a real terminal advances for s, expanding tabs to
// 8-column tab stops (ANSI escapes contribute zero width). It's the "truth"
// the layout must match.
func tabExpandWidth(s string) int {
	s = ansi.Strip(s)
	col := 0
	for _, r := range s {
		if r == '\t' {
			col += 8 - (col % 8)
		} else {
			col += ansi.StringWidth(string(r))
		}
	}
	return col
}

// TestRenderToolArgsSummaryTabWidth is a regression test for the Edit-tool
// glitch: a raw tab in the excerpt is zero-width to ansi.StringWidth but
// expands on the terminal, desyncing the header line (and the overlaid sidebar
// box) after it. The rendered summary must contain no raw tab and must have a
// visible width equal to its tab-expanded width.
func TestRenderToolArgsSummaryTabWidth(t *testing.T) {
	// Edit tool call whose old/new text is tab-indented source code.
	got := renderSummary(t, "Edit",
		[]string{"old_text", "new_text"},
		[]string{"\tvar selfDigest string\n\t\tif ok {", "\tvar selfDigest string\n\t\tif no {"})

	assert.NotContains(t, got, "\t", "summary must not emit raw tabs")
	assert.Equal(t, ansi.StringWidth(got), tabExpandWidth(got),
		"computed width must match terminal-rendered (tab-expanded) width")
}
