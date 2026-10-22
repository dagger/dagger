package idtui

import (
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql/dagui"
)

// LogRequest is a frontend's request for the display text of one span, as
// it routes it: the span's own, its descendants' when it rolls them up, and
// the records naming a digest the span created (dagui routes those to it).
type LogRequest struct {
	Span        dagui.SpanID
	Descendants bool
	Digests     []string
}

// LiveLogsFrontend is the optional interface a live session drives when its
// engine pushes log presence instead of display text (client.Params.
// EngineLogsPresence): the frontend then reads each span's display text on
// demand, only for the spans it renders. Only the pretty frontend implements
// it; the others render every log as it streams, and keep the full push.
type LiveLogsFrontend interface {
	// LiveLogs reports whether the frontend reads a live session's display
	// text on demand, and if so whether it follows the spans it renders
	// while the session runs (the interactive TUI) or only reads, at exit,
	// what its final render shows (the report).
	LiveLogs() (onDemand, follow bool)
	// SetLiveLogs switches the frontend to reading the display text of
	// traceID's spans on demand. It calls subscribe, on its event loop
	// (subscribe must not block), for each span as it starts rendering its
	// logs, at most once per span. Display text of traceID's spans arriving
	// on LogExporter (this process's own, which the store returns too) is
	// dropped from then on, along with what it already rendered of it; what
	// is read for a request must arrive on the returned exporter. The
	// primary output is no longer buffered as it streams (SetPrimaryOutput).
	SetLiveLogs(traceID dagui.TraceID, subscribe func(LogRequest)) sdklog.Exporter
	// RequestFinalLogs requests the logs the final render shows that
	// nothing requested yet, such as surfaced failures, so they can be read
	// before the session closes. It returns once they are requested.
	RequestFinalLogs()
	// PrimarySpan is the span whose output SetPrimaryOutput takes.
	PrimarySpan() dagui.SpanID
	// SetPrimaryOutput hands the frontend the primary span's output, read
	// once at exit, for the final render to write. Records are kept only if
	// they route to the primary span; it returns once they are applied, and
	// so after every record exported before it.
	SetPrimaryOutput([]sdklog.Record)
	// LiveLogsCoverage tells, for the records a read returned (by the
	// trace, span and digest a read selects them by), the span the frontend
	// routes them to, and whether that is a span it requested or a
	// descendant of one it requested with its descendants: a read may return
	// more than that.
	LiveLogsCoverage([]LogTarget) []LogRoute
	// LiveLogsActivity tells, for each span, whether more of the display
	// text it routes may still come (LogActivity). A reader following a
	// session's logs stops following spans that are idle, so a long
	// session's follow selects only the spans still working. It returns
	// nothing once the frontend quit.
	LiveLogsActivity([]dagui.SpanID) []LogActivity
}

// LogActivity is whether a span's display text may still grow.
type LogActivity struct {
	// Active: the span runs, or work its logs include does -- running
	// descendants, cause-linked effects such as the service whose stdio
	// names a digest it created -- or it was not received yet.
	Active bool
	// Through is the latest end of the span and of its cause-linked
	// effects. It only moves forward: when it does, work its logs include
	// ran since, however briefly.
	Through time.Time
}

// LogTarget names the records of one span and digest (if any).
type LogTarget struct {
	Trace  dagui.TraceID
	Span   dagui.SpanID
	Digest string
}

// LogRoute is where a frontend routes some records, and whether it asked
// for them.
type LogRoute struct {
	Coverage LogCoverage
	// Span is the span the records route to; zero when unknown.
	Span dagui.SpanID
}

// LogCoverage is whether a frontend asked for some records.
type LogCoverage int

const (
	// LogCovered records route to a requested span, or below one requested
	// with its descendants.
	LogCovered LogCoverage = iota
	// LogUncovered records route to a known span outside every request.
	LogUncovered
	// LogUnknown records route to a span (or a digest's creator) the
	// frontend has not received yet, so it cannot tell.
	LogUnknown
)

var _ LiveLogsFrontend = (*frontendPretty)(nil)

func (fe *frontendPretty) LiveLogs() (onDemand, follow bool) {
	return true, !fe.reportOnly
}

// dispatchWait runs fn like dispatch, and returns once it ran, or once the
// TUI quit and nothing will run it.
func (fe *frontendPretty) dispatchWait(fn func()) {
	done := make(chan struct{})
	fe.dispatch(func() {
		defer close(done)
		fn()
	})
	select {
	case <-done:
	case <-fe.quit:
	}
}

func (fe *frontendPretty) SetLiveLogs(traceID dagui.TraceID, subscribe func(LogRequest)) sdklog.Exporter {
	fe.dispatchWait(func() {
		fe.liveLogsTrace = traceID
		fe.db.PrimaryLogsOnDemand = true
		clear(fe.db.PrimaryLogs)
		if fe.traceLogProviders == nil {
			fe.traceLogProviders = make(map[dagui.TraceID]func(dagui.SpanID, bool))
		}
		fe.traceLogProviders[traceID] = func(id dagui.SpanID, descendants bool) {
			if fe.liveRequests == nil {
				fe.liveRequests = make(map[dagui.SpanID]bool)
			}
			fe.liveRequests[id] = descendants
			subscribe(LogRequest{Span: id, Descendants: descendants, Digests: fe.logDigests(id)})
		}
		// Forget what this process's own display text rendered so far: the
		// store returns it to the spans that ask.
		for id := range fe.logs.Logs {
			if fe.inTrace(id, traceID) {
				delete(fe.logs.Logs, id)
				delete(fe.logs.ToolArgs, id)
				delete(fe.logs.PrefixWriters, id)
				delete(fe.logs.SawEOF, id)
				fe.updateSpanTreesForLogs(id)
			}
		}
		// Rows already showing their logs asked before there was anything
		// to ask, and a mounted view does not ask again.
		if fe.rows != nil {
			for _, row := range fe.rows.Order {
				if row.Span != nil && row.Expanded {
					fe.requestLogs(row.Span.ID)
				}
			}
		}
		fe.Update()
	})
	return prettyLogExporter{frontendPretty: fe, read: true}
}

