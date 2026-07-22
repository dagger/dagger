package idtui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"

	"github.com/dagger/dagger/dagql/dagui"
)

func TestToolArgStyle(t *testing.T) {
	// Case insensitive matching
	assert.Equal(t, argStylePath, toolArgStyle("Read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("read", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("READ", "path"))

	// path variants
	assert.Equal(t, argStylePath, toolArgStyle("Read", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Read", "file_path"))
	assert.Equal(t, argStylePath, toolArgStyle("Write", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Edit", "filePath"))
	assert.Equal(t, argStylePath, toolArgStyle("Grep", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Find", "path"))
	assert.Equal(t, argStylePath, toolArgStyle("Ls", "path"))

	// Type_method matching: tries method part after _
	assert.Equal(t, argStyleContent, toolArgStyle("Container_withExec", "args"))
	assert.Equal(t, argStyleNone, toolArgStyle("Git_withCommit", "message")) // no "withcommit.message" rule
	// No rule for "file.path", so Directory_file doesn't match
	assert.Equal(t, argStyleNone, toolArgStyle("Directory_file", "path"))

	// Unknown tool: no special style for path
	assert.Equal(t, argStyleNone, toolArgStyle("SomeCustomTool", "path"))

	// description on declarative tools
	assert.Equal(t, argStyleDesc, toolArgStyle("DeclareOutput", "description"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Save", "description"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "description"))

	// prompt: always content style
	assert.Equal(t, argStyleContent, toolArgStyle("anything", "prompt"))
	assert.Equal(t, argStyleContent, toolArgStyle("Read", "prompt"))

	// command on Bash
	assert.Equal(t, argStyleContent, toolArgStyle("Bash", "command"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "command"))

	// content/contents on Write
	assert.Equal(t, argStyleContent, toolArgStyle("Write", "content"))
	assert.Equal(t, argStyleContent, toolArgStyle("Write", "contents"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "content"))

	// newText on Edit (oldText intentionally omitted)
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "newText"))
	assert.Equal(t, argStyleContent, toolArgStyle("Edit", "new_text"))

	// Grep.regex and Grep.pattern
	assert.Equal(t, argStyleDesc, toolArgStyle("Grep", "regex"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Grep", "pattern"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "regex"))

	// Commit.message
	assert.Equal(t, argStyleDesc, toolArgStyle("Commit", "message"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "message"))

	// Checks.include
	assert.Equal(t, argStyleDesc, toolArgStyle("Checks", "include"))
	assert.Equal(t, argStyleDesc, toolArgStyle("Check", "include"))
	assert.Equal(t, argStyleNone, toolArgStyle("Read", "include"))

	// isConventionalArg
	assert.True(t, isConventionalArg("Read", "path"))
	assert.True(t, isConventionalArg("Write", "content"))
	assert.True(t, isConventionalArg("anything", "prompt"))
	assert.False(t, isConventionalArg("Read", "limit"))
	assert.False(t, isConventionalArg("Read", "description"))
}

func TestFirstLine(t *testing.T) {
	assert.Equal(t, "hello", firstLine("hello"))
	assert.Equal(t, "hello …", firstLine("hello\nworld"))
	assert.Equal(t, "hello …", firstLine("hello  \nworld"))
	assert.Equal(t, " …", firstLine("\nworld"))
}

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
	renderToolArgsSummary(out, toolName, span)
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

func TestSanitizeSummary(t *testing.T) {
	// Tabs (source-code indentation, common for Edit old_text/new_text) become
	// spaces so ansi.StringWidth matches what the terminal renders.
	assert.Equal(t, "  var x", sanitizeSummary("\t var x"))
	// Other control characters are dropped entirely.
	assert.Equal(t, "ab", sanitizeSummary("a\x1b\rb"))
	// Ordinary text (including the ellipsis firstLine adds) is untouched.
	assert.Equal(t, "hello …", sanitizeSummary("hello …"))
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
