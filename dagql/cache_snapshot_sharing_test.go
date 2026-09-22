package dagql

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// shareTestRef is a snapshot reference with no storage behind it: sharing
// pins, opens and releases references, and never reads their bytes.
type shareTestRef struct {
	id      string
	manager *shareTestManager
}

func (r *shareTestRef) ID() string         { return r.id }
func (r *shareTestRef) SnapshotID() string { return r.id }
func (r *shareTestRef) Release(context.Context) error {
	if r.manager.beforeRelease != nil {
		r.manager.beforeRelease()
	}
	r.manager.released.Add(1)
	return nil
}
func (r *shareTestRef) Size(context.Context) (int64, error) { return 0, nil }
func (r *shareTestRef) Mount(context.Context, bool) (snapshots.MountableRef, error) {
	panic("snapshot sharing must not mount")
}
func (r *shareTestRef) ExportChain(context.Context, config.RefConfig) (*snapshots.ExportChain, error) {
	panic("snapshot sharing must not export")
}

var _ snapshots.ImmutableRef = (*shareTestRef)(nil)

// shareTestManager answers the pin, accessor and lease calls one share makes.
// It needs no mount, no privileges and no real store.
type shareTestManager struct {
	fakeSnapshotManager
	pins      atomic.Int32
	opens     atomic.Int32
	released  atomic.Int32
	attaches  atomic.Int32
	attachErr atomic.Pointer[error]
	// attachFails, when set, decides per snapshot whether an attachment
	// fails, which is how a partial attachment is produced.
	attachFails func(snapshotID string) error
	afterPin    func()
	// beforeRelease runs inside a reference's Release, before it is counted.
	beforeRelease func()
}

func (m *shareTestManager) PinSnapshot(_ context.Context, id string) (snapshots.ImmutableRef, error) {
	m.pins.Add(1)
	if m.afterPin != nil {
		m.afterPin()
	}
	return &shareTestRef{id: id, manager: m}, nil
}

func (m *shareTestManager) GetBySnapshotID(_ context.Context, id string, _ ...snapshots.RefOption) (snapshots.ImmutableRef, error) {
	m.opens.Add(1)
	return &shareTestRef{id: id, manager: m}, nil
}

// failAttach makes the next owner synchronization fail, which is how a real
// partial lease attachment surfaces to the owning generation.
func (m *shareTestManager) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	m.attaches.Add(1)
	if m.attachErr.Load() != nil {
		return *m.attachErr.Load()
	}
	if fails := m.attachFails; fails != nil {
		if err := fails(snapshotID); err != nil {
			return err
		}
	}
	return m.fakeSnapshotManager.AttachLease(ctx, leaseID, snapshotID)
}

// shareTestCache is transferTestCache with a snapshot manager that can answer
// a share, and with sharing admission already on.
func shareTestCache(t *testing.T) (context.Context, *Cache, *Server, *shareTestManager) {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	manager := &shareTestManager{}
	c.snapshotManager = manager
	require.NoError(t, c.EnableSnapshotSharing())
	return ctx, c, srv, manager
}

// shareTestEnv is shareTestCache's result for callers that name only some of
// its parts.
type shareTestEnv struct {
	ctx     context.Context
	cache   *Cache
	srv     *Server
	manager *shareTestManager
}

func newShareTestEnv(t *testing.T) shareTestEnv {
	t.Helper()
	ctx, c, srv, manager := shareTestCache(t)
	return shareTestEnv{ctx: ctx, cache: c, srv: srv, manager: manager}
}

func shareTestQueueDepth(c *Cache) (pending int, members int) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for _, item := range c.sharePending {
		pending++
		members += len(item.members)
	}
	return pending, members
}

func shareTestHolds(c *Cache, res AnyResult) int64 {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return res.cacheSharedResult().incomingOwnershipCount
}

// shareTestEncodedReceiver makes res an encoded, imported row with a pending
// snapshot part, which is what an imported row looks like before any demand.
func shareTestEncodedReceiver(t *testing.T, ctx context.Context, c *Cache, res AnyResult) {
	t.Helper()
	record, err := c.CapturePersistedRecord(ctx, res)
	require.NoError(t, err)
	row := res.cacheSharedResult()
	row.payloadMu.Lock()
	row.hasValue, row.self = false, nil
	row.persistedEnvelope = &record.Envelope
	row.imported = true
	row.payloadRevision++
	row.payloadMu.Unlock()
}

// sharePassBarrier reports finished passes on the real completion of the
// worker's pass, so a test waits on that signal instead of polling a clock.
type sharePassBarrier struct {
	passes  chan int
	skips   chan error
	entered chan int
	hold    atomic.Pointer[chan struct{}]
}

// holdPasses makes every pass park at its start until release is called, so
// queue state can be inspected while a cohort is active.
func (b *sharePassBarrier) holdPasses() (release func()) {
	gate := make(chan struct{})
	b.hold.Store(&gate)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.hold.Store(nil)
			close(gate)
		})
	}
}

// awaitEntered waits for a pass to reach its start barrier.
func (b *sharePassBarrier) awaitEntered(t *testing.T) int {
	t.Helper()
	select {
	case planned := <-b.entered:
		return planned
	case <-time.After(10 * time.Second):
		t.Fatal("no snapshot sharing pass started")
		return 0
	}
}

func newSharePassBarrier(c *Cache) *sharePassBarrier {
	b := &sharePassBarrier{passes: make(chan int, 64), skips: make(chan error, 64), entered: make(chan int, 64)}
	var slots int
	c.testBeforeSharePass = func(_ *snapshotShareItem, planned int) {
		slots = planned
		select {
		case b.entered <- planned:
		default:
		}
		if hold := b.hold.Load(); hold != nil {
			<-*hold
		}
	}
	c.testAfterSharePass = func(*snapshotShareItem) { b.passes <- slots }
	c.testShareSkipped = func(_ sharedResultID, _ PersistedPartAddress, err error) {
		select {
		case b.skips <- err:
		default:
		}
	}
	return b
}

// skipCauses drains the causes recorded by this pass, for a failure message.
func (b *sharePassBarrier) skipCauses() []error {
	var out []error
	for {
		select {
		case err := <-b.skips:
			out = append(out, err)
		default:
			return out
		}
	}
}

// awaitPass waits for one finished pass and returns how many slots it
// planned. The wait is bounded so a failure cannot hang the package.
func (b *sharePassBarrier) awaitPass(t *testing.T) int {
	t.Helper()
	planned, err := b.waitPass()
	require.NoError(t, err)
	return planned
}

func (b *sharePassBarrier) waitPass() (int, error) {
	select {
	case planned := <-b.passes:
		return planned, nil
	case <-time.After(10 * time.Second):
		return 0, errors.New("no snapshot sharing pass finished")
	}
}

func shareTestAppliedLinks(res AnyResult) []PersistedSnapshotRefLink {
	row := res.cacheSharedResult()
	row.payloadMu.RLock()
	defer row.payloadMu.RUnlock()
	return cloneSnapshotRefLinks(row.snapshotOwnerLinks)
}

func shareTestHasLink(res AnyResult, refKey string) bool {
	for _, link := range shareTestAppliedLinks(res) {
		if link.RefKey == refKey {
			return true
		}
	}
	return false
}

// A real union of two classes queues one cohort, and nothing is queued while
// admission is off.
func TestSnapshotSharingTriggers(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "snapshot"})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	receiver.cacheSharedResult().imported = true

	content := digest.FromString("shared-content")
	require.NoError(t, c.TeachContentDigest(ctx, donor, content))
	require.NoError(t, c.TeachContentDigest(ctx, receiver, content))
	pending, _ := shareTestQueueDepth(c)
	require.Zero(t, pending, "sharing is off until the engine enables it")
	require.False(t, c.SnapshotSharingEnabled())

	require.NoError(t, c.EnableSnapshotSharing())
	require.True(t, c.SnapshotSharingEnabled())
}

