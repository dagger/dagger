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
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tracesource"
)

// liveLogsFinishTimeout bounds the telemetry barrier and the final logs'
// read at exit, so a stuck engine never holds up the command's exit.
const liveLogsFinishTimeout = 10 * time.Second

const (
	// liveLogsPollInterval is how often a follow asks the frontend about
	// its spans' activity (idtui.LiveLogsFrontend.LiveLogsActivity).
	liveLogsPollInterval = time.Second
	// liveLogsRetireGrace is how long a span stays followed once it went
	// idle: its last rows may be stored a little after it ends.
	liveLogsRetireGrace = 3 * time.Second
)

// liveLogs reads a live session's display text on demand, for the spans
// its frontend renders (idtui.LiveLogsFrontend). Requested spans accumulate
// into one set, read with one query at a time:
//
//   - Following (the TUI), one follow query reads the whole set from a
//     cursor. A span joining the set is caught up with a one-shot read up to
//     that cursor, then the grown set is read from it to the store's end,
//     and followed from there, so every row is read once, and the cursor
//     advances however often spans join.
//   - Otherwise (the report), nothing is read until finish.
//
// Requests only accumulate -- an agent session's conversation is a
// scrollback, whose every message keeps its logs -- but a span whose work
// is over needs no following: once the frontend has reported it idle for
// liveLogsRetireGrace, a follow restart reads it to the store's end one
// last time and retires it from the set, keeping what was read. Otherwise
// a long session's set outgrows what one query may select
// (archive.MaxLogQuerySpans), and its logs stop; spans join a full set
// only as others retire. A retired span can get rows again (an effect, a
// service it installed restarting): it is watched, and once active again
// -- or once work it includes ran since, however briefly -- read from
// where it retired, like a catch-up, and followed again.
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
	// activity, when set, tells which spans may still get rows
	// (idtui.LiveLogsFrontend.LiveLogsActivity): a follow retires the
	// others.
	activity func([]dagui.SpanID) []idtui.LogActivity
	// now, pollEvery and grace pace retirement: activity is asked about the
	// set every pollEvery, and a span idle for grace is retired.
	now              func() time.Time
	pollEvery, grace time.Duration
	// maxSpans bounds the set, as the engine bounds a query
	// (archive.MaxLogQuerySpans): pending spans join only while it has room.
	maxSpans int
	// frontend is the frontend reading, which finishLiveLogs finishes.
	frontend idtui.LiveLogsFrontend

	mu sync.Mutex
	// spans are followed (or, without following, read at finish);
	// pending joined since and are not caught up yet.
	spans, pending map[string]archive.LogSpan
	// idleSince is when a followed span was first seen idle; through, the
	// LogActivity.Through it was last seen with.
	idleSince, through map[string]time.Time
	// retired spans are no longer followed, each read up to its cursor;
	// reactivated ones were retired, then seen active again, and are read
	// from that cursor to the follow's before they are followed again.
	retired, reactivated map[string]retiredLogSpan
	// cursor is where the follow of spans stands; "" before it read
	// anything, when a newcomer needs no catch-up.
	cursor string
	// owned maps a (span, digest) pair to the read that imported it first:
	// 0 for a follow (or finish's read), a catch-up's number otherwise. A
	// retiring span's pairs nothing followed selects pass to its gap read.
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

// retiredLogSpan is a span the follow no longer reads, the cursor it was
// read up to, and the LogActivity.Through it retired with.
type retiredLogSpan struct {
	span    archive.LogSpan
	cursor  string
	through time.Time
	// read numbers its gap read, should it reactivate (see liveLogs.owned).
	read uint64
}

