package dagql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// renewalRetryStatuses are responses meaning the address itself is unusable.
// The initial and the renewed attempt both consult this set before accepting
// any bytes from the response; only the initial attempt may renew.
var renewalRetryStatuses = map[int]struct{}{
	http.StatusUnauthorized: {},
	http.StatusForbidden:    {},
	http.StatusNotFound:     {},
	http.StatusGone:         {},
}

// partContentIdleTimeout bounds time blocked on the network without byte
// progress. It is not a total download limit.
const partContentIdleTimeout = 30 * time.Second

var errPartContentIdle = errors.New("content read made no progress")

// PartContentOverride replaces a source's availability and provider. Only
// tests and the environment-gated transfer fixture set it; every other engine
// leaves it nil.
type PartContentOverride interface {
	// Available is a pure address check; it must not request content.
	Available(PersistedPartOffer, time.Time) bool
	Provider(context.Context, PersistedPartOffer, *PartDemandState) content.InfoReaderProvider
}

type partContentOverride struct{ PartContentOverride }

// PartContentSource is the cache's one content source. Ranking and chain
// installation both use it. A nil source behaves like one constructed with a
// nil transport: the default transport, no bridge and no override.
type PartContentSource struct {
	transport http.RoundTripper // immutable; nil selects the default
	bridge    atomic.Pointer[RemoteCacheBridge]
	mu        sync.Mutex // M: attachment and this cache's exchange state
	override  atomic.Pointer[partContentOverride]
}

// NewPartContentSource performs no I/O. In-package fixtures supply their
// controlled transport here.
func NewPartContentSource(transport http.RoundTripper) *PartContentSource {
	return &PartContentSource{transport: transport}
}

// PartContentSource returns the source constructed with the cache.
func (c *Cache) PartContentSource() *PartContentSource {
	return c.partContentSource
}

// SetPartContentSource installs or clears the source's override. The cache
// must come from NewCache.
func (c *Cache) SetPartContentSource(override PartContentOverride) {
	if override == nil {
		c.partContentSource.override.Store(nil)
	} else {
		c.partContentSource.override.Store(&partContentOverride{override})
	}
}

func (s *PartContentSource) loadOverride() PartContentOverride {
	if s == nil {
		return nil
	}
	if o := s.override.Load(); o != nil {
		return o.PartContentOverride
	}
	return nil
}

func (s *PartContentSource) attachedBridge() *RemoteCacheBridge {
	if s == nil {
		return nil
	}
	return s.bridge.Load()
}

var defaultPartTransport = sync.OnceValue(func() http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Verify the offered compressed bytes unchanged.
	transport.DisableCompression = true
	return transport
})

func (s *PartContentSource) roundTripper() http.RoundTripper {
	if s != nil && s.transport != nil {
		return s.transport
	}
	return defaultPartTransport()
}

// Available reports whether an offer can supply bytes now. An empty chain is
// scratch; otherwise every blob needs a usable address, or the offer needs a
// renewal key with a bridge attached. It performs no I/O.
func (s *PartContentSource) Available(offer PersistedPartOffer, now time.Time) bool {
	if override := s.loadOverride(); override != nil {
		return override.Available(offer, now)
	}
	if len(offer.Chain.Layers) == 0 || chainAddressesUsable(&offer, now) {
		return true
	}
	return offer.Chain.RenewalKey != "" && s.attachedBridge() != nil
}

// Provider freezes the copied offer's layers and copies its addresses. It
// creates no request.
func (s *PartContentSource) Provider(ctx context.Context, offer PersistedPartOffer, demand *PartDemandState) content.InfoReaderProvider {
	if override := s.loadOverride(); override != nil {
		return override.Provider(ctx, offer, demand)
	}
	// Always a map: an offer may carry a renewal key and no address yet, and
	// its first renewal writes the addresses it receives here.
	addresses := maps.Clone(offer.Chain.Addresses)
	if addresses == nil {
		addresses = map[digest.Digest]BlobAddress{}
	}
	return &partContentProvider{
		source:     s,
		client:     &http.Client{Transport: s.roundTripper()},
		layers:     offer.Chain.Layers,
		renewalKey: offer.Chain.RenewalKey,
		demand:     demand,
		addresses:  addresses,
		renewed:    map[digest.Digest]bool{},
	}
}

type blobAddressUse uint8

const (
	blobAddressUsable blobAddressUse = iota
	// blobAddressMissing covers an absent or expired address, which may renew.
	blobAddressMissing
	// blobAddressNotHTTP is unavailable and never opened or renewed.
	blobAddressNotHTTP
)

