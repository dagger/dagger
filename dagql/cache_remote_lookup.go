package dagql

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencontainers/go-digest"
)

// A remote lookup asks the remote cache service for the Cloud's value of a
// call the cache missed. The miss only queues the call: the lookup that
// missed never waits, and the call runs as it would without the service.
// The integration takes the queued misses in batches; the service's answer
// sends what it has as merges and offers, which later calls can use.

// Accepted starting values for remote lookups.
const (
	// remoteLookupQueueCapacity bounds the misses waiting to be taken;
	// past it a miss is dropped and counted.
	remoteLookupQueueCapacity = 4096
	// remoteLookupAskedCapacity bounds the recipes a connection remembers
	// asking for; past it the memory starts afresh.
	remoteLookupAskedCapacity = 1 << 16
	// remoteLookupMaxExtras bounds the extra digests a miss names.
	remoteLookupMaxExtras = 8
	// remoteLookupLogCapacity bounds the misses a connection keeps for
	// LateRemoteMisses. A full log drops the misses LateRemoteMisses has
	// returned, and then its oldest quarter: a merge that lands now is
	// late for recent misses far more than for old ones.
	remoteLookupLogCapacity = 1 << 14
)

// What a miss did about a remote lookup, as its evidence records it
// (CacheDecision.MissRemoteLookup).
const (
	// RemoteLookupQueued: the miss queued a lookup.
	RemoteLookupQueued = "queued"
	// RemoteLookupAsked: the call's recipe was already asked for on the
	// connection.
	RemoteLookupAsked = "asked"
	// RemoteLookupDropped: the lookup queue was full.
	RemoteLookupDropped = "dropped"
)

// RemoteMiss is a call the cache missed, as a remote lookup names it: its
// recipe digest, then the extra digests the cache's own lookup tries, then
// its term, the self digest and input digests its structural lookup tries.
type RemoteMiss struct {
	Recipe digest.Digest
	Extra  []digest.Digest
	Field  string
	Self   digest.Digest
	Inputs []digest.Digest
	// Caller is the recipe digest of the cached call the missed call was
	// made under (CallerRecipe), or "" at its session's top.
	Caller digest.Digest
	// At is when the lookup missed.
	At time.Time
}

// remoteLookups is one bridge's lookup state. Its mutex is a leaf: it is
// never held while taking another lock.
type remoteLookups struct {
	// on is set while a connection serves lookups; a miss reads it without
	// the lock.
	on atomic.Bool

	mu     sync.Mutex
	closed bool
	// asked are the recipes queued on this connection, so a call is asked
	// for once.
	asked   map[digest.Digest]struct{}
	queue   []RemoteMiss
	dropped uint64
	// missed are the misses queued on this connection, kept for
	// LateRemoteMisses in the order they were queued, each numbered by
	// missedSeq; generation counts the connections, so that a mark is never
	// made on a later connection's misses.
	missed     []loggedMiss
	missedSeq  uint64
	generation uint64
	// trimmed counts the misses a full log dropped before any call returned
	// them, over every connection.
	trimmed uint64
	// notify holds a signal when misses were queued since the last take.
	notify chan struct{}
	// done closes when the bridge detaches.
	done chan struct{}
}

func (l *remoteLookups) init() {
	l.notify = make(chan struct{}, 1)
	l.done = make(chan struct{})
}

// StartRemoteLookups starts queuing the cache's misses for a connection that
// serves lookups, with nothing asked yet.
func (b *RemoteCacheBridge) StartRemoteLookups() {
	l := &b.lookups
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.asked = map[digest.Digest]struct{}{}
	l.queue, l.dropped = nil, 0
	l.missed = nil
	l.generation++
	l.on.Store(true)
}

// StopRemoteLookups stops queuing misses and drops the queued ones.
func (b *RemoteCacheBridge) StopRemoteLookups() {
	l := &b.lookups
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on.Store(false)
	l.asked, l.queue, l.dropped = nil, nil, 0
	l.missed = nil
	l.generation++
}

