package dagql

import (
	"context"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TransferFixturePartEvent is populated only after the gated fixture enables
// acquisition observation. It does not participate in cache decisions.
type TransferFixturePartEvent struct {
	// Sequence orders part events and reached points of one cache lifetime on
	// one counter, so a test can place an install inside a sharing pass.
	Sequence   uint64                     `json:"sequence"`
	Kind       string                     `json:"kind"`
	ResultID   uint64                     `json:"resultID"`
	Field      string                     `json:"field"`
	Address    PersistedPartAddress       `json:"address"`
	SnapshotID string                     `json:"snapshotID,omitempty"`
	Source     *TransferFixturePartSource `json:"source,omitempty"`
	// Detail is the cause of a share-skipped event, as text.
	Detail string `json:"detail,omitempty"`
}

type TransferFixturePartSource struct {
	ResultID uint64               `json:"resultID"`
	Address  PersistedPartAddress `json:"address"`
}
type partFixtureState struct {
	mu         sync.Mutex
	sequence   uint64
	events     []TransferFixturePartEvent
	reached    []FixtureObservation
	eventCap   int
	overflowed bool
	barriers   fixtureBarriers
}

// transferFixtureEventCap bounds the observed part events of one scenario.
const transferFixtureEventCap = 1 << 16

// SetTransferFixtureEventCap sets the bound and clears the events. An
// overflow is reported by TransferFixtureSnapshot as an error; events are
// never silently dropped.
func (c *Cache) SetTransferFixtureEventCap(n int) {
	state := c.partFixture.Load()
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.eventCap, state.events, state.overflowed = n, nil, false
}

func (c *Cache) EnableTransferFixtureParts() {
	c.partFixture.CompareAndSwap(nil, new(partFixtureState))
}
func (c *Cache) recordPartFixture(row *sharedResult, address PersistedPartAddress, kind string) {
	c.recordPartFixtureSnapshot(row, address, kind, "")
}
func (c *Cache) recordPartFixtureSnapshot(row *sharedResult, address PersistedPartAddress, kind, snapshotID string) {
	c.recordPartFixtureEvent(row, address, kind, snapshotID, nil)
}
func (c *Cache) recordPartFixtureDelegation(row *sharedResult, address PersistedPartAddress, kind string, proof *partDelegationProof) {
	if c.partFixture.Load() == nil {
		return
	}
	c.recordPartFixtureEvent(row, address, kind, "", &TransferFixturePartSource{ResultID: uint64(proof.parent.id), Address: clonePartAddress(proof.source)})
}

// recordPartFixtureSkip records a slot a sharing pass left alone, with why.
func (c *Cache) recordPartFixtureSkip(row *sharedResult, address PersistedPartAddress, cause error) {
	if c.partFixture.Load() == nil {
		return
	}
	c.recordPartFixtureDetail(row, address, "share-skipped", "", nil, cause.Error())
}

func (c *Cache) recordPartFixtureEvent(row *sharedResult, address PersistedPartAddress, kind, snapshotID string, source *TransferFixturePartSource) {
	c.recordPartFixtureDetail(row, address, kind, snapshotID, source, "")
}

func (c *Cache) recordPartFixtureDetail(row *sharedResult, address PersistedPartAddress, kind, snapshotID string, source *TransferFixturePartSource, detail string) {
	state := c.partFixture.Load()
	if state == nil {
		return
	}
	event := TransferFixturePartEvent{Kind: kind, Address: clonePartAddress(address), SnapshotID: snapshotID, Detail: detail}
	if kind == "selected-delegation" || kind == "installed-delegation" {
		event.Source = source
	}
	if row != nil {
		event.ResultID = uint64(row.id)
		if frame := row.loadResultCall(); frame != nil {
			event.Field = frame.Field
		}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	limit := state.eventCap
	if limit == 0 {
		limit = transferFixtureEventCap
	}
	if len(state.events)+len(state.reached) >= limit {
		state.overflowed = true
		return
	}
	state.sequence++
	event.Sequence = state.sequence
	state.events = append(state.events, event)
}

type partFixtureReleaseKey struct{}

// TransferFixtureLazyReleaseObserver is present only during a private
// operation invoked with the environment-gated fixture enabled.
func TransferFixtureLazyReleaseObserver(ctx context.Context) func(string, error) {
	observer, _ := ctx.Value(partFixtureReleaseKey{}).(func(string, error))
	return observer
}

func (c *Cache) partFixtureLazyContext(ctx context.Context, row *sharedResult, address PersistedPartAddress) context.Context {
	if c.partFixture.Load() == nil {
		return ctx
	}
	return context.WithValue(ctx, partFixtureReleaseKey{}, func(id string, err error) {
		kind := "lazy-ref-released"
		if err != nil {
			kind = "lazy-ref-release-error"
		}
		c.recordPartFixtureSnapshot(row, address, kind, id)
	})
}

// FixtureObservation is one reached point of §3.3's closed set, recorded
// whether or not a barrier was armed there. It carries the correlation the
// part events lack: the demand's task generation, the sharing pass, the chosen
// source route, the exchange.
type FixtureObservation struct {
	Sequence uint64 `json:"sequence"`
	FixtureBarrierEvent
}

// observeFixtureReach journals one reached point under the same bound as the
// part events. It never blocks and never changes the operation.
func (state *partFixtureState) observeFixtureReach(event FixtureBarrierEvent) {
	if event.Address != nil {
		address := clonePartAddress(*event.Address)
		event.Address = &address
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	limit := state.eventCap
	if limit == 0 {
		limit = transferFixtureEventCap
	}
	if len(state.events)+len(state.reached) >= limit {
		state.overflowed = true
		return
	}
	state.sequence++
	state.reached = append(state.reached, FixtureObservation{Sequence: state.sequence, FixtureBarrierEvent: event})
}

func (c *Cache) partFixtureReached() []FixtureObservation {
	state := c.partFixture.Load()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	reached := make([]FixtureObservation, len(state.reached))
	for i, o := range state.reached {
		reached[i] = o
		if o.Address != nil {
			address := clonePartAddress(*o.Address)
			reached[i].Address = &address
		}
	}
	return reached
}

func (c *Cache) partFixtureEvents() (_ []TransferFixturePartEvent, overflowed bool) {
	state := c.partFixture.Load()
	if state == nil {
		return nil, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	events := make([]TransferFixturePartEvent, len(state.events))
	for i, e := range state.events {
		events[i] = e
		events[i].Address = clonePartAddress(e.Address)
		if e.Source != nil {
			events[i].Source = &TransferFixturePartSource{ResultID: e.Source.ResultID, Address: clonePartAddress(e.Source.Address)}
		}
	}
	return events, state.overflowed
}

type partFixtureProvider struct {
	content.InfoReaderProvider
	cache   *Cache
	row     *sharedResult
	address PersistedPartAddress
}

func (p partFixtureProvider) Info(ctx context.Context, id digest.Digest) (content.Info, error) {
	return p.InfoReaderProvider.Info(ctx, id)
}
func (p partFixtureProvider) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	p.cache.recordPartFixture(p.row, p.address, "provider-read")
	// The real provider's open boundary: an injected failure returns no
	// reader, exactly as a failed open does.
	event := FixtureBarrierEvent{Point: FixtureChainReaderOpen, ResultID: uint64(p.row.id), Address: &p.address, Detail: desc.Digest.String()}
	if err := p.cache.fixtureReach(ctx, event); err != nil {
		return nil, err
	}
	reader, err := p.InfoReaderProvider.ReaderAt(ctx, desc)
	if err != nil {
		return nil, err
	}
	return &partFixtureReader{ReaderAt: reader, ctx: ctx, cache: p.cache, event: event}, nil
}

// partFixtureReader decorates the actual chain reader. A read fault is a
// truncated stream at that read boundary: earlier bytes and the Close
// obligation stay real. A close fault closes the actual reader first and
// never hides its error.
type partFixtureReader struct {
	content.ReaderAt
	ctx   context.Context
	cache *Cache
	event FixtureBarrierEvent
}

func (r *partFixtureReader) ReadAt(b []byte, off int64) (int, error) {
	event := r.event
	event.Point = FixtureChainRead
	if err := r.cache.fixtureReach(r.ctx, event); err != nil {
		return 0, err
	}
	return r.ReaderAt.ReadAt(b, off)
}

func (r *partFixtureReader) Close() error {
	err := r.ReaderAt.Close()
	event := r.event
	event.Point = FixtureChainClose
	if fault := r.cache.fixtureReach(r.ctx, event); fault != nil && err == nil {
		err = fault
	}
	return err
}
