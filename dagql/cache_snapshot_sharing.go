package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
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

// shareSlotNeedsServices reports whether installing this slot reconstructs
// persisted Service objects, which is the only part of a typed preparation
// that needs the engine's registered root and default-dependency factory.
func (slot *shareSlot) shareSlotNeedsServices() bool {
	if !slot.receiver.version.payload.hasValue {
		// An encoded receiver keeps the exact service IDs without decoding
		// them, so it needs no schema at all.
		return false
	}
	return slot.probe.Descriptor.Value != nil && len(slot.probe.Descriptor.Value.Services) > 0
}

func (slot *shareSlot) shareServiceRoots() []uint64 {
	if slot.probe.Descriptor.Value == nil {
		return nil
	}
	roots := make([]uint64, 0, len(slot.probe.Descriptor.Value.Services))
	for _, svc := range slot.probe.Descriptor.Value.Services {
		roots = append(roots, svc.ServiceResultID)
	}
	return roots
}

// shareSlotContext builds one slot's preparation context outside every lock:
// the marked worker base, the receiver's exact recorded call, and, only for a
// typed preparation that must reconstruct services, the engine's registered
// context and its schema-only server bound through DagQL's own server helper
// so a decoder cannot fall back to the root's client-dependent server.
//
// Root, factory and native decoder availability are all checked before the
// first exact reference load.
func (c *Cache) shareSlotContext(ctx context.Context, slot *shareSlot) (context.Context, error) {
	if call := slot.receiver.record.Call; call != nil {
		ctx = ContextWithCall(ctx, call)
	}
	if !slot.shareSlotNeedsServices() {
		return ctx, nil
	}
	prepare := c.partPreparationContext()
	if prepare == nil {
		return nil, fmt.Errorf("%w: no registered preparation context for a typed service receiver", ErrSnapshotShareIneligible)
	}
	prepared, srv, err := prepare(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: preparation context: %w", ErrSnapshotShareIneligible, err)
	}
	if srv == nil {
		return nil, fmt.Errorf("%w: preparation context supplied no server", ErrSnapshotShareIneligible)
	}
	if err := c.preflightShareDecode(prepared, slot.shareServiceRoots()); err != nil {
		return nil, err
	}
	return srvToContext(prepared, srv), nil
}

// runSnapshotSharePass probes the frozen cohort and installs what it can.
func (c *Cache) runSnapshotSharePass(ctx context.Context, item *snapshotShareItem) {
	for _, slot := range c.selectShareSlots(ctx, item) {
		slotCtx, err := c.shareSlotContext(ctx, slot)
		if err != nil {
			c.traceShareSkip(ctx, slot.receiver.row, slot.address, err)
			continue
		}
		_ = slotCtx
	}
}

// PartPreparationContext builds the engine-root context and schema-only
// server a typed sharing preparation needs to decode persisted services. The
// cache never constructs one itself: a cache with no registered callback
// treats a typed receiver that needs service construction as ineligible, and
// leaves service-free and encoded installs available.
type PartPreparationContext func(context.Context) (context.Context, *Server, error)

// ErrSnapshotShareIneligible marks a slot the pass will not attempt. It is a
// skip, not a failure: it changes no row state and no lookup eligibility.
var ErrSnapshotShareIneligible = errors.New("snapshot share ineligible")

