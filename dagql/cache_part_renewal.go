package dagql

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
)

// Accepted starting values for bounded address renewal.
const (
	renewalMailboxCapacity = 64
	renewalDeadline        = 2 * time.Second
)

// ErrRenewalUnavailable is the typed cause for every renewal attempt that
// cannot supply a fresh address: no bridge or key, a full or closed mailbox,
// a negative reply, the deadline, or a reply without a usable address.
var ErrRenewalUnavailable = errors.New("address renewal unavailable")

// ErrRemoteCacheBridgeClosed is returned by Take after the bridge detaches.
var ErrRemoteCacheBridgeClosed = errors.New("remote cache bridge closed")

type RenewalRequestID struct {
	Epoch    [16]byte
	Sequence uint64
}

type RenewalRequest struct {
	ID RenewalRequestID
	// Chain is the fingerprint of the exact copied ordered layers.
	Chain      digest.Digest
	RenewalKey string
	// Layers is an immutable copy with no result or owner runtime IDs.
	Layers     []snapshots.ExportLayer
	NeededBlob digest.Digest
	Deadline   time.Time
	// Done closes on cancellation, timeout, accepted reply, detach or shutdown.
	Done <-chan struct{}
}

type RenewalReply struct {
	ID          RenewalRequestID
	Chain       digest.Digest
	Unavailable bool
	Addresses   map[digest.Digest]BlobAddress
}

type RenewalReplyDisposition uint8

const (
	RenewalReplyDiscarded RenewalReplyDisposition = iota
	RenewalReplyAccepted
)

// RemoteCacheBridge is one attachment's renewal mailbox. Its state is guarded
// by the owning content source's mutex M, which also serializes attachment.
// M is never nested with the graph, gate, lazy, payload or demand mutexes.
type RemoteCacheBridge struct {
	mu    *sync.Mutex
	epoch [16]byte
	// cache is the attaching cache, for the gated fixture's observation
	// points only. They stand outside M and are nil-off.
	cache *Cache

	closed    bool
	sequence  uint64
	exchanges map[uint64]*renewalExchange
	queue     []uint64
	ready     chan struct{}
}

type renewalExchange struct {
	request   RenewalRequest
	requester context.Context
	done      chan struct{}
	addresses map[digest.Digest]BlobAddress
	err       error
}

func newRemoteCacheBridge(mu *sync.Mutex) (*RemoteCacheBridge, error) {
	b := &RemoteCacheBridge{mu: mu, exchanges: map[uint64]*renewalExchange{}, ready: make(chan struct{})}
	if _, err := rand.Read(b.epoch[:]); err != nil {
		return nil, fmt.Errorf("remote cache bridge epoch: %w", err)
	}
	return b, nil
}

func renewalUnavailable(reason string) error {
	return fmt.Errorf("%w: %s", ErrRenewalUnavailable, reason)
}

// request enqueues one exchange and waits for its single terminal result.
func (b *RemoteCacheBridge) request(ctx context.Context, request RenewalRequest) (map[digest.Digest]BlobAddress, error) {
	exchange, err := b.enqueue(ctx, request)
	if err != nil {
		return nil, err
	}
	b.reachRenewal(ctx, FixtureRenewalEnqueued, exchange.request.ID)
	return b.wait(ctx, exchange)
}

// reachRenewal observes the real exchange outside M.
func (b *RemoteCacheBridge) reachRenewal(ctx context.Context, point FixtureBarrierPoint, id RenewalRequestID) {
	if b.cache == nil {
		return
	}
	_ = b.cache.fixtureReach(ctx, FixtureBarrierEvent{Point: point, Detail: fmt.Sprintf("exchange=%d", id.Sequence)})
}

// enqueue installs one exchange without waiting for capacity. The exchange
// keeps its requester's context, so Take and Reply see a cancellation before
// the requester itself runs.
func (b *RemoteCacheBridge) enqueue(ctx context.Context, request RenewalRequest) (*renewalExchange, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	layers, err := cloneExportLayers(request.Layers)
	if err != nil {
		return nil, err
	}
	request.Layers = layers
	exchange := &renewalExchange{requester: ctx, done: make(chan struct{})}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.closed:
		return nil, renewalUnavailable("bridge detached")
	case len(b.exchanges) >= renewalMailboxCapacity:
		return nil, renewalUnavailable("mailbox full")
	}
	b.sequence++
	request.ID = RenewalRequestID{Epoch: b.epoch, Sequence: b.sequence}
	request.Done = exchange.done
	exchange.request = request
	b.exchanges[request.ID.Sequence] = exchange
	b.queue = append(b.queue, request.ID.Sequence)
	close(b.ready)
	b.ready = make(chan struct{})
	return exchange, nil
}

