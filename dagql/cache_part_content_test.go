package dagql

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// contentTestFault changes one URL's response. The zero value serves the
// object, honoring Range.
type contentTestFault struct {
	status       int
	truncate     bool
	corrupt      bool
	ignoreRange  bool
	stallHeaders bool
	// stallAfter blocks the body after that many bytes; negative disables it.
	stallAfter int
}

// contentTestTransport is an in-process RoundTripper over byte slices. It
// honors request cancellation and counts every request and open body.
type contentTestTransport struct {
	mu       sync.Mutex
	objects  map[string][]byte
	faults   map[string]contentTestFault
	requests map[string]int
	ranges   []string
	open     atomic.Int64
	// progress, when set, gates each stalled body byte on a receive.
	progress chan struct{}
}

func newContentTestTransport() *contentTestTransport {
	return &contentTestTransport{objects: map[string][]byte{}, faults: map[string]contentTestFault{}, requests: map[string]int{}}
}

func (tr *contentTestTransport) serve(url string, data []byte) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.objects[url] = data
}

func (tr *contentTestTransport) fault(url string, fault contentTestFault) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.faults[url] = fault
}

func (tr *contentTestTransport) count(url string) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.requests[url]
}

func (tr *contentTestTransport) total() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	n := 0
	for _, c := range tr.requests {
		n += c
	}
	return n
}

func (tr *contentTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	tr.mu.Lock()
	tr.requests[url]++
	tr.ranges = append(tr.ranges, req.Header.Get("Range"))
	data, ok := tr.objects[url]
	fault, faulted := tr.faults[url]
	tr.mu.Unlock()
	if !faulted {
		fault.stallAfter = -1
	}
	if !ok {
		return nil, fmt.Errorf("test transport has no object at %s", url)
	}
	if fault.stallHeaders {
		<-req.Context().Done()
		return nil, context.Cause(req.Context())
	}
	resp := &http.Response{Request: req, Header: http.Header{}, ContentLength: -1}
	if fault.status != 0 {
		resp.StatusCode = fault.status
		resp.Body = tr.body(req.Context(), []byte("refused"), -1)
		return resp, nil
	}
	if fault.corrupt {
		data = bytes.Repeat([]byte("x"), len(data))
	}
	var start, end int64
	resp.StatusCode = http.StatusOK
	if spec := req.Header.Get("Range"); spec != "" && !fault.ignoreRange {
		if _, err := fmt.Sscanf(spec, "bytes=%d-%d", &start, &end); err != nil {
			return nil, err
		}
		resp.StatusCode = http.StatusPartialContent
		resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		data = data[start : end+1]
	}
	if fault.truncate {
		data = data[:len(data)/2]
	}
	resp.Body = tr.body(req.Context(), data, fault.stallAfter)
	return resp, nil
}

func (tr *contentTestTransport) body(ctx context.Context, data []byte, stallAfter int) io.ReadCloser {
	tr.open.Add(1)
	return &contentTestBody{ctx: ctx, data: data, stallAfter: stallAfter, transport: tr}
}

type contentTestBody struct {
	ctx        context.Context
	data       []byte
	read       int
	stallAfter int
	transport  *contentTestTransport
	closeOnce  sync.Once
	closed     chan struct{}
	mu         sync.Mutex
}

func (b *contentTestBody) done() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed == nil {
		b.closed = make(chan struct{})
	}
	return b.closed
}

func (b *contentTestBody) Read(p []byte) (int, error) {
	if b.stallAfter >= 0 && b.read >= b.stallAfter {
		if b.transport.progress == nil {
			select {
			case <-b.ctx.Done():
				return 0, context.Cause(b.ctx)
			case <-b.done():
				return 0, errors.New("read on closed body")
			}
		}
		select {
		case <-b.ctx.Done():
			return 0, context.Cause(b.ctx)
		case <-b.done():
			return 0, errors.New("read on closed body")
		case <-b.transport.progress:
			p = p[:min(len(p), 1)]
		}
	}
	if err := context.Cause(b.ctx); err != nil {
		return 0, err
	}
	if b.read >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.read:])
	if b.stallAfter >= 0 && b.read < b.stallAfter {
		n = min(n, b.stallAfter-b.read)
	}
	b.read += n
	return n, nil
}

func (b *contentTestBody) Close() error {
	b.closeOnce.Do(func() {
		close(b.done())
		b.transport.open.Add(-1)
	})
	return nil
}

