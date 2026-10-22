package dagql

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

// rangeTestBlob is size bytes that don't repeat with any short period, so a
// chunk placed at the wrong offset shows.
func rangeTestBlob(size int) string {
	rng := rand.New(rand.NewPCG(1, 2))
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(rng.Uint32())
	}
	return string(data)
}

// A blob of at least the parallel threshold is read through several ranged
// requests, one per chunk, and its bytes arrive in order.
func TestPartRangeReadsLargeBlob(t *testing.T) {
	const size = 40 << 20
	data := rangeTestBlob(size)
	transport, source, offer, descriptor := contentReaderFixture(t, data)
	reader, err := source.Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
	require.NoError(t, err)
	got, err := io.ReadAll(io.NewSectionReader(reader, 0, size))
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.True(t, string(got) == data, "the blob's bytes arrive whole and in order")
	require.Equal(t, 5, transport.total(), "one request per 8 MiB chunk")
	transport.mu.Lock()
	ranges := slices.Clone(transport.ranges)
	transport.mu.Unlock()
	slices.Sort(ranges)
	require.Equal(t, []string{
		"bytes=0-8388607",
		"bytes=16777216-25165823",
		"bytes=25165824-33554431",
		"bytes=33554432-41943039",
		"bytes=8388608-16777215",
	}, ranges)
}

// rangeTestReader opens a reader for a size-byte blob with the given parallel
// config changes, counting its requests on stats.
func rangeTestReader(t *testing.T, size int, configure func(*partRangeConfig)) (*contentTestTransport, *PartContentSource, PersistedPartOffer, string, *partInstallStats) {
	t.Helper()
	data := rangeTestBlob(size)
	transport, source, offer, _ := contentReaderFixture(t, data)
	if configure != nil {
		configure(&source.ranges)
	}
	return transport, source, offer, data, &partInstallStats{}
}

// rangeTestSource is a size-byte blob's source and offer with the given
// parallel config changes, for tests that open their own readers.
func rangeTestSource(t *testing.T, size int, configure func(*partRangeConfig)) (*PartContentSource, PersistedPartOffer) {
	t.Helper()
	_, source, offer, _ := contentReaderFixture(t, rangeTestBlob(size))
	if configure != nil {
		configure(&source.ranges)
	}
	return source, offer
}

func openRangeTest(t *testing.T, source *PartContentSource, offer PersistedPartOffer, demand *PartDemandState, stats *partInstallStats) (contentReader, int64) {
	t.Helper()
	provider := source.Provider(t.Context(), offer, demand).(*partContentProvider)
	provider.stats = stats
	desc := offer.Chain.Layers[0].Descriptor
	reader, err := provider.ReaderAt(t.Context(), desc)
	require.NoError(t, err)
	return reader.(contentReader), desc.Size
}

type contentReader interface {
	io.ReaderAt
	io.Closer
	Size() int64
}

func readRangeTest(t *testing.T, reader contentReader, size int64) (string, error) {
	t.Helper()
	got, err := io.ReadAll(io.NewSectionReader(reader, 0, size))
	return string(got), err
}

// A store that answers a range with the whole object moves the rest of the
// blob onto one stream; the bytes still arrive whole, and the install counts
// the 200.
func TestPartRangeReadsWholeObjectFallback(t *testing.T) {
	transport, source, offer, data, stats := rangeTestReader(t, 40<<20, nil)
	transport.fault("https://blobs.invalid/blob", contentTestFault{ignoreRange: true, stallAfter: -1})
	reader, size := openRangeTest(t, source, offer, nil, stats)
	got, err := readRangeTest(t, reader, size)
	require.NoError(t, err)
	require.True(t, got == data)
	require.NoError(t, reader.Close())
	mode, statuses := stats.httpStatuses()
	require.Equal(t, "200", mode)
	require.True(t, strings.HasPrefix(statuses, "200="), statuses)
	require.Zero(t, transport.open.Load(), "every response is closed")
}

