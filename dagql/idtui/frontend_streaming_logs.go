package idtui

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
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
}

// Export processes log records: exports to the DB, groups by span, and either
// flushes immediately (if the span exists) or buffers for later.
// The caller must hold the mutex.
func (s *streamingLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if err := s.db.LogExporter().Export(ctx, records); err != nil {
		return err
	}

	if len(records) == 0 {
		return nil
	}

	// Group records by span and either flush immediately if span exists, or store for later
	spanGroups := make(map[dagui.SpanID][]sdklog.Record)
	for _, record := range records {
		spanID := dagui.SpanID{SpanID: record.SpanID()}
		spanGroups[spanID] = append(spanGroups[spanID], record)
	}

	for spanID, records := range spanGroups {
		// Check if span exists in DB
		dbSpan := s.db.Spans.Map[spanID]
		if dbSpan != nil && dbSpan.Name != "" {
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
	// Get span info from DB
	dbSpan := s.db.Spans.Map[spanID]
	if dbSpan == nil {
		return
	}

	// Check if we should show this span
	var skip bool
	for p := range dbSpan.Parents {
		if p.Encapsulate || !s.opts.ShouldShow(s.db, p) {
			skip = true
			break
		}
	}
	if dbSpan.ID == s.db.PrimarySpan {
		// don't print primary span logs; they'll be printed at the end
		skip = true
	}

	if skip || (dbSpan.Encapsulated && !dbSpan.IsFailedOrCausedFailure()) {
		return // Skip logs for encapsulated spans
	}

	// Set prefix
	r := newRenderer(s.db, 0, *s.opts, true)
	prefix := streamingLogsPrefix(r, s.profile, dbSpan)

	// Write all logs for this span, filtering out verbose logs
	for _, record := range records {
		// Check if this log is marked as verbose
		isVerbose := false
		record.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.LogsVerboseAttr && kv.Value.AsBool() {
				isVerbose = true
				return false // stop walking
			}
			return true // continue walking
		})

		// Skip verbose logs
		if isVerbose {
			continue
		}

		body := record.Body().AsString()
		if body == "" {
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

// flushPendingLogsForSpan flushes any pending logs when a span becomes available.
// The caller must hold the mutex.
func (s *streamingLogExporter) flushPendingLogsForSpan(spanID dagui.SpanID) {
	if records, exists := s.pendingLogs[spanID]; exists {
		s.flushLogsForSpan(spanID, records)
		delete(s.pendingLogs, spanID)
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