// wait holds no lock and returns the exchange's terminal result, completing
// it on cancellation or at its deadline.
func (b *RemoteCacheBridge) wait(ctx context.Context, exchange *renewalExchange) (map[digest.Digest]BlobAddress, error) {
	timer := time.NewTimer(time.Until(exchange.request.Deadline))
	defer timer.Stop()
	select {
	case <-exchange.done:
	case <-ctx.Done():
		b.finish(exchange, nil, context.Cause(ctx))
	case <-timer.C:
		b.finish(exchange, nil, renewalUnavailable("deadline exceeded"))
	}
	<-exchange.done
	return exchange.addresses, exchange.err
}

// retireCanceledLocked completes an exchange whose requester is canceled.
func (b *RemoteCacheBridge) retireCanceledLocked(exchange *renewalExchange) bool {
	cause := context.Cause(exchange.requester)
	if cause == nil {
		return false
	}
	b.finishLocked(exchange, nil, cause)
	return true
}

// finish publishes the first terminal result; later paths observe it.
func (b *RemoteCacheBridge) finish(exchange *renewalExchange, addresses map[digest.Digest]BlobAddress, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finishLocked(exchange, addresses, err)
}

func (b *RemoteCacheBridge) finishLocked(exchange *renewalExchange, addresses map[digest.Digest]BlobAddress, err error) bool {
	sequence := exchange.request.ID.Sequence
	if b.exchanges[sequence] != exchange {
		return false
	}
	delete(b.exchanges, sequence)
	if i := slices.Index(b.queue, sequence); i >= 0 {
		b.queue = slices.Delete(b.queue, i, i+1)
	}
	exchange.addresses, exchange.err = addresses, err
	close(exchange.done)
	return true
}

// TakeRenewalRequest delivers each queued request once, retiring requests
// whose requester is canceled. Only the integration's consumer blocks here;
// canceling a Take does not cancel delivered exchanges.
func (b *RemoteCacheBridge) TakeRenewalRequest(ctx context.Context) (*RenewalRequest, error) {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return nil, ErrRemoteCacheBridgeClosed
		}
		now := time.Now()
		for len(b.queue) > 0 {
			sequence := b.queue[0]
			b.queue = b.queue[1:]
			exchange := b.exchanges[sequence]
			if exchange == nil || b.retireCanceledLocked(exchange) || !now.Before(exchange.request.Deadline) {
				// The requester's own deadline completes an expired exchange.
				continue
			}
			request := exchange.request
			b.mu.Unlock()
			// The exchange's copy is immutable; give the consumer its own.
			layers, err := cloneExportLayers(request.Layers)
			if err != nil {
				return nil, err
			}
			request.Layers = layers
			b.reachRenewal(ctx, FixtureRenewalDelivered, request.ID)
			return &request, nil
		}
		ready := b.ready
		b.mu.Unlock()
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// ReplyRenewal publishes a valid reply for a live exchange. Late, duplicate,
// old-epoch, wrong-content and invalid replies are discarded without effect; a
// rejected reply leaves the exchange eligible until its original deadline. A
// reply for a canceled requester is discarded and retires the exchange.
func (b *RemoteCacheBridge) ReplyRenewal(reply RenewalReply) (disposition RenewalReplyDisposition) {
	defer func() {
		// Registered first, so it runs last: after M is released.
		if b.cache != nil {
			_ = b.cache.fixtureReach(context.Background(), FixtureBarrierEvent{Point: FixtureRenewalReplied, Detail: fmt.Sprintf("exchange=%d disposition=%d", reply.ID.Sequence, disposition)})
		}
	}()
	b.mu.Lock()
	exchange := b.liveExchangeLocked(reply)
	if exchange != nil && b.retireCanceledLocked(exchange) {
		exchange = nil
	}
	b.mu.Unlock()
	if exchange == nil {
		return RenewalReplyDiscarded
	}
	addresses, ok := validRenewalAddresses(exchange.request.Layers, reply)
	if !ok {
		return RenewalReplyDiscarded
	}
	var err error
	if reply.Unavailable {
		err = renewalUnavailable("integration reported no address")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.liveExchangeLocked(reply) != exchange || b.retireCanceledLocked(exchange) || !time.Now().Before(exchange.request.Deadline) {
		return RenewalReplyDiscarded
	}
	if !b.finishLocked(exchange, addresses, err) {
		return RenewalReplyDiscarded
	}
	return RenewalReplyAccepted
}

func (b *RemoteCacheBridge) liveExchangeLocked(reply RenewalReply) *renewalExchange {
	if b.closed || reply.ID.Epoch != b.epoch {
		return nil
	}
	exchange := b.exchanges[reply.ID.Sequence]
	if exchange == nil || exchange.request.Chain != reply.Chain {
		return nil
	}
	return exchange
}

// validRenewalAddresses copies a reply's addresses. Only digests of the
// requested chain may appear; a negative reply carries none.
func validRenewalAddresses(layers []snapshots.ExportLayer, reply RenewalReply) (map[digest.Digest]BlobAddress, bool) {
	if reply.Unavailable {
		return nil, len(reply.Addresses) == 0
	}
	addresses := make(map[digest.Digest]BlobAddress, len(reply.Addresses))
	for blob, address := range reply.Addresses {
		if !slices.ContainsFunc(layers, func(layer snapshots.ExportLayer) bool { return layer.Descriptor.Digest == blob }) {
			return nil, false
		}
		addresses[blob] = BlobAddress{URL: address.URL, ExpiresAtUnix: address.ExpiresAtUnix}
	}
	return addresses, true
}

// closeLocked retires every exchange as unavailable and refuses later use.
// M is held; completing signals runs no integration callback.
func (b *RemoteCacheBridge) closeLocked() {
	if b.closed {
		return
	}
	b.closed = true
	for _, exchange := range b.exchanges {
		b.finishLocked(exchange, nil, renewalUnavailable("bridge detached"))
	}
	b.queue = nil
	close(b.ready)
}

func cloneExportLayers(layers []snapshots.ExportLayer) ([]snapshots.ExportLayer, error) {
	if layers == nil {
		return nil, nil
	}
	data, err := json.Marshal(layers)
	if err != nil {
		return nil, fmt.Errorf("copy renewal layers: %w", err)
	}
	var cloned []snapshots.ExportLayer
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("copy renewal layers: %w", err)
	}
	return cloned, nil
}

