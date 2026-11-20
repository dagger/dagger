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

var (
	_ WorkFrontend = (*frontendPretty)(nil)
	_ WorkFrontend = (*frontendPlain)(nil)
)

// workReportRuns is how many runs of each class the WORK section lists.
const workReportRuns = 10

// SetTraceWork sets the work the frontend shows.
func (fe *frontendPretty) SetTraceWork(work *cloud.TraceWork) {
	bySpan := workRunsBySpan(work)
	fe.dispatch(func() {
		fe.work = work
		fe.workBySpan = bySpan
	})
}

// workRunsBySpan indexes the runs by the spans that show them: the run's span
// and, for a lazy evaluation, the call that returned its result.
func workRunsBySpan(work *cloud.TraceWork) map[dagui.SpanID]*cloud.WorkRun {
	bySpan := map[dagui.SpanID]*cloud.WorkRun{}
	if work == nil {
		return bySpan
	}
	for i := range work.Runs {
		run := &work.Runs[i]
		for _, id := range []string{run.SpanID, run.CallSpanID} {
			if spanID, err := trace.SpanIDFromHex(id); err == nil {
				bySpan[dagui.SpanID{SpanID: spanID}] = run
			}
		}
	}
	return bySpan
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

// WorkReportLines renders a trace's work: the share of each class of the
// trace's operations, then the uncached repeat runs and the new runs with the
// most CPU time, each with its span, so it can be shown with
// '<command> <trace> --span <span>'.
//
// The operations are the runs with work, new or repeat, and the cache hits. A
// cache hit is repeat work too: it reused a result of work that ran before.
// So the repeat work is the uncached repeat runs, which ran again, and the
// cache hits.
func WorkReportLines(out TermOutput, agent bool, work *cloud.TraceWork, command string) []string {
	if work == nil {
		return nil
	}
	if command == "" {
		command = defaultTraceCommand
	}
	s := work.Summary
	newOps, uncached, cached := s.New.Runs, s.Repeat.Runs, s.Cached.Calls
	repeat := uncached + cached
	total := newOps + repeat
	pct := func(n, of int) string {
		if of == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(of))
	}
	label := func(text string, color termenv.Color) string {
		return out.String(fmt.Sprintf("%-10s", text)).Foreground(color).String()
	}
	faint := func(text string) string { return out.String(text).Faint().String() }
	body := []string{
		fmt.Sprintf("%s %7d  %6s  %s", label("NEW", termenv.ANSICyan), newOps, pct(newOps, total),
			faint(formatCPU(s.New.CPUSeconds)+" CPU")),
		fmt.Sprintf("%s %7d  %6s", label("REPEAT", termenv.ANSIMagenta), repeat, pct(repeat, total)),
		fmt.Sprintf("%s %7d  %6s  %s", label("  uncached", termenv.ANSIMagenta), uncached, pct(uncached, total),
			faint(fmt.Sprintf("%s of repeat work, %s CPU", pct(uncached, repeat), formatCPU(s.Repeat.CPUSeconds)))),
		fmt.Sprintf("%s %7d  %6s  %s", label("  cached", termenv.ANSIBlue), cached, pct(cached, total),
			faint(pct(cached, repeat)+" of repeat work")),
		faint(fmt.Sprintf("%d operations: runs that did work, and cache hits", total)),
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
		title := "Uncached repeat work"
		if class == cloud.WorkNew {
			title = "New work"
		}
		body = append(body, "", out.String(fmt.Sprintf("%s, by CPU time:", title)).Bold().String())
		for _, run := range list[:min(len(list), workReportRuns)] {
			body = append(body, workRunLine(out, work.TraceID, run))
		}
		if len(list) > workReportRuns {
			body = append(body, faint(fmt.Sprintf("  ... and %d more", len(list)-workReportRuns)))
		}
	}
	body = append(body, "", faint(fmt.Sprintf(
		"Uncached repeat work ran before in this organization, within %d days, on Dagger Cloud Engines, and ran again. Show a run with '%s %s --span <span>'.",
		work.RepeatWindowDays, command, work.TraceID)))
	return reportSectionLines(out, agent, "WORK", body)
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
