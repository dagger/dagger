package daggercmd

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"sync"
	"time"

	telemetry "github.com/dagger/otel-go"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tracesource"
)

// liveLogsFinishTimeout bounds the exit's wait for the session's telemetry
// and its read of the final logs, together, so a stuck engine never holds
// up the command's exit for long. The primary output's read has its own
// budget (liveLogsPrimaryTimeout).
const liveLogsFinishTimeout = 10 * time.Second

// liveLogs reads a live session's display text on demand, for the spans
// its frontend renders (idtui.LiveLogsFrontend). Requested spans accumulate
// into one set, read with one query at a time:
//
//   - Following (the TUI), one follow query reads the whole set from a
//     cursor. A span joining the set is caught up with a one-shot read up to
//     that cursor, then the follow restarts over the grown set from it, so
//     every row is read once.
//   - Otherwise (the report), nothing is read until finish.
//
// finish, at exit, catches everything up to the store's end. Records are
// imported under their own span IDs: the frontend routes them and copies
// them into roll-ups itself, from the full span tree it has in a live run.
//
// A query may return rows outside its selection (rows of spans the engine
// has not placed yet), and the spans of a catch-up may overlap the ones
// followed, so each (span, digest) pair -- the unit a query selects rows by
// -- is owned by the read that first imported it: a catch-up imports
// nothing of a pair it does not own, which the follow already did.
//
// When the frontend lets go of spans (release), they leave the set, and the
// pairs routing to the spans whose display text it dropped are forgotten, so
// their next request reads them again from the start.
type liveLogs struct {
	reader  tracesource.LogReader
	traceID string
	export  sdklog.Exporter
	follow  bool
	// coverage, when set, classifies records by the frontend's requests
	// (idtui.LiveLogsFrontend.LiveLogsCoverage): a read imports only what
	// the frontend asked for, and claims only pairs it knows it did.
	coverage func([]idtui.LogTarget) []idtui.LogRoute
	// frontend is the frontend reading, which finishLiveLogs finishes.
	frontend idtui.LiveLogsFrontend

	mu sync.Mutex
	// spans are followed (or, without following, read at finish);
	// pending joined since and are not caught up yet.
	spans, pending map[string]archive.LogSpan
	// catching are the spans of the catch-up in flight.
	catching map[string]archive.LogSpan
	// cursor is where the follow of spans stands; "" before it read
	// anything, when a newcomer needs no catch-up.
	cursor string
	// owned maps a (span, digest) pair to the read that imported it first:
	// 0 for a follow (or finish's read), a catch-up's number otherwise.
	owned    map[logOwner]logClaim
	catchUps uint64
	// gen counts releases (release); released maps each span the frontend
	// dropped since the current read began to the release that dropped it:
	// the read imports nothing more for it.
	gen      uint64
	released map[dagui.SpanID]uint64
	finished bool
	wake     chan struct{}

	stop func()
}

type logOwner struct {
	span   [8]byte
	digest string
}

// logClaim is the read that owns a pair, and the span the frontend routes
// its records to.
type logClaim struct {
	read   uint64
	target dagui.SpanID
}

func newLiveLogs(reader tracesource.LogReader, traceID string, export sdklog.Exporter, follow bool) *liveLogs {
	return &liveLogs{
		reader:   reader,
		traceID:  traceID,
		export:   export,
		follow:   follow,
		spans:    map[string]archive.LogSpan{},
		pending:  map[string]archive.LogSpan{},
		owned:    map[logOwner]logClaim{},
		released: map[dagui.SpanID]uint64{},
		wake:     make(chan struct{}, 1),
		stop:     func() {},
	}
}