// renewalChainFingerprint correlates replies with the exact ordered layer
// records. It is never a result identity or egraph extra.
// RenewalChainFingerprint is the fingerprint a renewal request carries for
// these exact ordered layers. The gated test fixture uses it to key a
// pre-staged reply before the request exists.
func RenewalChainFingerprint(layers []snapshots.ExportLayer) (digest.Digest, error) {
	return renewalChainFingerprint(layers)
}

func renewalChainFingerprint(layers []snapshots.ExportLayer) (digest.Digest, error) {
	data, err := json.Marshal(layers)
	if err != nil {
		return "", fmt.Errorf("renewal chain fingerprint: %w", err)
	}
	return digest.FromBytes(append([]byte("dagger.remote-cache.renewal-chain.v1\x00"), data...)), nil
}

// renewalContentKey covers only the ordered blob digests, so changed
// descriptions, timestamps or annotations cannot renew a budget for the same bytes.
func renewalContentKey(layers []snapshots.ExportLayer) digest.Digest {
	data := []byte("dagger.remote-cache.renewal-content.v1\x00")
	for _, layer := range layers {
		data = append(data, layer.Descriptor.Digest...)
		data = append(data, 0)
	}
	return digest.FromBytes(data)
}

type renewalEpisodeKey struct {
	target  string
	content digest.Digest
}

type renewalEpisodeState uint8

const (
	renewalWaiting renewalEpisodeState = iota + 1
	renewalSupplied
	renewalUnavailableState
	renewalExhausted
)

// renewalEpisode is one demand's single fresh-address opportunity for one
// target address and content. Absence from the set is the unused state.
type renewalEpisode struct {
	state     renewalEpisodeState
	deadline  time.Time
	done      chan struct{}
	addresses map[digest.Digest]BlobAddress
	err       error
}

