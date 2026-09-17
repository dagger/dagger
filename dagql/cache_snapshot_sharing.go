package dagql

import (
	"context"
	"errors"
	"fmt"
)

// Early snapshot sharing gives an imported row R its own ownership of a
// B-local equivalent's completed snapshot, before any demand asks for it.
//
// Admission is off until the engine calls EnableSnapshotSharing, which it
// does after restored ownership is ready. The queue, its cohorts and the one
// cache-owned worker live here; the triggers that feed it are hooks in the
// e-graph, import and publication paths.
//
// Locks: E is Cache.egraphMu. Every field of the queue is guarded by E. The
// worker takes no lock while probing, preparing or committing beyond the
// ordinary part transaction's own E -> G -> P order, and it never holds E
// while waiting.

type snapshotShareAdmission uint8

const (
	// snapshotShareOff is the default. Notifications cost one flag check.
	snapshotShareOff snapshotShareAdmission = iota
	snapshotShareOn
	snapshotShareClosed
)

// ErrSnapshotSharingClosed is returned by EnableSnapshotSharing after close.
// Reopening admission would let a fresh item outlive the drain.
var ErrSnapshotSharingClosed = errors.New("snapshot sharing is closed")

// snapshotShareNotifications collects the classes changed by one E-held
// interval. It owns no row and never escapes that interval: the outer
// mutation flushes it into held work items before its own E unlock, after
// publication or rollback is decided.
type snapshotShareNotifications struct {
	classes map[eqClassID]struct{}
}

func (n *snapshotShareNotifications) record(class eqClassID) {
	if n == nil || class == 0 {
		return
	}
	if n.classes == nil {
		n.classes = map[eqClassID]struct{}{}
	}
	n.classes[class] = struct{}{}
}

// snapshotShareItem is one queued or active cohort: a canonical output class,
// one incoming hold per distinct registered member observed for it, and the
// counted cache operations that keep the cache open while it is outstanding.
type snapshotShareItem struct {
	class   eqClassID
	members map[sharedResultID]*sharedResult
	ops     []cacheOperation
}

