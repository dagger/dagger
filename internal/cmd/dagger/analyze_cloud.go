package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// writeTraceLogs writes the selected span's full logs, as raw text, to stdout
// or to --output. It is 'view --log' when there is no pager.
func (cli *CloudCLI) writeTraceLogs(cmd *cobra.Command, traceID string, sel spanSelector, o *traceViewOptions) error {
	ctx := cmd.Context()

	// Cloud's OTLP stream endpoints are addressed by trace ID and token
	// alone, so no org is resolved here.
	client, err := cli.cloudOTLPClient(ctx)
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	var outFile *os.File
	if o.output != "" {
		f, err := os.Create(o.output)
		if err != nil {
			return err
		}
		defer f.Close() // no-op on the happy path's explicit checked Close
		outFile = f
		w = f
	}

	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	spanID, descendants, err := sel.resolveSpan(ctx, client, traceID)
	if err != nil {
		return err
	}

	endedWithNewline := true
	var writeErr error
	n, streamErr := streamTraceLogText(ctx, client, traceID, spanID, descendants, func(body string) error {
		if _, err := io.WriteString(w, body); err != nil {
			// Nothing more can be written (disk full, closed pipe);
			// stop the stream rather than silently dropping the rest.
			writeErr = err
			cancel()
			return err
		}
		endedWithNewline = strings.HasSuffix(body, "\n")
		return nil
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
			return fmt.Errorf("close %s: %w", o.output, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d log messages to %s\n", n, o.output)
	}
	return nil
}

// streamTraceLogText streams a span's logs from Cloud and hands each text
// record's body to write, in order. It returns the number of bodies written.
func streamTraceLogText(ctx context.Context, client *cloudapi.OTLPClient, traceID, spanID string, descendants bool, write func(body string) error) (int, error) {
	var n int
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
			return write(body)
		}, db),
	})
	err := client.FetchLogs(ctx, traceID, cloudapi.LogSelection{
		SpanID:      spanID,
		Descendants: descendants,
	}, importer.ImportLogs)
	return n, err
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
