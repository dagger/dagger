package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// generateReport renders the SKIPPED MODULES section for the final report: the
// workspace modules best-effort `dagger generate` could not load and skipped,
// each as a failed status line with its load error nested. It mirrors the
// checks report but is flat (skips have no sub-nodes) and, crucially, persists
// on a successful run -- the live tree that showed each skip collapses when the
// command exits 0, so this is what the user still sees afterward. Returns nil
// when zoomed or when nothing was skipped, so the caller can fall through.
func (fe *frontendPretty) generateReport(_ tuist.Context, r *renderer, zoomed bool) []string {
	if zoomed {
		return nil
	}
	spans := fe.db.SkippedModuleSpans()
	if len(spans) == 0 {
		return nil
	}

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	for _, span := range spans {
		dur := dagui.FormatDuration(span.Activity.Duration(r.now))
		fmt.Fprintf(out, "%s %s %s %s\n",
			out.String(IconFailure).Foreground(termenv.ANSIRed).String(),
			span.Name,
			out.String(dur).Faint().String(),
			out.String("ERROR").Foreground(termenv.ANSIRed).String(),
		)
		if span.Status.Description != "" {
			fmt.Fprintf(out, "  %s\n",
				out.String("! "+span.Status.Description).Foreground(termenv.ANSIYellow).String())
		}
	}
	rows := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	return append([]string{reportHeadingLine(out, "SKIPPED MODULES")}, rows...)
}