func TestPartContentSourceAvailability(t *testing.T) {
	layers := renewalTestLayers("lower", "upper")
	now := time.Unix(1_700_000_000, 0)
	usable := func(url string, expires int64) map[digest.Digest]BlobAddress {
		return map[digest.Digest]BlobAddress{layers[0].Descriptor.Digest: {URL: "https://blobs.invalid/lower"}, layers[1].Descriptor.Digest: {URL: url, ExpiresAtUnix: expires}}
	}
	attached := NewPartContentSource(nil)
	bridge, err := newRemoteCacheBridge(&attached.mu)
	require.NoError(t, err)
	attached.bridge.Store(bridge)
	for _, tc := range []struct {
		name     string
		offer    PersistedPartOffer
		detached bool
		attached bool
	}{
		{name: "empty chain is scratch", offer: PersistedPartOffer{}, detached: true, attached: true},
		{name: "fixed address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, Addresses: usable("http://blobs.invalid/upper", 0)}}, detached: true, attached: true},
		{name: "unexpired address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, Addresses: usable("https://blobs.invalid/upper", now.Unix()+1)}}, detached: true, attached: true},
		{name: "expired address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, Addresses: usable("https://blobs.invalid/upper", now.Unix())}}},
		{name: "missing address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers}}},
		{name: "non-HTTP address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, Addresses: usable("s3://bucket/upper", 0)}}},
		{name: "relative address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, Addresses: usable("/upper", 0)}}},
		{name: "renewal key needs a bridge", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, RenewalKey: "key"}}, attached: true},
		{name: "renewal key with non-HTTP address", offer: PersistedPartOffer{Chain: OfferedChain{Layers: layers, Addresses: usable("s3://bucket/upper", 0), RenewalKey: "key"}}, attached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.detached, (*PartContentSource)(nil).Available(tc.offer, now))
			require.Equal(t, tc.detached, NewPartContentSource(nil).Available(tc.offer, now))
			require.Equal(t, tc.attached, attached.Available(tc.offer, now))
		})
	}
	// A non-HTTP address is unavailable, not a malformed offer.
	offer := PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory"}, Chain: OfferedChain{Layers: layers, Addresses: usable("s3://bucket/upper", 0)}}
	require.NoError(t, validateTransferOffer(offer, CapturedCodecOutput{Address: offer.Address, State: "pending", Value: &SnapshotValue{Kind: "directory"}}))

	// The test override replaces both methods inside the one source.
	override := &partAvailabilityHook{fn: func() {}}
	ctx, c, _ := transferTestCache(t)
	source := c.PartContentSource()
	require.Same(t, source, c.PartContentSource())
	c.SetPartContentSource(override)
	require.True(t, source.Available(PersistedPartOffer{Chain: OfferedChain{Layers: layers}}, now))
	require.Panics(t, func() { source.Provider(ctx, offer, nil) })
	c.SetPartContentSource(nil)
	require.False(t, source.Available(PersistedPartOffer{Chain: OfferedChain{Layers: layers}}, now))
	require.Same(t, source, c.PartContentSource())
}

// contentReaderFixture is one offered blob behind the test transport.
func contentReaderFixture(t *testing.T, data string) (*contentTestTransport, *PartContentSource, PersistedPartOffer, ocispec.Descriptor) {
	t.Helper()
	transport := newContentTestTransport()
	descriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.FromString(data), Size: int64(len(data))}
	transport.serve("https://blobs.invalid/blob", []byte(data))
	offer := PersistedPartOffer{Chain: OfferedChain{Layers: []snapshots.ExportLayer{{Descriptor: descriptor}}, Addresses: map[digest.Digest]BlobAddress{descriptor.Digest: {URL: "https://blobs.invalid/blob"}}}}
	return transport, NewPartContentSource(transport), offer, descriptor
}

func TestPartContentIdleReader(t *testing.T) {
	const data = "0123456789abcdefghijklmnopqrstuvwxyz"
	t.Run("header stall", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			transport, source, offer, descriptor := contentReaderFixture(t, data)
			transport.fault("https://blobs.invalid/blob", contentTestFault{stallHeaders: true})
			reader, err := source.Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			started := time.Now()
			_, err = reader.ReadAt(make([]byte, 4), 0)
			require.ErrorIs(t, err, errPartContentIdle)
			require.Equal(t, partContentIdleTimeout, time.Since(started))
			var contentErr *snapshots.ChainContentError
			require.ErrorAs(t, err, &contentErr)
			require.Equal(t, "provider", contentErr.Stage)
			require.NoError(t, reader.Close())
		})
	})
	t.Run("blocked read and progress", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			transport, source, offer, descriptor := contentReaderFixture(t, data)
			transport.progress = make(chan struct{})
			transport.fault("https://blobs.invalid/blob", contentTestFault{stallAfter: 4})
			reader, err := source.Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			buf := make([]byte, 4)
			n, err := reader.ReadAt(buf, 0)
			require.NoError(t, err)
			require.Equal(t, data[:4], string(buf[:n]))
			// Time outside ReadAt, such as local writer backpressure, is not idle.
			time.Sleep(10 * partContentIdleTimeout)
			started := time.Now()
			read := make(chan error, 1)
			go func() {
				n, err := reader.ReadAt(buf[:3], 4)
				if err == nil && string(buf[:n]) != data[4:7] {
					err = fmt.Errorf("read %q", buf[:n])
				}
				read <- err
			}()
			// Each byte arrives 20 seconds apart: progress resets the bound.
			for range 3 {
				time.Sleep(20 * time.Second)
				transport.progress <- struct{}{}
			}
			require.NoError(t, <-read)
			require.Equal(t, 60*time.Second, time.Since(started), "a moving stream has no total limit")
			started = time.Now()
			_, err = reader.ReadAt(buf[:1], 7)
			require.ErrorIs(t, err, errPartContentIdle)
			require.Equal(t, partContentIdleTimeout, time.Since(started))
			var contentErr *snapshots.ChainContentError
			require.ErrorAs(t, err, &contentErr)
			require.Equal(t, "copy", contentErr.Stage)
			synctest.Wait()
			require.Zero(t, transport.open.Load(), "an expired response is closed")
			require.NoError(t, reader.Close())
		})
	})
	t.Run("close unblocks read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			transport, source, offer, descriptor := contentReaderFixture(t, data)
			transport.fault("https://blobs.invalid/blob", contentTestFault{stallAfter: 0})
			reader, err := source.Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			read := make(chan error, 1)
			go func() {
				_, err := reader.ReadAt(make([]byte, 4), 0)
				read <- err
			}()
			time.Sleep(time.Second)
			synctest.Wait()
			// Close does not wait for the cursor mutex held by the blocked read.
			require.NoError(t, reader.Close())
			require.Error(t, <-read)
			synctest.Wait()
			require.Zero(t, transport.open.Load())
			_, err = reader.ReadAt(make([]byte, 4), 0)
			require.ErrorIs(t, err, context.Canceled)
		})
	})
	t.Run("caller cancellation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			transport, source, offer, descriptor := contentReaderFixture(t, data)
			transport.fault("https://blobs.invalid/blob", contentTestFault{stallAfter: 2})
			ctx, cancel := context.WithCancel(t.Context())
			reader, err := source.Provider(ctx, offer, nil).ReaderAt(ctx, descriptor)
			require.NoError(t, err)
			read := make(chan error, 1)
			go func() {
				_, err := reader.ReadAt(make([]byte, 4), 0)
				read <- err
			}()
			synctest.Wait()
			cancel()
			err = <-read
			require.ErrorIs(t, err, context.Canceled)
			var contentErr *snapshots.ChainContentError
			require.False(t, errors.As(err, &contentErr), "cancellation is not a content failure")
			synctest.Wait()
			require.Zero(t, transport.open.Load())
			require.NoError(t, reader.Close())
		})
	})
	t.Run("descriptor checks", func(t *testing.T) {
		transport, source, offer, descriptor := contentReaderFixture(t, data)
		provider := source.Provider(t.Context(), offer, nil)
		_, err := provider.Info(t.Context(), digest.FromString("other"))
		require.ErrorContains(t, err, "unknown supplied blob")
		for name, desc := range map[string]ocispec.Descriptor{
			"unknown digest": {MediaType: descriptor.MediaType, Digest: digest.FromString("other"), Size: descriptor.Size},
			"size":           {MediaType: descriptor.MediaType, Digest: descriptor.Digest, Size: descriptor.Size + 1},
			"media type":     {MediaType: ocispec.MediaTypeImageLayerGzip, Digest: descriptor.Digest, Size: descriptor.Size},
		} {
			_, err := provider.ReaderAt(t.Context(), desc)
			var contentErr *snapshots.ChainContentError
			require.ErrorAs(t, err, &contentErr, name)
			require.Equal(t, "provider", contentErr.Stage, name)
		}
		require.Zero(t, transport.total())
	})
	t.Run("ranges", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			transport, source, offer, descriptor := contentReaderFixture(t, data)
			reader, err := source.Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			buf := make([]byte, 5)
			for _, off := range []int64{30, 3, 8, 13, 0} {
				n, err := reader.ReadAt(buf, off)
				require.NoError(t, err)
				require.Equal(t, data[off:off+int64(n)], string(buf[:n]))
			}
			n, err := reader.ReadAt(buf, 34)
			require.ErrorIs(t, err, io.EOF)
			require.Equal(t, data[34:], string(buf[:n]))
			require.Equal(t, []string{"bytes=30-35", "bytes=3-35", "bytes=0-35", "bytes=34-35"}, transport.ranges)
			require.NoError(t, reader.Close())
			synctest.Wait()
			require.Zero(t, transport.open.Load())

			// A 206 must cover exactly the requested remainder.
			bad := newContentTestTransport()
			bad.serve("https://blobs.invalid/blob", []byte(data))
			short := &rangeRewriter{RoundTripper: bad, value: fmt.Sprintf("bytes 4-10/%d", len(data))}
			reader, err = NewPartContentSource(short).Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			_, err = reader.ReadAt(buf, 4)
			require.ErrorContains(t, err, "does not cover")
			synctest.Wait()
			require.Zero(t, bad.open.Load())
			require.NoError(t, reader.Close())
		})
	})
}