// inTrace reports whether span id belongs to traceID; a span not received
// yet is taken to.
func (fe *frontendPretty) inTrace(id dagui.SpanID, traceID dagui.TraceID) bool {
	span, ok := fe.db.Spans.Map[id]
	return !ok || !span.Received || span.TraceID == traceID
}

// logDigests lists the digests whose records dagui routes to span id: the
// output it created (see dagui's routeLog).
func (fe *frontendPretty) logDigests(id dagui.SpanID) []string {
	if span, ok := fe.db.Spans.Map[id]; ok && span.Output != "" {
		return []string{span.Output}
	}
	return nil
}

// primaryOutputOnDemand reports whether the primary output is read once at
// exit (SetPrimaryOutput) rather than requested like any span's logs.
func (fe *frontendPretty) primaryOutputOnDemand() bool {
	return fe.liveLogsTrace.IsValid() && fe.inTrace(fe.primarySpan(), fe.liveLogsTrace)
}

func (fe *frontendPretty) RequestFinalLogs() {
	fe.dispatchWait(func() {
		fe.finalLogs = true
		fe.recalculateViewLocked()
		// Rows showing their logs inline (e.g. every completed step at high
		// verbosity): the report mounts no views to ask for them.
		if fe.rows != nil {
			for _, row := range fe.rows.Order {
				if row.Span != nil && row.Expanded {
					fe.requestLogs(row.Span.ID)
				}
			}
		}
		// The final render zooms to the primary span, wherever the user
		// was: request its root causes too.
		if prim := fe.db.Spans.Map[fe.primarySpan()]; prim != nil {
			for _, origin := range fe.checkRootCauses(prim) {
				fe.requestLogs(origin.ID)
			}
		}
	})
}

func (fe *frontendPretty) PrimarySpan() dagui.SpanID {
	got := make(chan dagui.SpanID, 1)
	fe.dispatchWait(func() { got <- fe.primarySpan() })
	select {
	case id := <-got:
		return id
	default:
		return dagui.SpanID{}
	}
}

func (fe *frontendPretty) SetPrimaryOutput(records []sdklog.Record) {
	fe.dispatchWait(func() {
		primary := fe.primarySpan()
		var kept []sdklog.Record
		for _, rec := range records {
			if _, isText := dagui.LogBodyString(rec); isText && fe.db.LogTargetSpanID(rec) == primary {
				kept = append(kept, rec)
			}
		}
		fe.db.PrimaryLogs[primary] = kept
		// What this process logs from now on -- e.g. a failing cleanup --
		// is not in what was read: buffer it after.
		fe.db.PrimaryLogsOnDemand = false
	})
}

func (fe *frontendPretty) LiveLogsCoverage(targets []LogTarget) []LogRoute {
	routes := make([]LogRoute, len(targets))
	fe.dispatchWait(func() {
		for i, t := range targets {
			routes[i] = fe.liveLogRoute(t)
		}
	})
	return routes
}

func (fe *frontendPretty) LiveLogsActivity(spans []dagui.SpanID) []LogActivity {
	got := make(chan []LogActivity, 1)
	fe.dispatchWait(func() {
		activity := make([]LogActivity, len(spans))
		for i, id := range spans {
			activity[i] = fe.liveLogActivity(id)
		}
		got <- activity
	})
	select {
	case activity := <-got:
		return activity
	default:
		// The TUI quit: nothing tells.
		return nil
	}
}

func (fe *frontendPretty) liveLogActivity(id dagui.SpanID) LogActivity {
	span, ok := fe.db.Spans.Map[id]
	if !ok || !span.Received {
		return LogActivity{Active: true}
	}
	// RunningSpans holds the running descendants a roll-up shows; Activity
	// (in IsRunningOrEffectsRunning) the effects and lazy evaluations linked
	// to it or its descendants, a service's included.
	activity := LogActivity{
		Active:  span.IsRunningOrEffectsRunning() || span.RunningSpans.Len() > 0,
		Through: span.EndTime,
	}
	// Merged and ordered by start, so the last interval ends last.
	if ivals := span.Activity.CompletedIntervals; len(ivals) > 0 && ivals[len(ivals)-1].End.After(activity.Through) {
		activity.Through = ivals[len(ivals)-1].End
	}
	return activity
}

func (fe *frontendPretty) liveLogRoute(t LogTarget) LogRoute {
	target := fe.db.LogTarget(t.Trace, t.Span, t.Digest)
	span, ok := fe.db.Spans.Map[target]
	if !target.IsValid() || !ok || !span.Received {
		return LogRoute{Coverage: LogUnknown}
	}
	route := LogRoute{Coverage: LogCovered, Span: target}
	if _, ok := fe.liveRequests[target]; ok {
		return route
	}
	for id := span.ParentID; id.IsValid(); {
		parent, ok := fe.db.Spans.Map[id]
		if !ok || !parent.Received {
			return LogRoute{Coverage: LogUnknown, Span: target}
		}
		if fe.liveRequests[id] {
			return route
		}
		id = parent.ParentID
	}
	route.Coverage = LogUncovered
	return route
}