// blobAddressUsability is the shared address policy for Available and
// Provider: an absolute HTTP(S) URL whose expiry is zero or after now.
func blobAddressUsability(address BlobAddress, ok bool, now time.Time) blobAddressUse {
	if !ok || address.URL == "" || (address.ExpiresAtUnix != 0 && address.ExpiresAtUnix <= now.Unix()) {
		return blobAddressMissing
	}
	u, err := url.Parse(address.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return blobAddressNotHTTP
	}
	return blobAddressUsable
}

func chainAddressesUsable(offer *PersistedPartOffer, now time.Time) bool {
	for _, layer := range offer.Chain.Layers {
		address, ok := offer.Chain.Addresses[layer.Descriptor.Digest]
		if blobAddressUsability(address, ok, now) != blobAddressUsable {
			return false
		}
	}
	return true
}

type partContentProvider struct {
	source     *PartContentSource
	client     *http.Client
	layers     []snapshots.ExportLayer
	renewalKey string
	demand     *PartDemandState

	mu        sync.Mutex
	addresses map[digest.Digest]BlobAddress
	// renewed marks addresses supplied by this demand's renewal episode.
	renewed map[digest.Digest]bool
}

func (p *partContentProvider) layer(d digest.Digest) (snapshots.ExportLayer, bool) {
	for _, layer := range p.layers {
		if layer.Descriptor.Digest == d {
			return layer, true
		}
	}
	return snapshots.ExportLayer{}, false
}

// Info answers from the offered records, without network or renewal.
func (p *partContentProvider) Info(_ context.Context, d digest.Digest) (content.Info, error) {
	layer, ok := p.layer(d)
	if !ok {
		return content.Info{}, fmt.Errorf("unknown supplied blob %s", d)
	}
	return content.Info{Digest: d, Size: layer.Descriptor.Size}, nil
}

// ReaderAt is the missing-byte boundary. It resolves the blob's address,
// renewing an absent or expired one, and opens the response on first read.
func (p *partContentProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	layer, ok := p.layer(desc.Digest)
	var err error
	switch {
	case !ok:
		err = fmt.Errorf("unknown supplied blob %s", desc.Digest)
	case layer.Descriptor.Size != desc.Size || layer.Descriptor.MediaType != desc.MediaType:
		err = fmt.Errorf("supplied blob %s descriptor does not match the offered layer", desc.Digest)
	case desc.Size < 0:
		err = fmt.Errorf("supplied blob %s has negative size", desc.Digest)
	}
	if err != nil {
		return nil, partContentError(ctx, desc, "provider", err)
	}
	address, renewed, err := p.address(ctx, desc.Digest, "")
	if err != nil {
		return nil, partContentError(ctx, desc, "provider", err)
	}
	readerCtx, cancel := context.WithCancelCause(ctx)
	return &partHTTPReader{provider: p, desc: layer.Descriptor, parent: ctx, ctx: readerCtx, cancel: cancel, url: address, renewed: renewed}, nil
}

// address returns a usable address for blob and whether renewal supplied it.
// A missing address, or failed naming the address a retry status rejected,
// uses the demand's renewal episode; a renewed address never renews again.
func (p *partContentProvider) address(ctx context.Context, blob digest.Digest, failed string) (string, bool, error) {
	p.mu.Lock()
	address, ok := p.addresses[blob]
	renewed := p.renewed[blob]
	p.mu.Unlock()
	use := blobAddressUsability(address, ok, time.Now())
	switch {
	case failed == "" && use == blobAddressUsable, renewed && address.URL != failed && use == blobAddressUsable:
		return address.URL, renewed, nil
	case failed == "" && use == blobAddressNotHTTP:
		return "", false, fmt.Errorf("supplied blob %s is unavailable: its address is not HTTP(S)", blob)
	case renewed:
		return "", false, renewalUnavailable("renewed address is unusable")
	}
	if err := p.renew(ctx, blob); err != nil {
		return "", false, err
	}
	p.mu.Lock()
	address, ok = p.addresses[blob]
	renewed = p.renewed[blob]
	p.mu.Unlock()
	if !renewed || address.URL == failed || blobAddressUsability(address, ok, time.Now()) != blobAddressUsable {
		return "", false, renewalUnavailable("renewal supplied no usable address")
	}
	return address.URL, true, nil
}

