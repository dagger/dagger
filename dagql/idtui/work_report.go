package idtui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
)

// WorkFrontend is the optional interface of a frontend that shows the work of
// a trace ('dagger trace --work'): a NEW or REPEAT status on the spans that
// did work, and a WORK section at the end of the report.
type WorkFrontend interface {
	SetTraceWork(*cloud.TraceWork)
}

var _ WorkFrontend = (*frontendPretty)(nil)

// workReportRuns is how many runs of each class the WORK section lists.
const workReportRuns = 10

// SetTraceWork sets the work the frontend shows.
func (fe *frontendPretty) SetTraceWork(work *cloud.TraceWork) {
	bySpan := map[dagui.SpanID]*cloud.WorkRun{}
	if work != nil {
		for i := range work.Runs {
			run := &work.Runs[i]
			for _, id := range []string{run.SpanID, run.CallSpanID} {
				if spanID, err := trace.SpanIDFromHex(id); err == nil {
					bySpan[dagui.SpanID{SpanID: spanID}] = run
				}
			}
		}
	}
	fe.dispatch(func() {
		fe.work = work
		fe.workBySpan = bySpan
	})
}

// spanWork returns the run a span shows, or nil.
func (fe *frontendPretty) spanWork(id dagui.SpanID) *cloud.WorkRun {
	if fe.workBySpan == nil {
		return nil
	}
	return fe.workBySpan[id]
}

// renderWorkStatus renders a span's NEW or REPEAT status, and reports whether
// it has one.
func renderWorkStatus(out TermOutput, run *cloud.WorkRun) bool {
	if run == nil {
		return false
	}
	fmt.Fprint(out, out.String(" "))
	fmt.Fprint(out, workLabel(out, run.Work))
	return true
}

func workLabel(out TermOutput, work string) string {
	if work == cloud.WorkRepeat {
		return out.String("REPEAT").Foreground(termenv.ANSIMagenta).String()
	}
	return out.String("NEW").Foreground(termenv.ANSICyan).String()
}

// renderWorkSection renders the WORK section of the final report.
func (fe *frontendPretty) renderWorkSection() []string {
	if fe.work == nil {
		return nil
	}
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	return WorkReportLines(out, fe.agentStyle(), fe.work, fe.traceCommand)
}

// WorkReportLines renders a trace's work: the new, repeat and cached totals,
// then the new and repeat runs with the most CPU time, each with its span, so
// it can be shown with '<command> <trace> --span <span>'.
func WorkReportLines(out TermOutput, agent bool, work *cloud.TraceWork, command string) []string {
	if work == nil {
		return nil
	}
	if command == "" {
		command = defaultTraceCommand
	}
	s := work.Summary
	runs := func(n int) string {
		if n == 1 {
			return "1 run"
		}
		return fmt.Sprintf("%d runs", n)
	}
	body := []string{
		fmt.Sprintf("%s  %s, %s CPU", padLabel(out, cloud.WorkNew), runs(s.New.Runs), formatCPU(s.New.CPUSeconds)),
		fmt.Sprintf("%s  %s, %s CPU", padLabel(out, cloud.WorkRepeat), runs(s.Repeat.Runs), formatCPU(s.Repeat.CPUSeconds)),
		fmt.Sprintf("%s  %d cache hits", out.String(fmt.Sprintf("%-6s", "CACHED")).Foreground(termenv.ANSIBlue).String(), s.Cached.Calls),
	}
	for _, class := range []string{cloud.WorkRepeat, cloud.WorkNew} {
		var list []cloud.WorkRun
		for _, run := range work.Runs {
			if run.Work == class {
				list = append(list, run)
			}
		}
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].CPUSeconds > list[j].CPUSeconds })
		title := "Repeat work"
		if class == cloud.WorkNew {
			title = "New work"
		}
		body = append(body, "", out.String(fmt.Sprintf("%s, by CPU time:", title)).Bold().String())
		for _, run := range list[:min(len(list), workReportRuns)] {
			body = append(body, workRunLine(out, work.TraceID, run))
		}
		if len(list) > workReportRuns {
			body = append(body, out.String(fmt.Sprintf("  ... and %d more", len(list)-workReportRuns)).Faint().String())
		}
	}
	body = append(body, "", out.String(fmt.Sprintf(
		"Repeat work ran before in this organization, within %d days, on Dagger Cloud Engines. Show a run with '%s %s --span <span>'.",
		work.RepeatWindowDays, command, work.TraceID)).Faint().String())
	return reportSectionLines(out, agent, "WORK", body)
}

func padLabel(out TermOutput, work string) string {
	label := "NEW   "
	if work == cloud.WorkRepeat {
		label = "REPEAT"
	}
	color := termenv.ANSICyan
	if work == cloud.WorkRepeat {
		color = termenv.ANSIMagenta
	}
	return out.String(label).Foreground(color).String()
}

func workRunLine(out TermOutput, traceID string, run cloud.WorkRun) string {
	span := run.CallSpanID
	if span == "" {
		span = run.SpanID
	}
	var detail []string
	if run.Executions > 0 {
		detail = append(detail, plural(run.Executions, "execution"))
	}
	if run.Helpers > 0 {
		detail = append(detail, plural(run.Helpers, "helper command"))
	}
	if ev := run.Evidence; ev != nil {
		when := "ran"
		if gap := run.StartTime.Sub(ev.EndTime); gap > 0 {
			when = "ran " + dagui.FormatDuration(gap.Round(time.Second)) + " before"
		}
		if ev.TraceID == traceID {
			detail = append(detail, when+" in this trace, span "+ev.SpanID)
		} else {
			detail = append(detail, when+" in trace "+ev.TraceID)
		}
	}
	line := fmt.Sprintf("  %9s CPU  %s  %s", formatCPU(run.CPUSeconds), run.Name, out.String(span).Faint().String())
	if len(detail) > 0 {
		line += out.String("  " + strings.Join(detail, ", ")).Faint().String()
	}
	return line
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// formatCPU formats CPU seconds as a duration.
func formatCPU(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return dagui.FormatDuration(d.Round(time.Second))
}