// One encoded imported receiver takes a completed donor's snapshot without
// any demand, download or evaluation.
func TestSnapshotSharingInstallsFromEquivalent(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donorValue := &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "donor-snapshot"}}}
	donor := persistedListTestResult(t, ctx, c, srv, "donor", donorValue)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)

	content := digest.FromString("share-installs")
	require.NoError(t, c.TeachContentDigest(ctx, donor, content))
	require.NoError(t, c.TeachContentDigest(ctx, receiver, content))

	require.Equal(t, 1, barrier.awaitPass(t), "one planned slot")
	require.True(t, shareTestHasLink(receiver, "donor-snapshot"), "receiver owns the donated snapshot")
	require.Equal(t, int32(1), manager.pins.Load(), "exactly one independent pin")
	require.Zero(t, manager.opens.Load(), "an encoded receiver needs no accessor ref")
}

// shareTestPair builds an equivalent donor and an encoded imported receiver
// of the multi-part family and puts them in one output class.
func shareTestPair(t *testing.T, ctx context.Context, c *Cache, srv *Server, donorParts, receiverParts map[string]sharePartState) (AnyResult, AnyResult) {
	t.Helper()
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	donor := persistedListTestResult(t, ctx, c, srv, "share-donor", newShareTestValue("donor", donorParts))
	receiver := persistedListTestResult(t, ctx, c, srv, "share-receiver", newShareTestValue("receiver", receiverParts))
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	return donor, receiver
}

// shareTestUnite teaches both rows the same content digest, which is a real
// union of their output classes and one of the queue's triggers.
func shareTestUnite(t *testing.T, ctx context.Context, c *Cache, label string, rows ...AnyResult) {
	t.Helper()
	content := digest.FromString(label)
	for _, row := range rows {
		require.NoError(t, c.TeachContentDigest(ctx, row, content))
	}
}

// PreparedSequence: one encoded receiver missing two parts takes both from
// one donor in a single pass, each publishing once with every earlier role
// present, without a second Prepare or a stale-copy refusal.
func TestSnapshotSharingPreparedSequence(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}, "mount": {Snapshot: "mount-snap"}},
		map[string]sharePartState{"fs": {}, "mount": {}},
	)
	shareTestUnite(t, ctx, c, "prepared-sequence", donor, receiver)
	require.Equal(t, 2, barrier.awaitPass(t), "both missing parts are planned in one pass")

	links := shareTestAppliedLinks(receiver)
	require.Len(t, links, 2, "both roles are applied: %v, skips=%v", links, barrier.skipCauses())
	require.True(t, shareTestHasLink(receiver, "fs-snap"))
	require.True(t, shareTestHasLink(receiver, "mount-snap"))
	require.Equal(t, int32(2), manager.pins.Load(), "one independent pin per installed part")
	require.Equal(t, int32(2), manager.released.Load(), "each protection ref is released once after its owner sync")
}

// A three-part control: the second part's prepared envelope is the base for
// the third, so all three install in the original pass.
func TestSnapshotSharingPreparedSequenceThreeParts(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"a": {Snapshot: "a-snap"}, "b": {Snapshot: "b-snap"}, "c": {Snapshot: "c-snap"}},
		map[string]sharePartState{"a": {}, "b": {}, "c": {}},
	)
	shareTestUnite(t, ctx, c, "three-parts", donor, receiver)
	require.Equal(t, 3, barrier.awaitPass(t))
	require.Len(t, shareTestAppliedLinks(receiver), 3)
}

// IndependentParts: a receiver missing one part takes only that part, and an
// absent part is final and never overwritten.
func TestSnapshotSharingIndependentParts(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}, "meta": {Snapshot: "meta-snap"}, "gone": {Absent: true}},
		map[string]sharePartState{"fs": {}, "meta": {Snapshot: "receiver-meta"}, "gone": {Absent: true}},
	)
	shareTestUnite(t, ctx, c, "independent-parts", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t), "only the pending part is selected")
	require.True(t, shareTestHasLink(receiver, "fs-snap"))
	require.True(t, shareTestHasLink(receiver, "receiver-meta"), "a completed sibling is untouched")
	require.False(t, shareTestHasLink(receiver, "meta-snap"))
	require.Equal(t, int32(1), manager.pins.Load())
}

// DonorReceivesSibling: a row that receives one part earlier in commit order
// still donates its own unchanged completed part in the same pass, even
// though its whole payload revision moved.
func TestSnapshotSharingDonorReceivesSibling(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	c.snapshotManager = &shareTestManager{}
	barrier := newSharePassBarrier(c)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	// D owns fs and is missing mount; R is missing fs. Both are imported, so
	// D receives a mount from L and donates its fs to R in one pass.
	source := persistedListTestResult(t, ctx, c, srv, "sibling-source", newShareTestValue("source", map[string]sharePartState{"mount": {Snapshot: "mount-snap"}}))
	middle := persistedListTestResult(t, ctx, c, srv, "sibling-middle", newShareTestValue("middle", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}, "mount": {}}))
	receiver := persistedListTestResult(t, ctx, c, srv, "sibling-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}, "mount": {Snapshot: "receiver-mount"}}))
	shareTestEncodedReceiver(t, ctx, c, middle)
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, middle, source)
	partTestEquivalent(t, c, receiver, middle)
	partTestEquivalent(t, c, receiver, source)
	// Build the whole cohort before admitting a pass. Otherwise the worker can
	// take source and middle before the receiver joins, testing two passes
	// instead of a donor whose sibling changes in the same pass.
	shareTestUnite(t, ctx, c, "donor-receives-sibling", source, middle, receiver)
	require.NoError(t, c.EnableSnapshotSharing())
	c.notifySnapshotShareCompletion(ctx, middle.cacheSharedResult())

	require.Equal(t, 2, barrier.awaitPass(t), "the middle row receives one part and donates another")
	require.True(t, shareTestHasLink(middle, "mount-snap"), "middle received its missing mount")
	require.True(t, shareTestHasLink(receiver, "fs-snap"), "middle's unchanged fs was still donated")
}

// Triggers: each notification site enqueues work, and the ones that publish
// nothing enqueue nothing.
func TestSnapshotSharingTriggerSites(t *testing.T) {
	t.Run("new membership without a union", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		release := barrier.holdPasses()
		defer release()
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		// Put the donor in a class of its own, then insert the receiver into
		// that existing class with no merge of two classes.
		c.egraphMu.Lock()
		class := c.ensureEqClassForDigestLocked(ctx, "share-membership-class")
		notify, owner := c.beginShareNotificationsLocked()
		c.addResultOutputEqClassLocked(donor.cacheSharedResult().id, class)
		c.addResultOutputEqClassLocked(receiver.cacheSharedResult().id, class)
		c.flushShareNotificationsLocked(ctx, notify, owner)
		c.egraphMu.Unlock()
		require.Equal(t, 1, barrier.awaitEntered(t), "the inserted membership queued its class")
	})

	t.Run("no enqueue for an unregistered row", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		_, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		row := receiver.cacheSharedResult()
		c.egraphMu.Lock()
		delete(c.resultsByID, row.id)
		c.queueSnapshotShareRowLocked(ctx, row)
		pending := len(c.sharePending)
		c.resultsByID[row.id] = row
		c.egraphMu.Unlock()
		require.Zero(t, pending, "a collected row is never resurrected by a notification")
	})

	t.Run("lazy completion notifies after a successful share", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		shareTestUnite(t, ctx, c, "lazy-completion", donor, receiver)
		require.Equal(t, 1, barrier.awaitPass(t))
		require.True(t, shareTestHasLink(receiver, "fs-snap"))
		// The obtain task's own successful completion is a lazy completion,
		// so it queues the receiver's classes again; that successor pass
		// finds nothing left to do.
		require.Equal(t, 0, barrier.awaitPass(t), "the completion trigger queued an empty successor")
	})

	t.Run("admission off enqueues nothing", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "snapshot"})
		receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
		receiver.cacheSharedResult().imported = true
		shareTestUnite(t, ctx, c, "off", donor, receiver)
		pending, members := shareTestQueueDepth(c)
		require.Zero(t, pending)
		require.Zero(t, members)
	})
}

