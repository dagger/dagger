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
	// Available is a pure address/bridge check; it must not request content.
	Available(PersistedPartOffer, int64) bool
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

func (fixedPartContentSource) Available(offer PersistedPartOffer, now int64) bool {
	return chainAddressesUsable(&offer, now)
}

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

// Reads are serialized only while using this ReaderAt's response. Range
// responses are bounded requests; a Range-ignoring endpoint keeps one body for
// consecutive offsets. No blob-sized buffer or shared provider state is used.
type partHTTPReader struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *http.Client
	url    string
	size   int64
	mu     sync.Mutex
	stream *partHTTPStream
	offset int64
	stop   func() bool
}

type partHTTPStream struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (s *partHTTPStream) Close() error {
	s.once.Do(func() { s.err = s.ReadCloser.Close() })
	return s.err
}
func (r *partHTTPReader) closeStream() {
	if r.stream != nil {
		r.stop()
		_ = r.stream.Close()
		r.stream, r.stop = nil, nil
	}
}
func (r *partHTTPReader) Close() error {
	// Cancel first: a read holding mu may be blocked on the transport.
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeStream()
	return nil
}
func (r *partHTTPReader) Size() int64 { return r.size }
func (r *partHTTPReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := context.Cause(r.ctx); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("negative content offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	limit := min(int64(len(p)), r.size-off)
	if r.stream != nil && off != r.offset {
		r.closeStream()
	}
	if r.stream == nil {
		req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+limit-1))
		resp, err := r.client.Do(req)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode == http.StatusPartialContent {
			defer resp.Body.Close()
			n, err := io.ReadFull(resp.Body, p[:limit])
			if err == nil && limit < int64(len(p)) {
				err = io.EOF
			}
			return n, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return 0, fmt.Errorf("content HTTP status %d", resp.StatusCode)
		}
		r.stream = &partHTTPStream{ReadCloser: resp.Body}
		stream := r.stream
		r.stop = context.AfterFunc(r.ctx, func() { _ = stream.Close() })
		r.offset = 0
		if off > 0 {
			if _, err := io.CopyN(io.Discard, r.stream, off); err != nil {
				r.closeStream()
				return 0, errors.Join(err, context.Cause(r.ctx))
			}
			r.offset = off
		}
	}
	n, err := io.ReadFull(r.stream, p[:limit])
	r.offset += int64(n)
	if err != nil || r.offset == r.size {
		r.closeStream()
	}
	if err == nil && limit < int64(len(p)) {
		err = io.EOF
	}
	return n, errors.Join(err, context.Cause(r.ctx))
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
	d.exhaustedContent[partContentKey(sharedResultID(source.sourceID), source.descriptor.Address, source.offer, source.offerRev)] = struct{}{}
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
	defer func() { rerr = errors.Join(rerr, source.Release(ctx)); permit.Release() }()
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
	if c.partFixture.Load() != nil {
		provider = partFixtureProvider{InfoReaderProvider: provider, cache: c, row: receiver.cacheSharedResult(), address: source.target}
	}
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
	cleanup := &partCleanup{fn: imported.Release}
	defer func() { rerr = errors.Join(rerr, cleanup.release(ctx)) }()
	source.descriptor.SnapshotID = imported.SnapshotID()
	// Keep admitted authority across a stale receiver preparation. Its donor
	// can disappear during download; re-preparation must not require re-admission.
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		c.egraphMu.Lock()
		if !c.offerAllowedLocked(source.sessionID, source.offerOwner) {
			c.egraphMu.Unlock()
			return ErrPartReselect
		}
		c.retainOfferOwnerLocked(source.offerOwner)
		selected := &PartSourceLease{cache: c, sourceID: source.sourceID, offerOwner: source.offerOwner, descriptor: source.Descriptor(), target: clonePartAddress(source.target), offer: source.offer, readiness: PartDownloadable, route: source.route, offerRev: source.offerRev, sessionID: source.sessionID, record: source.record}
		c.egraphMu.Unlock()
		prepared, err := c.PrepareReadyPart(ctx, receiver, selected, permit)
		if err == nil {
			// Commit transfers this retryable cleanup to the installed row and
			// its continuation, without retaining another graph hold.
			prepared.beforeSyncCleanup = cleanup
			receipt, outcome, commitErr := c.CommitReadyPart(ctx, prepared)
			err = commitErr
			if outcome == PartInstalled {
				return errors.Join(err, c.finishReadyPartInline(ctx, receipt))
			}
			if outcome == PartInstallRefused && err == nil {
				err = ErrPartReselect
			}
		}
		if !partCanReselect(err) {
			return err
		}
		task := PartTaskFromContext(ctx)
		var outcome GateOutcome
		if permit.decision {
			gate := permit.gate
			gate.mu.Lock()
			var drain *DrainTicket
			for _, group := range gate.groups {
				if group.task == task && group.phase == LazyEvaluationPreparing && containsPart(group.writeSet, source.target) {
					drain = group.drain
					break
				}
			}
			gate.mu.Unlock()
			permit, outcome, err = c.TryAcquireForDecision(ctx, receiver, source.target, drain, task)
		} else {
			permit, outcome, err = c.TryAcquire(ctx, receiver, source.target, task)
		}
		if err != nil {
			return err
		}
		if outcome == GateAlreadyInstalled {
			return nil
		}
		if outcome != GateGranted {
			return ErrPartReselect
		}
	}

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
		c.recordPartFixture(row, address, "settled")
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