// noteRemoteMiss queues a miss, unless its recipe was asked for already on
// this connection. A full queue drops it, and counts it. It returns what it
// did, or "" when no connection serves lookups.
func (b *RemoteCacheBridge) noteRemoteMiss(miss RemoteMiss) string {
	l := &b.lookups
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || !l.on.Load() {
		return ""
	}
	if _, ok := l.asked[miss.Recipe]; ok {
		return RemoteLookupAsked
	}
	if len(l.queue) >= remoteLookupQueueCapacity {
		l.dropped++
		return RemoteLookupDropped
	}
	if len(l.asked) >= remoteLookupAskedCapacity {
		clear(l.asked)
	}
	l.asked[miss.Recipe] = struct{}{}
	l.queue = append(l.queue, miss)
	if len(l.missed) >= remoteLookupLogCapacity {
		l.trimMissedLocked()
	}
	l.missedSeq++
	l.missed = append(l.missed, loggedMiss{miss: miss, seq: l.missedSeq})
	select {
	case l.notify <- struct{}{}:
	default:
	}
	return RemoteLookupQueued
}

// TakeRemoteMisses waits for queued misses and takes at most limit of them.
// Once one is queued, it waits up to linger for limit to be. dropped counts the
// misses the full queue refused since the last take. Only the integration's
// consumer takes.
func (b *RemoteCacheBridge) TakeRemoteMisses(ctx context.Context, limit int, linger time.Duration) (misses []RemoteMiss, dropped uint64, err error) {
	l := &b.lookups
	// A limit below 1 takes one miss.
	if limit < 1 {
		limit = 1
	}
	// until is when the lingering for the queued misses ends; zero while
	// none is queued.
	var until time.Time
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, 0, ErrRemoteCacheBridgeClosed
		}
		n := len(l.queue)
		switch {
		case n == 0:
			until = time.Time{}
		case until.IsZero():
			until = time.Now().Add(linger)
		}
		if n >= limit || (n > 0 && !time.Now().Before(until)) {
			misses = slices.Clone(l.queue[:min(limit, n)])
			l.queue = slices.Delete(l.queue, 0, len(misses))
			dropped, l.dropped = l.dropped, 0
			l.mu.Unlock()
			return misses, dropped, nil
		}
		l.mu.Unlock()
		stop := func() bool { return false }
		var lingered <-chan time.Time
		if n > 0 {
			timer := time.NewTimer(time.Until(until))
			lingered = timer.C
			stop = timer.Stop
		}
		select {
		case <-l.notify:
		case <-lingered:
		case <-l.done:
			err = ErrRemoteCacheBridgeClosed
		case <-ctx.Done():
			err = context.Cause(ctx)
		}
		stop()
		if err != nil {
			return nil, 0, err
		}
	}
}

// close refuses later use, and wakes the consumer.
func (l *remoteLookups) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	l.on.Store(false)
	l.asked, l.queue = nil, nil
	close(l.done)
}

// noteRemoteMiss queues a call's lookup miss for the remote cache service,
// when a connection serves lookups and telemetry reports the call
// (CacheReported): the service learns calls only from what telemetry
// reports, so it can't know any other. It returns what it did
// (RemoteLookup*), or "". self and inputs are the term the lookup tried. It
// runs after the lookup, with no lock held.
func (c *Cache) noteRemoteMiss(req *CallRequest, recipe, caller, self digest.Digest, inputs []digest.Digest) string {
	b := c.partContentSource.attachedBridge()
	if b == nil || !b.lookups.on.Load() || recipe == "" || req.ListItem || !req.CacheReported {
		return ""
	}
	miss := RemoteMiss{Recipe: recipe, Field: req.ResultCall.Field, Self: self, Inputs: inputs, Caller: caller, At: time.Now()}
	for _, extra := range req.ResultCall.ExtraDigests {
		if len(miss.Extra) == remoteLookupMaxExtras {
			break
		}
		if extra.Digest != "" {
			miss.Extra = append(miss.Extra, extra.Digest)
		}
	}
	return b.noteRemoteMiss(miss)
}

