package idtui

import (
	"fmt"
	"sort"
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
	sort.SliceStable(fields, func(i, j int) bool {
		return toolArgPriority(toolName, fields[i].Key) < toolArgPriority(toolName, fields[j].Key)
	})

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
		if toolNameIs(toolName, "read") && (strings.EqualFold(f.Key, "offset") || strings.EqualFold(f.Key, "limit")) {
			val = strings.ToLower(f.Key) + "=" + val
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

func toolArgPriority(toolName, name string) int {
	lower := strings.ToLower(name)
	if toolNameIs(toolName, "read") {
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

func toolNameIs(toolName, want string) bool {
	lower := strings.ToLower(toolName)
	if idx := strings.LastIndex(lower, "_"); idx >= 0 {
		lower = lower[idx+1:]
	}
	return lower == strings.ToLower(want)
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
// characters so the rendered width always matches the computed width.
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
