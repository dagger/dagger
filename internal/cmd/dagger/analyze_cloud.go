package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var (
	analyzeLogLines   int
	analyzeNoLogs     bool
	analyzeLogTimeout time.Duration

	logsOutput      string
	logsDescendants bool
	logsTimeout     time.Duration
)

var cloudLogsCmd = newCloudLogsCmd()

func init() {
	cloudCmd.AddCommand(cloudLogsCmd)
}

func newCloudLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <trace-id> <span-id>",
		Short: "Print the full logs for a span in a Dagger Cloud trace",
		Long: `Stream the full logs for a single span. Use this as a follow-up to
'dagger cloud analyze' to inspect a failed span in detail. Redirect to a file
to grep large logs in a controlled way:

    dagger cloud logs <trace-id> <span-id> -o span.log
    grep -i error span.log`,
		Args: cobra.ExactArgs(2),
		RunE: cloudCLI.CloudLogs,
	}
	cmd.Flags().StringVarP(&logsOutput, "output", "o", "", "Write logs to a file instead of stdout")
	cmd.Flags().BoolVar(&logsDescendants, "descendants", true, "Include logs from descendant spans")
	cmd.Flags().DurationVar(&logsTimeout, "timeout", 2*time.Minute, "Max time to spend streaming logs")
	return cmd
}

func (cli *CloudCLI) Analyze(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	traceID := args[0]

	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}

	tq, err := client.TraceQuestions(ctx, org.ID, traceID)
	if err != nil {
		return err
	}
	if tq == nil {
		return fmt.Errorf("trace %q not found (or no data yet)", traceID)
	}

	if cloudJSON {
		return writeCloudJSON(cmd, tq)
	}

	cli.printAnalysis(cmd, client, org.ID, traceID, tq)
	return nil
}