// Coalescing: repeated notifications before an item is taken deduplicate
// their holds and keep one counted operation; a notification after the take
// creates a separate pending successor.
func TestSnapshotSharingCoalescing(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	release := barrier.holdPasses()
	defer release()
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	donorRow, receiverRow := donor.cacheSharedResult(), receiver.cacheSharedResult()
	c.egraphMu.Lock()
	class := c.ensureEqClassForDigestLocked(ctx, "coalescing-class")
	c.addResultOutputEqClassLocked(donorRow.id, class)
	c.addResultOutputEqClassLocked(receiverRow.id, class)
	before := donorRow.incomingOwnershipCount
	// Queue the same class repeatedly before the worker takes it.
	c.queueSnapshotShareLocked(ctx, class)
	c.queueSnapshotShareLocked(ctx, class)
	c.queueSnapshotShareLocked(ctx, class)
	pending := len(c.sharePending)
	var members, ops int
	for _, item := range c.sharePending {
		members, ops = len(item.members), len(item.ops)
	}
	held := donorRow.incomingOwnershipCount
	c.egraphMu.Unlock()
	require.Equal(t, 1, pending, "repeated notifications coalesce into one item")
	require.Equal(t, 2, members)
	require.Equal(t, 1, ops, "one counted operation for the coalesced item")
	require.Equal(t, before+1, held, "one hold per distinct member, taken once")

	// A notification after the take creates a separate pending successor.
	barrier.awaitEntered(t)
	c.egraphMu.Lock()
	c.queueSnapshotShareLocked(ctx, class)
	successors := len(c.sharePending)
	c.egraphMu.Unlock()
	require.Equal(t, 1, successors, "a successor is pending beside the active cohort")
	release()
}

// NoJoinRefusal: an active obtain generation for one address refuses that
// slot without running its Body, and the receiver's other address still
// installs, releases members and finishes.
func TestSnapshotSharingNoJoinRefusal(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}, "mount": {Snapshot: "mount-snap"}},
		map[string]sharePartState{"fs": {}, "mount": {}},
	)
	blocked := make(chan struct{})
	// Released on every exit, so a failed assertion cannot leave the
	// preseeded Body parked.
	unblock := sync.OnceFunc(func() { close(blocked) })
	defer unblock()
	entered := make(chan struct{})
	taskDone := make(chan error, 1)
	go func() {
		taskDone <- c.RunLazyTask(ctx, receiver, partTaskKey("obtain", PersistedPartAddress{Part: "fs"}), LazyTaskSpec{
			Body: func(context.Context) error {
				close(entered)
				<-blocked
				return nil
			},
		})
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("preseeded obtain task never started")
	}

	shareTestUnite(t, ctx, c, "no-join", donor, receiver)
	require.Equal(t, 2, barrier.awaitPass(t), "both addresses were planned")
	require.False(t, shareTestHasLink(receiver, "fs-snap"), "the busy address is refused")
	require.True(t, shareTestHasLink(receiver, "mount-snap"), "the other address still installs")
	require.Equal(t, int32(1), manager.pins.Load(), "the refused slot took no pin")
	unblock()
	select {
	case err := <-taskDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the preseeded obtain task never finished")
	}
}

// ReadinessAndExpiry: what makes a donor Ready and a receiver eligible.
func TestSnapshotSharingReadinessAndExpiry(t *testing.T) {
	t.Run("a desired-only role is not an applied role", func(t *testing.T) {
		ctx, c, srv, manager := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		// Keep the donor's intent, drop its applied links: the probe still
		// reads a completed descriptor, but nothing owns those bytes yet.
		row := donor.cacheSharedResult()
		row.payloadMu.Lock()
		row.snapshotLinkIntent = &snapshotLinkIntent{Links: cloneSnapshotRefLinks(row.snapshotOwnerLinks)}
		row.snapshotOwnerLinks = nil
		row.payloadRevision++
		row.payloadMu.Unlock()

		shareTestUnite(t, ctx, c, "desired-only", donor, receiver)
		require.Equal(t, 1, barrier.awaitPass(t))
		require.False(t, shareTestHasLink(receiver, "fs-snap"), "a desired link cannot donate")
		require.Zero(t, manager.pins.Load())
	})

	t.Run("an expired receiver is skipped", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		c.egraphMu.Lock()
		receiver.cacheSharedResult().expiresAtUnix = time.Now().Unix() - 1
		c.egraphMu.Unlock()
		shareTestUnite(t, ctx, c, "expired-receiver", donor, receiver)
		require.Equal(t, 0, barrier.awaitPass(t), "an expired receiver selects nothing")
		require.False(t, shareTestHasLink(receiver, "fs-snap"))
	})

	t.Run("an expired donor is skipped", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		c.egraphMu.Lock()
		donor.cacheSharedResult().expiresAtUnix = time.Now().Unix() - 1
		c.egraphMu.Unlock()
		shareTestUnite(t, ctx, c, "expired-donor", donor, receiver)
		require.Equal(t, 0, barrier.awaitPass(t))
		require.False(t, shareTestHasLink(receiver, "fs-snap"))
	})

	t.Run("a native receiver is never filled", func(t *testing.T) {
		ctx, c, srv, _ := shareTestCache(t)
		barrier := newSharePassBarrier(c)
		donor, receiver := shareTestPair(t, ctx, c, srv,
			map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
			map[string]sharePartState{"fs": {}},
		)
		row := receiver.cacheSharedResult()
		row.payloadMu.Lock()
		row.imported = false
		row.payloadMu.Unlock()
		c.egraphMu.Lock()
		row.imported = false
		c.egraphMu.Unlock()
		shareTestUnite(t, ctx, c, "native-receiver", donor, receiver)
		// The union's own E interval decides this, so the queue is final as
		// soon as the teaching call returns: a class with no imported member
		// queues nothing at all.
		pending, _ := shareTestQueueDepth(c)
		require.Zero(t, pending, "a native row is not a sharing receiver")
		require.False(t, shareTestHasLink(receiver, "fs-snap"))
		_ = barrier
	})
}

// StructuralRequirements: Own(L) must fit inside Own(R), and an offer-only
// resource on either side never enters that comparison.
func TestSnapshotSharingStructuralRequirements(t *testing.T) {
	for _, mode := range []string{"own", "offer-only", "missing"} {
		t.Run(mode, func(t *testing.T) {
			ctx, c, srv, _ := shareTestCache(t)
			barrier := newSharePassBarrier(c)
			resource := persistedListTestResult(t, ctx, c, srv, "resource", String("socket"))
			c.egraphMu.Lock()
			resource.cacheSharedResult().sessionResourceHandle = "socket"
			_, err := c.recomputeRequiredSessionResourcesLocked(resource.cacheSharedResult())
			c.egraphMu.Unlock()
			require.NoError(t, err)

			donor, receiver := shareTestPair(t, ctx, c, srv,
				map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
				map[string]sharePartState{"fs": {}},
			)
			transferTestDependency(c, ctx, donor, resource)
			switch mode {
			case "own":
				transferTestDependency(c, ctx, receiver, resource)
			case "offer-only":
				transferTestOffer(t, c, ctx, receiver, resource)
			}
			c.egraphMu.Lock()
			var requirementErr error
			for _, row := range []AnyResult{donor, receiver} {
				_, err := c.recomputeRequiredSessionResourcesLocked(row.cacheSharedResult())
				if err != nil {
					requirementErr = err
					break
				}
			}
			c.egraphMu.Unlock()
			require.NoError(t, requirementErr)

			shareTestUnite(t, ctx, c, "structural-"+mode, donor, receiver)
			barrier.awaitPass(t)
			if mode == "own" {
				require.True(t, shareTestHasLink(receiver, "fs-snap"), "the receiver already owns the donor's requirement")
				return
			}
			require.False(t, shareTestHasLink(receiver, "fs-snap"), "an offer-only or missing requirement refuses the share")
		})
	}
}

