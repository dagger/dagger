package dagql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// Barriers of the environment-gated test fixture. A barrier observes or pauses
// the actual implementation at a named point; it never supplies a result. The
// hooks are nil off-gate: with no fixture state the single check in
// fixtureReach returns at once.
//
// A pause never waits while holding E, a gate, a row's D or P, a core accessor
// or body latch, a mailbox mutex or gcmu: every call site stands immediately
// before entering or after leaving such a section.

// FixtureBarrierPoint is one of the closed set of points.
type FixtureBarrierPoint string

const (
	FixtureSourceSelected       FixtureBarrierPoint = "sourceSelected"
	FixturePrepareDone          FixtureBarrierPoint = "prepareDone"
	FixtureBeforeCommit         FixtureBarrierPoint = "beforeCommit"
	FixtureCommitPublished      FixtureBarrierPoint = "commitPublished"
	FixtureSharePassTaken       FixtureBarrierPoint = "sharePassTaken"
	FixtureShareAllPrepared     FixtureBarrierPoint = "shareAllPrepared"
	FixtureShareMembersReleased FixtureBarrierPoint = "shareMembersReleased"
	FixtureBeforeFinish         FixtureBarrierPoint = "beforeFinish"
	FixtureBeforeOwnerAttach    FixtureBarrierPoint = "beforeOwnerAttach"
	FixtureAfterOwnerAttach     FixtureBarrierPoint = "afterOwnerAttach"
	FixtureOwnerSyncDone        FixtureBarrierPoint = "ownerSyncDone"
	FixtureBeforeBeginOriginal  FixtureBarrierPoint = "beforeBeginOriginal"
	FixtureOriginalSealed       FixtureBarrierPoint = "originalSealed"
	FixtureLazyEntry            FixtureBarrierPoint = "lazyEntry"
	FixtureChainReaderOpen      FixtureBarrierPoint = "chainReaderOpen"
	FixtureChainRead            FixtureBarrierPoint = "chainRead"
	FixtureChainClose           FixtureBarrierPoint = "chainClose"
	FixtureRenewalEnqueued      FixtureBarrierPoint = "renewalEnqueued"
	FixtureRenewalDelivered     FixtureBarrierPoint = "renewalDelivered"
	FixtureRenewalReplied       FixtureBarrierPoint = "renewalReplied"
	FixtureDecodeCopied         FixtureBarrierPoint = "decodeCopied"
	FixtureDecodeBeforePublish  FixtureBarrierPoint = "decodeBeforePublish"
)

// FixtureBarrierAction is one of the closed set of actions. A fault may fail
// only an operation production can itself fail; none fabricates success,
// readiness, a revision or an ownership result.
type FixtureBarrierAction string

const (
	FixturePause                 FixtureBarrierAction = "pause"
	FixtureFailOwnerAttachBefore FixtureBarrierAction = "failOwnerAttachBefore"
	FixtureFailOwnerAttachAfter  FixtureBarrierAction = "failOwnerAttachAfter"
	FixtureFailChainOpen         FixtureBarrierAction = "failChainOpen"
	FixtureFailChainRead         FixtureBarrierAction = "failChainRead"
	FixtureFailChainClose        FixtureBarrierAction = "failChainClose"
)

// The named errors the error actions return. failChainRead returns
// io.ErrUnexpectedEOF, as a truncated stream does.
var (
	ErrFixtureLocalStorage = errors.New("fixture local storage fault")
	ErrFixtureTransport    = errors.New("fixture transport fault")
	ErrFixtureClose        = errors.New("fixture close fault")
)

var fixtureBarrierPoints = []FixtureBarrierPoint{
	FixtureSourceSelected, FixturePrepareDone, FixtureBeforeCommit, FixtureCommitPublished,
	FixtureSharePassTaken, FixtureShareAllPrepared, FixtureShareMembersReleased, FixtureBeforeFinish,
	FixtureBeforeOwnerAttach, FixtureAfterOwnerAttach, FixtureOwnerSyncDone,
	FixtureBeforeBeginOriginal, FixtureOriginalSealed, FixtureLazyEntry,
	FixtureChainReaderOpen, FixtureChainRead, FixtureChainClose,
	FixtureRenewalEnqueued, FixtureRenewalDelivered, FixtureRenewalReplied,
	FixtureDecodeCopied, FixtureDecodeBeforePublish,
}

// fixtureBarrierFaults is the one legal point of each error action.
var fixtureBarrierFaults = map[FixtureBarrierAction]FixtureBarrierPoint{
	FixtureFailOwnerAttachBefore: FixtureBeforeOwnerAttach,
	FixtureFailOwnerAttachAfter:  FixtureAfterOwnerAttach,
	FixtureFailChainOpen:         FixtureChainReaderOpen,
	FixtureFailChainRead:         FixtureChainRead,
	FixtureFailChainClose:        FixtureChainClose,
}