type rangeRewriter struct {
	http.RoundTripper
	value string
}

func (r *rangeRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.RoundTripper.RoundTrip(req)
	if err == nil && resp.StatusCode == http.StatusPartialContent {
		resp.Header.Set("Content-Range", r.value)
	}
	return resp, err
}

// renewalConsumer answers every taken request with reply(request).
func renewalConsumer(t *testing.T, bridge *RemoteCacheBridge, reply func(*RenewalRequest) RenewalReply) (taken func() []*RenewalRequest) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var requests []*RenewalRequest
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			request, err := bridge.TakeRenewalRequest(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			requests = append(requests, request)
			mu.Unlock()
			if reply != nil {
				bridge.ReplyRenewal(reply(request))
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		within(t, done)
	})
	return func() []*RenewalRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]*RenewalRequest(nil), requests...)
	}
}

func attachTestBridge(t *testing.T, source *PartContentSource) *RemoteCacheBridge {
	t.Helper()
	bridge, err := newRemoteCacheBridge(&source.mu)
	require.NoError(t, err)
	source.bridge.Store(bridge)
	t.Cleanup(func() {
		source.mu.Lock()
		bridge.closeLocked()
		source.mu.Unlock()
	})
	return bridge
}

type chainFixture struct {
	tag   string
	chain *snapshots.ExportChain
	blobs [][]byte
	lower *snapshots.ExportChain
}

