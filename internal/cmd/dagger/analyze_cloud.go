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
)

var (
	logsOutput      string
	logsTimeout     time.Duration
	logsSpan        string
	logsCheck       string
	logsTest        string
	logsDescendants bool
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
--test roll up their subtree; --span is just that span (add --descendants to
roll up its subtree too).`,
		Args: cobra.ExactArgs(1),
		RunE: cloudCLI.CloudLogs,
	}
	cmd.Flags().StringVar(&logsSpan, "span", "", "Read just this span's logs, by span ID")
	cmd.Flags().StringVar(&logsCheck, "check", "", "Read a check's logs, by name (rolls up its subtree)")
	cmd.Flags().StringVar(&logsTest, "test", "", "Read a test's logs, by name (rolls up its subtree)")
	cmd.Flags().BoolVar(&logsDescendants, "descendants", false, "With --span, roll up the span's subtree logs too")
	cmd.Flags().StringVarP(&logsOutput, "output", "o", "", "Write logs to a file instead of stdout")
	cmd.Flags().DurationVar(&logsTimeout, "timeout", 2*time.Minute, "Max time to spend streaming logs")
	return cmd
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

	w := cmd.OutOrStdout()
	var outFile *os.File
	if logsOutput != "" {
		f, err := os.Create(logsOutput)
		if err != nil {
			return err
		}
		defer f.Close() // no-op on the happy path's explicit checked Close
		outFile = f
		w = f
	}

	ctx, cancel := context.WithTimeout(ctx, logsTimeout)
	defer cancel()

	spanID, descendants, err := sel.resolveSpan(ctx, client, org.ID, traceID)
	if err != nil {
		return err
	}
	if logsDescendants {
		descendants = true
	}

	var n int
	endedWithNewline := true
	var writeErr error
	streamErr := client.StreamLogs(ctx, org.ID, traceID, spanID, descendants, func(msgs []cloudapi.LogMessage) {
		for _, m := range msgs {
			// A record's body is one Write from the traced program -- a chunk,
			// not a line: a single line can span records and a record can hold
			// a bare \r progress frame. Write bodies verbatim so the output
			// (and anything grepping it) sees the original stream. Empty
			// bodies are stream markers (EOF, progress); skip them so they
			// don't inflate the message count.
			if m.Body == "" {
				continue
			}
			n++
			if _, err := io.WriteString(w, m.Body); err != nil {
				// Nothing more can be written (disk full, closed pipe);
				// stop the stream rather than silently dropping the rest.
				writeErr = err
				cancel()
				return
			}
			endedWithNewline = strings.HasSuffix(m.Body, "\n")
		}
	})
	if writeErr != nil {
		return fmt.Errorf("write logs: %w", writeErr)
	}
	// A deadline is an expected way to stop a long stream, not an error.
	if streamErr != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return streamErr
	}
	if !endedWithNewline {
		// End on a newline without having invented line breaks mid-stream.
		io.WriteString(w, "\n")
	}
	if outFile != nil {
		// Surface close errors (e.g. a deferred flush failing on a full disk)
		// instead of reporting success over a truncated file.
		if err := outFile.Close(); err != nil {
			return fmt.Errorf("close %s: %w", logsOutput, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d log messages to %s\n", n, logsOutput)
	}
	return nil
}
