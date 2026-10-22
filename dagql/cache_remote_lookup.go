package dagql

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/opencontainers/go-digest"
)

// A remote lookup asks the remote cache service for the Cloud's value of a
// call the cache missed. The miss only queues the call: the lookup that
// missed never waits, and the call runs as it would without the service.
// The integration takes the queued misses in batches. When the service
// answers that a merge carries a call's value, the integration expects that
// merge: an evaluation of the call's entry waits for it, bounded, so that
// it can install the merged value's parts instead of running.

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

// How a merge an evaluation waits for ended (RemoteMergeWait.Outcome).
const (
	// RemoteMergeMerged: the merge ended on the engine, its parts admitted
	// as offers.
	RemoteMergeMerged = "merged"
	// RemoteMergeDeadline: the merge didn't arrive within the wait's bound.
	RemoteMergeDeadline = "deadline"
	// RemoteMergeDisconnected: the connection that would carry it ended.
	RemoteMergeDisconnected = "disconnected"
)

// RemoteMergeWait is a merge evaluations can wait for. Done closes when it
// ends, and Outcome then says how.
type RemoteMergeWait struct {
	once    sync.Once
	done    chan struct{}
	outcome string
}

func NewRemoteMergeWait() *RemoteMergeWait {
	return &RemoteMergeWait{done: make(chan struct{})}
}

// End ends the wait with outcome. Only the first End counts.
func (w *RemoteMergeWait) End(outcome string) {
	w.once.Do(func() {
		w.outcome = outcome
		close(w.done)
	})
}

func (w *RemoteMergeWait) Done() <-chan struct{} { return w.done }

// Outcome is how the wait ended, once Done has closed.
func (w *RemoteMergeWait) Outcome() string { return w.outcome }

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
	// expecting counts expectations, so an evaluation reads no lock when
	// there are none.
	expecting atomic.Int64

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
	done     chan struct{}
	expected map[digest.Digest]remoteMergeExpectation
}

// remoteMergeExpectation is a merge an evaluation of a recipe's entry waits
// for: until it ends, or until deadline.
type remoteMergeExpectation struct {
	wait     *RemoteMergeWait
	deadline time.Time
}

func (l *remoteLookups) init() {
	l.notify = make(chan struct{}, 1)
	l.done = make(chan struct{})
	l.expected = map[digest.Digest]remoteMergeExpectation{}
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
// Expectations stay until their merges end or their deadlines pass.
func (b *RemoteCacheBridge) StopRemoteLookups() {
	l := &b.lookups
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on.Store(false)
	l.asked, l.queue, l.dropped = nil, nil, 0
}

// noteRemoteMiss queues a miss, unless its recipe was asked for already on
// this connection. A full queue drops it, and counts it.
func (b *RemoteCacheBridge) noteRemoteMiss(miss RemoteMiss) {
	l := &b.lookups
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || !l.on.Load() {
		return
	}
	if _, ok := l.asked[miss.Recipe]; ok {
		return
	}
	if len(l.queue) >= remoteLookupQueueCapacity {
		l.dropped++
		return
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

// ExpectRemoteMerge makes an evaluation of recipe's entry wait until wait
// ends, or until deadline: a merge carrying the recipe's value is on its
// way, and its parts arrive as offers the evaluation can install. The
// expectation retires when wait ends, at deadline, or when the bridge
// detaches, whether or not an evaluation waited for it.
func (b *RemoteCacheBridge) ExpectRemoteMerge(recipe digest.Digest, wait *RemoteMergeWait, deadline time.Time) {
	l := &b.lookups
	exp := remoteMergeExpectation{wait: wait, deadline: deadline}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.expected[recipe] = exp
	l.expecting.Store(int64(len(l.expected)))
	l.mu.Unlock()
	go func() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-wait.Done():
		case <-timer.C:
		case <-l.done:
		}
		l.forget(recipe, exp)
	}()
}

// expectation returns the live expectation of the first of recipes that
// has one.
func (l *remoteLookups) expectation(recipes []digest.Digest, now time.Time) (digest.Digest, remoteMergeExpectation, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, recipe := range recipes {
		if exp, ok := l.expected[recipe]; ok && now.Before(exp.deadline) {
			return recipe, exp, true
		}
	}
	return "", remoteMergeExpectation{}, false
}

// forget drops recipe's expectation, if it is still exp.
func (l *remoteLookups) forget(recipe digest.Digest, exp remoteMergeExpectation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.expected[recipe]; ok && cur == exp {
		delete(l.expected, recipe)
		l.expecting.Store(int64(len(l.expected)))
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
	clear(l.expected)
	l.expecting.Store(0)
	close(l.done)
}

// noteRemoteMiss queues a call's lookup miss for the remote cache service,
// when a connection serves lookups. It runs after the lookup, with no lock
// held.
func (c *Cache) noteRemoteMiss(req *CallRequest, recipe digest.Digest) {
	b := c.partContentSource.attachedBridge()
	if b == nil || !b.lookups.on.Load() || recipe == "" || req.ListItem {
		return
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
	b.noteRemoteMiss(miss)
}

// awaitRemoteMerge waits, before an evaluation, for a merge expected to
// carry the entry's value, until it ends, its deadline passes, the bridge
// detaches or ctx ends. An entry whose evaluation is complete doesn't wait.
// Only the expectation's own end retires it: another evaluation of the entry
// still waits after this one gives up.
func (c *Cache) awaitRemoteMerge(ctx context.Context, shared *sharedResult) {
	b := c.partContentSource.attachedBridge()
	if b == nil || b.lookups.expecting.Load() == 0 {
		return
	}
	shared.lazyMu.Lock()
	complete := shared.lazyEvalComplete
	shared.lazyMu.Unlock()
	if complete {
		return
	}
	c.egraphMu.RLock()
	recipes := slices.Clone(shared.recipeKeys)
	c.egraphMu.RUnlock()
	start := time.Now()
	recipe, exp, ok := b.lookups.expectation(recipes, start)
	if !ok {
		return
	}
	timer := time.NewTimer(exp.deadline.Sub(start))
	defer timer.Stop()
	var outcome string
	select {
	case <-exp.wait.Done():
		outcome = exp.wait.Outcome()
	case <-timer.C:
		outcome = RemoteMergeDeadline
	case <-b.lookups.done:
		outcome = "detached"
	case <-ctx.Done():
		outcome = "canceled"
	}
	slog.InfoContext(ctx, "remote cache lookup wait",
		"entry", uint64(shared.id),
		"recipe", recipe.String(),
		"waited", time.Since(start),
		"outcome", outcome)
}