func newLiveLogs(reader tracesource.LogReader, traceID string, export sdklog.Exporter, follow bool) *liveLogs {
	return &liveLogs{
		reader:      reader,
		traceID:     traceID,
		export:      export,
		follow:      follow,
		now:         time.Now,
		pollEvery:   liveLogsPollInterval,
		grace:       liveLogsRetireGrace,
		maxSpans:    archive.MaxLogQuerySpans,
		spans:       map[string]archive.LogSpan{},
		pending:     map[string]archive.LogSpan{},
		idleSince:   map[string]time.Time{},
		through:     map[string]time.Time{},
		retired:     map[string]retiredLogSpan{},
		reactivated: map[string]retiredLogSpan{},
		owned:       map[logOwner]uint64{},
		wake:        make(chan struct{}, 1),
		stop:        func() {},
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
	l.activity = lf.LiveLogsActivity
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
	// A retired span keeps what it read: the frontend's request is not new.
	if _, ok := l.retired[span.SpanID]; ok {
		return
	}
	if _, ok := l.reactivated[span.SpanID]; ok {
		return
	}
	l.pending[span.SpanID] = span
	l.notify()
}

// notify wakes run, without blocking.
func (l *liveLogs) notify() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// run follows the set until ctx ends, or the session's telemetry drains.
func (l *liveLogs) run(ctx context.Context) {
	if l.activity != nil {
		pollCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { l.poll(pollCtx) })
		defer wg.Wait()
		defer cancel()
	}
	for ctx.Err() == nil {
		// Whatever woke us is taken by the reads below; a span joining
		// after them wakes us again.
		select {
		case <-l.wake:
		default:
		}
		// Reactivated spans have work running: they join first.
		if err := l.reactivate(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("error reading reactivated live logs", "err", err)
			l.pause(ctx)
			continue
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
		// Read the set up to the store's end before following it. A span
		// joining ends the follow below, and a catch-up reads the store from
		// its start: when spans join faster than that -- a long session,
		// parallel tool calls -- the follow is ended again before it reads
		// anything, and without this read its cursor would never move,
		// leaving everything stored since unread for every span.
		readStart := l.now()
		if err := l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{Spans: spans, Cursor: cursor}, l.importFollowed); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("error reading live logs", "err", err)
			l.pause(ctx)
			continue
		}
		// Every row stored before readStart is read now: the spans idle
		// since long enough before it get no more.
		l.retire(readStart)
		l.mu.Lock()
		spans = slices.Collect(maps.Values(l.spans))
		cursor = l.cursor
		joining := (len(l.pending) > 0 || len(l.reactivated) > 0) && len(l.spans) < l.maxSpans
		l.mu.Unlock()
		if joining || len(spans) == 0 {
			// Spans joined meanwhile, and there is room for them: catch
			// them up first. Or every span retired: wait for one to join.
			continue
		}
		// A span joining the set ends this follow, to catch it up and
		// follow the grown set; so does one to retire, or to reactivate.
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

// poll asks the frontend which spans are active, every pollEvery, until ctx
// ends.
func (l *liveLogs) poll(ctx context.Context) {
	ticker := time.NewTicker(l.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		l.pollActive()
	}
}

// pollActive asks the frontend about the followed spans and the retired
// ones. A followed span idle for grace wakes run to retire it; a retired one
// active again, or whose work ran since it retired, wakes it to follow it
// again.
func (l *liveLogs) pollActive() {
	l.mu.Lock()
	ids := make([]string, 0, len(l.spans)+len(l.retired))
	for id := range l.spans {
		ids = append(ids, id)
	}
	for id := range l.retired {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	spanIDs := make([]dagui.SpanID, len(ids))
	for i, id := range ids {
		// A span ID that does not parse is invalid, which the frontend
		// does not know, so it stays active.
		spanIDs[i].SpanID, _ = trace.SpanIDFromHex(id)
	}
	activity := l.activity(spanIDs)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wake bool
	// Past what activity returns, the frontend quit: nothing tells.
	for i, id := range ids[:min(len(ids), len(activity))] {
		act := activity[i]
		if _, ok := l.spans[id]; ok {
			prev, seen := l.through[id]
			l.through[id] = act.Through
			if act.Active || (seen && act.Through.After(prev)) {
				// Work its logs include runs, or ran since: its rows may
				// still be stored.
				delete(l.idleSince, id)
				continue
			}
			since, ok := l.idleSince[id]
			if !ok {
				since = now
				l.idleSince[id] = since
			}
			if !now.Before(since.Add(l.grace)) {
				wake = true
			}
		} else if r, ok := l.retired[id]; ok && (act.Active || act.Through.After(r.through)) {
			delete(l.retired, id)
			r.through = act.Through
			l.reactivated[id] = r
			wake = true
		}
	}
	if wake {
		l.notify()
	}
}

// retire takes out of the set the spans idle for grace by readStart, when a
// read of the set from the cursor to the store's end started: every row
// stored before then is read, and none comes after.
//
// The pairs a retiring span selects by its own ID or its digests, and no
// followed span does, pass to its gap read: work it includes may store more
// rows of them later -- a service it installed restarting writes its stdio
// under the installing span's ID again -- which only that read imports.
func (l *liveLogs) retire(readStart time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	retiring := map[[8]byte]uint64{}
	retiringDigests := map[string]uint64{}
	for id, since := range l.idleSince {
		span, ok := l.spans[id]
		if !ok {
			delete(l.idleSince, id)
			continue
		}
		if since.Add(l.grace).After(readStart) {
			continue
		}
		l.catchUps++
		r := retiredLogSpan{span: span, cursor: l.cursor, through: l.through[id], read: l.catchUps}
		l.retired[id] = r
		delete(l.idleSince, id)
		delete(l.through, id)
		delete(l.spans, id)
		if sid, err := trace.SpanIDFromHex(id); err == nil {
			retiring[sid] = r.read
		}
		for _, d := range span.Digests {
			retiringDigests[d] = r.read
		}
	}
	if len(retiring) == 0 {
		return
	}
	followed := map[[8]byte]bool{}
	followedDigests := map[string]bool{}
	for id, span := range l.spans {
		if sid, err := trace.SpanIDFromHex(id); err == nil {
			followed[sid] = true
		}
		for _, d := range span.Digests {
			followedDigests[d] = true
		}
	}
	for key := range l.owned {
		if followed[key.span] || (key.digest != "" && followedDigests[key.digest]) {
			continue
		}
		// The installing span is the one a service's restart reactivates:
		// its exec span links it.
		if read, ok := retiring[key.span]; ok {
			l.owned[key] = read
		} else if read, ok := retiringDigests[key.digest]; ok && key.digest != "" {
			l.owned[key] = read
		}
	}
}

// reactivate reads the reactivated spans, while the set has room for them,
// from where they retired up to the follow's cursor, then moves them back
// into the set. Like a catch-up, a gap read imports only the pairs it owns:
// the ones its span passed to it on retiring, and the work started since
// that no other read imported -- the follow read what the spans it
// followed meanwhile select. A read that fails resumes from the last batch
// it imported the next time.
func (l *liveLogs) reactivate(ctx context.Context) error {
	var errs error
	for {
		l.mu.Lock()
		if len(l.spans) >= l.maxSpans {
			l.mu.Unlock()
			return errs
		}
		var id string
		var r retiredLogSpan
		for id, r = range l.reactivated {
			break
		}
		if id == "" {
			l.mu.Unlock()
			return errs
		}
		delete(l.reactivated, id)
		cursor := l.cursor
		l.mu.Unlock()
		var err error
		if r.cursor != cursor {
			err = l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{
				Spans:   []archive.LogSpan{r.span},
				Cursor:  r.cursor,
				Through: cursor,
			}, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, next string) error {
				if err := l.importLogs(ctx, req, r.read); err != nil {
					return err
				}
				r.cursor = next
				return nil
			})
		}
		l.mu.Lock()
		if err != nil {
			l.reactivated[id] = r
		} else {
			l.spans[id] = r.span
			l.through[id] = r.through
		}
		l.mu.Unlock()
		if err != nil {
			return errors.Join(errs, err)
		}
	}
}

