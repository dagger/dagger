package remotecache

import (
	"context"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// lookups sends the cache's misses to the service, with at most
// LookupsInFlight lookups unanswered at once, until the session ends or the
// service refuses a lookup: a service that doesn't know lookups answers with
// an error, and is not asked again on this connection.
func (s *session) lookups() {
	if !s.cfg.Lookups {
		return
	}
	ctx, stop := context.WithCancel(s.ctx)
	defer stop()
	s.adapter.StartLookups()
	defer s.adapter.StopLookups()
	for {
		select {
		case s.lookupSlots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		misses, dropped, err := s.adapter.TakeLookupMisses(ctx, protocol.LookupMisses, lookupLinger)
		if err != nil {
			<-s.lookupSlots
			return
		}
		s.go_(func() {
			defer func() { <-s.lookupSlots }()
			if s.lookup(ctx, misses, dropped) {
				stop()
			}
		})
	}
}

// lookup sends one lookup and expects the merges its answer names, for the
// misses whose evaluations should wait for them. It reports whether the
// service refused the lookup.
func (s *session) lookup(ctx context.Context, misses []dagql.RemoteMiss, dropped uint64) (refused bool) {
	req := protocol.Lookup{
		Misses:     make([]protocol.LookupMiss, len(misses)),
		WaitMillis: s.cfg.LookupWait.Milliseconds(),
	}
	for i, miss := range misses {
		req.Misses[i] = protocol.LookupMiss{Recipe: miss.Recipe, Extra: miss.Extra, Field: miss.Field}
	}
	id := s.lastID.Add(1)
	sent := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	env, err := s.requestAs(reqCtx, id, protocol.TypeLookup, req)
	if err != nil {
		if env.Type == protocol.TypeError {
			s.cfg.Logger.Warn("remote cache lookups refused; not sending more on this connection", "lookup", id, "error", err)
			return true
		}
		if context.Cause(ctx) == nil {
			s.cfg.Logger.Warn("remote cache lookup failed", "lookup", id, "misses", len(misses), "error", err)
		}
		return false
	}
	answered := time.Now()
	var answer protocol.LookedUp
	if err := env.Decode(&answer); err != nil {
		s.cfg.Logger.Warn("remote cache lookup: undecodable answer", "lookup", id, "error", err)
		return false
	}
	if len(answer.Misses) != len(misses) {
		s.cfg.Logger.Warn("remote cache lookup: answer doesn't match the misses", "lookup", id, "misses", len(misses), "answered", len(answer.Misses))
		return false
	}
	counts := map[protocol.LookupStatus]int{}
	waits := 0
	for i, got := range answer.Misses {
		counts[got.Status]++
		if !s.waitsFor(got) {
			continue
		}
		deadline := answered.Add(s.cfg.LookupWait)
		s.adapter.ExpectMerge(misses[i].Recipe, s.merges.await(got.Merge, deadline), deadline)
		waits++
	}
	s.cfg.Logger.Info("remote cache lookup",
		"lookup", id,
		"misses", len(misses),
		"dropped", dropped,
		"rtt", answered.Sub(sent),
		"merging", counts[protocol.LookupMerging],
		"in_flight", counts[protocol.LookupInFlight],
		"deferred", counts[protocol.LookupDeferred],
		"not_stored", counts[protocol.LookupNotStored],
		"unknown", counts[protocol.LookupUnknown],
		"waits", waits)
	return false
}

// waitsFor reports whether an evaluation of a missed call waits for the
// merge its answer names: a merge that carries the value with some of its
// parts' bytes, when the engine waits at all.
func (s *session) waitsFor(got protocol.LookedUpMiss) bool {
	if s.cfg.LookupWait <= 0 || got.Merge == 0 || got.Parts == protocol.LookupPartsNone {
		return false
	}
	return got.Status == protocol.LookupMerging || got.Status == protocol.LookupInFlight
}

// mergeTracker follows the service's merges by request ID, for the lookups
// whose answers name them. IDs can arrive out of order, so each is tracked
// exactly: a merge being handled, or named before it arrived, has a wait
// that ends when it does; one that ended is remembered among the last
// endedMerges.
type mergeTracker struct {
	mu     sync.Mutex
	closed bool
	// waits are the waits of merges being handled, and of merges named by
	// an answer and not yet received; running marks the former.
	waits   map[uint64]*dagql.RemoteMergeWait
	running map[uint64]struct{}
	// ended remembers the last endedMerges merges that ended, in a ring.
	ended     map[uint64]struct{}
	endedRing []uint64
	next      int
}

func newMergeTracker() *mergeTracker {
	return &mergeTracker{
		waits:     map[uint64]*dagql.RemoteMergeWait{},
		running:   map[uint64]struct{}{},
		ended:     map[uint64]struct{}{},
		endedRing: make([]uint64, endedMerges),
	}
}

// start records a merge's arrival.
func (m *mergeTracker) start(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if m.waits[id] == nil {
		m.waits[id] = dagql.NewRemoteMergeWait()
	}
	m.running[id] = struct{}{}
}

// end records a merge's end, and releases what waits for it.
func (m *mergeTracker) end(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if w := m.waits[id]; w != nil {
		w.End(dagql.RemoteMergeMerged)
	}
	delete(m.waits, id)
	delete(m.running, id)
	if old := m.endedRing[m.next]; old != 0 {
		delete(m.ended, old)
	}
	m.endedRing[m.next] = id
	m.next = (m.next + 1) % len(m.endedRing)
	m.ended[id] = struct{}{}
}

// await returns a wait that ends when the merge does. For a merge not
// received yet, it also ends at deadline, when nothing waits for it any more.
func (m *mergeTracker) await(id uint64, deadline time.Time) *dagql.RemoteMergeWait {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.waits[id]
	switch {
	case m.closed:
		w = dagql.NewRemoteMergeWait()
		w.End(dagql.RemoteMergeDisconnected)
	case w != nil:
	default:
		w = dagql.NewRemoteMergeWait()
		if _, ok := m.ended[id]; ok {
			w.End(dagql.RemoteMergeMerged)
			return w
		}
		m.waits[id] = w
		time.AfterFunc(time.Until(deadline), func() { m.forget(id, w) })
	}
	return w
}

// forget drops a merge named and never received.
func (m *mergeTracker) forget(id uint64, w *dagql.RemoteMergeWait) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.waits[id] != w {
		return
	}
	if _, ok := m.running[id]; ok {
		return
	}
	w.End(dagql.RemoteMergeDeadline)
	delete(m.waits, id)
}

// close releases everything that waits, when the session ends.
func (m *mergeTracker) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, w := range m.waits {
		w.End(dagql.RemoteMergeDisconnected)
	}
	m.waits, m.running = nil, nil
}