// fixtureFaultError is what an error action injects at its point.
func fixtureFaultError(action FixtureBarrierAction) error {
	switch action {
	case FixtureFailOwnerAttachBefore, FixtureFailOwnerAttachAfter:
		return ErrFixtureLocalStorage
	case FixtureFailChainOpen:
		return ErrFixtureTransport
	case FixtureFailChainRead:
		return io.ErrUnexpectedEOF
	case FixtureFailChainClose:
		return ErrFixtureClose
	}
	return nil
}

// FixtureBarrierSelector narrows a barrier to an exact occurrence. Zero
// fields match anything; a set field must equal the reached event's.
type FixtureBarrierSelector struct {
	ResultID       uint64                `json:"resultID,omitempty"`
	Address        *PersistedPartAddress `json:"address,omitempty"`
	TaskGeneration uint64                `json:"taskGeneration,omitempty"`
	PassID         uint64                `json:"passID,omitempty"`
}

// FixtureBarrierRequest arms one barrier.
type FixtureBarrierRequest struct {
	Key      string                 `json:"key"`
	Point    FixtureBarrierPoint    `json:"point"`
	Selector FixtureBarrierSelector `json:"selector"`
	// Occurrence is the 1-based matching occurrence that fires; zero means
	// the first.
	Occurrence uint64               `json:"occurrence,omitempty"`
	Action     FixtureBarrierAction `json:"action"`
}

// FixtureBarrierEvent is the immutable observation captured at a point.
type FixtureBarrierEvent struct {
	Point          FixtureBarrierPoint   `json:"point"`
	ResultID       uint64                `json:"resultID,omitempty"`
	Address        *PersistedPartAddress `json:"address,omitempty"`
	TaskGeneration uint64                `json:"taskGeneration,omitempty"`
	PassID         uint64                `json:"passID,omitempty"`
	// Detail is a small point-specific observation: an outcome, a snapshot
	// identity, a count. It is never an instruction.
	Detail string `json:"detail,omitempty"`
}

// FixtureBarrierArmed and FixtureBarrierReached are the control results.
type FixtureBarrierArmed struct {
	Key        string `json:"key"`
	Generation uint64 `json:"generation"`
}
type FixtureBarrierReached struct {
	Key        string              `json:"key"`
	Generation uint64              `json:"generation"`
	Event      FixtureBarrierEvent `json:"event"`
	// Released is true once the occurrence was released or its operation's
	// context ended; a wait after that returns at once.
	Released bool `json:"released"`
}

type fixtureBarrier struct {
	req        FixtureBarrierRequest
	generation uint64
	seen       uint64
	fired      bool
	event      FixtureBarrierEvent
	reached    chan struct{}
	release    chan struct{}
	released   bool
}

type fixtureBarriers struct {
	mu         sync.Mutex
	generation uint64
	armed      map[string]*fixtureBarrier
	closed     bool
}

func validateFixtureBarrier(req FixtureBarrierRequest) error {
	if req.Key == "" {
		return fmt.Errorf("fixture barrier requires a key")
	}
	if !slices.Contains(fixtureBarrierPoints, req.Point) {
		return fmt.Errorf("unknown fixture barrier point %q", req.Point)
	}
	if req.Action == FixturePause {
		return nil
	}
	point, ok := fixtureBarrierFaults[req.Action]
	if !ok {
		return fmt.Errorf("unknown fixture barrier action %q", req.Action)
	}
	if point != req.Point {
		return fmt.Errorf("fixture barrier action %q is legal only at %q, not at %q", req.Action, point, req.Point)
	}
	return nil
}

// ArmTransferFixtureBarrier arms one one-shot barrier for this cache's
// lifetime. An unknown point or action, or an illegal pair, is rejected before
// anything is armed. Re-arming a key replaces its previous generation.
func (c *Cache) ArmTransferFixtureBarrier(req FixtureBarrierRequest) (FixtureBarrierArmed, error) {
	if err := validateFixtureBarrier(req); err != nil {
		return FixtureBarrierArmed{}, err
	}
	state := c.partFixture.Load()
	if state == nil {
		return FixtureBarrierArmed{}, fmt.Errorf("fixture barriers are not enabled")
	}
	b := &state.barriers
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return FixtureBarrierArmed{}, ErrCacheClosed
	}
	if old := b.armed[req.Key]; old != nil {
		old.releaseLocked()
	}
	if b.armed == nil {
		b.armed = map[string]*fixtureBarrier{}
	}
	b.generation++
	if req.Selector.Address != nil {
		address := clonePartAddress(*req.Selector.Address)
		req.Selector.Address = &address
	}
	b.armed[req.Key] = &fixtureBarrier{req: req, generation: b.generation, reached: make(chan struct{}), release: make(chan struct{})}
	return FixtureBarrierArmed{Key: req.Key, Generation: b.generation}, nil
}