// ReleaseThenExternalFinish: every cohort, donor and temporary hold has ended
// before the first external Finish, and each receipt still protects its
// receiver.
func TestSnapshotSharingReleaseThenExternalFinish(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	donorRow := donor.cacheSharedResult()
	c.egraphMu.RLock()
	donorBefore := donorRow.incomingOwnershipCount
	c.egraphMu.RUnlock()

	var atFinish int64
	c.testBeforeShareFinish = func(receipt *ReadyPartReceipt) {
		c.egraphMu.RLock()
		atFinish = donorRow.incomingOwnershipCount
		c.egraphMu.RUnlock()
		require.Same(t, receiver.cacheSharedResult(), receipt.receiver)
	}
	shareTestUnite(t, ctx, c, "release-then-finish", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "fs-snap"))
	require.Equal(t, donorBefore, atFinish, "every donor and member hold ended before Finish")
	// The installing task's own completion is a trigger, so one empty
	// successor pass follows and takes its own member holds. Wait for it to
	// drain before comparing the balance.
	require.Equal(t, 0, barrier.awaitPass(t), "the completion trigger queued an empty successor")
	c.egraphMu.RLock()
	after := donorRow.incomingOwnershipCount
	c.egraphMu.RUnlock()
	require.Equal(t, donorBefore, after, "the pass balances every hold it took")
}

// PreparationContextGuards: every guarded boundary refuses under the marker
// before any underlying work starts, and an unmarked context is unaffected.
func TestSnapshotSharingPreparationContextGuards(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	value := persistedListTestResult(t, ctx, c, srv, "guarded", &transferTestValue{Text: "pending"})
	marked := engine.WithSnapshotSharePreparation(ctx)

	require.ErrorIs(t, c.Evaluate(marked, value), engine.ErrSnapshotShareEvaluation)
	require.ErrorIs(t, c.EvaluateParts(marked, value, "snapshot"), engine.ErrSnapshotShareEvaluation)
	require.ErrorIs(t, c.demandPart(marked, value, PersistedPartAddress{Part: "snapshot"}), engine.ErrSnapshotShareEvaluation)
	require.ErrorIs(t, c.installChainPart(marked, value, &PartSourceLease{cache: c}, nil, nil), engine.ErrSnapshotShareEvaluation)

	// Provider returns an interface, so a marked context gets a refusing
	// provider rather than an error, and it never reaches the delegate.
	var delegateCalls atomic.Int32
	c.SetPartContentSource(shareTestOverride{calls: &delegateCalls})
	offer := PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}}
	provider := c.PartContentSource().Provider(marked, offer, nil)
	_, err := provider.Info(marked, digest.FromString("blob"))
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
	_, err = provider.ReaderAt(marked, ocispecs.Descriptor{})
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
	require.Zero(t, delegateCalls.Load(), "the delegate is never consulted under the marker")

	// An unmarked context is unaffected, and Available stays pure.
	require.NotNil(t, c.PartContentSource().Provider(ctx, offer, nil))
	require.Equal(t, int32(1), delegateCalls.Load())
	require.True(t, c.PartContentSource().Available(offer, time.Now()))

	// Descendants and detached cleanup contexts keep the marker; an
	// independent context does not.
	require.True(t, engine.IsSnapshotSharePreparation(context.WithoutCancel(marked)))
	child, cancel := context.WithCancel(marked)
	defer cancel()
	require.True(t, engine.IsSnapshotSharePreparation(child))
	require.False(t, engine.IsSnapshotSharePreparation(ctx))
	require.NoError(t, engine.CheckSnapshotSharePreparation(ctx, "unmarked"))
}

type shareTestOverride struct{ calls *atomic.Int32 }

func (o shareTestOverride) Available(PersistedPartOffer, time.Time) bool { return true }
func (o shareTestOverride) Provider(context.Context, PersistedPartOffer, *PartDemandState) content.InfoReaderProvider {
	o.calls.Add(1)
	return refusingContentProvider{}
}

// The worker base itself carries the marker, so no slot can evaluate.
func TestSnapshotSharingWorkerBaseIsMarked(t *testing.T) {
	c := newShareTestEnv(t).cache
	require.True(t, engine.IsSnapshotSharePreparation(c.snapshotShareWorkerBase()))
}

// DecodeWhileFinishPaused: with Finish paused, a decode of the receiver reads
// the complete desired roles, and only the owning task settles the part.
func TestSnapshotSharingDecodeWhileFinishPaused(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	row := receiver.cacheSharedResult()
	paused := make(chan struct{})
	resume := make(chan struct{})
	// Released on every exit, so a failed assertion cannot leave the worker
	// parked before its Finish.
	resumeFinish := sync.OnceFunc(func() { close(resume) })
	defer resumeFinish()
	var receipt *ReadyPartReceipt
	c.testBeforeShareFinish = func(r *ReadyPartReceipt) {
		receipt = r
		close(paused)
		<-resume
	}
	shareTestUnite(t, ctx, c, "decode-paused", donor, receiver)
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("the pass never reached its Finish phase")
	}

	key, _ := partAddressKey(PersistedPartAddress{Part: "fs"})
	gate := row.partGate.gate.Load()
	gate.mu.Lock()
	installedPhase := gate.outputs[key].phase
	gate.mu.Unlock()
	require.Equal(t, PartOutputInstalled, installedPhase, "installed and not yet settled")
	require.False(t, receipt.task.settled.Load())

	loaded, err := c.LoadResultByResultID(ctx, "", srv, uint64(row.id))
	require.NoError(t, err)
	decoded, ok := loaded.Unwrap().(*shareTestValue)
	require.True(t, ok)
	require.Equal(t, "fs-snap", decoded.Parts["fs"].Snapshot, "the decode reads the complete desired map")
	require.False(t, receipt.task.settled.Load(), "decode never settles the output")

	resumeFinish()
	require.Equal(t, 1, barrier.awaitPass(t))
	gate.mu.Lock()
	settledPhase := gate.outputs[key].phase
	gate.mu.Unlock()
	require.Equal(t, PartComplete, settledPhase, "only the owning task settles")
}

// Shutdown: close waits for an active pass, drops what is still queued, and
// ends every counted operation it took.
func TestSnapshotSharingShutdown(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	release := barrier.holdPasses()
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	shareTestUnite(t, ctx, c, "shutdown", donor, receiver)
	barrier.awaitEntered(t)

	closed := make(chan error, 1)
	go func() { closed <- c.Close(ctx) }()
	select {
	case err := <-closed:
		t.Fatalf("close returned while a pass was active: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("close never returned after the pass drained")
	}
	require.Zero(t, c.activeGlobalOperations.Load(), "every counted operation ended")
	pending, members := shareTestQueueDepth(c)
	require.Zero(t, pending)
	require.Zero(t, members)
	require.ErrorIs(t, c.EnableSnapshotSharing(), ErrSnapshotSharingClosed, "admission cannot reopen after close")
}

// FailedFinishLastOwner and the bookkeeping-only retry: a failed owner
// synchronization leaves the installed output and its protection in place,
// and a later demand retries only the bookkeeping - no second Prepare, pin or
// content request.
func TestSnapshotSharingFailedFinishAndRetry(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	row := receiver.cacheSharedResult()
	// Fail the owner synchronization of the installed share only.
	attachErr := errors.New("attach lease failed")
	manager.attachErr.Store(&attachErr)
	shareTestUnite(t, ctx, c, "failed-finish", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t))

	key, _ := partAddressKey(PersistedPartAddress{Part: "fs"})
	gate := row.partGate.gate.Load()
	gate.mu.Lock()
	phase := gate.outputs[key].phase
	gate.mu.Unlock()
	require.Equal(t, PartOutputInstalled, phase, "the output stays installed after a failed sync")
	require.Equal(t, int32(1), manager.pins.Load())
	require.Zero(t, manager.released.Load(), "protection is retained for the bookkeeping retry")

	// The retry: allow the attachment and demand the installed part.
	manager.attachErr.Store(nil)
	pinsBefore := manager.pins.Load()
	require.NoError(t, c.demandPart(ctx, receiver, PersistedPartAddress{Part: "fs"}))
	gate.mu.Lock()
	phase = gate.outputs[key].phase
	gate.mu.Unlock()
	require.Equal(t, PartComplete, phase, "the retry completed the owning bookkeeping")
	require.Equal(t, pinsBefore, manager.pins.Load(), "the retry never prepares or pins again")
	require.Equal(t, int32(1), manager.released.Load(), "protection is released once, by the owning generation")
}