// renew uses this demand's one episode for the target address and content.
// An absent bridge or key cannot claim it, but may use an episode another
// source already claimed.
func (p *partContentProvider) renew(ctx context.Context, blob digest.Digest) error {
	if p.demand == nil {
		return renewalUnavailable("no demand")
	}
	bridge := p.source.attachedBridge()
	deadline := time.Now().Add(renewalDeadline)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	key := renewalEpisodeKey{target: p.demand.targetKey(), content: renewalContentKey(p.layers)}
	episode, claimed := p.demand.claimRenewal(key, p.renewalKey != "" && bridge != nil, deadline)
	var addresses map[digest.Digest]BlobAddress
	var err error
	switch {
	case episode == nil && p.renewalKey == "":
		return renewalUnavailable("offer has no renewal key")
	case episode == nil:
		return renewalUnavailable("no bridge attached")
	case claimed:
		var chain digest.Digest
		chain, err = renewalChainFingerprint(p.layers)
		if err == nil {
			addresses, err = bridge.request(ctx, RenewalRequest{Chain: chain, RenewalKey: p.renewalKey, Layers: p.layers, NeededBlob: blob, Deadline: episode.deadline})
		}
		p.demand.settleRenewal(episode, addresses, err)
	default:
		addresses, err = p.demand.awaitRenewal(ctx, episode)
	}
	if err != nil {
		return err
	}
	// Recheck before content work; a late result cannot revive a canceled task.
	if err := context.Cause(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for d, address := range addresses {
		if _, known := p.layer(d); known {
			p.addresses[d] = address
			p.renewed[d] = true
		}
	}
	return nil
}

// partContentError wraps a reader failure once. Caller cancellation stays
// cancellation.
func partContentError(ctx context.Context, desc ocispecs.Descriptor, stage string, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	var contentErr *snapshots.ChainContentError
	if errors.As(err, &contentErr) {
		return err
	}
	return &snapshots.ChainContentError{Layer: desc.Digest, Stage: stage, Err: err}
}

// partHTTPReader keeps one sequential response for contiguous reads. Another
// offset reopens with Range. The cursor mutex serializes reads; Close cancels
// and closes the current response without waiting for it.
type partHTTPReader struct {
	provider *partContentProvider
	desc     ocispecs.Descriptor
	parent   context.Context
	ctx      context.Context
	cancel   context.CancelCauseFunc
	current  atomic.Pointer[partHTTPStream]

	mu      sync.Mutex
	url     string
	renewed bool
	stream  *partHTTPStream
	offset  int64
}

// partHTTPStream is one response. Its request context is canceled by Close,
// by the reader's cancellation and by the idle bound.
type partHTTPStream struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	body   io.ReadCloser
	stop   func() bool
	// idle is time spent blocked without byte progress, including the
	// connection and headers.
	idle time.Duration
	once sync.Once
}

// close gives the body one closer: this path if it stops the cancellation
// callback first, otherwise the callback that cancellation already started.
func (s *partHTTPStream) close() {
	s.once.Do(func() {
		if s.body != nil && s.stop() {
			_ = s.body.Close()
		}
		s.cancel(nil)
	})
}

// wait runs one blocked network operation under the idle bound. Only positive
// byte progress resets the idle time already spent; nothing is armed between
// operations, so local writer backpressure never counts.
func (s *partHTTPStream) wait(op func() (int, error)) (int, error) {
	start := time.Now()
	timer := time.AfterFunc(partContentIdleTimeout-s.idle, func() { s.cancel(errPartContentIdle) })
	n, err := op()
	timer.Stop()
	if n > 0 {
		s.idle = 0
	} else {
		s.idle += time.Since(start)
	}
	if err != nil && errors.Is(context.Cause(s.ctx), errPartContentIdle) {
		err = fmt.Errorf("%w for %s", errPartContentIdle, partContentIdleTimeout)
	}
	return n, err
}

func (s *partHTTPStream) Read(p []byte) (int, error) {
	return s.wait(func() (int, error) { return s.body.Read(p) })
}

func (r *partHTTPReader) Close() error {
	r.cancel(nil)
	if stream := r.current.Load(); stream != nil {
		stream.close()
	}
	return nil
}

func (r *partHTTPReader) Size() int64 { return r.desc.Size }