func (cli *CloudCLI) printAnalysis(cmd *cobra.Command, client *cloudapi.Client, orgID, traceID string, tq *cloudapi.TraceQuestions) {
	// idtui.NewOutput keeps styling consistent with the rest of the CLI: it
	// colors unless NO_COLOR is set or an AI agent is driving us (see
	// idtui.ColorProfile), so piped/agent output stays plain automatically.
	o := idtui.NewOutput(cmd.OutOrStdout())

	// Fetch all the failed spans' log tails up front, concurrently: the log
	// endpoints are slow, and rendering is sequential, so doing them inline would
	// serialize the wait. Results are keyed by span id and looked up while
	// rendering. nil when logs are disabled.
	logs := cli.prefetchLogTails(cmd.Context(), client, orgID, traceID, tq)

	fmt.Fprintf(o, "%s %s\n", bold(o, "TRACE"), traceID)
	if s := tq.OverallStatus; s != nil {
		fmt.Fprintf(o, "%s  %s\n", bold(o, "Status:"), statusHeadline(o, s.Outcome))
		if s.Command != "" {
			fmt.Fprintf(o, "%s %s\n", bold(o, "Command:"), s.Command)
		}
		if s.Error != "" {
			fmt.Fprintf(o, "%s   %s\n", bold(o, "Error:"), oneLine(s.Error))
		}
	} else {
		fmt.Fprintf(o, "%s  UNKNOWN (no root span found)\n", bold(o, "Status:"))
	}

	// What command caused the failure.
	var rootCause strings.Builder
	if len(tq.FailingCommands) == 0 {
		fmt.Fprintln(&rootCause, "(nothing failed)")
	}
	for i, fc := range tq.FailingCommands {
		if i > 0 {
			fmt.Fprintln(&rootCause)
		}
		label := "cause"
		if i == 0 {
			label = "root cause"
		}
		fmt.Fprintf(&rootCause, "%s %s\n", bold(o, "["+label+"]"), emptyDash(fc.Command))
		if fc.Error != "" {
			fmt.Fprintf(&rootCause, "  %s %s\n", bold(o, "error:"), oneLine(fc.Error))
		}
		fmt.Fprintf(&rootCause, "  %s  %s\n", bold(o, "span:"), fc.SpanID)
		cli.renderSpanLogs(&rootCause, o, traceID, fc.SpanID, logs[fc.SpanID])
	}
	section(o, "ROOT CAUSE", rootCause.String())

	// Checks. Always show the section so "no checks ran" is explicit rather than
	// an ambiguous omission.
	var passed, failed int
	for _, c := range tq.Checks {
		switch c.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		}
	}
	var checks strings.Builder
	if len(tq.Checks) == 0 {
		fmt.Fprintln(&checks, "(no checks ran)")
	}
	for i, c := range tq.Checks {
		if i > 0 {
			fmt.Fprintln(&checks)
		}
		fmt.Fprintf(&checks, "%s %s\n", marker(o, c.Status), bold(o, emptyDash(c.Name)))
		if c.Error != "" {
			fmt.Fprintf(&checks, "  %s %s\n", bold(o, "error:"), oneLine(c.Error))
		}
		if c.Status == "failed" {
			fmt.Fprintf(&checks, "  %s  %s\n", bold(o, "span:"), c.SpanID)
			cli.renderSpanLogs(&checks, o, traceID, c.SpanID, logs[c.SpanID])
		}
	}
	section(o, fmt.Sprintf("CHECKS (%d passed, %d failed, %d total)", passed, failed, len(tq.Checks)), checks.String())

	// Failed tests. Always show the section for the same reason.
	var tests strings.Builder
	if len(tq.FailedTests) == 0 {
		fmt.Fprintln(&tests, "(no failed tests)")
	}
	for i, t := range tq.FailedTests {
		if i > 0 {
			fmt.Fprintln(&tests)
		}
		name := t.Name
		if t.Suite != "" && t.Suite != t.Name {
			// Use an ASCII separator for agents; the test name is a prime grep
			// target and › is awkward to match.
			sep := " › "
			if idtui.RunningInAgent() {
				sep = " > "
			}
			name = t.Suite + sep + t.Name
		}
		fmt.Fprintf(&tests, "%s %s\n", marker(o, "failed"), bold(o, emptyDash(name)))
		if t.OriginCommand != "" {
			fmt.Fprintf(&tests, "  %s %s\n", bold(o, "caused by:"), t.OriginCommand)
		}
		if msg := firstNonEmptyStr(t.OriginError, t.Error); msg != "" {
			fmt.Fprintf(&tests, "  %s     %s\n", bold(o, "error:"), oneLine(msg))
		}
		fmt.Fprintf(&tests, "  %s      %s\n", bold(o, "span:"), t.SpanID)
		// Only the leaf failures (those with a distinct origin command) have
		// useful per-test logs; aggregate parent tests just roll up children.
		if t.OriginCommand != "" {
			cli.renderSpanLogs(&tests, o, traceID, t.SpanID, logs[t.SpanID])
		}
	}
	section(o, fmt.Sprintf("FAILED TESTS (%d)", len(tq.FailedTests)), tests.String())

	// This summary is intentionally a flat triage: it drops the call tree that
	// led to each failure (which function calls, with which arguments, and how
	// long they took). When that context matters, the full trace renders it.
	var more strings.Builder
	fmt.Fprintf(&more, "Full call tree, arguments, and timing:  dagger trace --full %s\n", traceID)
	fmt.Fprintf(&more, "Full logs for any span:                 dagger cloud logs %s <span-id> -o span.log\n", traceID)
	section(o, "MORE CONTEXT", more.String())
}

// section renders a titled block. For humans the title is a bold, all-caps
// heading with the body indented two spaces -- the style used elsewhere in the
// CLI. For agents it's a flat, greppable "== TITLE ==" marker with the body
// left at the margin. body is pre-rendered and may already contain styling.
func section(o *termenv.Output, title, body string) {
	body = strings.TrimRight(body, "\n")
	if idtui.RunningInAgent() {
		fmt.Fprintf(o, "\n== %s ==\n", title)
		if body != "" {
			fmt.Fprintln(o, body)
		}
		return
	}
	fmt.Fprintf(o, "\n%s\n", bold(o, title))
	if body != "" {
		fmt.Fprintln(o, indentLines(body, "  "))
	}
}

