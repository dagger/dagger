package idtui

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
)

// renderToolArgsSummary renders a compact, styled summary of an LLM tool
// call's recognized arguments for the span header line, e.g.
//
//	Read path/to/file
//	Grep some pattern
//	Bash echo hi …
//
// Recognized args are rendered according to their argStyle:
//   - argStylePath:    cyan file path
//   - argStyleDesc:    faint description, first line only
//   - argStyleContent: faint italic content, first line only
//
// It returns true if it rendered anything, in which case the caller should
// skip the default first-arg rendering. Unrecognized tools/args return false
// so the existing fallback rendering is preserved.
func renderToolArgsSummary(out TermOutput, toolName string, span *dagui.Span) bool {
	fields := toolArgFields(span)
	if len(fields) == 0 {
		return false
	}

	rendered := false
	for _, f := range fields {
		style := toolArgStyle(toolName, f.Key)
		if style == argStyleNone {
			continue
		}
		val := sanitizeSummary(firstLine(f.Value))
		if strings.TrimSpace(val) == "" {
			continue
		}
		fmt.Fprint(out, " ")
		switch style {
		case argStylePath:
			fmt.Fprint(out, out.String(val).Foreground(termenv.ANSICyan))
		case argStyleDesc:
			fmt.Fprint(out, out.String(val).Faint())
		case argStyleContent:
			fmt.Fprint(out, out.String(val).Faint().Italic())
		}
		rendered = true
	}
	return rendered
}

// firstLine returns the first line of s, appending an ellipsis if the value
// spanned multiple lines (or was otherwise truncated).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		trimmed := strings.TrimRight(s[:i], " \t\r")
		return trimmed + " …"
	}
	return s
}

// sanitizeSummary makes a value safe to render inline on the span header line.
//
// The header is laid out (and the sidebar overlay is composited onto it) using
// ansi.StringWidth, which counts a tab as ZERO columns — but terminals expand
// tabs to 8-column tab stops. A raw tab in the summary therefore renders wider
// than the layout believes, shoving everything after it (and the overlaid
// "Changes" box) out of alignment. Edit tool excerpts are literal source code
// and routinely start with tab indentation, so this bites the Edit tool in
// particular. Replace tabs with a single space and drop any other control
// characters so the rendered width always matches the computed width. (The
// unified diff below the header already expands tabs; see diffTabWidth.)
func sanitizeSummary(s string) string {
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

// renderToolArgs renders any additional content for an LLM tool-call row that
// is best shown below the title line. Currently this is only a unified diff for
// "edit" tools; for every other tool (and every non-edit / incomplete case) it
// is a no-op, leaving existing rendering untouched.
func (fe *frontendPretty) renderToolArgs(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	fields := toolArgFields(row.Span)
	if len(fields) == 0 {
		return
	}

	toolName := row.Span.LLMTool

	// For edit tools with complete old+new text, render a unified diff.
	fe.tryRenderEditDiff(out, r, row, prefix, toolName, fields)
}

// toolArgFields zips the span's parsed tool-argument name/value arrays into
// parsedField entries. These arrays are populated once the tool call has been
// fully parsed (see core/mcp.go), so every value is Complete.
func toolArgFields(span *dagui.Span) []parsedField {
	n := len(span.LLMToolArgNames)
	if len(span.LLMToolArgValues) < n {
		n = len(span.LLMToolArgValues)
	}
	if n == 0 {
		return nil
	}
	fields := make([]parsedField, 0, n)
	for i := 0; i < n; i++ {
		fields = append(fields, parsedField{
			Key:      span.LLMToolArgNames[i],
			Value:    span.LLMToolArgValues[i],
			Complete: true,
		})
	}
	return fields
}

// tryRenderEditDiff checks whether this is an edit tool call with complete
// old+new text and, if so, renders a unified diff with intraline highlighting.
// Returns true if the diff was rendered (caller should skip normal arg rendering).
func (fe *frontendPretty) tryRenderEditDiff(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, toolName string, fields []parsedField) bool {
	if !isEditTool(toolName) {
		return false
	}

	// Look up the fields we need. Accept various naming conventions.
	filePath := firstField(fields, "path", "filepath", "file_path")
	oldField := firstFieldEntry(fields, "oldtext", "old_text")
	newField := firstFieldEntry(fields, "newtext", "new_text")

	// Need at least one of old or new to show a diff.
	if oldField == nil && newField == nil {
		return false
	}

	// If the fields are still streaming, fall back to the default rendering
	// so the user sees the streaming glitch animation.
	if (oldField != nil && !oldField.Complete) || (newField != nil && !newField.Complete) {
		return false
	}

	oldText := ""
	newText := ""
	if oldField != nil {
		oldText = oldField.Value
	}
	if newField != nil {
		newText = newField.Value
	}

	// Compute available width for the diff.
	// fancyIndent with selfBar=true uses (row.Depth+1)*2 chars,
	// matching the existing maxWidth formula for content-style args.
	diffWidth := fe.window.Width - row.Depth*2 - 4
	if diffWidth < 40 {
		diffWidth = 40
	}

	diffView := renderEditDiff(fe.profile, filePath, oldText, newText, diffWidth)
	if diffView == "" {
		return false
	}

	for _, line := range strings.Split(strings.TrimRight(diffView, "\n"), "\n") {
		fmt.Fprint(out, prefix)
		r.fancyIndent(out, row, true, false)
		fmt.Fprintln(out, line)
	}
	return true
}

// isEditTool returns true if the tool name matches "edit" (case-insensitive,
// handles "Type_edit" Dagger method tools).
func isEditTool(toolName string) bool {
	lower := strings.ToLower(toolName)
	if lower == "edit" {
		return true
	}
	if idx := strings.LastIndex(lower, "_"); idx >= 0 {
		return lower[idx+1:] == "edit"
	}
	return false
}

// firstField returns the value of the first matching field name (case-insensitive).
func firstField(fields []parsedField, names ...string) string {
	for _, name := range names {
		for _, f := range fields {
			if strings.EqualFold(f.Key, name) && f.Value != "" {
				return f.Value
			}
		}
	}
	return ""
}

// firstFieldEntry returns the parsedField pointer for the first matching name.
func firstFieldEntry(fields []parsedField, names ...string) *parsedField {
	for _, name := range names {
		for i := range fields {
			if strings.EqualFold(fields[i].Key, name) {
				return &fields[i]
			}
		}
	}
	return nil
}