// CompletionRegistrationRace: the foreground waiter returns while the sharing
// notification is still blocked on the graph lock.
func TestSnapshotSharingCompletionRegistrationRace(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	release := barrier.holdPasses()
	defer release()
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	_ = donor

	graphHeld := make(chan struct{})
	atFinish := make(chan struct{})
	attempts := make(chan *lazyEvalAttempt, 1)
	c.testAfterLazyEvalFinish = func(attempt *lazyEvalAttempt) {
		select {
		case <-atFinish:
			return
		default:
		}
		attempts <- attempt
		close(atFinish)
		<-graphHeld
	}
	done := make(chan error, 1)
	go func() {
		done <- c.RunLazyTask(ctx, receiver, "obtain:race", LazyTaskSpec{Body: func(context.Context) error { return nil }})
	}()
	var attempt *lazyEvalAttempt
	select {
	case attempt = <-attempts:
	case <-time.After(10 * time.Second):
		t.Fatal("the attempt never reached its completion hook")
	}
	// Take the graph lock before the completion hook can, then let the
	// attempt publish its completion and wake its waiters.
	c.egraphMu.Lock()
	close(graphHeld)
	select {
	case <-attempt.done:
		// The wake happened while the sharing notification is still blocked
		// on the graph lock this test holds.
	case <-time.After(10 * time.Second):
		c.egraphMu.Unlock()
		t.Fatal("the waiter wake waited for the sharing notification")
	}
	c.egraphMu.Unlock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the task never finished")
	}
}

// A typed receiver whose donated descriptor carries Service references needs
// the engine's registered reconstruction context. With none registered the
// slot is ineligible, decided before any shared decode attempt is created;
// service-free installs on the same cache still happen.
func TestSnapshotSharingTypedServiceReceiverNeedsRegistration(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	service := persistedListTestResult(t, ctx, c, srv, "share-service", &transferTestValue{Text: "service"})
	serviceID := uint64(service.cacheSharedResult().id)
	donor := persistedListTestResult(t, ctx, c, srv, "svc-donor", newShareTestValue("donor", map[string]sharePartState{"fs": {Snapshot: "fs-snap", Service: serviceID}}))
	receiver := persistedListTestResult(t, ctx, c, srv, "svc-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}}))
	// A typed imported receiver: decoded, not encoded, so its preparation
	// reconstructs the donated descriptor's services.
	c.egraphMu.Lock()
	receiver.cacheSharedResult().imported = true
	c.egraphMu.Unlock()
	partTestEquivalent(t, c, receiver, donor)

	require.Nil(t, c.partPreparationContext(), "no engine callback is registered on this cache")
	shareTestUnite(t, ctx, c, "typed-service", donor, receiver)
	require.Equal(t, 0, barrier.awaitPass(t), "the slot never reaches preparation")
	require.Zero(t, manager.pins.Load(), "the slot is ineligible before any decode attempt")
	causes := barrier.skipCauses()
	require.NotEmpty(t, causes)
	found := false
	for _, err := range causes {
		found = found || errors.Is(err, ErrSnapshotShareIneligible)
	}
	require.True(t, found, "the skip is ShareIneligible, not a guard trip: %v", causes)
}

// A typed receiver takes one slot per pass, and the installing task's own
// completion queues the successor that fills the next part. Nothing is lost;
// it costs one extra pass per additional part.
func TestSnapshotSharingTypedReceiverFillsOnePartPerPass(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	donor := persistedListTestResult(t, ctx, c, srv, "typed-donor", newShareTestValue("donor", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}, "mount": {Snapshot: "mount-snap"}}))
	receiver := persistedListTestResult(t, ctx, c, srv, "typed-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}, "mount": {}}))
	c.egraphMu.Lock()
	receiver.cacheSharedResult().imported = true
	c.egraphMu.Unlock()
	partTestEquivalent(t, c, receiver, donor)
	shareTestUnite(t, ctx, c, "typed-one-per-pass", donor, receiver)

	require.Equal(t, 1, barrier.awaitPass(t), "a typed receiver plans one slot")
	require.Equal(t, 1, barrier.awaitPass(t), "its completion queues a successor that plans the other")
	require.Equal(t, 0, barrier.awaitPass(t), "the second install's successor finds nothing left")
	require.Equal(t, int32(2), manager.pins.Load(), "both parts are installed, one per pass")
	require.Equal(t, int32(2), manager.opens.Load(), "a typed receiver opens its own accessor ref per part")
	value, ok := receiver.Unwrap().(*shareTestValue)
	require.True(t, ok)
	require.Equal(t, "fs-snap", value.Parts["fs"].Snapshot)
	require.Equal(t, "mount-snap", value.Parts["mount"].Snapshot)
}

// A restored imported row carries a frame that has derived none of its
// digests and names its receiver by result ID. Deriving them reads the graph
// through E, so selection prepares every lookup before its own E section;
// preparing one inside it would leave the worker waiting for a lock it holds,
// and every later cache operation waiting for the worker.
func TestSnapshotSharingSelectsRestoredFrame(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	parent := persistedListTestResult(t, ctx, c, srv, "share-parent", String("parent"))
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}})
	shareTestUnite(t, ctx, c, "restored-frame", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t), "the live frame shares as usual")
	require.Equal(t, 0, barrier.awaitPass(t), "and its completion finds nothing left")

	row := receiver.cacheSharedResult()
	restored := row.loadResultCall().clone()
	restored.Receiver = &ResultCallRef{ResultID: uint64(parent.cacheSharedResult().id)}
	row.storeResultCall(restored)
	c.notifySnapshotShareCompletion(ctx, row)
	require.Equal(t, 0, barrier.awaitPass(t), "the restored frame is resolved and the pass ends")
	pending, _ := shareTestQueueDepth(c)
	require.Zero(t, pending, "the graph lock is free again")
}

// A slot runs as the ordinary obtain task of its address, so an ordinary
// demand for that address joins the slot's generation. When the slot is then
// refused, the demand must see batch 4's reselect and finish by its ordinary
// route; it once received sharing's private stop error and failed a request
// that would have succeeded with sharing off.
func TestSnapshotSharingRefusedSlotReselectsJoinedDemand(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	row := receiver.cacheSharedResult()
	address := PersistedPartAddress{Part: "fs"}

	joined := make(chan struct{})
	var joinOnce, pinOnce sync.Once
	c.testAfterLazyEvalJoin = func(*lazyEvalAttempt) { joinOnce.Do(func() { close(joined) }) }
	demandDone := make(chan error, 1)
	manager.afterPin = func() {
		// Only the slot's pin, inside its Prepare, while its generation is
		// active. The demand's own later install pins again.
		pinOnce.Do(func() {
			go func() { demandDone <- c.demandPart(ctx, receiver, address) }()
			select {
			case <-joined:
			case err := <-demandDone:
				t.Errorf("the demand returned before joining the slot's obtain task: %v", err)
				demandDone <- err
			case <-time.After(10 * time.Second):
				t.Error("the demand never joined the slot's obtain task")
			}
			// A concurrent representation change refuses the slot.
			row.payloadMu.Lock()
			row.payloadRevision++
			row.payloadMu.Unlock()
		})
	}
	shareTestUnite(t, ctx, c, "refused-slot-joined-demand", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t))
	select {
	case err := <-demandDone:
		require.NoError(t, err, "the joined demand selects again; no private sharing error reaches it")
	case <-time.After(10 * time.Second):
		t.Fatal("the joined demand never returned")
	}
	require.True(t, shareTestHasLink(receiver, "fs-snap"), "the demand installed the part by its ordinary route")
}