// claimRenewal returns the existing episode, or claims a new one when claim is
// true. Claiming never changes the demand revision: an episode changes no
// candidate, so it must not invalidate a SourceCheck.
func (d *PartDemandState) claimRenewal(key renewalEpisodeKey, claim bool, deadline time.Time) (_ *renewalEpisode, claimed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if episode := d.renewals[key]; episode != nil {
		return episode, false
	}
	if !claim {
		return nil, false
	}
	if d.renewals == nil {
		d.renewals = map[renewalEpisodeKey]*renewalEpisode{}
	}
	episode := &renewalEpisode{state: renewalWaiting, deadline: deadline, done: make(chan struct{})}
	d.renewals[key] = episode
	return episode, true
}

func (d *PartDemandState) settleRenewal(episode *renewalEpisode, addresses map[digest.Digest]BlobAddress, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if episode.state != renewalWaiting {
		return
	}
	episode.state = renewalSupplied
	if err != nil {
		episode.state = renewalUnavailableState
	}
	episode.addresses, episode.err = addresses, err
	close(episode.done)
}

// awaitRenewal uses a claimed episode's result. A settled episode answers
// at once, whenever it is consulted; only a waiting episode waits, at most
// until its original deadline, and a result settled by then still wins.
func (d *PartDemandState) awaitRenewal(ctx context.Context, episode *renewalEpisode) (map[digest.Digest]BlobAddress, error) {
	if addresses, settled, err := d.renewalResult(episode); settled {
		return addresses, err
	}
	timer := time.NewTimer(time.Until(episode.deadline))
	defer timer.Stop()
	select {
	case <-episode.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-timer.C:
		if addresses, settled, err := d.renewalResult(episode); settled {
			return addresses, err
		}
		return nil, renewalUnavailable("deadline exceeded")
	}
	addresses, _, err := d.renewalResult(episode)
	return addresses, err
}

func (d *PartDemandState) renewalResult(episode *renewalEpisode) (map[digest.Digest]BlobAddress, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch episode.state {
	case renewalWaiting:
		return nil, false, nil
	case renewalExhausted:
		return nil, true, renewalUnavailable("renewed content exhausted")
	default:
		return episode.addresses, true, episode.err
	}
}

// exhaustRenewalsLocked records chain failure for renewed episodes of this
// target and content. The caller holds d.mu and accounts for the revision.
func (d *PartDemandState) exhaustRenewalsLocked(offer *PersistedPartOffer) {
	if offer == nil {
		return
	}
	if episode := d.renewals[renewalEpisodeKey{target: d.targetKey(), content: renewalContentKey(offer.Chain.Layers)}]; episode != nil && episode.state == renewalSupplied {
		episode.state = renewalExhausted
	}
}

func (d *PartDemandState) targetKey() string {
	raw, _ := json.Marshal(d.target)
	return string(raw)
}

// AttachRemoteCacheBridge publishes this cache's renewal mailbox. A live
// attachment is returned again with created=false and no added ownership;
// otherwise a new mailbox with a fresh epoch is published. A closing cache
// refuses.
func (c *Cache) AttachRemoteCacheBridge() (bridge *RemoteCacheBridge, created bool, err error) {
	s := c.partContentSource
	if s == nil {
		return nil, false, errors.New("attach remote cache bridge: cache has no content source")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.closing.Load() {
		return nil, false, ErrCacheClosed
	}
	if bridge := s.bridge.Load(); bridge != nil {
		return bridge, false, nil
	}
	bridge, err = newRemoteCacheBridge(&s.mu)
	if err != nil {
		return nil, false, err
	}
	bridge.cache = c
	s.bridge.Store(bridge)
	return bridge, true, nil
}

// DetachRemoteCacheBridge retires exactly this attachment, completing its
// exchanges as unavailable. Nil, stale and repeated detach return false and
// leave a newer attachment alone.
func (c *Cache) DetachRemoteCacheBridge(bridge *RemoteCacheBridge) (detached bool) {
	s := c.partContentSource
	if s == nil || bridge == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detachLocked(bridge)
}

func (s *PartContentSource) detachLocked(bridge *RemoteCacheBridge) bool {
	if bridge == nil || s.bridge.Load() != bridge {
		return false
	}
	bridge.closeLocked()
	s.bridge.Store(nil)
	return true
}

// closeRemoteCacheBridge closes attachment admission and detaches before the
// cache waits for operations, so an idle consumer cannot hold close open.
func (c *Cache) closeRemoteCacheBridge() {
	s := c.partContentSource
	if s == nil {
		c.closing.Store(true)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.closing.Store(true)
	s.detachLocked(s.bridge.Load())
}
