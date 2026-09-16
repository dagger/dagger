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
	Kind     string               `json:"kind"`
	ResultID uint64               `json:"resultID"`
	Field    string               `json:"field"`
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
	state := c.partFixture.Load()
	if state == nil {
		return
	}
	event := TransferFixturePartEvent{Kind: kind, Address: clonePartAddress(address)}
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