// indentLines prefixes every non-empty line of s with prefix.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = prefix + ln
		}
	}
	return strings.Join(lines, "\n")
}

// logTarget is a span whose logs we want to fetch for the analysis.
type logTarget struct {
	spanID      string
	descendants bool
}

// logResult is the outcome of fetching one span's log tail.
type logResult struct {
	tail     *lineTail
	timedOut bool
	err      error
}

// logTargets returns the spans whose logs the analysis displays, in no
// particular order. Failing commands want their own output (descendants=false:
// a withExec's stdout/stderr is on the span itself); checks and leaf failed
// tests want their subtree.
func logTargets(tq *cloudapi.TraceQuestions) []logTarget {
	var targets []logTarget
	for _, fc := range tq.FailingCommands {
		targets = append(targets, logTarget{fc.SpanID, false})
	}
	for _, c := range tq.Checks {
		if c.Status == "failed" {
			targets = append(targets, logTarget{c.SpanID, true})
		}
	}
	for _, t := range tq.FailedTests {
		if t.OriginCommand != "" {
			targets = append(targets, logTarget{t.SpanID, true})
		}
	}
	return targets
}

// prefetchLogTails fetches every target span's log tail concurrently. The log
// endpoints are slow and rendering is sequential, so fetching inline would add
// up the waits; fetching in parallel makes the total roughly the slowest single
// span. Returns nil when logs are disabled. Per-span errors are kept in the
// result so one failure doesn't sink the rest.
func (cli *CloudCLI) prefetchLogTails(ctx context.Context, client *cloudapi.Client, orgID, traceID string, tq *cloudapi.TraceQuestions) map[string]*logResult {
	if analyzeNoLogs || analyzeLogLines <= 0 {
		return nil
	}
	targets := logTargets(tq)
	if len(targets) == 0 {
		return nil
	}

	results := make([]*logResult, len(targets))
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(8)
	for i, tgt := range targets {
		i, tgt := i, tgt
		eg.Go(func() error {
			tail, timedOut, err := cli.tailSpanLogs(ctx, client, orgID, traceID, tgt.spanID, tgt.descendants)
			results[i] = &logResult{tail: tail, timedOut: timedOut, err: err}
			return nil
		})
	}
	_ = eg.Wait()

	out := make(map[string]*logResult, len(targets))
	for i, tgt := range targets {
		out[tgt.spanID] = results[i]
	}
	return out
}

// renderSpanLogs writes a span's prefetched log tail and the command to get the
// full logs to w, styling with o. A nil result means logs were disabled, so
// just print the command.
func (cli *CloudCLI) renderSpanLogs(w io.Writer, o *termenv.Output, traceID, spanID string, res *logResult) {
	full := fmt.Sprintf("dagger cloud logs %s %s", traceID, spanID)
	switch {
	case res == nil:
		fmt.Fprintf(w, "  %s  %s\n", bold(o, "logs:"), full)
		return
	case res.err != nil:
		fmt.Fprintf(w, "  %s  (error fetching: %v) %s\n", bold(o, "logs:"), res.err, full)
		return
	case res.tail == nil || res.tail.total == 0:
		fmt.Fprintf(w, "  %s  (none) — %s\n", bold(o, "logs:"), full)
		return
	}

	suffix := ""
	if res.timedOut {
		suffix = ", timed out — partial"
	}
	header := fmt.Sprintf("logs (last %d of %d lines%s; full: %s):", len(res.tail.lines), res.tail.total, suffix, full)
	fmt.Fprintf(w, "  %s\n", bold(o, header))
	pipe := dim(o, "|")
	for _, ln := range res.tail.lines {
		fmt.Fprintf(w, "    %s %s\n", pipe, ln)
	}
}

