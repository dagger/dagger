package idtui

import (
	"fmt"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
)

// renderToolArgsSummary renders a compact, styled summary of an LLM tool
// call's recognized arguments (dagui's ToolArgsSummary) for the span header
// line, e.g.
//
//	Read path/to/file
//	Grep some pattern
//	Bash echo hi …
//
// Recognized args are rendered according to their dagui.ToolArgStyle:
//   - ToolArgPath:        cyan file path
//   - ToolArgDescription: faint description
//   - ToolArgContent:     faint italic content
//
// It returns true if it rendered anything, in which case the caller should
// skip the default first-arg rendering. Unrecognized tools/args return false
// so the fallback rendering is preserved.
func renderToolArgsSummary(out TermOutput, span *dagui.Span) bool {
	parts := span.ToolArgsSummary()
	for _, part := range parts {
		fmt.Fprint(out, " ")
		switch part.Style {
		case dagui.ToolArgPath:
			fmt.Fprint(out, out.String(part.Text).Foreground(termenv.ANSICyan))
		case dagui.ToolArgDescription:
			fmt.Fprint(out, out.String(part.Text).Faint())
		case dagui.ToolArgContent:
			fmt.Fprint(out, out.String(part.Text).Faint().Italic())
		}
	}
	return len(parts) > 0
}
