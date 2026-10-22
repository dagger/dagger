package daggercmd

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	telemetry "github.com/dagger/otel-go"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/internal/tracesource"
)

// liveLogsFinishTimeout bounds the reads at exit, so a stuck engine never
// holds up the command's exit.
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
type liveLogs struct {
	reader  tracesource.LogReader
	traceID string
	export  sdklog.Exporter
	follow  bool
	// coverage, when set, classifies records by the frontend's requests
	// (idtui.LiveLogsFrontend.LiveLogsCoverage): a read imports only what
	// the frontend asked for, and claims only pairs it knows it did.
	coverage func([]idtui.LogTarget) []idtui.LogCoverage

	mu sync.Mutex
	// spans are followed (or, without following, read at finish);
	// pending joined since and are not caught up yet.
	spans, pending map[string]archive.LogSpan
	// cursor is where the follow of spans stands; "" before it read
	// anything, when a newcomer needs no catch-up.
	cursor string
	// owned maps a (span, digest) pair to the read that imported it first:
	// 0 for a follow (or finish's read), a catch-up's number otherwise.
	owned    map[logOwner]uint64
	catchUps uint64
	finished bool
	wake     chan struct{}

	stop func()
}

type logOwner struct {
	span   [8]byte
	digest string
}

func newLiveLogs(reader tracesource.LogReader, traceID string, export sdklog.Exporter, follow bool) *liveLogs {
	return &liveLogs{
		reader:  reader,
		traceID: traceID,
		export:  export,
		follow:  follow,
		spans:   map[string]archive.LogSpan{},
		pending: map[string]archive.LogSpan{},
		owned:   map[logOwner]uint64{},
		wake:    make(chan struct{}, 1),
		stop:    func() {},
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
	l.coverage = lf.LiveLogsCoverage
	l.export = lf.SetLiveLogs(traceID, l.subscribe)
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
	select {
	case l.wake <- struct{}{}:
	default:
	}
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
		// A span joining the set ends this follow, to catch it up and
		// follow the grown set.
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
		err := l.reader.ReadLogs(followCtx, l.traceID, tracesource.LogQuery{Spans: spans, Follow: true, Cursor: cursor}, l.importFollowed)
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
	cursor := l.cursor
	l.catchUps++
	read := l.catchUps
	l.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	var imported bool
	var err error
	if cursor != "" {
		err = l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{
			Spans:   slices.Collect(maps.Values(batch)),
			Through: cursor,
		}, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, _ string) error {
			imported = true
			return l.importLogs(ctx, req, read)
		})
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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

// importFollowed imports a batch of the set's own read, which carries the
// cursor to resume after it.
func (l *liveLogs) importFollowed(ctx context.Context, req *collogspb.ExportLogsServiceRequest, cursor string) error {
	if err := l.importLogs(ctx, req, 0); err != nil {
		return err
	}
	l.mu.Lock()
	l.cursor = cursor
	l.mu.Unlock()
	return nil
}

// importLogs exports the records of req that read owns (see liveLogs), of
// those the frontend routes to what it requested.
func (l *liveLogs) importLogs(ctx context.Context, req *collogspb.ExportLogsServiceRequest, read uint64) error {
	var coverage []idtui.LogCoverage
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
		coverage = l.coverage(targets)
	}
	l.mu.Lock()
	i := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			sl.LogRecords = slices.DeleteFunc(sl.LogRecords, func(rec *otlplogsv1.LogRecord) bool {
				cover := idtui.LogCovered
				if i < len(coverage) {
					cover = coverage[i]
				}
				i++
				switch cover {
				case idtui.LogUncovered:
					// Not asked for: the read of a span that asks for it
					// later imports it then.
					return true
				case idtui.LogUnknown:
					// Its span is not placed yet: import it, but without
					// claiming the pair this read may not keep selecting.
					return false
				}
				key := logOwnerOf(rec)
				owner, ok := l.owned[key]
				if !ok {
					l.owned[key] = read
					return false
				}
				return owner != read && read != 0
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
	return errors.Join(err, l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{Spans: spans, Cursor: cursor}, l.importFollowed))
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
	WaitTelemetryIdle(context.Context, time.Duration)
}

// liveLogsSettle is how long the session's telemetry must stay quiet at exit
// before the frontend chooses what its final render reads: the spans of work
// that just ended, still on their way through the engine's batching and the
// subscriptions' polling, must be there to choose from.
const liveLogsSettle = 300 * time.Millisecond

// finishLiveLogs reads what the final render shows: the logs the frontend
// requests for it, and the primary output. It must run before the session
// closes, which ends its store's reads.
func finishLiveLogs(ctx context.Context, sess liveLogsSession, live *liveLogs) {
	lf, ok := Frontend.(idtui.LiveLogsFrontend)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveLogsFinishTimeout)
	defer cancel()
	settleCtx, cancelSettle := context.WithTimeout(ctx, 3*time.Second)
	sess.WaitTelemetryIdle(settleCtx, liveLogsSettle)
	cancelSettle()
	// Stop following first, so finish catches up the final requests itself,
	// rather than a follow restart it would cut short.
	live.stop()
	lf.RequestFinalLogs()
	if err := live.finish(ctx); err != nil {
		slog.Warn("error reading the final logs", "err", err)
	}
	records, err := readPrimaryOutput(ctx, live.reader, live.traceID, lf.PrimarySpan())
	if err != nil {
		slog.Warn("error reading the primary output", "err", err)
	}
	lf.SetPrimaryOutput(records)
}

// readPrimaryOutput reads the primary span's output, the process's own
// included, from the beginning.
func readPrimaryOutput(ctx context.Context, reader tracesource.LogReader, traceID string, primary dagui.SpanID) ([]sdklog.Record, error) {
	var out recordCollector
	err := reader.ReadLogs(ctx, traceID, tracesource.LogQuery{
		Spans: []archive.LogSpan{{SpanID: primary.String()}},
	}, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, _ string) error {
		return telemetry.ReexportLogsFromPB(ctx, &out, req)
	})
	return out.records, err
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