// newChainFixture builds a real two-layer chain in its own store.
func newChainFixture(t *testing.T, tag string) chainFixture {
	t.Helper()
	ctx := context.Background()
	store := testutil.NewStore(t)
	lower, _ := store.Build(t, nil, "lower.txt", "lower bytes "+tag)
	upper, _ := store.Build(t, lower, "upper.txt", "upper bytes "+tag)
	export := func(ref snapshots.ImmutableRef) *snapshots.ExportChain {
		chain, err := ref.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
		return chain
	}
	fixture := chainFixture{tag: tag, chain: export(upper), lower: export(lower)}
	require.Len(t, fixture.chain.Layers, 2)
	for _, layer := range fixture.chain.Layers {
		data, err := content.ReadBlob(ctx, fixture.chain.Provider, layer.Descriptor)
		require.NoError(t, err)
		fixture.blobs = append(fixture.blobs, data)
	}
	return fixture
}

func (f chainFixture) digest(i int) digest.Digest { return f.chain.Layers[i].Descriptor.Digest }

type chainRunOptions struct {
	// localPrefix imports the lower layer before the offered chain.
	localPrefix bool
	// lower defaults to a usable address when the prefix is not local.
	lower     BlobAddress
	upper     BlobAddress
	faults    map[string]contentTestFault
	attach    bool
	key       string
	reply     func(chainFixture, *RenewalRequest) RenewalReply
	configure func(*testutil.Store)
	// keyOnly offers the chain with a renewal key and no addresses at all.
	keyOnly bool
}

type chainRunResult struct {
	err       error
	requests  []*RenewalRequest
	transport *contentTestTransport
}

var expiredTestAddress = BlobAddress{URL: "https://old.invalid/blob", ExpiresAtUnix: 1}

func renewUpper(url string) func(chainFixture, *RenewalRequest) RenewalReply {
	return func(_ chainFixture, request *RenewalRequest) RenewalReply {
		return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{request.NeededBlob: {URL: url}}}
	}
}

func refuseRenewal(_ chainFixture, request *RenewalRequest) RenewalReply {
	return RenewalReply{ID: request.ID, Chain: request.Chain, Unavailable: true}
}

// runChainImport imports the fixture chain with the source's real provider
// into a fresh real store and checks every response was closed.
func runChainImport(t *testing.T, fixture chainFixture, opts chainRunOptions) chainRunResult {
	t.Helper()
	store := testutil.NewStore(t)
	lower := expiredTestAddress
	if opts.localPrefix {
		prefix, err := store.Manager.ImportChain(t.Context(), &snapshots.ExportChain{Layers: fixture.lower.Layers, Provider: fixture.lower.Provider})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, prefix.Release(context.Background())) })
	} else if lower = opts.lower; lower == (BlobAddress{}) {
		lower = BlobAddress{URL: "https://initial.invalid/lower"}
	}
	transport := newContentTestTransport()
	transport.serve("https://initial.invalid/lower", fixture.blobs[0])
	transport.serve("https://initial.invalid/upper", fixture.blobs[1])
	transport.serve("https://renewed.invalid/lower", fixture.blobs[0])
	transport.serve("https://renewed.invalid/upper", fixture.blobs[1])
	for url, fault := range opts.faults {
		transport.fault(url, fault)
	}
	source := NewPartContentSource(transport)
	var taken func() []*RenewalRequest
	if opts.attach {
		reply := opts.reply
		taken = renewalConsumer(t, attachTestBridge(t, source), func(request *RenewalRequest) RenewalReply { return reply(fixture, request) })
	}
	if opts.configure != nil {
		opts.configure(store)
	}
	offer := PersistedPartOffer{Chain: OfferedChain{Layers: fixture.chain.Layers, RenewalKey: opts.key, Addresses: map[digest.Digest]BlobAddress{fixture.digest(0): lower, fixture.digest(1): opts.upper}}}
	if opts.keyOnly {
		offer.Chain.Addresses = nil
	}
	provider := source.Provider(t.Context(), offer, &PartDemandState{target: PersistedPartAddress{Part: "snapshot"}})
	for i := range fixture.chain.Layers {
		_, err := provider.Info(t.Context(), fixture.digest(i))
		require.NoError(t, err)
	}
	require.Zero(t, transport.total(), "Info makes no request")
	ref, err := store.Manager.ImportChain(t.Context(), &snapshots.ExportChain{Layers: fixture.chain.Layers, Provider: provider})
	out := chainRunResult{err: err, transport: transport}
	if err == nil {
		testutil.CheckFile(t, ref, "lower.txt", "lower bytes "+fixture.tag)
		testutil.CheckFile(t, ref, "upper.txt", "upper bytes "+fixture.tag)
		require.NoError(t, ref.Release(t.Context()))
	}
	if taken != nil {
		out.requests = taken()
	}
	require.Zero(t, transport.open.Load(), "every response is closed")
	require.Zero(t, transport.count(expiredTestAddress.URL), "expired addresses are never requested")
	if opts.localPrefix {
		require.Zero(t, transport.count("https://initial.invalid/lower")+transport.count("https://renewed.invalid/lower"), "the local prefix needs no bytes")
	}
	return out
}