// EnableSnapshotSharing admits early sharing on a live cache. The engine
// calls it after restored ownership is ready and old transfer pins are
// released, and only on an engine that can receive imports. It is idempotent
// and is refused once close has run.
func (c *Cache) EnableSnapshotSharing() error {
	if c == nil {
		return fmt.Errorf("enable snapshot sharing: nil cache")
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	switch c.shareAdmission {
	case snapshotShareClosed:
		return ErrSnapshotSharingClosed
	case snapshotShareOn:
		return nil
	}
	c.shareAdmission = snapshotShareOn
	return nil
}

// SnapshotSharingEnabled reports whether admission is on.
func (c *Cache) SnapshotSharingEnabled() bool {
	if c == nil {
		return false
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.shareAdmission == snapshotShareOn
}

// beginShareNotificationsLocked opens this E interval's collector. A nested
// caller reuses the outer collector and does not flush it; only the interval
// that opened it flushes, before its own unlock.
func (c *Cache) beginShareNotificationsLocked() (*snapshotShareNotifications, bool) {
	if c == nil || c.shareAdmission != snapshotShareOn {
		return nil, false
	}
	if c.shareNotify != nil {
		return c.shareNotify, false
	}
	n := &snapshotShareNotifications{}
	c.shareNotify = n
	return n, true
}

// flushShareNotificationsLocked ends the interval opened by the owner and
// turns its recorded classes into held work items. queueSnapshotShareLocked
// enumerates each class's current registered members, so a class whose change
// was rolled back contributes only its survivors.
func (c *Cache) flushShareNotificationsLocked(ctx context.Context, n *snapshotShareNotifications, owner bool) {
	if !owner || n == nil {
		return
	}
	c.shareNotify = nil
	for class := range n.classes {
		c.queueSnapshotShareLocked(ctx, class)
	}
}

// discardShareNotificationsLocked ends the interval without queueing. It is
// for an exit that mutated nothing, such as the changed-frame retry in
// TeachContentDigest, which must not carry a collector across its unlock.
func (c *Cache) discardShareNotificationsLocked(n *snapshotShareNotifications, owner bool) {
	if !owner || n == nil {
		return
	}
	c.shareNotify = nil
}

// recordShareUnionLocked is called at the end of an actual class union, after
// the membership and reverse indexes have moved to the winning root. It also
// recanonicalizes the pending queue: two pending items whose classes merged
// become one item with the union of their members and both counted
// operations, which the taking worker retires down to one outside E.
func (c *Cache) recordShareUnionLocked(winner, loser eqClassID) {
	if c == nil {
		return
	}
	if loser != winner {
		if item := c.sharePending[loser]; item != nil {
			delete(c.sharePending, loser)
			c.shareQueue = removeShareClass(c.shareQueue, loser)
			item.class = winner
			if existing := c.sharePending[winner]; existing != nil {
				for id, row := range item.members {
					if _, held := existing.members[id]; held {
						// One hold per distinct member: drop the duplicate
						// through the ordinary release path.
						c.queueShareDuplicateHoldLocked(id, row)
						continue
					}
					existing.members[id] = row
				}
				existing.ops = append(existing.ops, item.ops...)
			} else {
				c.sharePending[winner] = item
				c.shareQueue = append(c.shareQueue, winner)
			}
		}
	}
	c.shareNotify.record(winner)
}

// queueShareDuplicateHoldLocked drops one duplicated member hold produced by
// coalescing two pending items. The decrement happens under E; any resulting
// collection runs through the ordinary unlocked release path taken by the
// next flush, so this only records the decrement.
func (c *Cache) queueShareDuplicateHoldLocked(id sharedResultID, row *sharedResult) {
	if row == nil || c.resultsByID[id] != row {
		return
	}
	c.shareDuplicateHolds = append(c.shareDuplicateHolds, row)
}

// recordShareMembershipLocked is called when a (result, canonical class) pair
// is newly inserted, including insertion into an existing class with no
// union at all.
func (c *Cache) recordShareMembershipLocked(class eqClassID) {
	if c == nil {
		return
	}
	c.shareNotify.record(class)
}

// snapshotShareMembersLocked returns the class's currently registered rows.
func (c *Cache) snapshotShareMembersLocked(class eqClassID) map[sharedResultID]*sharedResult {
	ids := c.outputEqClassResults[class]
	if len(ids) == 0 {
		return nil
	}
	members := make(map[sharedResultID]*sharedResult, len(ids))
	for id := range ids {
		row := c.resultsByID[id]
		if row == nil {
			continue
		}
		members[id] = row
	}
	return members
}

// queueSnapshotShareLocked records one class of interest. It inspects only
// graph facts, takes ownership and never enters D, a core guard, schema or
// storage. E is held.
func (c *Cache) queueSnapshotShareLocked(ctx context.Context, class eqClassID) {
	if c == nil || c.shareAdmission != snapshotShareOn {
		return
	}
	class = c.findEqClassLocked(class)
	if class == 0 {
		return
	}
	members := c.snapshotShareMembersLocked(class)
	if !shareClassHasReceiver(members) {
		// No imported member can receive, so this class cannot produce work.
		// Pending-part determination belongs to the unlocked probe, so a
		// conservatively queued completed imported row may still yield an
		// empty pass.
		return
	}
	item := c.sharePending[class]
	if item == nil {
		op, err := c.beginCacheOperation()
		if err != nil {
			// Closing won the race: enqueue nothing.
			return
		}
		item = &snapshotShareItem{class: class, members: map[sharedResultID]*sharedResult{}, ops: []cacheOperation{op}}
		if c.sharePending == nil {
			c.sharePending = map[eqClassID]*snapshotShareItem{}
		}
		c.sharePending[class] = item
		c.shareQueue = append(c.shareQueue, class)
	}
	for id, row := range members {
		if _, held := item.members[id]; held {
			continue
		}
		c.incrementIncomingOwnershipLocked(ctx, row)
		item.members[id] = row
	}
	c.ensureSnapshotShareWorkerLocked()
	c.wakeSnapshotShareWorker()
}

// queueSnapshotShareRowLocked queues every current output class of one row.
// The completion hooks use it: another imported member may lack a different
// part that this row's class already has, so the row's classes are notified
// rather than a list of newly completed part names.
func (c *Cache) queueSnapshotShareRowLocked(ctx context.Context, row *sharedResult) {
	if c == nil || row == nil || c.shareAdmission != snapshotShareOn {
		return
	}
	if c.resultsByID[row.id] != row {
		// Collection won before this notification took E. Do not resurrect.
		return
	}
	for class := range c.outputEqClassesForResultLocked(row.id) {
		c.queueSnapshotShareLocked(ctx, class)
	}
}

// notifySnapshotShareCompletion is the completion-side trigger. It runs
// outside every lazy lock and after the waiter wake, revalidates the row's
// registration under E and queues its current classes.
func (c *Cache) notifySnapshotShareCompletion(ctx context.Context, row *sharedResult) {
	if c == nil || row == nil {
		return
	}
	if c.shareAdmissionState() != snapshotShareOn {
		return
	}
	c.egraphMu.Lock()
	c.queueSnapshotShareRowLocked(ctx, row)
	duplicates := c.takeShareDuplicateHoldsLocked()
	c.egraphMu.Unlock()
	c.releaseShareDuplicateHolds(ctx, duplicates)
}

func (c *Cache) shareAdmissionState() snapshotShareAdmission {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.shareAdmission
}

func (c *Cache) takeShareDuplicateHoldsLocked() []*sharedResult {
	if len(c.shareDuplicateHolds) == 0 {
		return nil
	}
	rows := c.shareDuplicateHolds
	c.shareDuplicateHolds = nil
	return rows
}

func (c *Cache) releaseShareDuplicateHolds(ctx context.Context, rows []*sharedResult) {
	for _, row := range rows {
		if err := c.releasePartRow(context.WithoutCancel(ctx), row); err != nil {
			c.recordShareCleanupError(err)
		}
	}
}

func removeShareClass(queue []eqClassID, class eqClassID) []eqClassID {
	out := queue[:0]
	for _, id := range queue {
		if id != class {
			out = append(out, id)
		}
	}
	return out
}

// shareClassHasReceiver filters on graph-visible origin facts alone: no task
// is instantiated and no native body is probed on this path.
func shareClassHasReceiver(members map[sharedResultID]*sharedResult) bool {
	for _, row := range members {
		if row.imported {
			return true
		}
	}
	return false
}

func (c *Cache) ensureSnapshotShareWorkerLocked() {
	if c.shareWorkerStarted || c.shareAdmission != snapshotShareOn {
		return
	}
	c.shareWorkerStarted = true
	c.shareWake = make(chan struct{}, 1)
	ctx, cancel := context.WithCancelCause(c.snapshotShareWorkerBase())
	c.shareWorkerCancel = cancel
	c.shareWorkerDone = make(chan struct{})
	go c.runSnapshotShareWorker(ctx)
}

func (c *Cache) wakeSnapshotShareWorker() {
	if c.shareWake == nil {
		return
	}
	// A nonblocking wake signal, never a bounded send that could block a
	// lookup holding E.
	select {
	case c.shareWake <- struct{}{}:
	default:
	}
}

// snapshotShareWorkerBase is the engine/cache lifetime context for the
// worker: no client metadata, requesting session, user deadline or
// request-scoped operation lease. The per-slot recorded call and the
// preparation marker are added inside the pass.
func (c *Cache) snapshotShareWorkerBase() context.Context {
	return ContextWithCache(context.Background(), c)
}

func (c *Cache) takeSnapshotShareItemLocked() *snapshotShareItem {
	for len(c.shareQueue) > 0 {
		class := c.shareQueue[0]
		c.shareQueue = c.shareQueue[1:]
		item := c.sharePending[class]
		if item == nil {
			continue
		}
		delete(c.sharePending, class)
		return item
	}
	return nil
}

func (c *Cache) runSnapshotShareWorker(ctx context.Context) {
	defer close(c.shareWorkerDone)
	for {
		c.egraphMu.Lock()
		item := c.takeSnapshotShareItemLocked()
		duplicates := c.takeShareDuplicateHoldsLocked()
		c.egraphMu.Unlock()
		c.releaseShareDuplicateHolds(ctx, duplicates)
		if item == nil {
			select {
			case <-ctx.Done():
				c.dropSnapshotShareQueue(context.WithoutCancel(ctx))
				return
			case <-c.shareWake:
				continue
			}
		}
		// Taking an item freezes its cohort; a later notification creates or
		// extends a separate pending successor with its own holds.
		c.retireExtraShareOperations(item)
		if ctx.Err() == nil {
			c.runSnapshotSharePass(ctx, item)
		}
		c.releaseSnapshotShareItem(ctx, item)
	}
}

// retireExtraShareOperations ends the surplus counted operations a coalesced
// item accumulated, outside E, keeping one for the pass.
func (c *Cache) retireExtraShareOperations(item *snapshotShareItem) {
	for len(item.ops) > 1 {
		last := len(item.ops) - 1
		item.ops[last].finish(false)
		item.ops = item.ops[:last]
	}
}

// releaseSnapshotShareItem drops every cohort hold under E, runs the
// resulting collection callbacks unlocked with an uncanceled cleanup context,
// and ends the item's counted operation last.
func (c *Cache) releaseSnapshotShareItem(ctx context.Context, item *snapshotShareItem) {
	cleanupCtx := context.WithoutCancel(ctx)
	c.egraphMu.Lock()
	var queue collectionQueue
	var err error
	for _, row := range item.members {
		q, decErr := c.decrementIncomingOwnershipLocked(cleanupCtx, row, queue)
		queue = q
		err = errors.Join(err, decErr)
	}
	item.members = nil
	callbacks, collectErr := c.collectUnownedResultsLocked(cleanupCtx, queue)
	duplicates := c.takeShareDuplicateHoldsLocked()
	c.egraphMu.Unlock()
	err = errors.Join(err, collectErr, runOnReleaseFuncs(cleanupCtx, callbacks))
	c.releaseShareDuplicateHolds(cleanupCtx, duplicates)
	if err != nil {
		c.recordShareCleanupError(err)
	}
	for i := range item.ops {
		item.ops[i].finish(false)
	}
	item.ops = nil
}

// dropSnapshotShareQueue releases every still-queued item after admission
// closed. Dropping a queued item ends its counted operation, so the cache's
// quiescence wait cannot be held open by work that will never run.
func (c *Cache) dropSnapshotShareQueue(ctx context.Context) {
	for {
		c.egraphMu.Lock()
		item := c.takeSnapshotShareItemLocked()
		c.egraphMu.Unlock()
		if item == nil {
			return
		}
		c.releaseSnapshotShareItem(ctx, item)
	}
}

// closeSnapshotSharing closes admission and stops the worker. It runs
// immediately after the remote-cache bridge detach and before the cache waits
// for quiescence, so an enqueue either finished its E section before this
// step, and is dropped here, or fails admission afterwards. Its errors are
// joined into the caller's close error and never assigned over a seeded
// shutdown cause.
func (c *Cache) closeSnapshotSharing(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.egraphMu.Lock()
	c.shareAdmission = snapshotShareClosed
	cancel := c.shareWorkerCancel
	started := c.shareWorkerStarted
	c.egraphMu.Unlock()
	if !started {
		// No worker ever ran, so nothing can be queued; there is nothing to
		// drain and no goroutine to wake.
		return nil
	}
	cancel(ErrSnapshotSharingClosed)
	c.wakeSnapshotShareWorker()
	return nil
}

func (c *Cache) recordShareCleanupError(err error) {
	if err == nil {
		return
	}
	c.recordReleaseCleanupError("", true, fmt.Errorf("snapshot sharing: %w", err))
}

// runSnapshotSharePass probes the frozen cohort and installs what it can.
func (c *Cache) runSnapshotSharePass(ctx context.Context, item *snapshotShareItem) {
	_ = ctx
	_ = item
}
