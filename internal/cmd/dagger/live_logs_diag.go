package daggercmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
)

// Live log diagnostics: warnings about reads that went wrong without
// failing, for a session's trace to explain them.
//
//   - "live logs: replay": a (span, digest) pair a read imported unclaimed
//     (idtui.LogUnknown) was claimed by another read over the same rows (a
//     catch-up or finish's read of a pending span, from the store's start;
//     a reactivated span's gap read, over what was imported since it
//     retired), which imports its records again: they show twice.
//   - "live logs: unclaimed replay": likewise, but the other read imported
//     them unclaimed again, their span still unplaced.
//   - "live logs: long-lived roll-up": a span followed with its descendants
//     for over liveLogsLongRollUp, with the work that keeps it active, as a
//     roll-up widens every follow query while it is followed.
//
// The lines are Warn records of an internal span of their own, "live logs
// diagnostics", started with the first line, so a clean session records
// nothing. The session's trace keeps them (the CLI's telemetry goes to the
// engine's store, and Cloud; finish's own come after the command's telemetry
// was handed over, so withEngineAction hands them over again), but no
// frontend request selects that span, so the follow never reads them back,
// and they are not the primary span's output, printed at exit.
const (
	// liveLogsDiagRate bounds the lines per minute; the rest are counted,
	// and the count reported with the next minute's first line, or at
	// finish.
	liveLogsDiagRate = 20
	// liveLogsUnclaimedMax bounds the pairs remembered as imported
	// unclaimed, oldest first out.
	liveLogsUnclaimedMax = 4096
	// liveLogsLongRollUp is how long a span may be followed with its
	// descendants before what keeps it active is reported.
	liveLogsLongRollUp = 10 * time.Minute
	// liveLogsDiagSpans bounds the span IDs a line lists.
	liveLogsDiagSpans = 10
)

// liveLogsDiag emits diagnostics, rate-limited, without ever blocking.
type liveLogsDiag struct {
	// log emits a line, under mu; it must not block.
	log func(msg string, args ...any)
	now func() time.Time
	// span is the diagnostics span, once started: its own records are not
	// diagnosed.
	span atomic.Pointer[trace.SpanID]

	mu             sync.Mutex
	window         time.Time
	lines, dropped int
	// logged is whether any line was logged.
	logged bool
	closed bool
	// close flushes the lines emitted, once closed.
	close func()
}

func newLiveLogsDiag(log func(string, ...any)) *liveLogsDiag {
	return &liveLogsDiag{log: log, now: time.Now}
}

// startLiveLogsDiag logs diagnostics as Warn records of their own span,
// beneath ctx's, started with the first line. Lines are handed to a
// goroutine, so emitting never waits on telemetry; past its buffer, they are
// dropped.
func startLiveLogsDiag(ctx context.Context) *liveLogsDiag {
	type line struct {
		msg  string
		args []any
	}
	lines := make(chan line, 256)
	done := make(chan struct{})
	var started bool
	d := newLiveLogsDiag(nil)
	d.log = func(msg string, args ...any) {
		if !started {
			started = true
			go func() {
				defer close(done)
				ctx, span := Tracer().Start(ctx, "live logs diagnostics", telemetry.Internal(), telemetry.Encapsulate())
				span.End()
				id := span.SpanContext().SpanID()
				d.span.Store(&id)
				logger := slog.SpanLogger(ctx, InstrumentationLibrary)
				for l := range lines {
					logger.Warn(l.msg, l.args...)
				}
			}()
		}
		select {
		case lines <- line{msg, args}:
		default:
		}
	}
	d.close = func() {
		close(lines)
		if started {
			<-done
		}
	}
	return d
}

// emit logs a line, unless the minute's budget is spent.
func (d *liveLogsDiag) emit(msg string, args ...any) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if now := d.now(); now.Sub(d.window) >= time.Minute {
		if d.dropped > 0 {
			args = append(args, "dropped", d.dropped)
		}
		d.window, d.lines, d.dropped = now, 0, 0
	}
	if d.lines >= liveLogsDiagRate {
		d.dropped++
		return
	}
	d.lines++
	d.logged = true
	d.log("live logs: "+msg, args...)
}

// finish flushes the lines emitted so far, after one counting those the
// rate limit dropped since the last, and emits no more.
func (d *liveLogsDiag) finish() {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	if d.dropped > 0 {
		d.log("live logs: dropped", "dropped", d.dropped)
	}
	d.closed = true
	d.mu.Unlock()
	if d.close != nil {
		d.close()
	}
}

// wrote reports whether any line was logged.
func (d *liveLogsDiag) wrote() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.logged
}

// own reports whether span is the diagnostics span.
func (d *liveLogsDiag) own(span [8]byte) bool {
	id := d.span.Load()
	return id != nil && *id == trace.SpanID(span)
}

// liveLogsUnclaimed is a pair imported unclaimed: the records imported, and
// the last read, and batch (liveLogs.imports), that imported them.
type liveLogsUnclaimed struct {
	records  int
	read     uint64
	imported uint64
}

// replays reports whether a read that imports again what was imported after
// batch since (see liveLogs.importRead) imports u's records again. A gap
// read (since > 0) reads again only what the follow imported: a catch-up's
// rows are behind its cursor.
func (u liveLogsUnclaimed) replays(read, since uint64) bool {
	return u.imported > since && u.read != read && (since == 0 || u.read == 0)
}

// liveLogsReplay is a remembered pair a read imported again: records of it,
// claimed or, still unplaced, unclaimed again.
type liveLogsReplay struct {
	unclaimed liveLogsUnclaimed
	records   int
	claimed   bool
}

