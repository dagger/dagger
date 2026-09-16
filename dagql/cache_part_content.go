package dagql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Provider construction is metadata-only. ReaderAt is the missing-byte boundary.
type PartContentSource interface {
	Provider(context.Context, PersistedPartOffer, *PartDemandState) content.InfoReaderProvider
}
type partContentSourceBinding struct{ source PartContentSource }

func (c *Cache) SetPartContentSource(source PartContentSource) {
	if source == nil {
		c.partContentSource.Store(nil)
	} else {
		c.partContentSource.Store(&partContentSourceBinding{source: source})
	}
}

type fixedPartContentSource struct{}

func (fixedPartContentSource) Provider(_ context.Context, offer PersistedPartOffer, _ *PartDemandState) content.InfoReaderProvider {
	return &fixedPartProvider{offer: offer, client: http.DefaultClient}
}

type fixedPartProvider struct {
	offer  PersistedPartOffer
	client *http.Client
}

func (p *fixedPartProvider) Info(_ context.Context, d digest.Digest) (content.Info, error) {
	for _, l := range p.offer.Chain.Layers {
		if l.Descriptor.Digest == d {
			return content.Info{Digest: d, Size: l.Descriptor.Size}, nil
		}
	}
	return content.Info{}, fmt.Errorf("unknown supplied blob %s", d)
}
func (p *fixedPartProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	a, ok := p.offer.Chain.Addresses[desc.Digest]
	if !ok || a.URL == "" || (a.ExpiresAtUnix != 0 && a.ExpiresAtUnix <= time.Now().Unix()) {
		return nil, fmt.Errorf("supplied blob %s has no usable address", desc.Digest)
	}
	if desc.Size < 0 {
		return nil, fmt.Errorf("supplied blob has negative size")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &partHTTPReader{ctx: ctx, cancel: cancel, client: p.client, url: a.URL, size: desc.Size}, nil
}

// Range reads bound memory to the importer's buffer. Closing a reader cancels
// outstanding requests; no response body or donor row outlives the import.
type partHTTPReader struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *http.Client
	url    string
	size   int64
	once   sync.Once
}

func (r *partHTTPReader) Size() int64  { return r.size }
func (r *partHTTPReader) Close() error { r.once.Do(r.cancel); return nil }
func (r *partHTTPReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("negative blob offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	limit := len(p)
	if int64(limit) > r.size-off {
		limit = int(r.size - off)
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(limit)-1))
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && (off != 0 || resp.StatusCode != http.StatusOK) {
		return 0, fmt.Errorf("blob request: HTTP %d", resp.StatusCode)
	}
	n, err := io.ReadFull(resp.Body, p[:limit])
	if err == nil && limit < len(p) {
		err = io.EOF
	}
	return n, err
}

type partDemandContextKey struct{}

func partDemandFromContext(ctx context.Context) *PartDemandState {
	d, _ := ctx.Value(partDemandContextKey{}).(*PartDemandState)
	return d
}
func (d *PartDemandState) exhaust(source *PartSourceLease, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.exhaustedContent == nil {
		d.exhaustedContent = map[string]struct{}{}
	}
	d.exhaustedContent[partContentKey(sharedResultID(source.sourceID), source.descriptor.Address, source.offer)] = struct{}{}
	d.failures = append(d.failures, partContentFailure{source: source.sourceID, address: clonePartAddress(source.descriptor.Address), offerRevision: source.offerRev, cause: err})
	d.revision++
}

type partContentFailure struct {
	source        uint64
	address       PersistedPartAddress
	offerRevision uint64
	cause         error
}

func (d *PartDemandState) causes() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var err error
	for _, f := range d.failures {
		err = errors.Join(err, f.cause)
	}
	return err
}
func (c *Cache) installChainPart(ctx context.Context, receiver AnyResult, source *PartSourceLease, permit *PartPermit, demand *PartDemandState) (rerr error) {
	handedOff := false
	defer func() {
		if !handedOff {
			rerr = errors.Join(rerr, source.Release(ctx))
			permit.Release()
		}
	}()
	if c.snapshotManager == nil {
		return fmt.Errorf("chain acquisition: no snapshot manager")
	}
	var contentSource PartContentSource = fixedPartContentSource{}
	if binding := c.partContentSource.Load(); binding != nil {
		contentSource = binding.source
	}
	copied, err := clonePartOffers([]PersistedPartOffer{*source.offer})
	if err != nil {
		return err
	}
	provider := contentSource.Provider(ctx, copied[0], demand)
	imported, err := c.snapshotManager.ImportChain(ctx, &snapshots.ExportChain{Layers: copied[0].Chain.Layers, Provider: provider})
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		var contentErr *snapshots.ChainContentError
		if errors.As(err, &contentErr) {
			demand.exhaust(source, err)
			return ErrPartReselect
		}
		return err
	}
	defer func() { rerr = errors.Join(rerr, imported.Release(context.WithoutCancel(ctx))) }()
	source.descriptor.SnapshotID = imported.SnapshotID()
	handedOff = true
	return c.InstallReadyPart(ctx, receiver, source, permit)
}

// Only the owning installation can settle. Detachment uses the current slot,
// including replacements made after this acquisition was admitted.
func (c *Cache) settlePart(ctx context.Context, row *sharedResult, address PersistedPartAddress, ownerTask *PartTaskToken, generation uint64) error {
	ctx = context.WithoutCancel(ctx)
	c.egraphMu.Lock()
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	key, _ := partAddressKey(address)
	state := gate.outputs[key]
	if state.task != ownerTask || state.installation != generation {
		gate.mu.Unlock()
		c.egraphMu.Unlock()
		return fmt.Errorf("part settlement: installation changed")
	}
	if state.phase == PartComplete {
		gate.mu.Unlock()
		c.egraphMu.Unlock()
		return nil
	}
	queue, err := c.retireFinalPartOffersLocked(ctx, row, address)
	if err == nil {
		state.phase = PartComplete
		gate.outputs[key] = state
		gate.revision++
	}
	gate.mu.Unlock()
	callbacks, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(err, collectErr, runOnReleaseFuncs(ctx, callbacks))
}
func (c *Cache) retireFinalPartOffersLocked(ctx context.Context, row *sharedResult, address PersistedPartAddress) (collectionQueue, error) {
	return c.retirePartOfferLocked(ctx, row, address)
}