// shareTestNoPassYet fails if a pass finishes within a short window. It is the
// file's one negative wait: the barrier the test holds is what must keep the
// pass from completing.
func shareTestNoPassYet(t *testing.T, b *sharePassBarrier, why string) {
	t.Helper()
	select {
	case <-b.passes:
		t.Fatalf("the pass completed %s", why)
	case <-time.After(200 * time.Millisecond):
	}
}

// Cancellation while a Body is preparing: the launching call's return is not
// evidence that the Body ended, so the pass keeps waiting for the Body's own
// report, which comes after the preparation is released.
func TestSnapshotSharingCancelDuringPreparation(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	donorHolds, receiverHolds := shareTestHolds(c, donor), shareTestHolds(c, receiver)
	released := armLazyAttemptReleased(c)
	entered, resume := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(resume) })
	defer unblock()
	manager.afterPin = func() { close(entered); <-resume }
	shareTestUnite(t, ctx, c, "cancel-preparing", donor, receiver)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the preparation never reached PinSnapshot")
	}
	c.closeSnapshotSharing()
	shareTestNoPassYet(t, barrier, "while its Body was still inside PinSnapshot")
	unblock()
	require.Equal(t, 1, barrier.awaitPass(t))
	// The pass joins RunLazyTask, whose caller wakes before the attempt's
	// deferred receiver hold is released. Observe that release before counting.
	waitLazyAttemptReleased(t, released)
	require.False(t, shareTestHasLink(receiver, "fs-snap"), "a canceled preparation installs nothing")
	require.Equal(t, manager.pins.Load(), manager.released.Load(), "the canceled preparation released its pin")
	require.Equal(t, donorHolds, shareTestHolds(c, donor))
	require.Equal(t, receiverHolds, shareTestHolds(c, receiver))
}

// Cancellation after a preparation exists: the Body aborts, and reports the
// abort only after its release has finished. The pass takes that report to
// mean the Body owns nothing, so it must not release members or complete
// while the release is still running.
func TestSnapshotSharingAbortReportsAfterCleanup(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	donorHolds := shareTestHolds(c, donor)
	releasing, resume := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(resume) })
	defer unblock()
	// Cancel inside Prepare, so the Body finds its context done at the commit
	// latch and aborts a live preparation.
	manager.afterPin = func() { c.closeSnapshotSharing() }
	manager.beforeRelease = sync.OnceFunc(func() { close(releasing); <-resume })
	shareTestUnite(t, ctx, c, "abort-cleanup", donor, receiver)
	select {
	case <-releasing:
	case <-time.After(10 * time.Second):
		t.Fatal("the aborted preparation never reached its release")
	}
	shareTestNoPassYet(t, barrier, "while an aborted preparation was still being released")
	require.Greater(t, shareTestHolds(c, donor), donorHolds, "member release waits for the abort's cleanup")
	unblock()
	require.Equal(t, 1, barrier.awaitPass(t))
	require.False(t, shareTestHasLink(receiver, "fs-snap"))
	require.Equal(t, manager.pins.Load(), manager.released.Load())
	require.Equal(t, donorHolds, shareTestHolds(c, donor))
}

// Cancellation between a Commit's publication and the delivery of its
// receipt: the Body alone reports, so the receipt reaches the pass, is
// finished after member release, and the output settles. The launching call
// once reported a refusal here and the installed receipt, its pin and a
// receiver hold were lost.
func TestSnapshotSharingCancelAfterPublicationDeliversReceipt(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	donor := persistedListTestResult(t, ctx, c, srv, "publish-donor", newShareTestValue("donor", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}}))
	receiverValue := newShareTestValue("receiver", map[string]sharePartState{"fs": {}})
	receiver := persistedListTestResult(t, ctx, c, srv, "publish-receiver", receiverValue)
	row := receiver.cacheSharedResult()
	c.egraphMu.Lock()
	row.imported = true
	c.egraphMu.Unlock()
	partTestEquivalent(t, c, receiver, donor)
	receiverHolds := shareTestHolds(c, receiver)

	// Commit's last act, after every lock is released, is its fixture event.
	// Holding the fixture's mutex parks the Body between publication and its
	// receipt report.
	fixture := new(partFixtureState)
	fixture.mu.Lock()
	release := sync.OnceFunc(fixture.mu.Unlock)
	defer release()
	manager.afterPin = func() { c.partFixture.Store(fixture) }
	published := make(chan struct{})
	receiverValue.afterStoreUnlock = sync.OnceFunc(func() { close(published) })

	content := digest.FromString("cancel-after-publication")
	donorErr := c.TeachContentDigest(ctx, donor, content)
	receiverErr := c.TeachContentDigest(ctx, receiver, content)
	var publishedInTime bool
	select {
	case <-published:
		publishedInTime = true
	case <-time.After(10 * time.Second):
	}
	c.closeSnapshotSharing()
	var completedEarly bool
	select {
	case <-barrier.passes:
		completedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	release()
	require.NoError(t, donorErr)
	require.NoError(t, receiverErr)
	require.True(t, publishedInTime, "the slot never published")
	require.False(t, completedEarly, "the pass completed while its Body still held an undelivered receipt")
	require.Equal(t, 1, barrier.awaitPass(t))

	key, _ := partAddressKey(PersistedPartAddress{Part: "fs"})
	gate := row.partGate.gate.Load()
	gate.mu.Lock()
	phase := gate.outputs[key].phase
	gate.mu.Unlock()
	require.Equal(t, PartComplete, phase, "the delivered receipt was finished and the output settled")
	require.Equal(t, "fs-snap", receiverValue.Parts["fs"].Snapshot)
	require.Equal(t, receiverHolds, shareTestHolds(c, receiver), "the receipt's and the task's receiver holds ended")
	require.Equal(t, int32(1), manager.released.Load(), "the installed part's protection was released once")
}

// FailedFinishLastOwner: the receiver loses its ordinary owners while the
// external Finish is paused, and that Finish's owner synchronization then
// fails. Once the receipt's and the task's own holds end, nothing owns the
// row: it is collected and its protection is released exactly once. The
// retained bookkeeping owns no receipt and no hold of its own. The control
// with an ordinary owner is TestSnapshotSharingFailedFinishAndRetry.
func TestSnapshotSharingFailedFinishLastOwner(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	row := receiver.cacheSharedResult()
	attachErr := errors.New("attach lease failed")
	manager.attachErr.Store(&attachErr)
	paused, resume := make(chan struct{}), make(chan struct{})
	resumeFinish := sync.OnceFunc(func() { close(resume) })
	defer resumeFinish()
	c.testBeforeShareFinish = func(*ReadyPartReceipt) {
		close(paused)
		<-resume
	}
	released := make(chan struct{})
	manager.beforeRelease = sync.OnceFunc(func() { close(released) })

	shareTestUnite(t, ctx, c, "failed-finish-last-owner", donor, receiver)
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("the pass never reached its Finish phase")
	}
	// Remove the receiver's ordinary owners: the session and the saved edge.
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err := c.removePersistedEdge(ctx, row.id)
	require.NoError(t, err)
	c.egraphMu.RLock()
	installedRegistered := c.resultsByID[row.id] == row
	c.egraphMu.RUnlock()
	require.True(t, installedRegistered, "the receipt and the task still hold the installed receiver")
	require.Zero(t, manager.released.Load())

	resumeFinish()
	require.Equal(t, 1, barrier.awaitPass(t))
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("the unowned receiver's protection was never released")
	}
	c.egraphMu.RLock()
	_, registered := c.resultsByID[row.id]
	holds := row.incomingOwnershipCount
	c.egraphMu.RUnlock()
	require.False(t, registered, "with no owner left the receiver is collected")
	require.Zero(t, holds, "the failed Finish's retry state owns no receipt and no hold")
	drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	require.NoError(t, c.waitForQuiescence(drain))
	require.Equal(t, int32(1), manager.pins.Load())
	require.Equal(t, int32(1), manager.released.Load(), "protection is released exactly once")
}