// rememberUnclaimedLocked remembers the pairs a read imported unclaimed;
// those it imported again are replays. It runs with mu held.
func (l *liveLogs) rememberUnclaimedLocked(read, since uint64, unclaimed map[logOwner]int, replays *map[logOwner]*liveLogsReplay) {
	for key, n := range unclaimed {
		u, ok := l.unclaimed[key]
		switch {
		case !ok:
			if len(l.unclaimedOrder) >= liveLogsUnclaimedMax {
				delete(l.unclaimed, l.unclaimedOrder[0])
				l.unclaimedOrder = l.unclaimedOrder[1:]
			}
			l.unclaimedOrder = append(l.unclaimedOrder, key)
		case u.replays(read, since):
			if *replays == nil {
				*replays = map[logOwner]*liveLogsReplay{}
			}
			(*replays)[key] = &liveLogsReplay{unclaimed: u, records: n}
		}
		u.records += n
		u.read, u.imported = read, l.imports
		l.unclaimed[key] = u
	}
}

// claimUnclaimedLocked notes that a read claimed a record of key: a replay
// when key is remembered, imported unclaimed by another read where this one
// reads again. It runs with mu held, and only while some pair is remembered.
func (l *liveLogs) claimUnclaimedLocked(key logOwner, read, since uint64, replays *map[logOwner]*liveLogsReplay) {
	if r, ok := (*replays)[key]; ok {
		r.records++
		return
	}
	u, ok := l.unclaimed[key]
	if !ok {
		return
	}
	l.forgetUnclaimedLocked(func(k logOwner) bool { return k == key })
	if !u.replays(read, since) {
		return
	}
	if *replays == nil {
		*replays = map[logOwner]*liveLogsReplay{}
	}
	(*replays)[key] = &liveLogsReplay{unclaimed: u, records: 1, claimed: true}
}

// forgetUnclaimedLocked forgets the remembered pairs forget picks. It runs
// with mu held.
func (l *liveLogs) forgetUnclaimedLocked(forget func(logOwner) bool) {
	l.unclaimedOrder = slices.DeleteFunc(l.unclaimedOrder, func(key logOwner) bool {
		if forget(key) {
			delete(l.unclaimed, key)
			return true
		}
		return false
	})
}

// reportReplays reports the replays of a read. rollUps are the spans
// followed with their descendants meanwhile, and rollUpCount how many.
func (l *liveLogs) reportReplays(read uint64, replays map[logOwner]*liveLogsReplay, rollUps string, rollUpCount int) {
	for key, r := range replays {
		span := trace.SpanID(key.span).String()
		if r.claimed {
			l.diag.emit("replay", "span", span, "digest", key.digest,
				"unclaimedRecords", r.unclaimed.records, "unclaimedRead", r.unclaimed.read,
				"claimedRecords", r.records, "claimedRead", read,
				"rollUps", rollUps, "rollUpCount", rollUpCount)
		} else {
			l.diag.emit("unclaimed replay", "span", span, "digest", key.digest,
				"unclaimedRecords", r.unclaimed.records, "unclaimedRead", r.unclaimed.read,
				"records", r.records, "read", read,
				"rollUps", rollUps, "rollUpCount", rollUpCount)
		}
	}
}

// rollUpsLocked lists the spans followed with their descendants, sorted, up
// to liveLogsDiagSpans of them, and counts them all. It runs with mu held.
func (l *liveLogs) rollUpsLocked() (string, int) {
	var ids []string
	for id, span := range l.spans {
		if span.Descendants {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return strings.Join(ids[:min(len(ids), liveLogsDiagSpans)], ","), len(ids)
}

// liveLogsRollUp is since when a span is followed with its descendants, and
// whether that was reported as long-lived.
type liveLogsRollUp struct {
	since    time.Time
	reported bool
}

// reportLongRollUps reports, once each, the spans followed with their
// descendants for over liveLogsLongRollUp, and the work that keeps them
// active. It runs with each poll, so it tells how long a span is followed to
// within a poll; it must not run with mu held, as the frontend answers on
// its event loop.
func (l *liveLogs) reportLongRollUps() {
	if l.diag == nil || l.running == nil {
		return
	}
	now := l.now()
	var ids []dagui.SpanID
	var followed []time.Duration
	l.mu.Lock()
	for id := range l.rollUps {
		if !l.spans[id].Descendants {
			delete(l.rollUps, id)
		}
	}
	for id, span := range l.spans {
		if !span.Descendants {
			continue
		}
		r, ok := l.rollUps[id]
		switch {
		case !ok:
			l.rollUps[id] = liveLogsRollUp{since: now}
		case !r.reported && now.Sub(r.since) >= liveLogsLongRollUp:
			r.reported = true
			l.rollUps[id] = r
			var sid dagui.SpanID
			sid.SpanID, _ = trace.SpanIDFromHex(id)
			ids = append(ids, sid)
			followed = append(followed, now.Sub(r.since))
		}
	}
	l.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	running := l.running(ids)
	// Past what running returns, the frontend quit: nothing tells.
	for i, id := range ids[:min(len(ids), len(running))] {
		r := running[i]
		var spans []string
		var agent bool
		for _, s := range r.Running {
			var kind string
			switch {
			case s.Effect:
				kind = " [effect]"
			case s.Agent:
				kind, agent = " [agent]", true
			case s.InAgent:
				kind, agent = " [in agent]", true
			}
			spans = append(spans, fmt.Sprintf("%s %q%s", s.ID, s.Name, kind))
		}
		l.diag.emit("long-lived roll-up", "span", id.String(),
			"followedFor", followed[i].Round(time.Second).String(), "self", r.Self,
			"running", r.Total, "agent", agent, "effects", r.Effects,
			"runningSpans", strings.Join(spans, "; "))
	}
}
