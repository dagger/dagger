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
	Kind       string                     `json:"kind"`
	ResultID   uint64                     `json:"resultID"`
	Field      string                     `json:"field"`
	Address    PersistedPartAddress       `json:"address"`
	SnapshotID string                     `json:"snapshotID,omitempty"`
	Source     *TransferFixturePartSource `json:"source,omitempty"`
}

type TransferFixturePartSource struct {
	ResultID uint64               `json:"resultID"`
	Address  PersistedPartAddress `json:"address"`
}
type partFixtureState struct {
	mu     sync.Mutex
	events []TransferFixturePartEvent
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
func (c *Cache) recordPartFixtureEvent(row *sharedResult, address PersistedPartAddress, kind, snapshotID string, source *TransferFixturePartSource) {
	state := c.partFixture.Load()
	if state == nil {
		return
	}
	event := TransferFixturePartEvent{Kind: kind, Address: clonePartAddress(address), SnapshotID: snapshotID}
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
	state.events = append(state.events, event)
	state.mu.Unlock()
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
func (c *Cache) partFixtureEvents() []TransferFixturePartEvent {
	state := c.partFixture.Load()
	if state == nil {
		return nil
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
	return events
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
	return p.InfoReaderProvider.ReaderAt(ctx, desc)
}