// startLiveLogs switches fe to reading the session's display text on
// demand, when the session negotiated log presence, and returns the reader;
// nil otherwise.
func startLiveLogs(ctx context.Context, fe idtui.Frontend, client tracesource.LiveLogClient, presence bool, traceID dagui.TraceID) *liveLogs {
	lf, ok := fe.(idtui.LiveLogsFrontend)
	if !ok || !presence || !traceID.IsValid() {
		return nil
	}
	_, follow := lf.LiveLogs()
	l := newLiveLogs(tracesource.Live{Client: client}, traceID.String(), nil, follow)
	l.frontend = lf
	l.coverage = lf.LiveLogsCoverage
	l.export = lf.SetLiveLogs(traceID, l.subscribe)
	if cf, ok := fe.(idtui.LogCacheFrontend); ok {
		cf.SetLogRelease(traceID, l.release)
		if searcher, ok := client.(liveLogSearcher); ok {
			cf.SetLogSearch(traceID, logSearchFunc(searcher.SearchLogs))
		}
	}
	if follow {
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			l.run(ctx)
		}()
		l.stop = func() { cancel(); <-done }
	}
	return l
}

// subscribe adds a span to the set. It runs on the frontend's event loop,
// so it never blocks.
func (l *liveLogs) subscribe(req idtui.LogRequest) {
	span := archive.LogSpan{SpanID: req.Span.String(), Descendants: req.Descendants, Digests: req.Digests}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return
	}
	if _, ok := l.spans[span.SpanID]; ok {
		return
	}
	if _, ok := l.pending[span.SpanID]; ok {
		return
	}
	l.pending[span.SpanID] = span
	l.notify()
}

func (l *liveLogs) notify() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// release lets go of what the frontend dropped (idtui.LogRelease): the
// spans it no longer requests leave the set, ending the follow so it
// resumes without them, and the pairs routing to the spans it dropped are
// forgotten, so a later request imports them again from the start. Like
// subscribe, it runs on the frontend's event loop.
func (l *liveLogs) release(r idtui.LogRelease) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return
	}
	l.gen++
	dropped := make(map[dagui.SpanID]bool, len(r.Dropped))
	for _, id := range r.Dropped {
		dropped[id] = true
		l.released[id] = l.gen
	}
	for key, claim := range l.owned {
		if dropped[claim.target] {
			delete(l.owned, key)
		}
	}
	var left bool
	for _, id := range r.Spans {
		hex := id.String()
		if _, ok := l.spans[hex]; ok {
			delete(l.spans, hex)
			left = true
		}
		delete(l.pending, hex)
		delete(l.catching, hex)
	}
	if left {
		l.notify()
	}
}

// beginRead starts a read: no other read is in flight, so the spans
// released before it no longer matter. It returns the release generation
// the read starts at.
func (l *liveLogs) beginRead() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.released)
	return l.gen
}

// run follows the set until ctx ends, or the session's telemetry drains.
func (l *liveLogs) run(ctx context.Context) {
	for ctx.Err() == nil {
		// Whatever woke us is taken by the catch-up below; a span joining
		// after it wakes us again.
		select {
		case <-l.wake:
		default:
		}
		if err := l.catchUp(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("error catching up live logs", "err", err)
			l.pause(ctx)
			continue
		}
		l.mu.Lock()
		spans := slices.Collect(maps.Values(l.spans))
		cursor := l.cursor
		l.mu.Unlock()
		if len(spans) == 0 {
			select {
			case <-ctx.Done():
			case <-l.wake:
			}
			continue
		}
		// A span joining or leaving the set ends this follow, to catch it
		// up and follow the set as it is now.
		followCtx, cancel := context.WithCancel(ctx)
		woken := make(chan struct{})
		go func() {
			select {
			case <-l.wake:
				close(woken)
				cancel()
			case <-followCtx.Done():
			}
		}()
		err := l.reader.ReadLogs(followCtx, l.traceID, tracesource.LogQuery{Spans: spans, Follow: true, Cursor: cursor}, l.importFollowed(l.beginRead()))
		cancel()
		select {
		case <-woken:
			continue
		default:
		}
		switch {
		case ctx.Err() != nil:
			return
		case err == nil:
			// The session's telemetry drained: there is nothing more to
			// follow, and finish reads what joins after.
			return
		default:
			slog.Warn("error following live logs", "err", err)
			l.pause(ctx)
		}
	}
}

func (l *liveLogs) pause(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}

