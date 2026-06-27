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
)

var (
	logsOutput  string
	logsTimeout time.Duration
	logsSpan    string
	logsCheck   string
	logsTest    string
)

var cloudLogsCmd = newCloudLogsCmd()

func init() {
	cloudCmd.AddCommand(cloudLogsCmd)
}

func newCloudLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <trace-id> [--span <id> | --check <name> | --test <name>]",
		Short: "Print the full logs for a Dagger Cloud trace, or a check/test/span within it",
		Long: `Stream the full logs for a trace. Use this as a follow-up to
'dagger trace' to inspect a failure in detail, addressing it by name rather than
an opaque span ID. Redirect to a file to grep large logs in a controlled way:

    dagger cloud logs <trace-id> --check build:lint -o span.log
    grep -i error span.log

With no --span/--check/--test, the whole trace's logs are streamed. --check and
--test roll up their subtree; --span is just that span.`,
		Args: cobra.ExactArgs(1),
		RunE: cloudCLI.CloudLogs,
	}
	cmd.Flags().StringVar(&logsSpan, "span", "", "Read just this span's logs, by span ID")
	cmd.Flags().StringVar(&logsCheck, "check", "", "Read a check's logs, by name (rolls up its subtree)")
	cmd.Flags().StringVar(&logsTest, "test", "", "Read a test's logs, by name (rolls up its subtree)")
	cmd.Flags().StringVarP(&logsOutput, "output", "o", "", "Write logs to a file instead of stdout")
	cmd.Flags().DurationVar(&logsTimeout, "timeout", 2*time.Minute, "Max time to spend streaming logs")
	return cmd
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

func (cli *CloudCLI) CloudLogs(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	traceID := args[0]

	sel := spanSelector{span: logsSpan, check: logsCheck, test: logsTest}
	if err := sel.validate(); err != nil {
		return err
	}

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

	spanID, descendants, err := sel.resolveSpan(ctx, client, org.ID, traceID)
	if err != nil {
		return err
	}

	var n int
	streamErr := client.StreamLogs(ctx, org.ID, traceID, spanID, descendants, func(msgs []cloudapi.LogMessage) {
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

// bold styles headings and labels.
func bold(o *termenv.Output, s string) string {
	return o.String(s).Bold().String()
}

// dim styles de-emphasized decoration, like the log line gutter.
func dim(o *termenv.Output, s string) string {
	return o.String(s).Foreground(termenv.ANSIBrightBlack).String()
}

