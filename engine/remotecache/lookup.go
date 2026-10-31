package remotecache

import (
	"context"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// lookups sends the cache's misses to the service, with at most
// LookupsInFlight lookups unanswered at once, until the session ends.
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
			s.lookup(ctx, misses, dropped)
		})
	}
}

// lookup sends one lookup and logs its answer. The service sends what it
// found as ordinary merges. A lookup the service answers with an error has
// failed, as one it doesn't answer has.
func (s *session) lookup(ctx context.Context, misses []dagql.RemoteMiss, dropped uint64) {
	req := protocol.Lookup{
		Misses: make([]protocol.LookupMiss, len(misses)),
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
		if context.Cause(ctx) == nil {
			s.cfg.Logger.Warn("remote cache lookup failed", "lookup", id, "misses", len(misses), "error", err)
		}
		return
	}
	answered := time.Now()
	var answer protocol.LookedUp
	if err := env.Decode(&answer); err != nil {
		s.cfg.Logger.Warn("remote cache lookup: undecodable answer", "lookup", id, "error", err)
		return
	}
	if len(answer.Misses) != len(misses) {
		s.cfg.Logger.Warn("remote cache lookup: answer doesn't match the misses", "lookup", id, "misses", len(misses), "answered", len(answer.Misses))
		return
	}
	counts := map[protocol.LookupStatus]int{}
	for _, got := range answer.Misses {
		counts[got.Status]++
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
		"unknown", counts[protocol.LookupUnknown])
}