// EncodedInstallRetryAfterDecode: owner synchronization fails after an encoded
// Commit, the receiver is then decoded, and a demand for the installed part
// retries only the owning generation's bookkeeping: no second Prepare, pin or
// open. The partial variant fails the attachment of one of two installed
// snapshots. A canceled Finish has no sharing variant: the pass finishes on an
// uncancelable context, and batch 4's TestReadyPartReceipt covers a canceled
// external Finish.
func TestSnapshotSharingEncodedInstallRetryAfterDecode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		fails func(snapshotID string) bool
	}{
		{"failed attachment", []string{"fs"}, func(string) bool { return true }},
		{"partial attachment", []string{"fs", "mount"}, func(id string) bool { return id == "mount-snap" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c, srv, manager := shareTestCache(t)
			barrier := newSharePassBarrier(c)
			donorParts, receiverParts := map[string]sharePartState{}, map[string]sharePartState{}
			for _, part := range tc.parts {
				donorParts[part] = sharePartState{Snapshot: part + "-snap"}
				receiverParts[part] = sharePartState{}
			}
			donor, receiver := shareTestPair(t, ctx, c, srv, donorParts, receiverParts)
			row := receiver.cacheSharedResult()
			attachErr := errors.New("attach lease failed")
			manager.attachFails = func(id string) error {
				if tc.fails(id) {
					return attachErr
				}
				return nil
			}
			shareTestUnite(t, ctx, c, "retry-after-decode-"+tc.name, donor, receiver)
			require.Equal(t, len(tc.parts), barrier.awaitPass(t))

			phases := func() map[string]PartOutputPhase {
				out := map[string]PartOutputPhase{}
				gate := row.partGate.gate.Load()
				gate.mu.Lock()
				defer gate.mu.Unlock()
				for _, part := range tc.parts {
					key, _ := partAddressKey(PersistedPartAddress{Part: PartKey(part)})
					out[part] = gate.outputs[key].phase
				}
				return out
			}
			for part, phase := range phases() {
				require.Equal(t, PartOutputInstalled, phase, "%s stays installed after the failed synchronization", part)
			}

			// Decode the receiver while its installed outputs await bookkeeping.
			// The decode's own row lease synchronization may attach the
			// installed roles, so the fault is lifted first; it must settle
			// nothing and release no protection.
			manager.attachFails = nil
			loaded, err := c.LoadResultByResultID(ctx, "", srv, uint64(row.id))
			require.NoError(t, err)
			decoded, ok := loaded.Unwrap().(*shareTestValue)
			require.True(t, ok)
			for _, part := range tc.parts {
				require.Equal(t, part+"-snap", decoded.Parts[part].Snapshot)
			}
			for part, phase := range phases() {
				require.Equal(t, PartOutputInstalled, phase, "decoding settles nothing: %s", part)
			}

			// The retry: demand every installed part.
			pins, opens, released := manager.pins.Load(), manager.opens.Load(), manager.released.Load()
			require.Zero(t, released, "protection is retained for the bookkeeping retry")
			for _, part := range tc.parts {
				require.NoError(t, c.demandPart(ctx, loaded, PersistedPartAddress{Part: PartKey(part)}))
			}
			for part, phase := range phases() {
				require.Equal(t, PartComplete, phase, "the retry completed %s's owning bookkeeping", part)
			}
			require.Equal(t, pins, manager.pins.Load(), "the retry never prepares or pins again")
			require.Equal(t, opens, manager.opens.Load(), "and opens nothing")
			require.Equal(t, int32(len(tc.parts)), manager.released.Load(), "each protection is released once, by its owning generation")
		})
	}
}

// Trigger sites of a live import: every imported row's final classes are
// queued in the import's own E interval, and an import that publishes nothing
// queues nothing.
func TestSnapshotSharingImportTriggers(t *testing.T) {
	ctx, a, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, a, srv, "import-root", String("value"))
	bundle := exportTestBundle(t, ctx, a, root)

	t.Run("live import queues the imported classes", func(t *testing.T) {
		ctx, b, _, _ := shareTestCache(t)
		barrier := newSharePassBarrier(b)
		mapping, err := b.ImportValues(ctx, bundle)
		require.NoError(t, err)
		require.Len(t, mapping, 1)
		require.Equal(t, 0, barrier.awaitPass(t), "the import queued a cohort; with no donor it plans nothing")
	})
	for name, arm := range map[string]func(*Cache, context.CancelFunc){
		"a failed identity plan queues nothing": func(b *Cache, _ context.CancelFunc) {
			b.testTransferPlanPrepared = func(int) error { return errors.New("identity plan refused") }
		},
		"a failed root validation queues nothing": func(b *Cache, cancel context.CancelFunc) {
			b.testBeforeTransferCommit = cancel
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, b, _, _ := shareTestCache(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			arm(b, cancel)
			_, err := b.ImportValues(ctx, bundle)
			require.Error(t, err)
			b.egraphMu.RLock()
			pendingCount := len(b.sharePending)
			workerStarted := b.shareWorkerStarted
			notifyNil := b.shareNotify == nil
			b.egraphMu.RUnlock()
			require.Zero(t, pendingCount)
			require.False(t, workerStarted, "nothing was ever queued, so no worker was started")
			require.True(t, notifyNil, "the failed import left no collector behind")
		})
	}
}

// A refused address in the middle of a receiver's sequence costs only itself:
// the last successful prefix is carried over it, so the address after it
// prepares from that prefix and installs in the same pass.
func TestSnapshotSharingPrefixCarriedOverRefusedAddress(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"a": {Snapshot: "a-snap"}, "b": {Snapshot: "b-snap"}, "c": {Snapshot: "c-snap"}},
		map[string]sharePartState{"a": {}, "b": {}, "c": {}},
	)
	// Keep the middle address busy, so its slot is refused admission.
	blocked, entered := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(blocked) })
	defer unblock()
	taskDone := make(chan error, 1)
	go func() {
		taskDone <- c.RunLazyTask(ctx, receiver, partTaskKey("obtain", PersistedPartAddress{Part: "b"}), LazyTaskSpec{
			Body: func(context.Context) error {
				close(entered)
				<-blocked
				return nil
			},
		})
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the preseeded obtain task never started")
	}
	// What the first pass installed, read at its end on the worker's own
	// goroutine, before any successor pass can start.
	var firstPass struct {
		once    sync.Once
		a, b, c bool
		pins    int32
		// released counts references given back: with an encoded receiver the
		// pass opens none, so these are its transient pins.
		released int32
	}
	passed := c.testAfterSharePass
	c.testAfterSharePass = func(item *snapshotShareItem) {
		firstPass.once.Do(func() {
			firstPass.a, firstPass.b, firstPass.c = shareTestHasLink(receiver, "a-snap"), shareTestHasLink(receiver, "b-snap"), shareTestHasLink(receiver, "c-snap")
			firstPass.pins = manager.pins.Load()
			firstPass.released = manager.released.Load()
		})
		passed(item)
	}
	shareTestUnite(t, ctx, c, "prefix-carried", donor, receiver)
	require.Equal(t, 3, barrier.awaitPass(t))
	require.True(t, firstPass.a, "the first address installs")
	require.False(t, firstPass.b, "the busy address is refused")
	require.True(t, firstPass.c, "the address after the refused one installs in the same pass")
	require.Equal(t, int32(2), firstPass.pins)
	require.Equal(t, firstPass.pins, firstPass.released, "zero transient pins afterwards: the pass released every pin it took, and took none for the refused address")
	unblock()
	select {
	case err := <-taskDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the preseeded obtain task never finished")
	}
}

