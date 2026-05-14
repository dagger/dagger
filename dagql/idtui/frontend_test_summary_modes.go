package idtui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const testSummaryExcerptLogLines = 8

func appendTestSummaryLogRecords(logs map[dagui.SpanID]*Vterm, profile termenv.Profile, spanID dagui.SpanID, records []sdklog.Record) {
	if logs == nil || !spanID.IsValid() {
		return
	}
	for _, record := range records {
		contentType, skip := testSummaryLogRecordInfo(record)
		if skip {
			continue
		}
		body := record.Body().AsString()
		if body == "" {
			continue
		}
		vt := logs[spanID]
		if vt == nil {
			vt = NewVterm(profile)
			logs[spanID] = vt
		}
		if contentType == "text/markdown" {
			_, _ = vt.WriteMarkdown([]byte(body))
		} else {
			_, _ = fmt.Fprint(vt, body)
		}
	}
}

func testSummaryLogRecordInfo(record sdklog.Record) (contentType string, skip bool) {
	record.WalkAttributes(func(kv log.KeyValue) bool {
		switch kv.Key {
		case telemetry.ContentTypeAttr:
			contentType = kv.Value.AsString()
		case telemetry.StdioEOFAttr, telemetry.LogsVerboseAttr:
			if kv.Value.AsBool() {
				skip = true
				return false
			}
		}
		return true
	})
	return contentType, skip
}

func renderPlainTestSummaryLines(view *dagui.TestView, logs map[dagui.SpanID]*Vterm, logLimit int) []string {
	return renderTextTestSummaryLines(view, logs, textTestSummaryOptions{
		Title:       "Tests",
		Indent:      0,
		EntryIndent: 0,
		LogPrefix:   "  | ",
		Separator:   " > ",
		LogLimit:    logLimit,
	})
}

func renderReportTestSummaryLines(view *dagui.TestView, logs map[dagui.SpanID]*Vterm, scope string, indent, logLimit int) []string {
	title := "Tests"
	if scope != "" {
		title += " · " + scope
	}
	return renderTextTestSummaryLines(view, logs, textTestSummaryOptions{
		Title:       title,
		Indent:      indent,
		EntryIndent: 2,
		LogPrefix:   "  | ",
		Separator:   " > ",
		LogLimit:    logLimit,
	})
}

type textTestSummaryOptions struct {
	Title       string
	Indent      int
	EntryIndent int
	LogPrefix   string
	Separator   string
	LogLimit    int
}

func renderTextTestSummaryLines(view *dagui.TestView, logs map[dagui.SpanID]*Vterm, opts textTestSummaryOptions) []string {
	if view == nil || !view.HasTests() {
		return nil
	}
	indent := strings.Repeat(" ", max(opts.Indent, 0))
	entryIndent := strings.Repeat(" ", max(opts.Indent+opts.EntryIndent, 0))
	lines := []string{indent + opts.Title + ": " + renderTextTestCountsSummary(view.Counts)}
	entries := collectTestSummaryEntries(view)
	if testSummaryEntriesHasDetails(entries) || view.Counts.Passing > 0 {
		lines = append(lines, "")
	}
	appendEntryGroup := func(entries []testSummaryEntry) {
		for _, entry := range entries {
			lines = append(lines, entryIndent+textTestSummaryStatus(entry.category)+" "+textTestSummaryLabel(entry.label, opts.Separator))
			for _, logLine := range textTestSummaryLogLines(logs, entry, opts.LogLimit) {
				lines = append(lines, entryIndent+opts.LogPrefix+logLine)
			}
		}
	}
	appendEntryGroup(entries.failing)
	appendEntryGroup(entries.running)
	appendEntryGroup(entries.skipped)
	if view.Counts.Passing > 0 {
		lines = append(lines, entryIndent+fmt.Sprintf("PASS %d passed", view.Counts.Passing))
	}
	return lines
}

func renderLogsTestSummaryLines(out TermOutput, view *dagui.TestView, logs map[dagui.SpanID]*Vterm, width, logLimit int) []string {
	if view == nil || !view.HasTests() {
		return nil
	}
	caret := out.String(CaretDownFilled).Foreground(termenv.ANSIBrightBlack).String()
	lines := []string{
		clipANSI(caret+" "+out.String("Tests").Bold().String(), width),
		clipANSI(renderTestCountsSummary(out, view.Counts, width), width),
	}
	entries := collectTestSummaryEntries(view)
	if testSummaryEntriesHasDetails(entries) || view.Counts.Passing > 0 {
		lines = append(lines, "")
	}
	appendEntryGroup := func(entries []testSummaryEntry) {
		for _, entry := range entries {
			lines = append(lines, renderLogsTestSummaryEntry(out, entry, width))
			for _, logLine := range textTestSummaryLogLines(logs, entry, logLimit) {
				lines = append(lines, clipANSI("    "+clipPlain(logLine, max(width-4, 1)), width))
			}
		}
	}
	appendEntryGroup(entries.failing)
	appendEntryGroup(entries.running)
	appendEntryGroup(entries.skipped)
	if view.Counts.Passing > 0 {
		counts := dagui.TestCounts{Passing: view.Counts.Passing}
		lines = append(lines, clipANSI(renderTestCountsSummary(out, counts, width), width))
	}
	return lines
}

func renderLogsTestSummaryEntry(out TermOutput, entry testSummaryEntry, width int) string {
	caret := out.String(CaretDownFilled).Foreground(testCategoryColor(entry.category)).String()
	icon := out.String(testCategoryIcon(entry.category)).Foreground(testCategoryColor(entry.category)).String()
	prefix := caret + " " + icon + " "
	label := clipPlain(entry.label, max(width-lipgloss.Width(prefix), 1))
	return clipANSI(prefix+label, width)
}

func textTestSummaryLogLines(logs map[dagui.SpanID]*Vterm, entry testSummaryEntry, limit int) []string {
	if entry.span == nil || logs == nil {
		return nil
	}
	if entry.category != dagui.TestCategoryFailing && entry.category != dagui.TestCategorySkipped {
		return nil
	}
	vt := logs[entry.span.ID]
	if vt == nil || vt.UsedHeight() == 0 {
		return nil
	}
	var buf strings.Builder
	if err := vt.Print(&buf); err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if limit >= 0 && len(lines) > limit {
		hidden := len(lines) - limit
		lines = append([]string{fmt.Sprintf("... %d more log lines ...", hidden)}, lines[hidden:]...)
	}
	return lines
}

func testSummaryEntriesHasDetails(entries testSummaryEntries) bool {
	return len(entries.failing) > 0 || len(entries.running) > 0 || len(entries.skipped) > 0
}

func renderTextTestCountsSummary(counts dagui.TestCounts) string {
	var parts []string
	add := func(count int, label string) {
		if count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, label))
		}
	}
	add(counts.Failing, "failed")
	add(counts.Running, "running")
	add(counts.Skipped, "skipped")
	add(counts.Passing, "passed")
	if len(parts) == 0 {
		return "0 tests"
	}
	return strings.Join(parts, ", ")
}

func textTestSummaryStatus(category dagui.TestCategory) string {
	switch category {
	case dagui.TestCategoryFailing, dagui.TestCategoryMixed:
		return "FAIL"
	case dagui.TestCategoryRunning:
		return "RUN"
	case dagui.TestCategorySkipped:
		return "SKIP"
	default:
		return "PASS"
	}
}

func textTestSummaryLabel(label, separator string) string {
	if separator == "" || separator == " › " {
		return label
	}
	return strings.ReplaceAll(label, " › ", separator)
}