// loggedMiss is a miss kept for LateRemoteMisses: its number on the
// connection, and whether a call has returned it.
type loggedMiss struct {
	miss  RemoteMiss
	seq   uint64
	found bool
}

// trimMissedLocked makes room in a full miss log: it drops the misses
// already returned, then, while the log is still over three quarters full,
// its oldest quarter.
func (l *remoteLookups) trimMissedLocked() {
	kept := slices.DeleteFunc(l.missed, func(m loggedMiss) bool { return m.found })
	if len(kept) > remoteLookupLogCapacity*3/4 {
		kept = slices.Delete(kept, 0, remoteLookupLogCapacity/4)
		l.trimmed += remoteLookupLogCapacity / 4
	}
	l.missed = kept
}

// LateRemoteMisses returns the misses queued on the current connection
// before before, that no earlier call returned, whose lookup now finds one
// of entries, by the cache's entry numbers (missFinds). It is how a merge
// that landed late is counted: the entries are the ones it landed on, and a
// miss it returns is a call that missed before the merge would have served
// it. A miss whose recipe the connection had already asked for, or that a
// full queue dropped, isn't kept, and a full log drops the oldest
// (remoteLookupLogCapacity), so misses older than the log are never
// counted. Of the misses kept it is an upper bound: an entry found by digest
// or term is counted without the session checks a real lookup makes, so a
// result the missed session couldn't have used, such as one needing a
// secret it lacks, still counts.
func (c *Cache) LateRemoteMisses(before time.Time, entries map[uint64]struct{}) []RemoteMiss {
	b := c.partContentSource.attachedBridge()
	if b == nil || len(entries) == 0 {
		return nil
	}
	l := &b.lookups
	l.mu.Lock()
	generation := l.generation
	var candidates []loggedMiss
	for _, m := range l.missed {
		if !m.found && m.miss.At.Before(before) {
			candidates = append(candidates, m)
		}
	}
	l.mu.Unlock()
	var late []loggedMiss
	for _, m := range candidates {
		if c.missFinds(m.miss, entries) {
			late = append(late, m)
		}
	}
	if c.testBetweenLateMissHolds != nil {
		c.testBetweenLateMissHolds()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.generation != generation {
		return nil
	}
	// The log may have been trimmed meanwhile, and a call made meanwhile
	// may have returned some of them: each is found by its number.
	var returned []RemoteMiss
	for _, m := range late {
		i, ok := slices.BinarySearchFunc(l.missed, m.seq, func(e loggedMiss, seq uint64) int { return cmp.Compare(e.seq, seq) })
		if ok && !l.missed[i].found {
			l.missed[i].found = true
			returned = append(returned, m.miss)
		}
	}
	return returned
}

// TrimmedRemoteMisses returns how many misses a full log dropped before
// LateRemoteMisses could count them, over the bridge's connections: the
// misses the late count never saw.
func (c *Cache) TrimmedRemoteMisses() uint64 {
	b := c.partContentSource.attachedBridge()
	if b == nil {
		return 0
	}
	b.lookups.mu.Lock()
	defer b.lookups.mu.Unlock()
	return b.lookups.trimmed
}

// missFinds reports whether a miss's lookup now finds one of entries: by
// the class of its recipe or of one of its extra digests, or by its term,
// as the service resolves a lookup.
func (c *Cache) missFinds(miss RemoteMiss, entries map[uint64]struct{}) bool {
	found := func(numbers []uint64) bool {
		return slices.ContainsFunc(numbers, func(n uint64) bool {
			_, ok := entries[n]
			return ok
		})
	}
	if found(c.EquivalentEntries(miss.Recipe.String())) {
		return true
	}
	for _, extra := range miss.Extra {
		if found(c.EquivalentEntries(extra.String())) {
			return true
		}
	}
	return found(c.TermEntries(miss.Self, miss.Inputs))
}
