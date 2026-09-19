package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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

	// Cloud's OTLP stream endpoints are addressed by trace ID and token
	// alone, so no org is resolved here.
	client, err := cli.cloudOTLPClient(ctx)
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

	spanID, descendants, err := sel.resolveSpan(ctx, client, traceID)
	if err != nil {
		return err
	}
	if logsDescendants {
		descendants = true
	}

	var n int
	endedWithNewline := true
	var writeErr error
	// Every record class comes down; dagui's ingest sorts the text output
	// from the semantic records riding the log channel (call payloads,
	// progress, agent state, span names) exactly as the frontend does, so
	// what's written is byte-for-byte the traced program's output.
	db := dagui.NewDB()
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Logs: logTextWriter(func(body string) error {
			// A record's body is one Write from the traced program -- a
			// chunk, not a line: a single line can span records and a record
			// can hold a bare \r progress frame. Write bodies verbatim so the
			// output (and anything grepping it) sees the original stream.
			// Empty bodies are stream markers (EOF, progress); skip them so
			// they don't inflate the message count.
			if body == "" {
				return nil
			}
			n++
			if _, err := io.WriteString(w, body); err != nil {
				// Nothing more can be written (disk full, closed pipe);
				// stop the stream rather than silently dropping the rest.
				writeErr = err
				cancel()
				return err
			}
			endedWithNewline = strings.HasSuffix(body, "\n")
			return nil
		}, db),
	})
	streamErr := client.FetchLogs(ctx, traceID, cloudapi.LogSelection{
		SpanID:      spanID,
		Descendants: descendants,
	}, importer.ImportLogs)
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

// logTextWriter is a log exporter that hands each text record's body to
// write, in order, after db has classified the batch: the semantic records
// (call payloads, progress, agent state, span names) are consumed by the DB
// and never reach write, and an empty string body -- a stdio EOF marker --
// does, for the caller to skip.
func logTextWriter(write func(body string) error, db *dagui.DB) sdklog.Exporter {
	return logTextExporter{write: write, db: db}
}

type logTextExporter struct {
	write func(body string) error
	db    *dagui.DB
}

func (e logTextExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, record := range e.db.IngestLogs(records) {
		body, ok := dagui.LogBodyString(record)
		if !ok {
			continue
		}
		if err := e.write(body); err != nil {
			return err
		}
	}
	return nil
}

func (logTextExporter) Shutdown(context.Context) error   { return nil }
func (logTextExporter) ForceFlush(context.Context) error { return nil }