// catchUp reads the pending spans' rows up to the follow's cursor, then
// moves them into the set, as many as it has room for. Without a cursor
// there is nothing to catch up: the next read starts from the beginning.
func (l *liveLogs) catchUp(ctx context.Context) error {
	l.mu.Lock()
	batch := map[string]archive.LogSpan{}
	for id, span := range l.pending {
		if len(l.spans)+len(batch) >= l.maxSpans {
			break
		}
		batch[id] = span
		delete(l.pending, id)
	}
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
	if len(targets) == 0 {
		// Most batches of a read scan only rows of other spans, e.g. a
		// catch-up reading the store from its start: asking the frontend
		// about them, and exporting nothing, would cost its event loop a
		// round trip each.
		return nil
	}
	var coverage []idtui.LogCoverage
	if l.coverage != nil {
		coverage = l.coverage(targets)
	}
	l.mu.Lock()
	i, kept := 0, 0
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
			kept += len(sl.LogRecords)
		}
	}
	l.mu.Unlock()
	if kept == 0 {
		return nil
	}
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

// finish stops following and reads everything followed up to the store's
// end: rows the follow had not reached yet, the spans the frontend
// requested for its final render (idtui.LiveLogsFrontend.RequestFinalLogs),
// and retired spans seen active again. Retired spans were read to the end
// of their work already. What the set has no room for is read on its own,
// like catch-ups. Nothing is read after it.
func (l *liveLogs) finish(ctx context.Context) error {
	l.stop()
	l.mu.Lock()
	if l.finished {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	if l.activity != nil {
		// Work that ran since the last poll may have rows for retired
		// spans.
		l.pollActive()
	}
	err := l.reactivate(ctx)
	err = errors.Join(err, l.catchUp(ctx))
	l.mu.Lock()
	l.finished = true
	spans := slices.Collect(maps.Values(l.spans))
	cursor := l.cursor
	reactivated := slices.Collect(maps.Values(l.reactivated))
	pending := slices.Collect(maps.Values(l.pending))
	l.mu.Unlock()
	if len(spans) > 0 {
		err = errors.Join(err, l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{Spans: spans, Cursor: cursor}, l.importFollowed))
	}
	for _, r := range reactivated {
		err = errors.Join(err, l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{
			Spans:  []archive.LogSpan{r.span},
			Cursor: r.cursor,
		}, func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, _ string) error {
			return l.importLogs(ctx, req, r.read)
		}))
	}
	for chunk := range slices.Chunk(pending, l.maxSpans) {
		l.mu.Lock()
		l.catchUps++
		read := l.catchUps
		l.mu.Unlock()
		err = errors.Join(err, l.reader.ReadLogs(ctx, l.traceID, tracesource.LogQuery{Spans: chunk},
			func(ctx context.Context, req *collogspb.ExportLogsServiceRequest, _ string) error {
				return l.importLogs(ctx, req, read)
			}))
	}
	return err
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