// catchUp reads the pending spans' rows up to the follow's cursor, then
// moves them into the set. Without a cursor there is nothing to catch up:
// the next read starts from the beginning.
func (l *liveLogs) catchUp(ctx context.Context) error {
	l.mu.Lock()
	batch := l.pending
	l.pending = map[string]archive.LogSpan{}
	l.catching = maps.Clone(batch)
	cursor := l.cursor
	l.catchUps++
	read := l.catchUps
	l.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	start := l.beginRead()
	var imported bool
	var err error
	if cursor != "" {
		err = l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{
			Spans:   slices.Collect(maps.Values(batch)),
			Through: cursor,
		}, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, _ string) error {
			imported = true
			return l.importLogs(ctx, req, read, start)
		})
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// What the frontend released meanwhile is left out.
	batch, l.catching = l.catching, nil
	if err != nil && !imported {
		// Nothing landed: try these again.
		for id, span := range batch {
			if _, ok := l.pending[id]; !ok {
				l.pending[id] = span
			}
		}
		return err
	}
	// A partial catch-up is not retried, which would import its rows
	// twice; the follow carries on from the cursor.
	maps.Copy(l.spans, batch)
	return err
}

// importFollowed returns the import of the set's own read, which started at
// release generation start: each batch carries the cursor to resume after
// it.
func (l *liveLogs) importFollowed(start uint64) func(context.Context, *collogspb.ExportLogsServiceRequest, string) error {
	return func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, cursor string) error {
		if err := l.importLogs(ctx, req, 0, start); err != nil {
			return err
		}
		l.mu.Lock()
		l.cursor = cursor
		l.mu.Unlock()
		return nil
	}
}

// importLogs exports the records of req that read owns (see liveLogs), of
// those the frontend routes to what it requested, leaving out the spans it
// dropped since the read started (at release generation start).
func (l *liveLogs) importLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest, read, start uint64) error {
	var routes []idtui.LogRoute
	if l.coverage != nil {
		var targets []idtui.LogTarget
		for _, rl := range req.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				for _, rec := range sl.GetLogRecords() {
					key := logOwnerOf(rec)
					var target idtui.LogTarget
					copy(target.Trace.TraceID[:], rec.GetTraceId())
					target.Span.SpanID = key.span
					target.Digest = key.digest
					targets = append(targets, target)
				}
			}
		}
		routes = l.coverage(targets)
	}
	l.mu.Lock()
	i := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			sl.LogRecords = slices.DeleteFunc(sl.LogRecords, func(rec *otlplogsv1.LogRecord) bool {
				key := logOwnerOf(rec)
				route := idtui.LogRoute{Coverage: idtui.LogCovered, Span: dagui.SpanID{SpanID: key.span}}
				if i < len(routes) {
					route = routes[i]
				}
				i++
				if route.Span.IsValid() && l.released[route.Span] > start {
					// Dropped while this read was in flight: a later
					// request reads it again.
					return true
				}
				switch route.Coverage {
				case idtui.LogUncovered:
					// Not asked for: the read of a span that asks for it
					// later imports it then.
					return true
				case idtui.LogUnknown:
					// Its span is not placed yet: import it, but without
					// claiming the pair this read may not keep selecting.
					return false
				}
				claim, ok := l.owned[key]
				if !ok {
					l.owned[key] = logClaim{read: read, target: route.Span}
					return false
				}
				return claim.read != read && read != 0
			})
		}
	}
	l.mu.Unlock()
	return telemetry.ReexportLogsFromPB(ctx, l.export, req)
}

func logOwnerOf(rec *otlplogsv1.LogRecord) logOwner {
	var key logOwner
	copy(key.span[:], rec.GetSpanId())
	for _, kv := range rec.GetAttributes() {
		if kv.GetKey() == telemetry.DagDigestAttr {
			key.digest = kv.GetValue().GetStringValue()
			break
		}
	}
	return key
}

// finish stops following and reads everything requested up to the store's
// end: rows the follow had not reached yet, and the spans the frontend
// requested for its final render (idtui.LiveLogsFrontend.RequestFinalLogs).
// Nothing is read after it.
func (l *liveLogs) finish(ctx context.Context) error {
	l.stop()
	l.mu.Lock()
	if l.finished {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	err := l.catchUp(ctx)
	l.mu.Lock()
	l.finished = true
	spans := slices.Collect(maps.Values(l.spans))
	cursor := l.cursor
	l.mu.Unlock()
	if len(spans) == 0 {
		return err
	}
	return errors.Join(err, l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{Spans: spans, Cursor: cursor}, l.importFollowed(l.beginRead())))
}