func (b *fixtureBarrier) releaseLocked() {
	if !b.released {
		b.released = true
		close(b.release)
	}
}

func (b *fixtureBarriers) lookup(key string, generation uint64) (*fixtureBarrier, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	barrier := b.armed[key]
	if barrier == nil || barrier.generation != generation {
		return nil, fmt.Errorf("fixture barrier %q generation %d is not armed", key, generation)
	}
	return barrier, nil
}

// WaitTransferFixtureBarrier waits, within ctx, until the armed occurrence is
// reached, and returns its observation. It is idempotent after completion and
// cannot reopen an old generation.
func (c *Cache) WaitTransferFixtureBarrier(ctx context.Context, key string, generation uint64) (FixtureBarrierReached, error) {
	state := c.partFixture.Load()
	if state == nil {
		return FixtureBarrierReached{}, fmt.Errorf("fixture barriers are not enabled")
	}
	barrier, err := state.barriers.lookup(key, generation)
	if err != nil {
		return FixtureBarrierReached{}, err
	}
	select {
	case <-barrier.reached:
	case <-ctx.Done():
		return FixtureBarrierReached{}, context.Cause(ctx)
	}
	state.barriers.mu.Lock()
	defer state.barriers.mu.Unlock()
	return FixtureBarrierReached{Key: key, Generation: generation, Event: barrier.event, Released: barrier.released}, nil
}

// ReleaseTransferFixtureBarrier wakes that exact occurrence. Releasing twice,
// or releasing a barrier that fires later, is harmless: a released pause does
// not wait.
func (c *Cache) ReleaseTransferFixtureBarrier(key string, generation uint64) error {
	state := c.partFixture.Load()
	if state == nil {
		return fmt.Errorf("fixture barriers are not enabled")
	}
	barrier, err := state.barriers.lookup(key, generation)
	if err != nil {
		return err
	}
	state.barriers.mu.Lock()
	barrier.releaseLocked()
	state.barriers.mu.Unlock()
	return nil
}

// closeFixtureBarriers releases every paused operation so its real cleanup
// drains, and refuses later arming. It runs at cache close.
func (c *Cache) closeFixtureBarriers() {
	state := c.partFixture.Load()
	if state == nil {
		return
	}
	b := &state.barriers
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, barrier := range b.armed {
		barrier.releaseLocked()
	}
}

// TransferFixtureBarrierCount reports the barriers still armed and unfired.
func (c *Cache) TransferFixtureBarrierCount() int {
	state := c.partFixture.Load()
	if state == nil {
		return 0
	}
	b := &state.barriers
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, barrier := range b.armed {
		if !barrier.fired {
			n++
		}
	}
	return n
}

func (s FixtureBarrierSelector) matches(e FixtureBarrierEvent) bool {
	if s.ResultID != 0 && s.ResultID != e.ResultID {
		return false
	}
	if s.TaskGeneration != 0 && s.TaskGeneration != e.TaskGeneration {
		return false
	}
	if s.PassID != 0 && s.PassID != e.PassID {
		return false
	}
	if s.Address != nil {
		if e.Address == nil || e.Address.Part != s.Address.Part || !slices.Equal(e.Address.OutputPath, s.Address.OutputPath) {
			return false
		}
	}
	return true
}

// fixtureReach is the hook the implementation calls at a point. Off-gate it is
// one atomic load. It returns a non-nil error only for an error action armed
// at this exact occurrence; a pause returns nil after its release, or the
// operation's own cancellation cause.
func (c *Cache) fixtureReach(ctx context.Context, event FixtureBarrierEvent) error {
	state := c.partFixture.Load()
	if state == nil {
		return nil
	}
	state.observeFixtureReach(event)
	b := &state.barriers
	b.mu.Lock()
	var hit *fixtureBarrier
	for _, barrier := range b.armed {
		if barrier.fired || barrier.req.Point != event.Point || !barrier.req.Selector.matches(event) {
			continue
		}
		barrier.seen++
		want := barrier.req.Occurrence
		if want == 0 {
			want = 1
		}
		if barrier.seen != want {
			continue
		}
		barrier.fired = true
		if event.Address != nil {
			address := clonePartAddress(*event.Address)
			event.Address = &address
		}
		barrier.event = event
		close(barrier.reached)
		hit = barrier
		break
	}
	b.mu.Unlock()
	if hit == nil {
		return nil
	}
	if hit.req.Action != FixturePause {
		return fixtureFaultError(hit.req.Action)
	}
	select {
	case <-hit.release:
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		hit.releaseLocked()
		b.mu.Unlock()
		return context.Cause(ctx)
	}
}
