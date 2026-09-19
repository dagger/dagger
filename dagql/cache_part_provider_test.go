package dagql

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPartFixedProviderAndContentExhaustion(t *testing.T) {
	const body = "bounded range content"
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeContent(w, r, "blob", time.Time{}, strings.NewReader(body))
	}))
	defer server.Close()
	descriptor := ocispec.Descriptor{Digest: digest.FromString(body), Size: int64(len(body))}
	address := PersistedPartAddress{OutputPath: PersistedRefPath{}.Field("items").Index(0), Part: "snapshot"}
	offer := PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: []snapshots.ExportLayer{{Descriptor: descriptor}}, Addresses: map[digest.Digest]BlobAddress{descriptor.Digest: {URL: server.URL}}}}
	source := NewPartContentSource(nil)
	require.True(t, source.Available(offer, time.Now()))
	provider := source.Provider(t.Context(), offer, &PartDemandState{})
	_, err := provider.Info(t.Context(), descriptor.Digest)
	require.NoError(t, err)
	require.Zero(t, requests.Load())
	reader, err := provider.ReaderAt(t.Context(), descriptor)
	require.NoError(t, err)
	require.Zero(t, requests.Load())
	got, err := io.ReadAll(io.NewSectionReader(reader, 3, 8))
	require.NoError(t, err)
	require.Equal(t, body[3:11], string(got))
	require.EqualValues(t, 1, requests.Load())
	require.NoError(t, reader.Close())
	_, err = reader.ReadAt(make([]byte, 2), 0)
	require.ErrorIs(t, err, context.Canceled)
	offer.Chain.Addresses[descriptor.Digest] = BlobAddress{URL: server.URL, ExpiresAtUnix: 1}
	require.False(t, source.Available(offer, time.Now()))
	_, err = source.Provider(t.Context(), offer, &PartDemandState{}).ReaderAt(t.Context(), descriptor)
	require.Error(t, err)
	require.EqualValues(t, 1, requests.Load())
	demand := &PartDemandState{}
	admitted := &PartSourceLease{sourceID: 7, descriptor: PartDescriptor{Address: address}, offer: &offer, offerRev: 1}
	failure := errors.New("missing content")
	demand.exhaust(admitted, failure)
	require.True(t, demand.exhausted(7, address, &offer, 1))
	require.ErrorIs(t, demand.causes(), failure)
	refreshed, _ := clonePartOffers([]PersistedPartOffer{offer})
	refreshed[0].Chain.Addresses[descriptor.Digest] = BlobAddress{URL: server.URL + "/fresh", ExpiresAtUnix: time.Now().Unix() + 3600}
	require.True(t, demand.exhausted(7, address, &refreshed[0], 1), "addresses alone do not change the admitted revision")
	require.False(t, demand.exhausted(7, address, &refreshed[0], 2), "a newly admitted offer revision is eligible")
	require.False(t, demand.exhausted(8, address, &refreshed[0], 1), "a different source remains eligible")
	sibling := clonePartAddress(address)
	sibling.OutputPath[1].Index = 1
	require.False(t, demand.exhausted(7, sibling, &refreshed[0], 1))
	refreshed[0].Chain.Layers[0].Descriptor.Digest = digest.FromString("new content")
	require.False(t, demand.exhausted(7, address, &refreshed[0], 1))
}

func TestPartOfferReplacementNotExhausted(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	address := PersistedPartAddress{Part: "snapshot"}
	record := PersistedPartOffer{Address: address, Value: SnapshotValue{Kind: "directory"}}
	replace := func() {
		c.egraphMu.Lock()
		owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
		require.NoError(t, err)
		queue, err := c.replacePartOfferLocked(ctx, receiver.cacheSharedResult(), address, &partOffer{record: record, owner: owner})
		require.NoError(t, err)
		callbacks, err := c.collectUnownedResultsLocked(ctx, queue)
		c.egraphMu.Unlock()
		require.NoError(t, err)
		require.NoError(t, runOnReleaseFuncs(ctx, callbacks))
	}
	replace()
	demand := &PartDemandState{}
	source, _, err := c.scanPartSources(ctx, receiver, address, demand)
	require.NoError(t, err)
	require.NotNil(t, source)
	oldRevision := source.offerRev
	demand.exhaust(source, errors.New("first content attempt failed"))
	require.NoError(t, source.Release(ctx))
	source, _, err = c.scanPartSources(ctx, receiver, address, demand)
	require.NoError(t, err)
	require.Nil(t, source)
	replace() // Same immutable content, newly admitted revision.
	source, _, err = c.scanPartSources(ctx, receiver, address, demand)
	require.NoError(t, err)
	require.NotNil(t, source)
	require.Greater(t, source.offerRev, oldRevision)
	require.NoError(t, source.Release(ctx))
}