// liveLogSearcher is an engine client's search of its own live store.
type liveLogSearcher interface {
	SearchLogs(context.Context, archive.LogSearch) (archive.LogSearchResult, error)
}

// logSearchPages bounds the pages of matches a frontend's search reads.
const logSearchPages = 10

// logSearchFunc is a frontend's search of a source's display text
// (idtui.LogCacheFrontend.SetLogSearch): a case-insensitive substring, as
// the frontend matches what it holds. It reads up to logSearchPages pages of
// matches, and says when the store had more.
func logSearchFunc(search func(context.Context, archive.LogSearch) (archive.LogSearchResult, error)) idtui.LogSearchFunc {
	return func(ctx context.Context, pattern string) (idtui.LogSearchHits, error) {
		var hits idtui.LogSearchHits
		seen := map[idtui.LogTarget]bool{}
		query := archive.LogSearch{Pattern: pattern, IgnoreCase: true, Limit: archive.MaxLogSearchLimit}
		for page := 0; ; page++ {
			result, err := search(ctx, query)
			if err != nil {
				return hits, err
			}
			for _, match := range result.Matches {
				traceID, err1 := trace.TraceIDFromHex(match.TraceID)
				spanID, err2 := trace.SpanIDFromHex(match.SpanID)
				if err1 != nil || err2 != nil {
					continue
				}
				target := idtui.LogTarget{
					Trace:  dagui.TraceID{TraceID: traceID},
					Span:   dagui.SpanID{SpanID: spanID},
					Digest: match.Digest,
				}
				if !seen[target] {
					seen[target] = true
					hits.Targets = append(hits.Targets, target)
				}
			}
			if !result.More {
				return hits, nil
			}
			if page+1 >= logSearchPages || result.Next <= query.After {
				hits.Partial = true
				return hits, nil
			}
			query.After = result.Next
		}
	}
}

// liveLogsWanted reports whether to ask the engine for log presence: the
// frontend reads display text on demand, and nothing else in this process
// receives the session's logs -- exporters configured in the environment
// need every body pushed. (A Cloud the engine does not publish to itself
// needs them too; the client settles that with the engine.)
func liveLogsWanted(ctx context.Context) bool {
	lf, ok := Frontend.(idtui.LiveLogsFrontend)
	if !ok || frontendTelemetryDisabled(ctx) || cliLocalTelemetry == nil {
		return false
	}
	if onDemand, _ := lf.LiveLogs(); !onDemand {
		return false
	}
	// The frontend's processor is the only one left once local telemetry's
	// is taken out.
	return len(withoutIndex(telemetry.LogProcessors, cliLocalTelemetry.logs)) == 1
}

// liveLogsSession is the session a live log reader reads.
type liveLogsSession interface {
	TelemetryBarrier(context.Context) error
	WaitTelemetryIdle(context.Context, time.Duration)
}

// liveLogsSettle is how long the session's telemetry must stay quiet at exit,
// with an engine that predates TelemetryBarrier, before the frontend chooses
// what its final render reads: the spans of work that just ended, still on
// their way through the engine's batching and the subscriptions' polling,
// must be there to choose from.
const liveLogsSettle = 300 * time.Millisecond

// liveLogsPrimaryTimeout bounds the primary output's read at exit. It is
// generous: the output is the command's result, usually small, and a slow
// remote engine must not cut it short. liveLogsPrimaryAttempts bounds the
// retries of a failed read.
const (
	liveLogsPrimaryTimeout  = time.Minute
	liveLogsPrimaryAttempts = 3
)

// liveLogsRetryDelay is the pause before retrying the primary output's read.
var liveLogsRetryDelay = 500 * time.Millisecond

// flushableSpanForwarder is a telemetry.SpanForwarder that flushes its
// processors, so client.TelemetryBarrier can see what the session delivered
// through them, not just into them.
type flushableSpanForwarder struct {
	telemetry.SpanForwarder
}