func requireChainContentFailure(t *testing.T, err error, layer digest.Digest, stage string) {
	t.Helper()
	var contentErr *snapshots.ChainContentError
	require.ErrorAs(t, err, &contentErr)
	require.Equal(t, stage, contentErr.Stage)
	require.Equal(t, layer, contentErr.Layer)
}

func TestRenewalChainControls(t *testing.T) {
	fixture := newChainFixture(t, "controls")
	upper := fixture.digest(1)
	initial := BlobAddress{URL: "https://initial.invalid/upper"}
	t.Run("expired address renews once", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: expiredTestAddress, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
		require.NoError(t, got.err)
		require.Len(t, got.requests, 1)
		request := got.requests[0]
		require.Equal(t, upper, request.NeededBlob)
		require.Equal(t, "key", request.RenewalKey)
		chain, err := renewalChainFingerprint(fixture.chain.Layers)
		require.NoError(t, err)
		require.Equal(t, chain, request.Chain)
		copied, err := renewalChainFingerprint(request.Layers)
		require.NoError(t, err)
		require.Equal(t, chain, copied, "the request carries the exact ordered layers")
		require.Equal(t, 1, got.transport.count("https://renewed.invalid/upper"))
		require.Equal(t, 1, got.transport.total())
	})
	t.Run("all local layers need no address", func(t *testing.T) {
		store := testutil.NewStore(t)
		ref, err := store.Manager.ImportChain(t.Context(), &snapshots.ExportChain{Layers: fixture.chain.Layers, Provider: fixture.chain.Provider})
		require.NoError(t, err)
		defer ref.Release(context.Background())
		transport := newContentTestTransport()
		source := NewPartContentSource(transport)
		taken := renewalConsumer(t, attachTestBridge(t, source), nil)
		offer := PersistedPartOffer{Chain: OfferedChain{Layers: fixture.chain.Layers, RenewalKey: "key"}}
		again, err := store.Manager.ImportChain(t.Context(), &snapshots.ExportChain{Layers: fixture.chain.Layers, Provider: source.Provider(t.Context(), offer, &PartDemandState{})})
		require.NoError(t, err)
		require.Equal(t, ref.SnapshotID(), again.SnapshotID())
		require.NoError(t, again.Release(t.Context()))
		require.Zero(t, transport.total())
		require.Empty(t, taken())
	})
	// An offer may carry a renewal key and no address yet. Its first renewal
	// supplies every address the chain needs.
	t.Run("a key-only offer renews into its first addresses", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{keyOnly: true, attach: true, key: "key", reply: func(f chainFixture, request *RenewalRequest) RenewalReply {
			return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{f.digest(0): {URL: "https://renewed.invalid/lower"}, f.digest(1): {URL: "https://renewed.invalid/upper"}}}
		}})
		require.NoError(t, got.err)
		require.Len(t, got.requests, 1)
		require.Equal(t, 1, got.transport.count("https://renewed.invalid/lower"))
		require.Equal(t, 1, got.transport.count("https://renewed.invalid/upper"))
		require.Equal(t, 2, got.transport.total())
	})
	t.Run("one episode covers the chain", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{lower: expiredTestAddress, upper: expiredTestAddress, attach: true, key: "key", reply: func(f chainFixture, request *RenewalRequest) RenewalReply {
			return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{f.digest(0): {URL: "https://renewed.invalid/lower"}, f.digest(1): {URL: "https://renewed.invalid/upper"}}}
		}})
		require.NoError(t, got.err)
		require.Len(t, got.requests, 1)
		require.Equal(t, fixture.digest(0), got.requests[0].NeededBlob)
		require.Equal(t, 1, got.transport.count("https://renewed.invalid/lower"))
		require.Equal(t, 1, got.transport.count("https://renewed.invalid/upper"))
	})
	t.Run("a later unusable layer cannot renew again", func(t *testing.T) {
		// The reply refreshes only the lower layer; the upper layer then fails
		// without a second exchange.
		got := runChainImport(t, fixture, chainRunOptions{lower: expiredTestAddress, upper: expiredTestAddress, attach: true, key: "key", reply: func(f chainFixture, request *RenewalRequest) RenewalReply {
			return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{f.digest(0): {URL: "https://renewed.invalid/lower"}}}
		}})
		requireChainContentFailure(t, got.err, upper, "provider")
		require.ErrorIs(t, got.err, ErrRenewalUnavailable)
		require.Len(t, got.requests, 1)
		require.Equal(t, 1, got.transport.total())
	})
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone} {
		t.Run(fmt.Sprintf("initial %d renews", status), func(t *testing.T) {
			got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: initial, faults: map[string]contentTestFault{initial.URL: {status: status}}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
			require.NoError(t, got.err)
			require.Len(t, got.requests, 1)
			require.Equal(t, 1, got.transport.count(initial.URL))
			require.Equal(t, 1, got.transport.count("https://renewed.invalid/upper"))
		})
		t.Run(fmt.Sprintf("renewed %d exhausts", status), func(t *testing.T) {
			got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: initial, faults: map[string]contentTestFault{initial.URL: {status: http.StatusForbidden}, "https://renewed.invalid/upper": {status: status}}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
			requireChainContentFailure(t, got.err, upper, "provider")
			require.ErrorContains(t, got.err, fmt.Sprintf("HTTP %d", status))
			require.Len(t, got.requests, 1, "the renewed attempt cannot claim another episode")
			require.Equal(t, 1, got.transport.count("https://renewed.invalid/upper"))
		})
	}
	t.Run("same address in reply is not a new opportunity", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: initial, faults: map[string]contentTestFault{initial.URL: {status: http.StatusForbidden}}, attach: true, key: "key", reply: renewUpper(initial.URL)})
		requireChainContentFailure(t, got.err, upper, "provider")
		require.ErrorIs(t, got.err, ErrRenewalUnavailable)
		require.Equal(t, 1, got.transport.count(initial.URL))
	})
	t.Run("transport error does not renew", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: BlobAddress{URL: "https://unreachable.invalid/upper"}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
		requireChainContentFailure(t, got.err, upper, "provider")
		require.ErrorContains(t, got.err, "no object")
		require.Empty(t, got.requests)
	})
	t.Run("server error does not renew", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: initial, faults: map[string]contentTestFault{initial.URL: {status: http.StatusInternalServerError}}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
		requireChainContentFailure(t, got.err, upper, "provider")
		require.ErrorContains(t, got.err, "status 500")
		require.Empty(t, got.requests)
	})
	t.Run("non-HTTP address is unavailable", func(t *testing.T) {
		got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: BlobAddress{URL: "s3://bucket/upper"}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
		requireChainContentFailure(t, got.err, upper, "provider")
		require.ErrorIs(t, got.err, ErrRenewalUnavailable)
		require.Empty(t, got.requests, "a non-HTTP scheme is not a renewal trigger")
		require.Zero(t, got.transport.total())
	})
	for name, fault := range map[string]contentTestFault{"truncated": {truncate: true, stallAfter: -1}, "wrong digest": {corrupt: true, stallAfter: -1}} {
		t.Run(name, func(t *testing.T) {
			got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: initial, faults: map[string]contentTestFault{initial.URL: fault}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
			requireChainContentFailure(t, got.err, upper, "copy")
			require.Empty(t, got.requests, "a byte failure never requests addresses")
			require.Equal(t, 1, got.transport.total())
		})
	}
	t.Run("stalled stream", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			started := time.Now()
			got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: initial, faults: map[string]contentTestFault{initial.URL: {stallAfter: 10}}, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper")})
			requireChainContentFailure(t, got.err, upper, "copy")
			require.ErrorIs(t, got.err, errPartContentIdle)
			require.Equal(t, partContentIdleTimeout, time.Since(started))
			require.Empty(t, got.requests)
		})
	})
	failure := errors.New("injected local storage failure")
	for name, configure := range map[string]func(*testutil.Store){
		"local writer fault": func(s *testutil.Store) { s.BeforeWrite = func([]byte) error { return failure } },
		"local lease fault": func(s *testutil.Store) {
			s.BeforeAdd = func(context.Context, leases.Lease, leases.Resource) error { return failure }
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := runChainImport(t, fixture, chainRunOptions{localPrefix: true, upper: expiredTestAddress, attach: true, key: "key", reply: renewUpper("https://renewed.invalid/upper"), configure: configure})
			require.ErrorIs(t, got.err, failure)
			var contentErr *snapshots.ChainContentError
			require.False(t, errors.As(got.err, &contentErr), "local storage keeps its own classification")
		})
	}
	for name, tc := range map[string]struct {
		opts   chainRunOptions
		reason string
		taken  int
	}{
		"no bridge":      {opts: chainRunOptions{key: "key"}, reason: "no bridge attached"},
		"no renewal key": {opts: chainRunOptions{attach: true, reply: renewUpper("https://renewed.invalid/upper")}, reason: "offer has no renewal key"},
		"negative reply": {opts: chainRunOptions{attach: true, key: "key", reply: refuseRenewal}, reason: "integration reported no address", taken: 1},
		"expired reply": {opts: chainRunOptions{attach: true, key: "key", reply: func(_ chainFixture, request *RenewalRequest) RenewalReply {
			return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{request.NeededBlob: {URL: "https://renewed.invalid/upper", ExpiresAtUnix: 1}}}
		}}, reason: "no usable address", taken: 1},
		"non-HTTP reply": {opts: chainRunOptions{attach: true, key: "key", reply: renewUpper("ftp://renewed.invalid/upper")}, reason: "no usable address", taken: 1},
		"reply for another blob": {opts: chainRunOptions{attach: true, key: "key", reply: func(f chainFixture, request *RenewalRequest) RenewalReply {
			return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{f.digest(0): {URL: "https://renewed.invalid/lower"}}}
		}}, reason: "no usable address", taken: 1},
	} {
		t.Run(name, func(t *testing.T) {
			tc.opts.localPrefix = true
			tc.opts.upper = expiredTestAddress
			got := runChainImport(t, fixture, tc.opts)
			requireChainContentFailure(t, got.err, upper, "provider")
			require.ErrorIs(t, got.err, ErrRenewalUnavailable)
			require.ErrorContains(t, got.err, tc.reason)
			require.Len(t, got.requests, tc.taken)
			require.Zero(t, got.transport.total())
		})
	}
}