// A decoded receiver's one slot per pass is spent only by a slot that passed
// the slot-context checks. Its first address here needs a service
// reconstruction context that is not registered, so it is ineligible; the
// service-free sibling still installs in the same pass.
func TestSnapshotSharingTypedIneligibleFirstAddress(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	donor := persistedListTestResult(t, ctx, c, srv, "ineligible-donor", newShareTestValue("donor", map[string]sharePartState{
		"fs": {Snapshot: "fs-snap", Service: 999}, "mount": {Snapshot: "mount-snap"},
	}))
	receiver := persistedListTestResult(t, ctx, c, srv, "ineligible-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}, "mount": {}}))
	c.egraphMu.Lock()
	receiver.cacheSharedResult().imported = true
	c.egraphMu.Unlock()
	partTestEquivalent(t, c, receiver, donor)
	shareTestUnite(t, ctx, c, "typed-ineligible-first", donor, receiver)

	require.Equal(t, 1, barrier.awaitPass(t), "the eligible sibling is planned: %v", barrier.skipCauses())
	require.True(t, shareTestHasLink(receiver, "mount-snap"), "the ineligible service address did not suppress its service-free sibling")
	require.False(t, shareTestHasLink(receiver, "fs-snap"))
	require.Equal(t, int32(1), manager.pins.Load())
}

// A decoded receiver fills one part per pass. When its slot installs and
// selection left it a further address, the successor cohort is queued in the
// E section that releases the active cohort, so a donor whose last owner is
// that cohort stays held until the successor has taken the next part. The
// donor keeps no ordinary owner across the two passes.
func TestSnapshotSharingTypedSuccessorHoldsTheDonor(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	release := barrier.holdPasses()
	defer release()
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	donor := persistedListTestResult(t, ctx, c, srv, "window-donor", newShareTestValue("donor", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}, "mount": {Snapshot: "mount-snap"}}))
	receiverValue := newShareTestValue("receiver", map[string]sharePartState{"fs": {}, "mount": {}})
	receiver := persistedListTestResult(t, ctx, c, srv, "window-receiver", receiverValue)
	donorRow := donor.cacheSharedResult()
	c.egraphMu.Lock()
	receiver.cacheSharedResult().imported = true
	c.egraphMu.Unlock()
	partTestEquivalent(t, c, receiver, donor)
	shareTestUnite(t, ctx, c, "typed-successor", donor, receiver)
	require.Equal(t, 1, barrier.awaitEntered(t), "a decoded receiver plans one slot")

	// With the first pass parked at its start, take away every ordinary owner
	// of the donor: its session and its saved edge. The cohort holds it alone.
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err := c.removePersistedEdge(ctx, donorRow.id)
	require.NoError(t, err)
	c.egraphMu.RLock()
	donorRegistered := c.resultsByID[donorRow.id] == donorRow
	c.egraphMu.RUnlock()
	require.True(t, donorRegistered)

	release()
	require.Equal(t, 1, barrier.awaitPass(t), "the first pass installs one part")
	require.Equal(t, 1, barrier.awaitPass(t), "the successor still finds the donor and installs the other")
	require.Equal(t, "fs-snap", receiverValue.Parts["fs"].Snapshot)
	require.Equal(t, "mount-snap", receiverValue.Parts["mount"].Snapshot)
	require.Equal(t, int32(2), manager.pins.Load())
}

// The decode preflight covers exactly what a shared attempt would decode. A
// row that is already typed is decoded by nobody, so its unadmitted family
// does not make a slot ineligible; the same row encoded does. An admitted
// family whose native class the supplied decoding server lacks is ineligible
// too, decided before any shared decode attempt exists.
func TestSnapshotSharingDecodePreflight(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	// transferTestValue's family is not admitted for background decode.
	unadmitted := persistedListTestResult(t, ctx, c, srv, "preflight-unadmitted", &transferTestValue{Text: "typed"})
	unadmittedID := uint64(unadmitted.cacheSharedResult().id)
	require.NoError(t, c.preflightShareDecode(ctx, srv, []uint64{unadmittedID}), "an already typed row is outside the decode closure")
	shareTestEncodedReceiver(t, ctx, c, unadmitted)
	err := c.preflightShareDecode(ctx, srv, []uint64{unadmittedID})
	require.ErrorIs(t, err, ErrSnapshotShareIneligible)
	require.ErrorContains(t, err, "not admitted for background decode")

	admitted := persistedListTestResult(t, ctx, c, srv, "preflight-admitted", newShareTestValue("admitted", map[string]sharePartState{"fs": {}}))
	shareTestEncodedReceiver(t, ctx, c, admitted)
	admittedID := uint64(admitted.cacheSharedResult().id)
	require.NoError(t, c.preflightShareDecode(ctx, srv, []uint64{admittedID}))
	bare := newDagqlServerForTest(t, &persistCodecRoot{})
	err = c.preflightShareDecode(ctx, bare, []uint64{admittedID})
	require.ErrorIs(t, err, ErrSnapshotShareIneligible)
	require.ErrorContains(t, err, "no native class")

	require.ErrorIs(t, c.preflightShareDecode(ctx, srv, []uint64{1 << 40}), ErrSnapshotShareIneligible, "an unregistered exact reference is ineligible")
}

// A donor whose row-wide lease reconciliation is in flight is busy for the
// pass: the probe sees the held lease guard without waiting for it, and
// selects nothing from that row. Once the reconciliation has ended a later
// trigger shares as usual.
func TestSnapshotSharingDonorLeaseReconciliationIsBusy(t *testing.T) {
	ctx, c, srv, manager := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	donorRow := donor.cacheSharedResult()
	// What syncResultSnapshotLeases holds for the whole of a reconciliation.
	donorRow.leaseSyncMu.Lock()
	reconciled := sync.OnceFunc(donorRow.leaseSyncMu.Unlock)
	defer reconciled()
	content := digest.FromString("lease-reconciliation")
	donorErr := c.TeachContentDigest(ctx, donor, content)
	receiverErr := c.TeachContentDigest(ctx, receiver, content)
	planned, passErr := barrier.waitPass()
	pins := manager.pins.Load()
	reconciled()
	require.NoError(t, donorErr)
	require.NoError(t, receiverErr)
	require.NoError(t, passErr)
	require.Equal(t, 0, planned, "a reconciling donor is not Ready, and the probe did not wait for it")
	require.Zero(t, pins)
	c.notifySnapshotShareCompletion(ctx, donorRow)
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "fs-snap"))
}

// The preparation callback is registered once, before admission is enabled,
// and never after close.
func TestSnapshotSharingPreparationContextIsSetOnce(t *testing.T) {
	prepare := func(ctx context.Context) (context.Context, *Server, error) { return ctx, nil, nil }

	_, c, _ := transferTestCache(t)
	require.Error(t, c.SetPartPreparationContext(nil))
	require.NoError(t, c.SetPartPreparationContext(prepare))
	require.ErrorContains(t, c.SetPartPreparationContext(prepare), "already registered")
	require.NoError(t, c.EnableSnapshotSharing())

	enabled := newShareTestEnv(t).cache
	require.ErrorContains(t, enabled.SetPartPreparationContext(prepare), "already enabled", "a first registration after admission is refused")

	_, closed, _ := transferTestCache(t)
	closed.closeSnapshotSharing()
	require.ErrorIs(t, closed.SetPartPreparationContext(prepare), ErrSnapshotSharingClosed)
}