func (f flushableSpanForwarder) ForceFlush(ctx context.Context) error {
	var errs error
	for _, p := range f.Processors {
		errs = errors.Join(errs, p.ForceFlush(ctx))
	}
	return errs
}

// settleLiveLogs waits until the session's telemetry for the work done so
// far has reached the frontend: exactly, with TelemetryBarrier, or else
// until it goes quiet.
func settleLiveLogs(ctx context.Context, sess liveLogsSession) {
	err := sess.TelemetryBarrier(ctx)
	if err == nil || ctx.Err() != nil {
		return
	}
	if !errors.Is(err, client.ErrTelemetryBarrierUnsupported) {
		slog.Warn("error waiting for the session's telemetry", "err", err)
	}
	settleCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	sess.WaitTelemetryIdle(settleCtx, liveLogsSettle)
	cancel()
}

// exitInterrupts returns a context canceled by an interrupt (SIGINT); it is
// a variable for tests.
var exitInterrupts = func(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt)
}

// exitContext is the context of the reads at exit. What ended the command
// does not cancel it -- an interrupt that canceled the run must not cost its
// report -- but an interrupt during the reads does: ctx ending from then
// on (the TUI's Ctrl-C), or another SIGINT.
func exitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	exitCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	stopAfter := func() bool { return false }
	if ctx.Err() == nil {
		stopAfter = context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	}
	sigCtx, stopSignals := exitInterrupts(exitCtx)
	return sigCtx, func() {
		stopSignals()
		stopAfter()
		cancel(nil)
	}
}

// finishLiveLogs reads what the final render shows: the logs the frontend
// requests for it, and the primary output. It must run before the session
// closes, which ends its store's reads. An interrupt stops it promptly, and
// the final render shows what was read by then (see exitContext).
func finishLiveLogs(ctx context.Context, sess liveLogsSession, live *liveLogs) {
	lf := live.frontend
	if lf == nil {
		return
	}
	ctx, stop := exitContext(ctx)
	defer stop()
	finishCtx, cancel := context.WithTimeout(ctx, liveLogsFinishTimeout)
	settleLiveLogs(finishCtx, sess)
	// Stop following first, so finish catches up the final requests itself,
	// rather than a follow restart it would cut short.
	live.stop()
	lf.RequestFinalLogs()
	if err := live.finish(finishCtx); err != nil && ctx.Err() == nil {
		slog.Warn("error reading the final logs", "err", err)
	}
	cancel()
	primaryCtx, cancel := context.WithTimeout(ctx, liveLogsPrimaryTimeout)
	defer cancel()
	records, err := readPrimaryOutput(primaryCtx, live.reader, live.traceID, lf.PrimarySpan())
	if err != nil && ctx.Err() == nil {
		slog.Warn("error reading the primary output", "err", err)
	}
	lf.SetPrimaryOutput(records)
}

// readPrimaryOutput reads the primary span's output, the process's own
// included, from the beginning. A failed read is retried whole, up to
// liveLogsPrimaryAttempts times: it is the command's result.
func readPrimaryOutput(ctx context.Context, reader tracesource.LogReader, traceID string, primary dagui.SpanID) ([]sdklog.Record, error) {
	var err error
	for attempt := 1; ; attempt++ {
		var out recordCollector
		err = reader.ReadLogs(ctx, traceID, tracesource.LogQuery{
			Spans: []archive.LogSpan{{SpanID: primary.String()}},
		}, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, _ string) error {
			return telemetry.ReexportLogsFromPB(ctx, &out, req)
		})
		if err == nil || attempt >= liveLogsPrimaryAttempts || ctx.Err() != nil {
			return out.records, err
		}
		slog.Debug("retrying the primary output's read", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return out.records, err
		case <-time.After(liveLogsRetryDelay):
		}
	}
}

type recordCollector struct {
	records []sdklog.Record
}

func (c *recordCollector) Export(_ context.Context, records []sdklog.Record) error {
	for _, rec := range records {
		c.records = append(c.records, rec.Clone())
	}
	return nil
}

func (c *recordCollector) Shutdown(context.Context) error   { return nil }
func (c *recordCollector) ForceFlush(context.Context) error { return nil }