func (cli *CloudCLI) CloudLogs(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	traceID, spanID := args[0], args[1]

	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}

	var w io.Writer = cmd.OutOrStdout()
	if logsOutput != "" {
		f, err := os.Create(logsOutput)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	ctx, cancel := context.WithTimeout(ctx, logsTimeout)
	defer cancel()

	var n int
	streamErr := client.StreamLogs(ctx, org.ID, traceID, spanID, logsDescendants, func(msgs []cloudapi.LogMessage) {
		for _, m := range msgs {
			n++
			io.WriteString(w, m.Body)
			if !strings.HasSuffix(m.Body, "\n") {
				io.WriteString(w, "\n")
			}
		}
	})
	// A deadline is an expected way to stop a long stream, not an error.
	if streamErr != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return streamErr
	}
	if logsOutput != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d log messages to %s\n", n, logsOutput)
	}
	return nil
}

// lineTail keeps the last n lines streamed to it (a ring buffer), tracking the
// total seen so the caller can report "last N of M".
type lineTail struct {
	n     int
	lines []string
	total int
}

func (t *lineTail) addBody(body string) {
	body = strings.TrimSuffix(body, "\n")
	if body == "" {
		return
	}
	for _, ln := range strings.Split(body, "\n") {
		t.total++
		t.lines = append(t.lines, ln)
		if len(t.lines) > t.n {
			t.lines = t.lines[1:]
		}
	}
}

func (cli *CloudCLI) tailSpanLogs(ctx context.Context, client *cloudapi.Client, orgID, traceID, spanID string, descendants bool) (*lineTail, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, analyzeLogTimeout)
	defer cancel()

	tail := &lineTail{n: analyzeLogLines}
	err := client.StreamLogs(cctx, orgID, traceID, spanID, descendants, func(msgs []cloudapi.LogMessage) {
		for _, m := range msgs {
			tail.addBody(m.Body)
		}
	})
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return tail, true, nil
	}
	return tail, false, err
}

// marker is the leading status indicator for a check or test line. For humans
// it's a colored glyph matching the report frontend's vocabulary (green ✔ /
// red ✘); for agents it's a greppable ASCII token like "[FAILED]", so a single
// `grep '\[FAILED\]'` surfaces every failure across the summary.
func marker(o *termenv.Output, status string) string {
	if idtui.RunningInAgent() {
		return statusToken(status)
	}
	switch status {
	case "passed":
		return o.String("✔").Foreground(termenv.ANSIGreen).String()
	case "failed":
		return o.String("✘").Foreground(termenv.ANSIRed).String()
	case "running":
		return o.String("…").Foreground(termenv.ANSIYellow).String()
	default:
		return o.String("?").Foreground(termenv.ANSIBrightBlack).String()
	}
}

// statusToken is the ASCII status tag shown to agents, e.g. "[FAILED]".
func statusToken(status string) string {
	w := strings.ToUpper(status)
	if w == "" {
		w = "UNKNOWN"
	}
	return "[" + w + "]"
}

// statusHeadline renders the overall outcome on the Status line: a colored
// glyph plus the uppercased word for humans, or just the ASCII token for agents
// (which already spells out the outcome, so no separate word is needed).
func statusHeadline(o *termenv.Output, status string) string {
	if idtui.RunningInAgent() {
		return statusToken(status)
	}
	word := strings.ToUpper(emptyDash(status))
	switch status {
	case "passed":
		return o.String("✔ " + word).Foreground(termenv.ANSIGreen).String()
	case "failed":
		return o.String("✘ " + word).Foreground(termenv.ANSIRed).String()
	default:
		return marker(o, status) + " " + word
	}
}

// bold styles headings and labels.
func bold(o *termenv.Output, s string) string {
	return o.String(s).Bold().String()
}

// dim styles de-emphasized decoration, like the log line gutter.
func dim(o *termenv.Output, s string) string {
	return o.String(s).Foreground(termenv.ANSIBrightBlack).String()
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
