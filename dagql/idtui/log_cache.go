package idtui

import (
	"slices"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

// Logs read on demand are held in memory only while they are shown, plus a
// few recently shown: the frontend bounds what it holds, and reads a span
// again from the start when it shows it again.
//
// A span is shown while a view of its logs is mounted (LogsView), while it
// is zoomed, focused or open in the pager, and for a grace period after a
// render site last asked for it. Spans that are not shown are kept in a
// small LRU; past it, their requests are evicted (unsubscribed) and their
// terminals dropped.
//
// A roll-up's terminal holds prefixed copies of its descendants' records,
// written as each is imported, so a descendant's terminal and its roll-up's
// cannot be rebuilt apart without duplicating lines. Spans are therefore
// evicted and dropped in groups: a span belongs to the group of its topmost
// ancestor (or itself) that rolls up its logs, or whose request reads its
// descendants. A group is held while any of its requested spans is.
const (
	defaultLogCacheSize  = 32
	defaultLogCacheGrace = 2 * time.Second
	logSweepInterval     = 500 * time.Millisecond
)

// LogCacheFrontend is the optional interface of a frontend that reads logs
// on demand (SetLogProvider, SetTraceLogProvider, SetLiveLogs) and lets them
// go once it stops showing them, and that searches the source of the logs it
// does not hold.
type LogCacheFrontend interface {
	// SetLogRelease registers the callback that lets go of the logs read
	// for a trace's spans (a zero traceID: SetLogProvider's).
	SetLogRelease(traceID dagui.TraceID, release func(LogRelease))
	// SetLogSearch registers the search of a trace's display text (a zero
	// traceID: SetLogProvider's), for the logs not loaded. Without one, a
	// search covers only what is loaded.
	SetLogSearch(traceID dagui.TraceID, search LogSearchFunc)
}

var _ LogCacheFrontend = (*frontendPretty)(nil)

// LogRelease is a frontend letting go of logs it read on demand (see
// LogCacheFrontend.SetLogRelease).
type LogRelease struct {
	// Spans are no longer requested: stop following them.
	Spans []dagui.SpanID
	// Dropped are the spans whose display text the frontend dropped: forget
	// what was imported for them, so that a later request reads them again
	// from the start, and import nothing more for them that a read already
	// in flight returns.
	Dropped []dagui.SpanID
}

// logCache is the frontend's bookkeeping for bounding on-demand logs.
type logCache struct {
	size  int
	grace time.Duration
	now   func() time.Time
	// shown is when each requested span was last shown.
	shown map[dagui.SpanID]time.Time
	// mounts counts the mounted views of each span's logs.
	mounts map[dagui.SpanID]int
	// rollUps holds the requested spans whose request reads descendants.
	rollUps map[dagui.SpanID]bool
	// releases are keyed like the log providers: by trace, or the zero
	// trace for the provider of every trace.
	releases    map[dagui.TraceID]func(LogRelease)
	sweepQueued bool
}

func (c *logCache) init() {
	if c.shown != nil {
		return
	}
	c.size = defaultLogCacheSize
	c.grace = defaultLogCacheGrace
	c.now = time.Now
	c.shown = map[dagui.SpanID]time.Time{}
	c.mounts = map[dagui.SpanID]int{}
	c.rollUps = map[dagui.SpanID]bool{}
	c.releases = map[dagui.TraceID]func(LogRelease){}
}

// SetLogRelease registers the callback that lets go of the logs of a
// trace's spans (or, with a zero traceID, the spans SetLogProvider's
// provider reads), for the provider that reads them: logs of spans whose
// provider has none are never dropped. It is called on the event loop, so
// it must not block.
func (fe *frontendPretty) SetLogRelease(traceID dagui.TraceID, release func(LogRelease)) {
	fe.dispatch(func() {
		fe.logCache.init()
		fe.logCache.releases[traceID] = release
	})
}

// logReleaseKey is the key of the release for a span's provider (see
// logProviderFor), and whether there is one.
func (fe *frontendPretty) logReleaseKey(id dagui.SpanID) (dagui.TraceID, bool) {
	if fe.logCache.releases == nil {
		return dagui.TraceID{}, false
	}
	if span, ok := fe.db.Spans.Map[id]; ok && span.Received {
		if fe.traceLogProviders[span.TraceID] != nil {
			_, ok := fe.logCache.releases[span.TraceID]
			return span.TraceID, ok
		}
	} else {
		return dagui.TraceID{}, false
	}
	if fe.logProvider == nil {
		return dagui.TraceID{}, false
	}
	_, ok := fe.logCache.releases[dagui.TraceID{}]
	return dagui.TraceID{}, ok
}

// touchLogs records that a span's logs were shown just now.
func (fe *frontendPretty) touchLogs(id dagui.SpanID) {
	if !id.IsValid() || !fe.hasLogProvider() {
		return
	}
	fe.logCache.init()
	fe.logCache.shown[id] = fe.logCache.now()
}

func (fe *frontendPretty) mountLogs(id dagui.SpanID) {
	fe.logCache.init()
	fe.logCache.mounts[id]++
	fe.touchLogs(id)
}

func (fe *frontendPretty) dismountLogs(id dagui.SpanID) {
	fe.logCache.init()
	if fe.logCache.mounts[id]--; fe.logCache.mounts[id] <= 0 {
		delete(fe.logCache.mounts, id)
	}
	fe.touchLogs(id)
	fe.scheduleLogSweep()
}

// logsPinned reports whether a span is shown, so its logs are never dropped.
func (fe *frontendPretty) logsPinned(id dagui.SpanID, now time.Time) bool {
	c := &fe.logCache
	if c.mounts[id] > 0 || id == fe.ZoomedSpan || id == fe.FocusedSpan || id == fe.primarySpan() {
		return true
	}
	if fe.logPager != nil && fe.logPager.SpanID == id {
		return true
	}
	shown, ok := c.shown[id]
	return ok && now.Sub(shown) < c.grace
}

// scheduleLogSweep sweeps the logs soon, once per interval at most.
func (fe *frontendPretty) scheduleLogSweep() {
	c := &fe.logCache
	if len(c.releases) == 0 || c.sweepQueued || fe.reportOnly || fe.tui == nil {
		return
	}
	c.sweepQueued = true
	time.AfterFunc(logSweepInterval, func() {
		fe.dispatch(func() {
			c.sweepQueued = false
			fe.sweepLogs()
		})
	})
}

// logGroupOf is the group span id belongs to (see the top of this file).
func (fe *frontendPretty) logGroupOf(id dagui.SpanID, memo map[dagui.SpanID]dagui.SpanID) dagui.SpanID {
	if g, ok := memo[id]; ok {
		return g
	}
	group := id
	for cur := id; cur.IsValid(); {
		span, ok := fe.db.Spans.Map[cur]
		if !ok {
			break
		}
		if fe.logGroupRoot(cur) {
			group = cur
		}
		cur = span.ParentID
	}
	memo[id] = group
	return group
}

// logGroupRoot reports whether a span's group takes in its descendants.
func (fe *frontendPretty) logGroupRoot(id dagui.SpanID) bool {
	if fe.logCache.rollUps[id] {
		return true
	}
	span, ok := fe.db.Spans.Map[id]
	return ok && span.RollUpLogs
}

// sweepLogs evicts the requests of the groups past the LRU, and drops the
// terminals of every group it does not hold.
func (fe *frontendPretty) sweepLogs() {
	c := &fe.logCache
	if len(c.releases) == 0 || fe.reportOnly || fe.finalRender || fe.finalLogs {
		return
	}
	now := c.now()
	type logGroup struct {
		root     dagui.SpanID
		requests []dagui.SpanID
		last     time.Time
		pinned   bool
		held     bool
	}
	groups := map[dagui.SpanID]*logGroup{}
	memo := map[dagui.SpanID]dagui.SpanID{}
	groupFor := func(id dagui.SpanID) *logGroup {
		root := fe.logGroupOf(id, memo)
		g := groups[root]
		if g == nil {
			g = &logGroup{root: root}
			groups[root] = g
		}
		return g
	}
	inGrace := false
	for id := range fe.requestedLogs {
		if _, ok := fe.logReleaseKey(id); !ok {
			continue
		}
		g := groupFor(id)
		g.requests = append(g.requests, id)
		if shown := c.shown[id]; shown.After(g.last) {
			g.last = shown
		}
		if fe.logsPinned(id, now) {
			g.pinned = true
			if c.mounts[id] == 0 && now.Sub(c.shown[id]) < c.grace {
				inGrace = true
			}
		}
	}
	// A shown span holds its group, requested or not (e.g. a focused roll-up
	// holding its descendants' copies).
	terms := map[dagui.SpanID]bool{}
	for _, m := range [][]dagui.SpanID{keysOf(fe.logs.Logs), keysOf(fe.logs.ToolArgs), keysOf(fe.logs.PrefixWriters)} {
		for _, id := range m {
			if _, ok := fe.logReleaseKey(id); ok {
				// Read on demand, and placed.
				terms[id] = true
			}
		}
	}
	for id := range terms {
		if fe.logsPinned(id, now) {
			groupFor(id).pinned = true
		}
	}
	var lru []*logGroup
	for _, g := range groups {
		if g.pinned {
			g.held = true
		} else {
			lru = append(lru, g)
		}
	}
	slices.SortFunc(lru, func(a, b *logGroup) int { return b.last.Compare(a.last) })
	for i, g := range lru {
		g.held = i < c.size
	}
	if inGrace && len(lru) >= c.size {
		// Groups shown a moment ago may be evicted once their grace ends.
		c.sweepQueued = true
		time.AfterFunc(c.grace, func() {
			fe.dispatch(func() {
				c.sweepQueued = false
				fe.sweepLogs()
			})
		})
	}

	releases := map[dagui.TraceID]*LogRelease{}
	releaseFor := func(id dagui.SpanID) *LogRelease {
		key, _ := fe.logReleaseKey(id)
		r := releases[key]
		if r == nil {
			r = &LogRelease{}
			releases[key] = r
		}
		return r
	}
	for _, g := range lru {
		if g.held {
			continue
		}
		for _, id := range g.requests {
			r := releaseFor(id)
			r.Spans = append(r.Spans, id)
			delete(fe.requestedLogs, id)
			delete(fe.liveRequests, id)
			delete(c.rollUps, id)
			delete(c.shown, id)
		}
	}

	// Drop the terminals no held group covers.
	drop := map[dagui.SpanID]bool{}
	for id := range terms {
		if g := groups[fe.logGroupOf(id, memo)]; g == nil || !g.held {
			drop[id] = true
		}
	}
	for id := range drop {
		r := releaseFor(id)
		r.Dropped = append(r.Dropped, id)
		delete(fe.logs.Logs, id)
		delete(fe.logs.ToolArgs, id)
		delete(fe.logs.PrefixWriters, id)
		delete(fe.logs.SawEOF, id)
		if c.mounts[id] == 0 {
			delete(fe.logsViews, logsViewKey{spanID: id})
			delete(fe.logsViews, logsViewKey{spanID: id, toolArgs: true})
		}
	}
	// What is not shown is not repainted: a render site that shows it again
	// asks for it again.
	for key, r := range releases {
		if len(r.Spans) > 0 || len(r.Dropped) > 0 {
			c.releases[key](*r)
		}
	}
}

func keysOf[V any](m map[dagui.SpanID]V) []dagui.SpanID {
	keys := make([]dagui.SpanID, 0, len(m))
	for id := range m {
		keys = append(keys, id)
	}
	return keys
}
