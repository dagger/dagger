package daggercmd

import (
	"context"
	"io"

	"github.com/dagger/dagger/dagql/idtui"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// cliDiagnosticExporter writes the text already formatted by slog to stderr,
// except while a TUI draws on the terminal: a raw write there scrambles its
// frames, so the diagnostics go to the frontend instead, which shows them in
// the TUI. The SDK's batch processor around it drops under load and exports
// on its own goroutine, so a diagnostic never waits on either; it bounds the
// shutdown wait too. This exporter owns no frontend state and never closes
// stderr: shutting a frontend's exporter down would end its stream.
type cliDiagnosticExporter struct {
	out io.Writer
	// owner, when set, is the frontend that may own the terminal; its log
	// exporter receives the diagnostics while it does.
	owner    idtui.TerminalOwner
	frontend sdklog.Exporter
}

// newCLIDiagnosticExporter writes diagnostics to out, or to frontend while it
// owns the terminal. outIsTerminal says whether out is that terminal: a TUI
// draws on stderr when it is one, else on stdout, which out can't scramble.
func newCLIDiagnosticExporter(out io.Writer, outIsTerminal bool, frontend idtui.Frontend) cliDiagnosticExporter {
	e := cliDiagnosticExporter{out: out}
	if owner, ok := frontend.(idtui.TerminalOwner); ok && outIsTerminal {
		e.owner = owner
		e.frontend = frontend.LogExporter()
	}
	return e
}

func (e cliDiagnosticExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if e.owner != nil && e.owner.OwnsTerminal() {
		_ = e.frontend.Export(ctx, records) // Reporting failures through OTel would recursively enqueue diagnostics.
		return nil
	}
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
