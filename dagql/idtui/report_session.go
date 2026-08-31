package idtui

import (
	"context"
	"io"

	"github.com/muesli/termenv"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/vito/tuist"
)

// ReportSession renders scoped final reports (the pretty end-of-run report,
// as plain text) over a telemetry DB that is loaded incrementally.
//
// It exists to split the two halves that used to be conflated in a single
// long-lived reporter:
//
//   - INGESTION is stateful and shared: the DB and the log buffers accumulate
//     a session's spans and log lines, so a report costs only the rows
//     appended since the last one.
//   - RENDERING is not. Each Render builds a FRESH frontend over that state,
//     so no render can observe another render's scope, expansion map, claims
//     or memoized rows -- and, crucially, scoping a report no longer means
//     mutating the shared DB's primary span (see ReportRenderOpts.Root).
//
// The invariant this buys, and which the rest of the report code is written
// to: a scoped report (ReportRenderOpts.ScopedSubtree with a Root) renders
// exactly that root span's real subtree -- its own output, the spans that
// actually ran beneath it, and the checks/tests/services/messages surfaced
// from within it. Nothing reached by a cause link, and nothing from an
// earlier or later report, can appear in it.
type ReportSession struct {
	db   *dagui.DB
	logs *prettyLogs
}

// NewReportSession returns a report renderer over db. The caller owns db (and
// any locking it needs): ReportSession never writes to it outside of
// LogExporter and Render.
func NewReportSession(db *dagui.DB) *ReportSession {
	return &ReportSession{
		db: db,
		// Plain ASCII: a rendered report is embedded in an API result (e.g. an
		// LLM tool result), whose consumer is reading text, not a terminal.
		logs: newPrettyLogs(termenv.Ascii, db),
	}
}

// DB returns the session's telemetry DB.
func (s *ReportSession) DB() *dagui.DB { return s.db }

// LogExporter ingests log records into the session. Logs have to flow through
// here rather than through db.LogExporter() alone: the rendered ┃ log lines
// come from these buffers, while the DB only tracks which spans have logs.
func (s *ReportSession) LogExporter() sdklog.Exporter {
	return reportLogExporter{s}
}

type reportLogExporter struct {
	s *ReportSession
}

func (e reportLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	// Copy the slice — the OTel SDK reuses it after Export returns.
	logsCopy := make([]sdklog.Record, len(logs))
	copy(logsCopy, logs)
	if err := e.s.db.LogExporter().Export(ctx, logsCopy); err != nil {
		return err
	}
	return e.s.logs.Export(ctx, logsCopy)
}

func (e reportLogExporter) Shutdown(context.Context) error { return nil }

func (e reportLogExporter) ForceFlush(context.Context) error { return nil }

// Render writes the final report for opts to w.
//
// The frontend is constructed per render and thrown away: it is the render's
// entire mutable state, so two renders with different roots over the same DB
// cannot leak into one another.
func (s *ReportSession) Render(w io.Writer, opts ReportRenderOpts) error {
	fe := newWithTerminalProfile(io.Discard, s.db, tuist.NewStdTerminal(), termenv.Ascii)
	fe.reportOnly = true
	// Share the ingested log buffers: they're the expensive, incremental half.
	fe.logs = s.logs
	fe.SetReportRenderOpts(opts)
	return fe.FinalRender(w)
}