var errRenewalFallbackRan = errors.New("receiver Lazy operation ran")

type renewalFallbackOperation struct{ runs atomic.Int32 }

func (o *renewalFallbackOperation) Run(context.Context) error {
	o.runs.Add(1)
	return errRenewalFallbackRan
}
func (o *renewalFallbackOperation) Capture(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	return PersistedObjectEncoding{}, errors.New("fallback capture is not reached")
}
func (o *renewalFallbackOperation) Release(context.Context) error { return nil }

type exhaustionFixture struct {
	ctx       context.Context
	cache     *Cache
	srv       *Server
	transport *contentTestTransport
	receiver  AnyResult
	donor     AnyResult
	operation *renewalFallbackOperation
	demand    *PartDemandState
	address   PersistedPartAddress
}

func newExhaustionFixture(t *testing.T, chains ...chainFixture) *exhaustionFixture {
	t.Helper()
	store := testutil.NewStore(t)
	ctx, c, srv := transferTestCache(t)
	c.snapshotManager = store.Manager
	f := &exhaustionFixture{ctx: ctx, cache: c, srv: srv, transport: newContentTestTransport(), operation: new(renewalFallbackOperation), address: PersistedPartAddress{Part: "snapshot"}}
	for _, chain := range chains {
		for i, blob := range chain.blobs {
			f.transport.serve(fmt.Sprintf("https://fixed.invalid/%s/%d", chain.tag, i), blob)
			f.transport.serve(fmt.Sprintf("https://renewed.invalid/%s/%d", chain.tag, i), blob)
		}
	}
	// In-package fixtures supply their transport at the construction point,
	// before the cache is used.
	c.partContentSource = NewPartContentSource(f.transport)
	f.receiver = persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	f.donor = persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "pending"})
	partTestEquivalent(t, c, f.receiver, f.donor)
	partEncodedReceiver(t, ctx, c, f.receiver)
	id := uint64(f.receiver.cacheSharedResult().id)
	partLazyPreparationHooks.Store(id, func(context.Context) (LazyOperationInvocation, error) { return f.operation, nil })
	t.Cleanup(func() { partLazyPreparationHooks.Delete(id) })
	f.demand = &PartDemandState{target: f.address}
	return f
}