func (r *partHTTPReader) closeStream() {
	if r.stream != nil {
		r.stream.close()
		r.current.CompareAndSwap(r.stream, nil)
		r.stream = nil
	}
}

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
	size := r.desc.Size
	if off >= size {
		return 0, io.EOF
	}
	limit := min(int64(len(p)), size-off)
	if r.stream != nil && off != r.offset {
		r.closeStream()
	}
	if r.stream == nil {
		if err := r.open(off); err != nil {
			return 0, err
		}
	}
	n, err := io.ReadFull(r.stream, p[:limit])
	r.offset += int64(n)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	if err != nil || r.offset == size {
		r.closeStream()
	}
	if err != nil {
		return n, partContentError(r.parent, r.desc, "copy", err)
	}
	if limit < int64(len(p)) {
		err = io.EOF
	}
	return n, err
}

// open requests the remainder of the blob from off. A retry status on an
// address that did not come from renewal uses the demand's one episode.
func (r *partHTTPReader) open(off int64) error {
	for {
		stream, resp, err := r.request(off) //nolint:bodyclose // The body is handed to the stream wrapper, which closes it in stream.close and its context AfterFunc.
		if err != nil {
			return partContentError(r.parent, r.desc, "provider", err)
		}
		if _, retry := renewalRetryStatuses[resp.StatusCode]; retry {
			stream.close()
			err := fmt.Errorf("content address returned HTTP %d", resp.StatusCode)
			if r.renewed {
				return partContentError(r.parent, r.desc, "provider", err)
			}
			address, _, renewErr := r.provider.address(r.ctx, r.desc.Digest, r.url)
			if renewErr != nil {
				return partContentError(r.parent, r.desc, "provider", errors.Join(err, renewErr))
			}
			r.url, r.renewed = address, true
			continue
		}
		r.stream = stream
		r.current.Store(stream)
		r.offset = off
		if err := context.Cause(r.ctx); err != nil {
			r.closeStream()
			return err
		}
		if err := r.accept(resp, off); err != nil {
			r.closeStream()
			return err
		}
		return nil
	}
}

func (r *partHTTPReader) request(off int64) (*partHTTPStream, *http.Response, error) {
	ctx, cancel := context.WithCancelCause(r.ctx)
	stream := &partHTTPStream{ctx: ctx, cancel: cancel}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		stream.close()
		return nil, nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, r.desc.Size-1))
	var resp *http.Response
	_, err = stream.wait(func() (int, error) {
		var err error
		resp, err = r.provider.client.Do(req) //nolint:bodyclose // The body is handed to the stream wrapper, which closes it in stream.close and its context AfterFunc.
		return 0, err
	})
	if err != nil {
		stream.close()
		return nil, nil, err
	}
	stream.body = resp.Body
	stream.stop = context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
	return stream, resp, nil
}

// accept validates the response for off: a 206 must cover exactly the
// requested remainder; a 200 is the whole object, whose prefix is discarded
// without buffering it.
func (r *partHTTPReader) accept(resp *http.Response, off int64) error {
	size := r.desc.Size
	switch resp.StatusCode {
	case http.StatusPartialContent:
		var start, end, total int64
		value := resp.Header.Get("Content-Range")
		if _, err := fmt.Sscanf(value, "bytes %d-%d/%d", &start, &end, &total); err != nil || value != fmt.Sprintf("bytes %d-%d/%d", start, end, total) || start != off || end != size-1 || total != size {
			return partContentError(r.parent, r.desc, "provider", fmt.Errorf("content range %q does not cover bytes %d-%d of %d", value, off, size-1, size))
		}
	case http.StatusOK:
		if resp.ContentLength >= 0 && resp.ContentLength != size {
			return partContentError(r.parent, r.desc, "provider", fmt.Errorf("content length %d, want %d", resp.ContentLength, size))
		}
		if off > 0 {
			if _, err := io.CopyN(io.Discard, r.stream, off); err != nil {
				if errors.Is(err, io.EOF) {
					err = io.ErrUnexpectedEOF
				}
				return partContentError(r.parent, r.desc, "copy", err)
			}
		}
	default:
		return partContentError(r.parent, r.desc, "provider", fmt.Errorf("content HTTP status %d", resp.StatusCode))
	}
	return nil
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
	d.exhaustRenewalsLocked(source.offer)
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
	copied, err := clonePartOffers([]PersistedPartOffer{*source.offer})
	if err != nil {
		return err
	}
	provider := c.PartContentSource().Provider(ctx, copied[0], demand)
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
			return partRefused("chain: content failed, source exhausted")
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
			return partRefused("chain: offer owner not allowed")
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
				err = partRefused("chain: commit refused")
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
			return partRefused("chain: reacquire not granted")
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
