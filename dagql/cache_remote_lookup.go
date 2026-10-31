package dagql

import (
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
// recipe digest, then the extra digests the cache's own lookup tries.
type RemoteMiss struct {
	Recipe digest.Digest
	Extra  []digest.Digest
	Field  string
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
	l.on.Store(true)
}

// StopRemoteLookups stops queuing misses and drops the queued ones.
func (b *RemoteCacheBridge) StopRemoteLookups() {
	l := &b.lookups
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on.Store(false)
	l.asked, l.queue, l.dropped = nil, nil, 0
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
// when a connection serves lookups, and returns what it did (RemoteLookup*),
// or "". It runs after the lookup, with no lock held.
func (c *Cache) noteRemoteMiss(req *CallRequest, recipe digest.Digest) string {
	b := c.partContentSource.attachedBridge()
	if b == nil || !b.lookups.on.Load() || recipe == "" || req.ListItem {
		return ""
	}
	miss := RemoteMiss{Recipe: recipe, Field: req.ResultCall.Field}
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