func exhaustionOffer(chain chainFixture, key string, fixed bool) PersistedPartOffer {
	addresses := map[digest.Digest]BlobAddress{}
	for i := range chain.blobs {
		address := expiredTestAddress
		if fixed {
			address = BlobAddress{URL: fmt.Sprintf("https://fixed.invalid/%s/%d", chain.tag, i)}
		}
		addresses[chain.digest(i)] = address
	}
	return PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: chain.chain.Layers, Addresses: addresses, RenewalKey: key}}
}

func (f *exhaustionFixture) attach(t *testing.T, row AnyResult, offer PersistedPartOffer) {
	t.Helper()
	f.cache.egraphMu.Lock()
	defer f.cache.egraphMu.Unlock()
	owner, err := f.cache.newOfferOwnerLocked(f.ctx, offer.Owner)
	require.NoError(t, err)
	require.NoError(t, f.cache.attachPartOfferLocked(row.cacheSharedResult(), offer.Address, &partOffer{record: offer, owner: owner}))
}

// demand repeats the receiver's Lazy-operation decision as the demand
// dispatcher does, until it installs a source or evaluates the operation.
func (f *exhaustionFixture) run() error {
	route := LazyOperationRoute{Group: LazyGroupAddress{Group: LazyGroupWhole}, WriteSet: []PersistedPartAddress{f.address}, HasLazyOperation: true}
	for {
		err := f.cache.runLazyOperationDecision(f.ctx, f.receiver, f.address, route, f.demand)
		if !partCanReselect(err) {
			return err
		}
	}
}

func (f *exhaustionFixture) installed() bool {
	return len(f.receiver.cacheSharedResult().loadSnapshotOwnerLinks()) == 1
}

func renewChain(chain chainFixture) func(*RenewalRequest) RenewalReply {
	return func(request *RenewalRequest) RenewalReply {
		addresses := map[digest.Digest]BlobAddress{}
		for i := range chain.blobs {
			addresses[chain.digest(i)] = BlobAddress{URL: fmt.Sprintf("https://renewed.invalid/%s/%d", chain.tag, i)}
		}
		return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: addresses}
	}
}

