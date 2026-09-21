package schema

import (
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// The fixture's file-backed content override serves only offers that name no
// address and no renewal key. Any other offer is left to the cache's real
// content source, whose own availability rule then applies.
func TestFixtureContentOverrideScope(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close(t.Context()) })
	cache.SetPartContentSource(fixturePartContentSource{path: t.TempDir()})
	blob := digest.FromString("blob")
	layers := []snapshots.ExportLayer{{Descriptor: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer, Digest: blob, Size: 4}}}
	now := time.Now()

	plain := dagql.PersistedPartOffer{Chain: dagql.OfferedChain{Layers: layers}}
	require.True(t, cache.PartContentSource().Available(plain, now), "a plain offer is served from the fixture's blob files")

	renewable := dagql.PersistedPartOffer{Chain: dagql.OfferedChain{Layers: layers, RenewalKey: "key"}}
	require.False(t, cache.PartContentSource().Available(renewable, now), "the real rule: a renewal key needs an attached bridge")

	expired := dagql.PersistedPartOffer{Chain: dagql.OfferedChain{Layers: layers, Addresses: map[digest.Digest]dagql.BlobAddress{blob: {URL: "https://content.remote-cache.invalid/blob", ExpiresAtUnix: now.Add(-time.Hour).Unix()}}}}
	require.False(t, cache.PartContentSource().Available(expired, now), "the real rule: an expired address is unusable")
	live := expired
	live.Chain.Addresses = map[digest.Digest]dagql.BlobAddress{blob: {URL: "https://content.remote-cache.invalid/blob", ExpiresAtUnix: now.Add(time.Hour).Unix()}}
	require.True(t, cache.PartContentSource().Available(live, now))
}
