package dagui

import (
	"sort"
	"strings"

	"github.com/iancoleman/strcase"
)

// This file holds the shared, presentation-agnostic knowledge of LLM tool-call
// spans: which tools and arguments are recognized, how each argument should be
// styled in a tool call's header, and how it is summarized. Frontends (the
// TUI, the Dagger Cloud web UI) apply their own styling to the result.

// ToolArgStyle describes how a recognized tool argument is rendered in a tool
// call's header.
type ToolArgStyle int

const (
	// ToolArgNone means the argument is not recognized and isn't summarized.
	ToolArgNone ToolArgStyle = iota
	// ToolArgPath means the value is a file path (the TUI shows it in cyan).
	ToolArgPath
	// ToolArgDescription means the value is a description (the TUI shows it
	// faint).
	ToolArgDescription
	// ToolArgContent means the value is content, e.g. a prompt or a command
	// (the TUI shows it faint italic).
	ToolArgContent
)

// ToolArgPart is one recognized argument of a tool call's header summary:
// its display text and how to style it.
type ToolArgPart struct {
	Text  string
	Style ToolArgStyle
}

// toolArgStyles maps "toolname.argname" (lowercase) to a rendering style.
// Tool names are lowercased for matching, so "Read", "read", and
// "myserver_read" (after prefix stripping) all match "read".
//
// For Dagger object method tools like "Container_withExec", the lookup
// tries both the full name and the method part after "_".
var toolArgStyles = map[string]ToolArgStyle{
	// File-oriented tools: path in cyan
	"read.path":       ToolArgPath,
	"read.filepath":   ToolArgPath,
	"read.file_path":  ToolArgPath,
	"read.offset":     ToolArgDescription,
	"read.limit":      ToolArgDescription,
	"write.path":      ToolArgPath,
	"write.filepath":  ToolArgPath,
	"write.file_path": ToolArgPath,
	"edit.path":       ToolArgPath,
	"edit.filepath":   ToolArgPath,
	"edit.file_path":  ToolArgPath,
	"grep.path":       ToolArgPath,
	"find.path":       ToolArgPath,
	"ls.path":         ToolArgPath,

	// Write content
	"write.content":  ToolArgContent,
	"write.contents": ToolArgContent,

	// Edit payloads
	"edit.oldtext":  ToolArgContent,
	"edit.old_text": ToolArgContent,
	"edit.newtext":  ToolArgContent,
	"edit.new_text": ToolArgContent,

	// Shell commands
	"bash.command":  ToolArgContent,
	"withexec.args": ToolArgContent,

	// Grep pattern
	"grep.regex":   ToolArgDescription,
	"grep.pattern": ToolArgDescription,

	// Declarative tools
	"declareoutput.description": ToolArgDescription,
	"save.description":          ToolArgDescription,

	// Commit
	"commit.message": ToolArgDescription,

	// Check/test tools
	"checks.include": ToolArgDescription,
	"check.include":  ToolArgDescription,

	// Sub-agents
	"research.description":   ToolArgDescription,
	"research.prompt":        ToolArgContent,
	"rabbithole.description": ToolArgDescription,
	"rabbithole.prompt":      ToolArgContent,
}

// ToolArgStyleFor returns the rendering style for a given (tool, arg) pair.
// The lookup is case-insensitive. For "Type_method" tool names, it tries
// both the full name and just the method part.
func ToolArgStyleFor(tool, arg string) ToolArgStyle {
	lower := strings.ToLower(tool)
	argLower := strings.ToLower(arg)

	// Try full tool name first
	if style, ok := toolArgStyles[lower+"."+argLower]; ok {
		return style
	}

	// Try method part only (e.g. "container_withexec" → "withexec")
	if idx := strings.LastIndex(lower, "_"); idx >= 0 {
		method := lower[idx+1:]
		if style, ok := toolArgStyles[method+"."+argLower]; ok {
			return style
		}
	}

	// Shell argv is always useful as header text, regardless of the tool name.
	if argLower == "args" {
		return ToolArgContent
	}

	// Fallback: prompt is always content-style regardless of tool.
	if argLower == "prompt" {
		return ToolArgContent
	}

	return ToolArgNone
}