func TestRenewalExhaustion(t *testing.T) {
	chain := newChainFixture(t, "shared")
	other := newChainFixture(t, "distinct")
	refuse := func(request *RenewalRequest) RenewalReply {
		return RenewalReply{ID: request.ID, Chain: request.Chain, Unavailable: true}
	}
	requireFallback := func(t *testing.T, f *exhaustionFixture, err error, reason string) {
		t.Helper()
		require.ErrorIs(t, err, errRenewalFallbackRan)
		require.EqualValues(t, 1, f.operation.runs.Load())
		require.False(t, f.installed())
		require.ErrorIs(t, f.demand.causes(), ErrRenewalUnavailable)
		require.ErrorContains(t, f.demand.causes(), reason)
	}
	t.Run("equivalent donor fixed address still works", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		taken := renewalConsumer(t, attachTestBridge(t, f.cache.PartContentSource()), refuse)
		f.attach(t, f.receiver, exhaustionOffer(chain, "receiver-key", false))
		f.attach(t, f.donor, exhaustionOffer(chain, "donor-key", true))
		require.NoError(t, f.run())
		require.True(t, f.installed())
		require.Len(t, taken(), 1)
		require.Equal(t, "receiver-key", taken()[0].RenewalKey)
		require.Zero(t, f.operation.runs.Load())
		require.Equal(t, 1, f.transport.count("https://fixed.invalid/shared/1"))
		require.EqualValues(t, 1, f.demand.revision, "only the refused receiver chain was exhausted")
	})
	t.Run("equivalent donor key does not reset the episode", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		// The donor's key would be answered; its content already used the episode.
		taken := renewalConsumer(t, attachTestBridge(t, f.cache.PartContentSource()), func(request *RenewalRequest) RenewalReply {
			if request.RenewalKey == "receiver-key" {
				return refuse(request)
			}
			return renewChain(chain)(request)
		})
		f.attach(t, f.receiver, exhaustionOffer(chain, "receiver-key", false))
		f.attach(t, f.donor, exhaustionOffer(chain, "donor-key", false))
		requireFallback(t, f, f.run(), "integration reported no address")
		require.Len(t, taken(), 1)
		require.EqualValues(t, 2, f.demand.revision, "both sources were tried and exhausted")
		require.Zero(t, f.transport.total())
	})
	t.Run("distinct chain can still renew", func(t *testing.T) {
		f := newExhaustionFixture(t, chain, other)
		taken := renewalConsumer(t, attachTestBridge(t, f.cache.PartContentSource()), func(request *RenewalRequest) RenewalReply {
			if request.RenewalKey == "receiver-key" {
				return refuse(request)
			}
			return renewChain(other)(request)
		})
		f.attach(t, f.receiver, exhaustionOffer(chain, "receiver-key", false))
		f.attach(t, f.donor, exhaustionOffer(other, "donor-key", false))
		require.NoError(t, f.run())
		require.True(t, f.installed())
		require.Len(t, taken(), 2)
		require.Zero(t, f.operation.runs.Load())
		require.Equal(t, 1, f.transport.count("https://renewed.invalid/distinct/1"))
	})
	for name, replace := range map[string]func(PersistedPartOffer) PersistedPartOffer{
		"rotated key": func(o PersistedPartOffer) PersistedPartOffer {
			o.Chain.RenewalKey = "rotated-key"
			return o
		},
		"refreshed expiry": func(o PersistedPartOffer) PersistedPartOffer {
			for d := range o.Chain.Addresses {
				o.Chain.Addresses[d] = BlobAddress{URL: "https://old.invalid/blob", ExpiresAtUnix: 2}
			}
			return o
		},
	} {
		t.Run("slot replacement with "+name, func(t *testing.T) {
			f := newExhaustionFixture(t, chain)
			offer := exhaustionOffer(chain, "receiver-key", false)
			var replaced *OfferDisposition
			bridge := attachTestBridge(t, f.cache.PartContentSource())
			taken := renewalConsumer(t, bridge, func(request *RenewalRequest) RenewalReply {
				// The integration replaces the slot while the episode is pending.
				copied, err := clonePartOffers([]PersistedPartOffer{offer})
				require.NoError(t, err)
				out, err := f.cache.OfferParts(f.ctx, f.receiver, []PersistedPartOffer{replace(copied[0])})
				if err == nil {
					replaced = &out[0]
				}
				return refuse(request)
			})
			f.attach(t, f.receiver, offer)
			requireFallback(t, f, f.run(), "integration reported no address")
			require.NotNil(t, replaced)
			require.Equal(t, OfferAccepted, replaced.Outcome)
			require.True(t, replaced.Replaced)
			require.Len(t, taken(), 1, "the replacement's new revision is retried without another episode")
			require.EqualValues(t, 2, f.demand.revision, "the per-source set admitted the new revision")
		})
	}
	t.Run("no bridge", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		f.attach(t, f.receiver, exhaustionOffer(chain, "receiver-key", false))
		requireFallback(t, f, f.run(), "no bridge attached")
	})
	t.Run("no renewal key", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		taken := renewalConsumer(t, attachTestBridge(t, f.cache.PartContentSource()), renewChain(chain))
		f.attach(t, f.receiver, exhaustionOffer(chain, "", false))
		requireFallback(t, f, f.run(), "offer has no renewal key")
		require.Empty(t, taken())
	})
	t.Run("full mailbox", func(t *testing.T) {
		f := newExhaustionFixture(t, chain)
		bridge := attachTestBridge(t, f.cache.PartContentSource())
		ctx, cancel := context.WithCancel(context.Background())
		var fillers sync.WaitGroup
		defer func() {
			cancel()
			fillers.Wait()
		}()
		for range renewalMailboxCapacity {
			fillers.Go(func() {
				_, _ = bridge.request(ctx, RenewalRequest{Deadline: time.Now().Add(time.Hour)})
			})
		}
		waitQueued(t, bridge, renewalMailboxCapacity)
		f.attach(t, f.receiver, exhaustionOffer(chain, "receiver-key", false))
		requireFallback(t, f, f.run(), "mailbox full")
		live, queued := liveExchanges(bridge)
		require.Equal(t, renewalMailboxCapacity, live, "no request was enqueued")
		require.Equal(t, renewalMailboxCapacity, queued)
	})
}