func TestPartFixedProviderMultiBuffer(t *testing.T) {
	body := strings.Repeat("0123456789abcdef", 20000)
	for _, ranges := range []bool{true, false} {
		name := "full-body"
		if ranges {
			name = "ranges"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Range") == "" {
					t.Error("every request names the remainder it needs")
				}
				if ranges {
					http.ServeContent(w, r, "blob", time.Time{}, strings.NewReader(body))
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			descriptor := ocispec.Descriptor{Digest: digest.FromString(body), Size: int64(len(body))}
			offer := PersistedPartOffer{Chain: OfferedChain{Layers: []snapshots.ExportLayer{{Descriptor: descriptor}}, Addresses: map[digest.Digest]BlobAddress{descriptor.Digest: {URL: server.URL}}}}
			reader, err := (*PartContentSource)(nil).Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
			require.NoError(t, err)
			defer reader.Close()
			const bufferSize = 16 * 1024
			buffer := make([]byte, bufferSize)
			reads := int64(0)
			for off := 0; off < len(body); {
				n, err := reader.ReadAt(buffer, int64(off))
				require.Equal(t, body[off:off+n], string(buffer[:n]))
				if off+n == len(body) {
					require.ErrorIs(t, err, io.EOF)
				} else {
					require.NoError(t, err)
				}
				off += n
				reads++
			}
			// Batch 5 keeps one sequential response for contiguous reads, whether
			// the endpoint answers the open-ended Range with 206 or with 200.
			want := int64(1)
			require.Equal(t, want, requests.Load())
			// A nonsequential offset starts a fresh Range request; a 200 response
			// discards just the prefix and then resumes sequential consumption.
			n, err := reader.ReadAt(buffer[:31], 37)
			require.NoError(t, err)
			require.Equal(t, body[37:68], string(buffer[:n]))
			n, err = reader.ReadAt(buffer[:31], 68)
			require.NoError(t, err)
			require.Equal(t, body[68:99], string(buffer[:n]))
			extra := int64(1)
			require.Equal(t, want+extra, requests.Load(), "the contiguous read reuses the reopened response")
			n, err = reader.ReadAt(buffer[:31], 7)
			require.NoError(t, err)
			require.Equal(t, body[7:38], string(buffer[:n]))
			require.Equal(t, want+extra+1, requests.Load(), "rewind closes the retained stream and reissues")
			t.Logf("bytes=%d buffer=%d reads=%d requests=%d initial-path-requests=%d", len(body), bufferSize, reads, requests.Load(), want)
		})
	}
}

func TestPartFixedProviderCancellationClosesStream(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	descriptor := ocispec.Descriptor{Digest: digest.FromString("stream"), Size: 100000}
	// A provider serves only blobs of its offered chain.
	offer := PersistedPartOffer{Chain: OfferedChain{Layers: []snapshots.ExportLayer{{Descriptor: descriptor}}, Addresses: map[digest.Digest]BlobAddress{descriptor.Digest: {URL: server.URL}}}}
	reader, err := (*PartContentSource)(nil).Provider(ctx, offer, nil).ReaderAt(ctx, descriptor)
	require.NoError(t, err)
	_, err = reader.ReadAt(make([]byte, 16), 0)
	require.NoError(t, err)
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not close retained response")
	}
	_, err = reader.ReadAt(make([]byte, 16), 16)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, reader.Close())
}