// ToolNameIs reports whether tool names the tool want, case-insensitively and
// ignoring any prefix up to the last "_" (e.g. a "server_" prefix).
func ToolNameIs(tool, want string) bool {
	lower := strings.ToLower(tool)
	if idx := strings.LastIndex(lower, "_"); idx >= 0 {
		lower = lower[idx+1:]
	}
	return lower == strings.ToLower(want)
}

// toolArgPriority orders a tool's arguments in its header summary: Read leads
// with its path, offset and limit, and argv goes last.
func toolArgPriority(tool, arg string) int {
	lower := strings.ToLower(arg)
	if ToolNameIs(tool, "read") {
		switch lower {
		case "path", "filepath", "file_path":
			return 0
		case "offset":
			return 1
		case "limit":
			return 2
		}
	}
	if lower == "args" {
		return 4
	}
	return 3
}

// ToolArgsSummary returns the header summary of a tool call's recognized
// arguments, e.g. Read's path or Bash's command, in display order. Each value
// is reduced to its first line (with " …" when truncated) and sanitized for
// inline display (see SanitizeInline); blank values are skipped, and Read's
// offset and limit are labeled ("offset=20"). It's empty when nothing is
// recognized, in which case frontends fall back to ToolArgsFallback.
func (snapshot *SpanSnapshot) ToolArgsSummary() []ToolArgPart {
	tool := snapshot.LLMTool
	names, values := snapshot.LLMToolArgNames, snapshot.LLMToolArgValues
	n := min(len(names), len(values))
	if n == 0 {
		return nil
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return toolArgPriority(tool, names[order[i]]) < toolArgPriority(tool, names[order[j]])
	})

	var parts []ToolArgPart
	for _, i := range order {
		name := names[i]
		style := ToolArgStyleFor(tool, name)
		if style == ToolArgNone {
			continue
		}
		val := SanitizeInline(firstLine(values[i]))
		if strings.TrimSpace(val) == "" {
			continue
		}
		if ToolNameIs(tool, "read") && (strings.EqualFold(name, "offset") || strings.EqualFold(name, "limit")) {
			val = strings.ToLower(name) + "=" + val
		}
		parts = append(parts, ToolArgPart{Text: val, Style: style})
	}
	return parts
}

// ToolArgsFallback returns what to show for a tool call whose arguments
// aren't recognized (see ToolArgsSummary): the first line of its first
// argument, sanitized for inline display, or "" if it has no arguments. The
// rest are likely to be noisy, and only the first line is kept so a large
// multiline value (e.g. a commit message body) doesn't dominate the header.
func (snapshot *SpanSnapshot) ToolArgsFallback() string {
	if len(snapshot.LLMToolArgValues) == 0 {
		return ""
	}
	return SanitizeInline(firstLine(snapshot.LLMToolArgValues[0]))
}

// CollapsesToolOutput reports whether the span is a tool call whose output
// stays collapsed until expanded: Read and Grep, whose output is bulky and
// whose header already says what they did.
func (snapshot *SpanSnapshot) CollapsesToolOutput() bool {
	if snapshot.LLMRole == "" || snapshot.LLMTool == "" {
		return false
	}
	return ToolNameIs(snapshot.LLMTool, "read") || ToolNameIs(snapshot.LLMTool, "grep")
}

// ToolServerLabel returns how a tool call's server is displayed, e.g.
// "Committer" -> "committer".
func ToolServerLabel(server string) string {
	return strcase.ToLowerCamel(server)
}

// ToolNameLabel returns how a tool call's tool name is displayed, e.g.
// "updateComment" -> "UpdateComment".
func ToolNameLabel(tool string) string {
	return strcase.ToCamel(tool)
}

// firstLine returns the first line of s, appending an ellipsis if the value
// spanned multiple lines.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		trimmed := strings.TrimRight(s[:i], " \t\r")
		return trimmed + " …"
	}
	return s
}

// SanitizeInline makes a value safe to render inline on a single header line:
// tabs become a single space and other control characters are dropped.
//
// Terminal layouts measure text with ansi.StringWidth, which counts a tab as
// zero columns while terminals expand it to the next tab stop, so a raw tab
// would render wider than laid out. Edit tool excerpts are literal source code
// and routinely start with tab indentation, so this bites the Edit tool in
// particular.
func SanitizeInline(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1 // drop other control chars (stray CR, escapes, etc.)
		default:
			return r
		}
	}, s)
}