// SetPartPreparationContext registers the engine's preparation callback. It
// is set once before live sharing admission; replacing it after admission is
// an initialization error, and NewCache defaults it to nil.
func (c *Cache) SetPartPreparationContext(prepare PartPreparationContext) error {
	if c == nil {
		return fmt.Errorf("set part preparation context: nil cache")
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if c.shareAdmission == snapshotShareOn && c.partPreparation != nil {
		return fmt.Errorf("set part preparation context: already registered and admitted")
	}
	c.partPreparation = prepare
	return nil
}

func (c *Cache) partPreparationContext() PartPreparationContext {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.partPreparation
}

// clearPartPreparationContext drops the registered callback after the cache
// has drained. A decoded value never retains it.
func (c *Cache) clearPartPreparationContext() {
	if c == nil {
		return
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.partPreparation = nil
}

// shareRowProbe is one member's unlocked observation: its copied record, the
// revision that observation belongs to, and its declared parts by full
// address. No graph lock is held while it is taken.
type shareRowProbe struct {
	row     *sharedResult
	record  PersistedRecord
	version capturedRowRevision
	probes  map[string]PartProbe
	order   []string
	usable  bool
}

// shareSlot is one selected (receiver, full address) installation with its
// chosen donor. The pass owns it; it holds no lease or permit until its Body
// admits one.
type shareSlot struct {
	receiver   *shareRowProbe
	donor      *shareRowProbe
	address    PersistedPartAddress
	addressKey string
	probe      PartProbe
}

// shareMemberOrder is the deterministic member order used for both receiver
// visits and donor ties: ascending registered result ID.
func shareMemberOrder(members map[sharedResultID]*sharedResult) []*sharedResult {
	rows := make([]*sharedResult, 0, len(members))
	for _, row := range members {
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b *sharedResult) int { return int(a.id) - int(b.id) })
	return rows
}

// probeShareMembers observes every cohort member without graph locks. An
// unreadable member is skipped, not an error: its state is simply not stable
// enough to share from or into during this pass.
func (c *Cache) probeShareMembers(ctx context.Context, rows []*sharedResult) map[sharedResultID]*shareRowProbe {
	out := make(map[sharedResultID]*shareRowProbe, len(rows))
	for _, row := range rows {
		probe := &shareRowProbe{row: row, probes: map[string]PartProbe{}}
		out[row.id] = probe
		if row.attachmentState() != resultAttachmentClean {
			// An unfinished or failed publication is skipped. A queue hold
			// taken by the early publication flush can retain such a row for
			// exactly one pass; releasing the cohort ends that retention.
			continue
		}
		record, version, probes, err := c.probeAllParts(ctx, row)
		if err != nil {
			// A busy or not-yet-ready row is an ordinary skip; its cause
			// belongs to diagnostics, not to a row-sticky sharing error.
			c.traceShareSkip(ctx, row, PersistedPartAddress{}, err)
			continue
		}
		probe.record, probe.version = record, version
		for _, p := range probes {
			key, err := partAddressKey(p.Descriptor.Address)
			if err != nil {
				continue
			}
			if _, seen := probe.probes[key]; seen {
				continue
			}
			probe.probes[key] = p
			probe.order = append(probe.order, key)
		}
		slices.Sort(probe.order)
		probe.usable = true
	}
	return out
}

// shareReceiverEligible reports the graph-visible facts a receiver needs. The
// pending-part decision belongs to the unlocked probe, not here.
func (c *Cache) shareReceiverEligibleLocked(row *sharedResult, now int64) bool {
	return row != nil && row.imported && c.resultsByID[row.id] == row && !partRowExpired(row, now)
}

// shareDonorReady is the unlocked half of the Ready donor rule: a stable
// complete descriptor for that address, real bytes behind it, no overlapping
// writer and no unfinished ownership bookkeeping for that output. The applied
// role naming the same SnapshotID is proved under E by the sessionless
// constructor.
func shareDonorReady(p PartProbe) bool {
	return p.LocalComplete && !p.Busy && !p.Descriptor.Absent &&
		p.Descriptor.SnapshotID != "" && p.Descriptor.Value != nil
}

// shareReceiverPending selects an eligible missing address. Legal absence is
// already final, an installed or complete part is never overwritten, and a
// part with no snapshot descriptor on the donor side is not a
// snapshot-sharing target at all.
func shareReceiverPending(p PartProbe) bool {
	return !p.LocalComplete && !p.Busy && !p.Descriptor.Absent
}

// selectShareSlots picks one Ready donor for each eligible missing address,
// in deterministic member and address order. Probing an unready earlier
// member does not prevent examining a later Ready member.
func (c *Cache) selectShareSlots(ctx context.Context, item *snapshotShareItem) []*shareSlot {
	rows := shareMemberOrder(item.members)
	now := time.Now().Unix()
	c.egraphMu.RLock()
	eligible := make(map[sharedResultID]bool, len(rows))
	for _, row := range rows {
		eligible[row.id] = c.shareReceiverEligibleLocked(row, now)
	}
	c.egraphMu.RUnlock()

	observed := c.probeShareMembers(ctx, rows)
	var slots []*shareSlot
	taken := map[sharedResultID]map[string]bool{}
	for _, row := range rows {
		if !eligible[row.id] {
			continue
		}
		receiver := observed[row.id]
		if receiver == nil || !receiver.usable {
			continue
		}
		for _, key := range receiver.order {
			if !shareReceiverPending(receiver.probes[key]) {
				continue
			}
			if taken[row.id][key] {
				continue
			}
			for _, candidate := range rows {
				if candidate == row {
					continue
				}
				donor := observed[candidate.id]
				if donor == nil || !donor.usable {
					continue
				}
				p, ok := donor.probes[key]
				if !ok || !shareDonorReady(p) {
					continue
				}
				if taken[row.id] == nil {
					taken[row.id] = map[string]bool{}
				}
				taken[row.id][key] = true
				slots = append(slots, &shareSlot{
					receiver:   receiver,
					donor:      donor,
					address:    clonePartAddress(p.Descriptor.Address),
					addressKey: key,
					probe:      p,
				})
				break
			}
		}
	}
	return slots
}

// traceShareSkip records one pass diagnostic. Sharing adds no fixture event
// kind: a sharing Commit emits the existing installed-ready, and until batch
// 7's sharing report group exists the absence of selected-ready distinguishes
// it from a demand install.
func (c *Cache) traceShareSkip(ctx context.Context, row *sharedResult, address PersistedPartAddress, cause error) {
	_ = ctx
	slog.Debug("snapshot sharing skipped", "row", uint64(row.id), "part", string(address.Part), "cause", cause)
}

// preflightShareDecode checks, before any shared persisted-decode attempt is
// created, that every exact record the selected decoding closure will load
// belongs to an audited background-admitted family. It walks the copied
// encoded records with batch 2's reference visitor, following the declared
// child references a decoder actually loads; the recorded call's descriptive
// references and storage roles are not decoding requests. Rows are held for
// the walk, and a cycle is detected rather than followed.
//
// An unavailable family is ShareIneligible: a skip decided here, never a
// guard trip inside a shared attempt that a foreground joiner would also see.
func (c *Cache) preflightShareDecode(ctx context.Context, roots []uint64) error {
	if len(roots) == 0 {
		return nil
	}
	seen := map[uint64]bool{}
	var held []*sharedResult
	defer func() {
		for _, row := range held {
			if err := c.releasePartRow(context.WithoutCancel(ctx), row); err != nil {
				c.recordShareCleanupError(err)
			}
		}
	}()
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == 0 || seen[id] {
			// A reference back into the walk is a cycle: already checked.
			continue
		}
		seen[id] = true
		c.egraphMu.Lock()
		row := c.resultsByID[sharedResultID(id)]
		if row != nil {
			c.incrementIncomingOwnershipLocked(ctx, row)
		}
		c.egraphMu.Unlock()
		if row == nil {
			return fmt.Errorf("%w: exact reference %d is not registered", ErrSnapshotShareIneligible, id)
		}
		held = append(held, row)
		var version capturedRowRevision
		record, err := c.capturePartRecord(ctx, row, row.imported, nil, &version)
		if err != nil {
			return fmt.Errorf("%w: exact reference %d is not readable: %w", ErrSnapshotShareIneligible, id, err)
		}
		// Inline payloads count too: a list row's items carry their own
		// object families, and each is decoded by its own native decoder.
		env := record.Envelope
		if err := walkTransferPayloads(&env, record.Call, record.SnapshotLinks, func(f PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
			if !f.BackgroundDecode {
				return nil, fmt.Errorf("%w: payload family %q is not admitted for background decode", ErrSnapshotShareIneligible, f.Name)
			}
			return v.Payload, nil
		}); err != nil {
			if errors.Is(err, ErrSnapshotShareIneligible) {
				return err
			}
			return fmt.Errorf("%w: payload families of row %d: %w", ErrSnapshotShareIneligible, id, err)
		}
		if _, err := VisitEncodedReferences(record, func(ref *PersistedRef) error {
			if ref.Kind != PersistedRefChild || ref.ResultID == 0 {
				return nil
			}
			queue = append(queue, ref.ResultID)
			return nil
		}); err != nil {
			return fmt.Errorf("%w: exact references of row %d: %w", ErrSnapshotShareIneligible, id, err)
		}
	}
	return nil
}