// A chunk whose response breaks off is requested again, up to the retry
// bound; past it, the read fails as a content failure.
func TestPartRangeReadsRetryChunk(t *testing.T) {
	t.Run("transient", func(t *testing.T) {
		transport, source, offer, data, stats := rangeTestReader(t, 40<<20, nil)
		transport.fault("https://blobs.invalid/blob", contentTestFault{truncateFirst: 2, stallAfter: -1})
		reader, size := openRangeTest(t, source, offer, nil, stats)
		got, err := readRangeTest(t, reader, size)
		require.NoError(t, err)
		require.True(t, got == data)
		require.NoError(t, reader.Close())
		require.Equal(t, 7, transport.total(), "five chunks and two retries")
	})
	t.Run("persistent", func(t *testing.T) {
		transport, source, offer, _, stats := rangeTestReader(t, 40<<20, nil)
		transport.fault("https://blobs.invalid/blob", contentTestFault{truncate: true, stallAfter: -1})
		reader, size := openRangeTest(t, source, offer, nil, stats)
		_, err := readRangeTest(t, reader, size)
		var contentErr *snapshots.ChainContentError
		require.ErrorAs(t, err, &contentErr)
		require.Equal(t, "copy", contentErr.Stage)
		require.NoError(t, reader.Close())
		require.Zero(t, transport.open.Load())
	})
}

// Chunks refused with a retry status renew the address once for the demand,
// however many chunks were refused, and read on from the renewed address.
func TestPartRangeReadsRenewOnce(t *testing.T) {
	transport, source, offer, data, stats := rangeTestReader(t, 40<<20, nil)
	transport.fault("https://blobs.invalid/blob", contentTestFault{status: http.StatusForbidden})
	transport.serve("https://renewed.invalid/blob", []byte(data))
	desc := offer.Chain.Layers[0].Descriptor
	offer.Chain.RenewalKey = "key"
	bridge := attachTestBridge(t, source)
	taken := renewalConsumer(t, bridge, func(request *RenewalRequest) RenewalReply {
		return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: map[digest.Digest]BlobAddress{desc.Digest: {URL: "https://renewed.invalid/blob"}}}
	})
	reader, size := openRangeTest(t, source, offer, &PartDemandState{target: PersistedPartAddress{Part: "snapshot"}}, stats)
	got, err := readRangeTest(t, reader, size)
	require.NoError(t, err)
	require.True(t, got == data)
	require.NoError(t, reader.Close())
	require.Len(t, taken(), 1, "one renewal for the demand")
	require.Positive(t, transport.count("https://blobs.invalid/blob"))
	require.Equal(t, 5, transport.count("https://renewed.invalid/blob"))
}

// Closing a reader mid-blob closes every request and returns its streams to
// the engine's budget.
func TestPartRangeReadsCloseCancels(t *testing.T) {
	transport, source, offer, _, stats := rangeTestReader(t, 40<<20, nil)
	transport.opened = make(chan struct{}, 8)
	transport.fault("https://blobs.invalid/blob", contentTestFault{stallAfter: 1})
	reader, _ := openRangeTest(t, source, offer, nil, stats)
	read := make(chan error, 1)
	go func() {
		_, err := reader.ReadAt(make([]byte, 1<<20), 0)
		read <- err
	}()
	for range 4 {
		within(t, transport.opened)
	}
	require.NoError(t, reader.Close())
	require.Error(t, <-read)
	require.Zero(t, transport.open.Load(), "every response is closed")
	require.True(t, source.ranges.extra.TryAcquire(maxPartExtraStreams), "every extra stream is returned")
}

// The engine-wide budget bounds the extra streams: a reader that finds none
// left reads its blob on one stream, and a closed reader's streams go back.
func TestPartRangeReadsStreamBudget(t *testing.T) {
	source, offer := rangeTestSource(t, 40<<20, func(cfg *partRangeConfig) {
		cfg.extra = semaphore.NewWeighted(2)
	})
	first, _ := openRangeTest(t, source, offer, nil, nil)
	require.IsType(t, &partRangeReader{}, first)
	require.Equal(t, 3, first.(*partRangeReader).workers, "the first stream and the two in the budget")
	second, _ := openRangeTest(t, source, offer, nil, nil)
	require.IsType(t, &partHTTPReader{}, second, "no stream left: one stream")
	require.NoError(t, second.Close())
	require.NoError(t, first.Close())
	third, _ := openRangeTest(t, source, offer, nil, nil)
	require.IsType(t, &partRangeReader{}, third)
	require.NoError(t, third.Close())
}

