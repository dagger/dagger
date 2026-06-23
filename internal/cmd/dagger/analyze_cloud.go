package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
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

var cloudAnalyzeCmd = newAnalyzeCmd(false)
var analyzeCmd = newAnalyzeCmd(true)

var cloudLogsCmd = newCloudLogsCmd()

func init() {
	cloudCmd.AddCommand(cloudAnalyzeCmd, cloudLogsCmd)
	rootCmd.AddCommand(analyzeCmd)
}

func newAnalyzeCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze <trace-id>",
		Short: "Summarize why a Dagger Cloud trace failed (LLM-friendly)",
		Long: `Summarize a Dagger Cloud trace: overall status, the command(s) that caused
the failure, check results, and failed tests -- computed server-side without
loading the whole trace.

For each failed span it also shows the tail of that span's logs and prints the
exact command to fetch the full logs, which can be redirected to a file and
grepped:

    dagger cloud logs <trace-id> <span-id> -o span.log`,
		Args:    cobra.ExactArgs(1),
		Hidden:  hidden,
		Aliases: []string{"diagnose"},
		RunE:    cloudCLI.Analyze,
	}
	cmd.Flags().BoolVar(&cloudJSON, "json", false, "Print the analysis as JSON (no logs)")
	cmd.Flags().IntVar(&analyzeLogLines, "log-lines", 20, "Lines of log tail to show per failed span (0 to disable)")
	cmd.Flags().BoolVar(&analyzeNoLogs, "no-logs", false, "Don't fetch logs, just the summary")
	cmd.Flags().DurationVar(&analyzeLogTimeout, "log-timeout", 30*time.Second, "Max time to spend fetching each span's log tail")
	return cmd
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
	out := cmd.OutOrStdout()

	// Fetch all the failed spans' log tails up front, concurrently: the log
	// endpoints are slow, and rendering is sequential, so doing them inline would
	// serialize the wait. Results are keyed by span id and looked up while
	// rendering. nil when logs are disabled.
	logs := cli.prefetchLogTails(cmd.Context(), client, orgID, traceID, tq)

	fmt.Fprintf(out, "TRACE %s\n", traceID)
	if s := tq.OverallStatus; s != nil {
		fmt.Fprintf(out, "Status:  %s\n", strings.ToUpper(emptyDash(s.Outcome)))
		if s.Command != "" {
			fmt.Fprintf(out, "Command: %s\n", s.Command)
		}
		if s.Error != "" {
			fmt.Fprintf(out, "Error:   %s\n", oneLine(s.Error))
		}
	} else {
		fmt.Fprintln(out, "Status:  UNKNOWN (no root span found)")
	}

	// What command caused the failure.
	fmt.Fprintf(out, "\n== ROOT CAUSE ==\n")
	if len(tq.FailingCommands) == 0 {
		fmt.Fprintln(out, "(nothing failed)")
	}
	for i, fc := range tq.FailingCommands {
		label := "cause"
		if i == 0 {
			label = "root cause"
		}
		fmt.Fprintf(out, "\n[%s] %s\n", label, emptyDash(fc.Command))
		if fc.Error != "" {
			fmt.Fprintf(out, "  error: %s\n", oneLine(fc.Error))
		}
		fmt.Fprintf(out, "  span:  %s\n", fc.SpanID)
		cli.renderSpanLogs(out, traceID, fc.SpanID, logs[fc.SpanID])
	}

	// Checks.
	if len(tq.Checks) > 0 {
		var passed, failed int
		for _, c := range tq.Checks {
			switch c.Status {
			case "passed":
				passed++
			case "failed":
				failed++
			}
		}
		fmt.Fprintf(out, "\n== CHECKS (%d passed, %d failed, %d total) ==\n", passed, failed, len(tq.Checks))
		for _, c := range tq.Checks {
			fmt.Fprintf(out, "\n%s %s\n", checkMark(c.Status), emptyDash(c.Name))
			if c.Error != "" {
				fmt.Fprintf(out, "  error: %s\n", oneLine(c.Error))
			}
			if c.Status == "failed" {
				fmt.Fprintf(out, "  span:  %s\n", c.SpanID)
				cli.renderSpanLogs(out, traceID, c.SpanID, logs[c.SpanID])
			}
		}
	}

	// Failed tests.
	if len(tq.FailedTests) > 0 {
		fmt.Fprintf(out, "\n== FAILED TESTS (%d) ==\n", len(tq.FailedTests))
		for _, t := range tq.FailedTests {
			name := t.Name
			if t.Suite != "" && t.Suite != t.Name {
				name = t.Suite + " › " + t.Name
			}
			fmt.Fprintf(out, "\n✗ %s", emptyDash(name))
			if t.FailureStatus != "" {
				fmt.Fprintf(out, "  (%s)", t.FailureStatus)
			}
			fmt.Fprintln(out)
			if t.OriginCommand != "" {
				fmt.Fprintf(out, "  caused by: %s\n", t.OriginCommand)
			}
			if msg := firstNonEmptyStr(t.OriginError, t.Error); msg != "" {
				fmt.Fprintf(out, "  error:     %s\n", oneLine(msg))
			}
			fmt.Fprintf(out, "  span:      %s\n", t.SpanID)
			// Only the leaf failures (those with a distinct origin command) have
			// useful per-test logs; aggregate parent tests just roll up children.
			if t.OriginCommand != "" {
				cli.renderSpanLogs(out, traceID, t.SpanID, logs[t.SpanID])
			}
		}
	}

	fmt.Fprintf(out, "\nFull logs for any span: dagger cloud logs %s <span-id> -o span.log\n", traceID)
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

// renderSpanLogs prints a span's prefetched log tail and the command to get the
// full logs. A nil result means logs were disabled, so just print the command.
func (cli *CloudCLI) renderSpanLogs(out io.Writer, traceID, spanID string, res *logResult) {
	full := fmt.Sprintf("dagger cloud logs %s %s", traceID, spanID)
	if res == nil {
		fmt.Fprintf(out, "  logs:  %s\n", full)
		return
	}
	if res.err != nil {
		fmt.Fprintf(out, "  logs:  (error fetching: %v) %s\n", res.err, full)
		return
	}
	if res.tail == nil || res.tail.total == 0 {
		fmt.Fprintf(out, "  logs:  (none) — %s\n", full)
		return
	}

	suffix := ""
	if res.timedOut {
		suffix = ", timed out — partial"
	}
	fmt.Fprintf(out, "  logs (last %d of %d lines%s; full: %s):\n", len(res.tail.lines), res.tail.total, suffix, full)
	for _, ln := range res.tail.lines {
		fmt.Fprintf(out, "    | %s\n", ln)
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

func checkMark(status string) string {
	switch status {
	case "passed":
		return "✓"
	case "failed":
		return "✗"
	case "running":
		return "…"
	default:
		return "?"
	}
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
