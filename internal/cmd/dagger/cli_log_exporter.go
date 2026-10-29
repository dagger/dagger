package daggercmd

import (
	"context"
	"io"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// cliDiagnosticExporter writes the text already formatted by slog. The SDK's
// batch processor bounds the queue and shutdown wait; this exporter owns no
// frontend state and never closes stderr.
type cliDiagnosticExporter struct{ out io.Writer }

func (e cliDiagnosticExporter) Export(ctx context.Context, records []sdklog.Record) error {
	for _, record := range records {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Cancellation drops best-effort diagnostics.
		}
		if _, err := io.WriteString(e.out, record.Body().AsString()); err != nil {
			return nil //nolint:nilerr // Reporting sink failures through OTel would recursively enqueue diagnostics.
		}
	}
	return nil
}

func (cliDiagnosticExporter) Shutdown(context.Context) error   { return nil }
func (cliDiagnosticExporter) ForceFlush(context.Context) error { return nil }