// Readers sharing the budget at once each read their blob whole.
func TestPartRangeReadsConcurrent(t *testing.T) {
	transport, source, offer, data, _ := rangeTestReader(t, 40<<20, func(cfg *partRangeConfig) {
		cfg.extra = semaphore.NewWeighted(3)
	})
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for range 3 {
		reader, size := openRangeTest(t, source, offer, nil, &partInstallStats{})
		wg.Go(func() {
			got, err := io.ReadAll(io.NewSectionReader(reader, 0, size))
			if err == nil && string(got) != data {
				err = errors.New("the blob's bytes differ")
			}
			results <- errors.Join(err, reader.Close())
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Zero(t, transport.open.Load())
	require.True(t, source.ranges.extra.TryAcquire(3))
}

// A read behind the window of fetched chunks reads on one stream from there.
func TestPartRangeReadsOutsideWindow(t *testing.T) {
	_, source, offer, data, _ := rangeTestReader(t, 40<<20, nil)
	reader, _ := openRangeTest(t, source, offer, nil, nil)
	first := make([]byte, 9<<20)
	_, err := reader.ReadAt(first, 0)
	require.NoError(t, err)
	again := make([]byte, 1<<20)
	_, err = reader.ReadAt(again, 0)
	require.NoError(t, err)
	require.True(t, string(again) == data[:1<<20])
	require.NotNil(t, reader.(*partRangeReader).single)
	require.NoError(t, reader.Close())
}

// Small blobs, and a source with parallel reads off, read on one stream.
func TestPartRangeReadsThresholds(t *testing.T) {
	source, offer := rangeTestSource(t, 1<<20, nil)
	small, _ := openRangeTest(t, source, offer, nil, nil)
	require.IsType(t, &partHTTPReader{}, small)
	require.NoError(t, small.Close())
	source, offer = rangeTestSource(t, 40<<20, func(cfg *partRangeConfig) { cfg.streams = 1 })
	off, _ := openRangeTest(t, source, offer, nil, nil)
	require.IsType(t, &partHTTPReader{}, off)
	require.NoError(t, off.Close())
}

// The install line says how the part's blobs were read: the most requests
// at once, whether the store answered ranges, and each status's count.
func TestOfferInstallLogsRanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		held     bool
		streams  int64
		mode     string
		statuses string
		// ahead counts the upper layer, fetched while the lower applied.
		ahead int64
	}{
		{name: "downloaded", streams: 1, mode: "206", statuses: "206=2", ahead: 1},
		{name: "already held", held: true, mode: "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := newChainFixture(t, "install-ranges")
			f := newExhaustionFixture(t, chain)
			f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
			if tc.held {
				held, err := f.cache.snapshotManager.ImportChain(f.ctx, &snapshots.ExportChain{Layers: chain.chain.Layers, Provider: chain.chain.Provider})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, held.Release(context.Background())) })
			}
			require.NoError(t, f.run())
			installs := f.logs.lines(t, "remote cache part installed")
			require.Len(t, installs, 1)
			line := installs[0]
			require.Equal(t, tc.streams, line.int(t, "streams"))
			require.Equal(t, tc.mode, line.fields["range"])
			require.Equal(t, tc.statuses, line.fields["statuses"])
			require.Equal(t, tc.ahead, line.int(t, "ahead"))
			require.Zero(t, line.int(t, "joined"), "the import joined only its own download")
			requireFieldOrder(t, line.raw, []string{"import", "streams", "range", "statuses", "ahead", "joined", "commit"})
		})
	}
}
