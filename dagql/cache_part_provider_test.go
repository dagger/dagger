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
	source := fixedPartContentSource{}
	require.True(t, source.Available(offer, time.Now().Unix()))
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
	require.False(t, source.Available(offer, time.Now().Unix()))
	_, err = source.Provider(t.Context(), offer, &PartDemandState{}).ReaderAt(t.Context(), descriptor)
	require.Error(t, err)
	require.EqualValues(t, 1, requests.Load())
	demand := &PartDemandState{}
	admitted := &PartSourceLease{sourceID: 7, descriptor: PartDescriptor{Address: address}, offer: &offer, offerRev: 1}
	failure := errors.New("missing content")
	demand.exhaust(admitted, failure)
	require.True(t, demand.exhausted(7, address, &offer))
	require.ErrorIs(t, demand.causes(), failure)
	refreshed, _ := clonePartOffers([]PersistedPartOffer{offer})
	refreshed[0].Chain.Addresses[descriptor.Digest] = BlobAddress{URL: server.URL + "/fresh", ExpiresAtUnix: time.Now().Unix() + 3600}
	require.True(t, demand.exhausted(7, address, &refreshed[0]), "fresh addresses do not repeat exhausted content")
	require.False(t, demand.exhausted(8, address, &refreshed[0]), "a different source remains eligible")
	sibling := clonePartAddress(address)
	sibling.OutputPath[1].Index = 1
	require.False(t, demand.exhausted(7, sibling, &refreshed[0]))
	refreshed[0].Chain.Layers[0].Descriptor.Digest = digest.FromString("new content")
	require.False(t, demand.exhausted(7, address, &refreshed[0]))
}
