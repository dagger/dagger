package dagui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolArgStyleFor(t *testing.T) {
	// Case insensitive matching
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Read", "path"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("read", "path"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("READ", "path"))

	// path variants
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Read", "filePath"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Read", "file_path"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Write", "path"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Edit", "filePath"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Grep", "path"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Find", "path"))
	assert.Equal(t, ToolArgPath, ToolArgStyleFor("Ls", "path"))

	// Type_method matching: tries method part after _
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Container_withExec", "args"))
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("SomeCustomTool", "args"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Git_withCommit", "message")) // no "withcommit.message" rule
	// No rule for "file.path", so Directory_file doesn't match
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Directory_file", "path"))

	// Unknown tool: no special style for path
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("SomeCustomTool", "path"))

	// description on declarative tools
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("DeclareOutput", "description"))
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Save", "description"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Read", "description"))

	// prompt: always content style
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("anything", "prompt"))
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Read", "prompt"))

	// command on Bash
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Bash", "command"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Read", "command"))

	// content/contents on Write
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Write", "content"))
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Write", "contents"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Read", "content"))

	// newText on Edit
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Edit", "newText"))
	assert.Equal(t, ToolArgContent, ToolArgStyleFor("Edit", "new_text"))

	// Grep.regex and Grep.pattern
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Grep", "regex"))
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Grep", "pattern"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Read", "regex"))

	// Commit.message
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Commit", "message"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Read", "message"))

	// Checks.include
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Checks", "include"))
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Check", "include"))
	assert.Equal(t, ToolArgNone, ToolArgStyleFor("Read", "include"))

	// Read pagination
	assert.Equal(t, ToolArgDescription, ToolArgStyleFor("Read", "limit"))
}

func TestToolNameIs(t *testing.T) {
	assert.True(t, ToolNameIs("Read", "read"))
	assert.True(t, ToolNameIs("read", "Read"))
	assert.True(t, ToolNameIs("myserver_Read", "read"))
	assert.False(t, ToolNameIs("ReadFile", "read"))
	assert.False(t, ToolNameIs("Read_file", "read"))
}

func TestFirstLine(t *testing.T) {
	assert.Equal(t, "hello", firstLine("hello"))
	assert.Equal(t, "hello …", firstLine("hello\nworld"))
	assert.Equal(t, "hello …", firstLine("hello  \nworld"))
	assert.Equal(t, " …", firstLine("\nworld"))
}

func TestSanitizeInline(t *testing.T) {
	// Tabs (source-code indentation, common for Edit old_text/new_text) become
	// spaces so ansi.StringWidth matches what the terminal renders.
	assert.Equal(t, "  var x", SanitizeInline("\t var x"))
	// Other control characters are dropped entirely.
	assert.Equal(t, "ab", SanitizeInline("a\x1b\rb"))
	// Ordinary text (including the ellipsis firstLine adds) is untouched.
	assert.Equal(t, "hello …", SanitizeInline("hello …"))
}

func toolCall(tool string, names, values []string) *SpanSnapshot {
	return &SpanSnapshot{
		LLMRole:          "assistant",
		LLMTool:          tool,
		LLMToolArgNames:  names,
		LLMToolArgValues: values,
	}
}

func TestToolArgsSummary(t *testing.T) {
	// No args, or nothing recognized: empty, so frontends fall back.
	assert.Empty(t, toolCall("Read", nil, nil).ToolArgsSummary())
	assert.Empty(t, toolCall("SomeCustomTool", []string{"foo"}, []string{"bar"}).ToolArgsSummary())

	// Styles per argument.
	assert.Equal(t, []ToolArgPart{{Text: "main.go", Style: ToolArgPath}},
		toolCall("Read", []string{"path"}, []string{"main.go"}).ToolArgsSummary())
	assert.Equal(t, []ToolArgPart{{Text: "needle", Style: ToolArgDescription}},
		toolCall("Grep", []string{"pattern"}, []string{"needle"}).ToolArgsSummary())
	assert.Equal(t, []ToolArgPart{{Text: "echo hi", Style: ToolArgContent}},
		toolCall("Bash", []string{"command"}, []string{"echo hi"}).ToolArgsSummary())

	// Read leads with path, offset and limit, which are labeled; unrecognized
	// args are skipped.
	assert.Equal(t, []ToolArgPart{
		{Text: "main.go", Style: ToolArgPath},
		{Text: "offset=20", Style: ToolArgDescription},
		{Text: "limit=10", Style: ToolArgDescription},
	}, toolCall("Read",
		[]string{"limit", "description", "offset", "filePath"},
		[]string{"10", "ignored", "20", "main.go"}).ToolArgsSummary())

	// argv goes last.
	assert.Equal(t, []ToolArgPart{
		{Text: "summarize", Style: ToolArgContent},
		{Text: "foo bar", Style: ToolArgContent},
	}, toolCall("Run", []string{"args", "prompt"}, []string{"foo bar", "summarize"}).ToolArgsSummary())

	// First line only, sanitized; blank values are skipped.
	assert.Equal(t, []ToolArgPart{{Text: " x := 1 …", Style: ToolArgContent}},
		toolCall("Edit",
			[]string{"old_text", "new_text"},
			[]string{"  ", "\tx := 1\n\ty := 2"}).ToolArgsSummary())

	// Mismatched name/value lengths are zipped to the shorter.
	assert.Equal(t, []ToolArgPart{{Text: "main.go", Style: ToolArgPath}},
		toolCall("Read", []string{"path", "offset"}, []string{"main.go"}).ToolArgsSummary())
}

func TestToolArgsFallback(t *testing.T) {
	assert.Equal(t, "", toolCall("Custom", nil, nil).ToolArgsFallback())
	assert.Equal(t, "fix it …", toolCall("Custom",
		[]string{"message", "other"},
		[]string{"fix\tit\n\nlong body", "x"}).ToolArgsFallback())
}

func TestCollapsesToolOutput(t *testing.T) {
	assert.True(t, toolCall("Read", nil, nil).CollapsesToolOutput())
	assert.True(t, toolCall("server_grep", nil, nil).CollapsesToolOutput())
	assert.False(t, toolCall("Bash", nil, nil).CollapsesToolOutput())

	// Only tool-call display spans (with an LLM role) collapse.
	notDisplay := toolCall("Read", nil, nil)
	notDisplay.LLMRole = ""
	assert.False(t, notDisplay.CollapsesToolOutput())
}

func TestToolLabels(t *testing.T) {
	assert.Equal(t, "committer", ToolServerLabel("Committer"))
	assert.Equal(t, "myServer", ToolServerLabel("my_server"))
	assert.Equal(t, "UpdateComment", ToolNameLabel("updateComment"))
	assert.Equal(t, "Read", ToolNameLabel("read"))
}