// settleLiveLogs waits until the session's telemetry for the work done so
// far has reached the frontend: exactly, with TelemetryBarrier, or else
// until it goes quiet.
func settleLiveLogs(ctx context.Context, sess liveLogsSession) {
	barrierCtx, cancel := context.WithTimeout(ctx, liveLogsFinishTimeout)
	err := sess.TelemetryBarrier(barrierCtx)
	cancel()
	if err == nil {
		return
	}
	if !errors.Is(err, client.ErrTelemetryBarrierUnsupported) {
		slog.Warn("error waiting for the session's telemetry", "err", err)
	}
	settleCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	sess.WaitTelemetryIdle(settleCtx, liveLogsSettle)
	cancel()
}

// finishLiveLogs reads what the final render shows: the logs the frontend
// requests for it, and the primary output. It must run before the session
// closes, which ends its store's reads.
func finishLiveLogs(ctx context.Context, sess liveLogsSession, live *liveLogs) {
	lf := live.frontend
	if lf == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	settleLiveLogs(ctx, sess)
	// Stop following first, so finish catches up the final requests itself,
	// rather than a follow restart it would cut short.
	live.stop()
	lf.RequestFinalLogs()
	finishCtx, cancel := context.WithTimeout(ctx, liveLogsFinishTimeout)
	if err := live.finish(finishCtx); err != nil {
		slog.Warn("error reading the final logs", "err", err)
	}
	cancel()
	primaryCtx, cancel := context.WithTimeout(ctx, liveLogsPrimaryTimeout)
	defer cancel()
	records, err := readPrimaryOutput(primaryCtx, live.reader, live.traceID, lf.PrimarySpan())
	if err != nil {
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
