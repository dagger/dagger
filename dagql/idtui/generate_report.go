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

	regenerated := fe.db.RegeneratedModuleSpans()

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	for _, span := range spans {
		dur := dagui.FormatDuration(span.Activity.Duration(r.now))
		if regen := regenerated[span.Name]; regen != nil {
			// The run changed files under this module and the engine loaded it
			// again with those changes applied: that outcome supersedes the
			// pre-generation load error, which would only send the user chasing
			// a problem this run fixed (or hide the one it didn't).
			dur = dagui.FormatDuration(regen.Activity.Duration(r.now))
			if regen.IsFailed() {
				writeSkippedModuleError(out, span.Name, dur, regen.Status.Description)
			} else {
				fmt.Fprintf(out, "%s %s %s %s\n",
					out.String(IconSuccess).Foreground(termenv.ANSIGreen).String(),
					span.Name,
					out.String(dur).Faint().String(),
					out.String("REGENERATED").Foreground(termenv.ANSIGreen).String(),
				)
				if regen.Message != "" {
					fmt.Fprintf(out, "  %s\n", out.String(regen.Message).Faint().String())
				}
			}
			continue
		}
		writeSkippedModuleError(out, span.Name, dur, span.Status.Description)
	}
	rows := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	return append([]string{reportHeadingLine(out, fe.agentStyle(), "SKIPPED MODULES")}, rows...)
}

// writeSkippedModuleError renders a skipped module's failed status line and
// its error. The error may carry the failure detail on further lines (e.g.
// the SDK runtime's compiler output); those stay aligned under the "! ".
func writeSkippedModuleError(out TermOutput, name, dur, description string) {
	fmt.Fprintf(out, "%s %s %s %s\n",
		out.String(IconFailure).Foreground(termenv.ANSIRed).String(),
		name,
		out.String(dur).Faint().String(),
		out.String("ERROR").Foreground(termenv.ANSIRed).String(),
	)
	for i, line := range strings.Split(description, "\n") {
		if line == "" && i == 0 {
			continue
		}
		marker := "  "
		if i == 0 {
			marker = "! "
		}
		fmt.Fprintf(out, "  %s\n",
			out.String(marker+line).Foreground(termenv.ANSIYellow).String())
	}
}
