package dagql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	// ranges is how large blobs are read in parallel. Fixed once the
	// source is in use.
	ranges partRangeConfig
}

// NewPartContentSource performs no I/O. In-package fixtures supply their
// controlled transport here.
func NewPartContentSource(transport http.RoundTripper) *PartContentSource {
	return &PartContentSource{transport: transport, ranges: defaultPartRangeConfig()}
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
	// Many installs, and the chunks of a large blob, fetch from one blob
	// host at once; keep their connections for reuse. Reading on one stream
	// keeps the default.
	if partStreamsSetting() > 1 {
		transport.MaxIdleConnsPerHost = maxPartIdleConnsPerHost
	}
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
	// Provider returns an interface rather than an error, so marked
	// construction returns a refusing provider. The check precedes the
	// override lookup: a gated engine installs a fixture override, and a
	// marked context must not reach the delegate that counts reads.
	if engine.IsSnapshotSharePreparation(ctx) {
		return refusingContentProvider{}
	}
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
	// stats, when set, counts the install's requests.
	stats *partInstallStats

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
	if reader := p.rangeReader(ctx, layer.Descriptor, address, renewed); reader != nil {
		return reader, nil
	}
	readerCtx, cancel := context.WithCancelCause(ctx)
	p.stats.usedStreams(1)
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
func (p *partContentProvider) renew(ctx context.Context, blob digest.Digest) (rerr error) {
	start := time.Now()
	defer func() {
		end := time.Now()
		p.stats.renewed(start, end)
		profRenewal(ctx, blob, start, end, rerr)
	}()
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
// and closes the current response without waiting for the cursor.
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
	// closed is closed once the cancellation callback has closed body.
	closed chan struct{}
	// idle is time spent blocked without byte progress, including the
	// connection and headers.
	idle time.Duration
	once sync.Once
}

// close gives the body one closer: this path if it stops the cancellation
// callback first, otherwise the callback that cancellation already started,
// which close waits for so the body is closed when it returns.
func (s *partHTTPStream) close() {
	s.once.Do(func() {
		if s.body != nil {
			if s.stop() {
				_ = s.body.Close()
			} else {
				<-s.closed
			}
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
	return r.provider.get(r.ctx, r.url, off, r.desc.Size-1)
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

// logOfferInstall records a part installed from an offer: the entry, the part,
// the bytes downloaded for it and the time from the start of the import to
// the install, over which the download and the layers' application
// alternate, layer by layer. A layer the import reused or already had is not
// downloaded. Then where the time went, before and during the install. It
// logs them, and stamps them on the span of ctx: the lazy-evaluation span of
// the task that installed the part, which names the entry.
func logOfferInstall(ctx context.Context, receiver AnyResult, source *PartSourceLease, downloaded int64, duration time.Duration, timing partInstallTiming) {
	args := []any{
		"entry", uint64(receiver.cacheSharedResult().id),
		"output", source.target.OutputPath.String(),
		"part", string(source.target.Part),
		"bytes", downloaded,
		"duration", duration,
		"source", "offer",
		"waited", timing.waited,
	}
	args = append(args, timing.fields()...)
	args = append(args, "commit", timing.commit)
	slog.InfoContext(ctx, "remote cache part installed", args...)
	key, err := partAddressKey(source.target)
	if err != nil {
		return
	}
	ns := func(d time.Duration) string { return strconv.FormatInt(d.Nanoseconds(), 10) }
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String(telemetryattrs.CacheOfferPartAttr, key),
		attribute.String(telemetryattrs.CacheOfferBytesAttr, strconv.FormatInt(downloaded, 10)),
		attribute.String(telemetryattrs.CacheOfferDurationAttr, ns(duration)),
		attribute.String(telemetryattrs.CacheOfferWaitedAttr, ns(timing.waited)),
		attribute.String(telemetryattrs.CacheOfferLayersAttr, strconv.Itoa(timing.layers)),
		attribute.String(telemetryattrs.CacheOfferRequestsAttr, strconv.Itoa(timing.requests)),
		attribute.String(telemetryattrs.CacheOfferTTFBAttr, ns(timing.ttfb)),
		attribute.String(telemetryattrs.CacheOfferConnsNewAttr, strconv.Itoa(timing.connsNew)),
		attribute.String(telemetryattrs.CacheOfferRenewAttr, ns(timing.renew)),
		attribute.String(telemetryattrs.CacheOfferReadWaitAttr, ns(timing.readWait)),
		attribute.String(telemetryattrs.CacheOfferLayerWaitAttr, ns(timing.LayerWait)),
		attribute.String(telemetryattrs.CacheOfferFetchAttr, ns(timing.Fetch)),
		attribute.String(telemetryattrs.CacheOfferApplyAttr, ns(timing.Apply)),
		attribute.String(telemetryattrs.CacheOfferImportAttr, ns(timing.imported)),
		attribute.String(telemetryattrs.CacheOfferCommitAttr, ns(timing.commit)),
		attribute.String(telemetryattrs.CacheOfferStreamsAttr, strconv.Itoa(timing.streams)),
		attribute.String(telemetryattrs.CacheOfferRangeAttr, timing.rangeMode),
		attribute.String(telemetryattrs.CacheOfferStatusesAttr, timing.statuses),
		attribute.String(telemetryattrs.CacheOfferAheadAttr, strconv.Itoa(timing.Ahead)),
		attribute.String(telemetryattrs.CacheOfferJoinedAttr, strconv.Itoa(timing.Joined)),
	)
}

// logOfferInstallFailed records an install from an offer that reached the
// import and installed nothing: the outcome, and the bytes and time it took,
// as far as it got. It stamps no span, since CacheOfferPartAttr names an
// installed part. Refusals and supersessions are coordination, at info level.
func logOfferInstallFailed(ctx context.Context, receiver AnyResult, source *PartSourceLease, err error, outcome string, level slog.Level, downloaded int64, duration time.Duration, timing partInstallTiming, committed bool) {
	args := []any{
		"entry", uint64(receiver.cacheSharedResult().id),
		"output", source.target.OutputPath.String(),
		"part", string(source.target.Part),
		"outcome", outcome,
		"bytes", downloaded,
		"waited", timing.waited,
		"duration", duration,
	}
	args = append(args, timing.fields()...)
	if committed {
		args = append(args, "commit", timing.commit)
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	slog.FromContext(ctx).Log(ctx, level, "remote cache part install failed", args...)
}

// partInstallFailure classifies an install attempt that installed nothing,
// for its log line and its profile: canceled, content (the source's bytes
// were bad), refused and superseded (coordination, at info level), or error.
func partInstallFailure(ctx context.Context, err error, contentFailed bool) (string, slog.Level) {
	var stuck *PartNoProgressError
	switch {
	case context.Cause(ctx) != nil:
		return "canceled", slog.LevelInfo
	case contentFailed:
		return "content", slog.LevelWarn
	case partCanReselect(err), errors.As(err, &stuck):
		return "refused", slog.LevelInfo
	case err == nil:
		return "superseded", slog.LevelInfo
	}
	return "error", slog.LevelWarn
}

// partInstallStats counts one install's content requests. Atomic, as a
// reader's Close may run beside the import's reads.
type partInstallStats struct {
	requests atomic.Int64
	// ttfb sums each request's wait for its response headers, in nanoseconds.
	ttfb atomic.Int64
	// connsNew counts the requests that opened a connection rather than
	// reusing an idle one.
	connsNew atomic.Int64
	// renewNS sums the time getting fresh addresses, in nanoseconds.
	renewNS atomic.Int64
	// streams is the most requests one of the install's blobs had open at
	// once.
	streams atomic.Int64

	mu sync.Mutex
	// statuses counts the requests' HTTP statuses.
	statuses map[int]int
}

// request counts one request, sent at sent, whose response headers arrived
// or whose send failed just now. A nil stats counts nothing.
func (s *partInstallStats) request(sent time.Time) {
	if s == nil {
		return
	}
	s.requests.Add(1)
	s.ttfb.Add(int64(time.Since(sent)))
}

// renewed adds a renewal from start to end. A nil stats counts nothing.
func (s *partInstallStats) renewed(start, end time.Time) {
	if s == nil {
		return
	}
	s.renewNS.Add(int64(end.Sub(start)))
}

// partInstallTiming is where one install's time went, as its lines log it.
// ttfb is part of readWait, readWait of imported (the next layer's blob is
// read while the layer before it applies), and the import's three sums of
// imported.
type partInstallTiming struct {
	// waited runs from the demand's start to the install's.
	waited time.Duration
	layers int
	// requests and ttfb are the content requests and their summed wait for
	// response headers, connsNew those on a new connection, and renew the
	// time getting fresh addresses. readWait is the time blocked in content
	// reads.
	requests int
	ttfb     time.Duration
	connsNew int
	renew    time.Duration
	readWait time.Duration
	// streams, rangeMode and statuses are how the blobs were read: the most
	// requests at once, whether the store answered ranges, and the count of
	// each HTTP status.
	streams   int
	rangeMode string
	statuses  string
	snapshots.ImportTiming
	imported time.Duration
	// commit runs from the end of the import to the install, or to failure.
	commit time.Duration
}

func (t *partInstallTiming) count(stats *partInstallStats, downloaded *countingProvider) {
	t.requests = int(stats.requests.Load())
	t.ttfb = time.Duration(stats.ttfb.Load())
	t.connsNew = int(stats.connsNew.Load())
	t.renew = time.Duration(stats.renewNS.Load())
	t.readWait = time.Duration(downloaded.readNS.Load())
	t.streams = int(stats.streams.Load())
	t.rangeMode, t.statuses = stats.httpStatuses()
}

func (t partInstallTiming) fields() []any {
	return []any{
		"layers", t.layers,
		"requests", t.requests,
		"ttfb", t.ttfb,
		"conns_new", t.connsNew,
		"renew", t.renew,
		"read_wait", t.readWait,
		"layer_wait", t.LayerWait,
		"fetch", t.Fetch,
		"apply", t.Apply,
		"import", t.imported,
		"streams", t.streams,
		"range", t.rangeMode,
		"statuses", t.statuses,
		"ahead", t.Ahead,
		"joined", t.Joined,
	}
}

// countingProvider counts the bytes read through a provider, and the time
// blocked reading them. An import reads a blob only when it has neither the
// snapshot nor the blob already.
type countingProvider struct {
	content.InfoReaderProvider
	read   atomic.Int64
	readNS atomic.Int64
}

func (p *countingProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	ra, err := p.InfoReaderProvider.ReaderAt(ctx, desc)
	if err != nil {
		return nil, err
	}
	return countingReaderAt{ReaderAt: ra, read: &p.read, readNS: &p.readNS}, nil
}

type countingReaderAt struct {
	content.ReaderAt
	read   *atomic.Int64
	readNS *atomic.Int64
}

func (r countingReaderAt) ReadAt(b []byte, off int64) (int, error) {
	start := time.Now()
	n, err := r.ReaderAt.ReadAt(b, off)
	r.readNS.Add(int64(time.Since(start)))
	r.read.Add(int64(n))
	return n, err
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
	start := time.Now()
	// The content-source boundary: refuse before the offer clone and before
	// the fixture's provider-read counter, so the sentinel precedes any
	// observable read.
	if err := engine.CheckSnapshotSharePreparation(ctx, "acquire chain content"); err != nil {
		return err
	}
	if c.snapshotManager == nil {
		return fmt.Errorf("chain acquisition: no snapshot manager")
	}
	copied, err := clonePartOffers([]PersistedPartOffer{*source.offer})
	if err != nil {
		return err
	}
	// The install's own work runs under its profile op; what follows the
	// install doesn't.
	outerCtx := ctx
	ctx, profOp := profBeginInstall(ctx, receiver, source.target, copied[0].Chain.Layers, start)
	stats := &partInstallStats{}
	provider := c.PartContentSource().Provider(ctx, copied[0], demand)
	if p, ok := provider.(*partContentProvider); ok {
		p.stats = stats
	}
	if c.partFixture.Load() != nil {
		provider = partFixtureProvider{InfoReaderProvider: provider, cache: c, row: receiver.cacheSharedResult(), address: source.target}
	}
	downloaded := &countingProvider{InfoReaderProvider: provider}
	timing := partInstallTiming{layers: len(copied[0].Chain.Layers)}
	if demand != nil && !demand.started.IsZero() {
		timing.waited = start.Sub(demand.started)
	}
	// Every attempt that reaches the import logs one line: installed, or
	// failed with its outcome.
	var installed, contentFailed bool
	var committing time.Time
	defer func() {
		if installed {
			return
		}
		now := time.Now()
		timing.count(stats, downloaded)
		if !committing.IsZero() {
			timing.commit = now.Sub(committing)
		}
		// One classification for the line and the profile.
		outcome, level := partInstallFailure(ctx, rerr, contentFailed)
		logOfferInstallFailed(ctx, receiver, source, rerr, outcome, level, downloaded.read.Load(), now.Sub(start), timing, !committing.IsZero())
		profEndInstall(ctx, profOp, receiver, outcome, downloaded.read.Load(), committing, now)
	}()
	importStart := time.Now()
	imported, err := c.snapshotManager.ImportChain(ctx, &snapshots.ExportChain{Layers: copied[0].Chain.Layers, Provider: downloaded, Timing: &timing.ImportTiming})
	timing.imported = time.Since(importStart)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		var contentErr *snapshots.ChainContentError
		if errors.As(err, &contentErr) {
			contentFailed = true
			demand.exhaust(source, err)
			return partRefused("chain: content failed, source exhausted")
		}
		return err
	}
	cleanup := &partCleanup{fn: imported.Release}
	defer func() { rerr = errors.Join(rerr, cleanup.release(ctx)) }()
	committing = time.Now()
	source.descriptor.SnapshotID = imported.SnapshotID()
	// Keep admitted authority across a stale receiver preparation. Its donor
	// can disappear during download; re-preparation must not require re-admission.
	watch := partReselectWatch{loop: "installChainPart"}
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		watch.again(ctx, receiver.cacheSharedResult(), source.target)
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
				now := time.Now()
				timing.count(stats, downloaded)
				timing.commit = now.Sub(committing)
				installed = true
				logOfferInstall(ctx, receiver, source, downloaded.read.Load(), now.Sub(start), timing)
				profEndInstall(ctx, profOp, receiver, partInstalledOutcome, downloaded.read.Load(), committing, now)
				return errors.Join(err, c.finishReadyPartInline(outerCtx, receipt))
			}
			if outcome == PartInstallRefused && err == nil {
				err = partRefused("chain: commit refused")
			}
		}
		if !partCanReselect(err) {
			return err
		}
		watch.refused(err)
		if stuck := demand.refused(watch.loop, watch.n, source.target, err); stuck != nil {
			return stuck
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

// refusingContentProvider holds no pointer to the source, the transport or
// the renewal bridge. Info and ReaderAt return the sentinel without touching
// any of them.
type refusingContentProvider struct{}

func (refusingContentProvider) Info(context.Context, digest.Digest) (content.Info, error) {
	return content.Info{}, engine.ErrSnapshotShareEvaluation
}

func (refusingContentProvider) ReaderAt(context.Context, ocispecs.Descriptor) (content.ReaderAt, error) {
	return nil, engine.ErrSnapshotShareEvaluation
}
