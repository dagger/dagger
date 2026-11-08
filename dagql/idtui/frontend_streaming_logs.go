package idtui

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// streamingLogExporter contains shared log export logic used by both the dots
// and logs frontends. It handles grouping log records by span, buffering logs
// for spans that haven't arrived yet, and flushing them with a prefix.
type streamingLogExporter struct {
	db          *dagui.DB
	opts        *dagui.FrontendOpts
	profile     termenv.Profile
	out         TermOutput
	prefixW     *multiprefixw.Writer
	pendingLogs map[dagui.SpanID][]sdklog.Record
	// heldLogs holds the logs of running spans that are hidden while their
	// parents are shown, such as internal spans: they print if the span
	// becomes shown, as when it fails, and are dropped when it ends otherwise.
	heldLogs map[dagui.SpanID][]sdklog.Record
	testLogs map[dagui.SpanID]*Vterm
}

// Export processes log records: ingests them in the DB, groups renderable text
// by span, and either flushes immediately (if the span exists) or buffers for
// later. The caller must hold the mutex.
func (s *streamingLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	records = s.db.IngestLogs(records)
	if len(records) == 0 {
		return nil
	}

	// Group records by span and either flush immediately if span exists, or store for later
	spanGroups := make(map[dagui.SpanID][]sdklog.Record)
	for _, record := range records {
		spanID := s.db.LogTargetSpanID(record)
		if !spanID.IsValid() {
			continue
		}
		spanGroups[spanID] = append(spanGroups[spanID], record)
	}

	for spanID, records := range spanGroups {
		// Check if span exists in DB
		dbSpan := s.db.Spans.Map[spanID]
		if dbSpan != nil && dbSpan.Received && dbSpan.Name != "" {
			// Span exists, flush immediately
			s.flushLogsForSpan(spanID, records)
		} else {
			// Span doesn't exist yet, store for later
			s.pendingLogs[spanID] = append(s.pendingLogs[spanID], records...)
		}
	}

	return nil
}

// flushLogsForSpan writes logs for a specific span with proper prefix.
// The caller must hold the mutex.
func (s *streamingLogExporter) flushLogsForSpan(spanID dagui.SpanID, records []sdklog.Record) {
	appendTestSummaryLogRecords(s.testLogs, s.profile, spanID, records)

	// Get span info from DB
	dbSpan := s.db.Spans.Map[spanID]
	if dbSpan == nil {
		return
	}

	if dbSpan.ID == s.db.PrimarySpan {
		// don't print primary span logs; they'll be printed at the end
		return
	}
	if !streamsSpan(s.db, s.opts, dbSpan) {
		if dbSpan.IsRunning() && parentsStream(s.db, s.opts, dbSpan) {
			s.heldLogs[spanID] = append(s.heldLogs[spanID], records...)
		}
		return
	}
	s.printLogs(dbSpan, records)
}

// printLogs writes a span's logs under its prefix.
// The caller must hold the mutex.
func (s *streamingLogExporter) printLogs(dbSpan *dagui.Span, records []sdklog.Record) {
	r := newRenderer(s.db, 0, *s.opts, true)
	prefix := streamingLogsPrefix(r, s.profile, dbSpan)

	// Write all logs for this span, filtering out verbose logs
	for _, record := range records {
		// Check if this log is marked as verbose
		isVerbose := false
		record.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.LogsVerboseAttr {
				isVerbose, _ = dagui.LogValueBool(kv.Value)
				return !isVerbose
			}
			return true // continue walking
		})

		// Skip verbose logs
		if isVerbose {
			continue
		}

		body, ok := dagui.LogBodyString(record)
		if !ok || body == "" {
			continue
		}

		// Only set prefix + track finisher when we're actually gonna print
		s.prefixW.Prefix = prefix
		fmt.Fprint(s.prefixW, body)

		// When context-switching, print an overhang so it's clear when the logs
		// haven't line-terminated
		s.prefixW.LineOverhang =
			s.out.String(multiprefixw.DefaultLineOverhang).
				Foreground(termenv.ANSIBrightBlack).String()
	}
}

// streamsSpan reports whether the logs and dots frontends print a span: only
// when the span and all of its parents would show in the trace, as in the
// other frontends, and no parent encapsulates it. An internal span, for
// example, prints only with -vvv, unless it failed.
func streamsSpan(db *dagui.DB, opts *dagui.FrontendOpts, span *dagui.Span) bool {
	return opts.ShouldShow(db, span) && parentsStream(db, opts, span) &&
		(!span.Encapsulated || span.IsFailedOrCausedFailure())
}

// parentsStream reports whether all of a span's parents would show in the
// trace, and none encapsulates it.
func parentsStream(db *dagui.DB, opts *dagui.FrontendOpts, span *dagui.Span) bool {
	for p := range span.Parents {
		if p.Encapsulate || !opts.ShouldShow(db, p) {
			return false
		}
	}
	return true
}

// flushResolvedLogsForSpan flushes any logs that became routable once the span
// or its creator became available.
// The caller must hold the mutex.
func (s *streamingLogExporter) flushResolvedLogsForSpan(spanID dagui.SpanID) {
	s.releaseHeldLogs(spanID)
	if records := s.db.DrainResolvedLogs(spanID); len(records) > 0 {
		s.flushLogsForSpan(spanID, records)
	}
	if records, exists := s.pendingLogs[spanID]; exists {
		s.flushLogsForSpan(spanID, records)
		delete(s.pendingLogs, spanID)
	}
}

// releaseHeldLogs prints a span's held logs once it is shown, and drops them
// once it has ended without being shown.
// The caller must hold the mutex.
func (s *streamingLogExporter) releaseHeldLogs(spanID dagui.SpanID) {
	held, ok := s.heldLogs[spanID]
	if !ok {
		return
	}
	span := s.db.Spans.Map[spanID]
	switch {
	case span != nil && streamsSpan(s.db, s.opts, span):
		delete(s.heldLogs, spanID)
		s.printLogs(span, held)
	case span == nil || !span.IsRunning():
		delete(s.heldLogs, spanID)
	}
}

// streamingLogsPrefix renders the prefix for log lines from a span.
func streamingLogsPrefix(r *renderer, profile termenv.Profile, span *dagui.Span) string {
	var spanName strings.Builder
	out := NewOutput(&spanName, termenv.WithProfile(profile))
	fmt.Fprintf(out, "%s ", CaretDownFilled)
	if span.Call() != nil {
		r.renderCall(out, span, span.Call(), "", false, 0, false, nil, true /* no type */)
	} else {
		fmt.Fprintf(&spanName, "%s", out.String(span.Name).Bold())
	}
	return spanName.String() + "\n"
}
