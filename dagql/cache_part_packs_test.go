package dagql

import (
	"context"
	"slices"
	"testing"

	"github.com/dagger/dagger/engine/remotecache/pack"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// packTestObject returns a pack of the chain's blobs, in order: its ID and
// its bytes.
func packTestObject(t *testing.T, chain chainFixture) (digest.Digest, []byte) {
	t.Helper()
	members := make([]pack.Member, len(chain.blobs))
	for i, blob := range chain.blobs {
		members[i] = pack.Member{Digest: chain.digest(i), Size: int64(len(blob))}
	}
	header, err := pack.Header(members)
	require.NoError(t, err)
	id, err := pack.ID(members)
	require.NoError(t, err)
	data := append([]byte{}, header...)
	for _, blob := range chain.blobs {
		data = append(data, blob...)
	}
	return id, data
}

// packTestOffer offers the chain with every layer stored in the pack id, at
// url.
func packTestOffer(chain chainFixture, id digest.Digest, url string) PersistedPartOffer {
	offer := exhaustionOffer(chain, "", true)
	offer.Chain.Packs = map[digest.Digest]digest.Digest{}
	for i := range chain.blobs {
		offer.Chain.Addresses[chain.digest(i)] = BlobAddress{URL: url}
		offer.Chain.Packs[chain.digest(i)] = id
	}
	return offer
}

// A chain whose layers are stored in one pack installs from one request for
// the whole pack, and its line counts the packed layers.
func TestOfferInstallFromPack(t *testing.T) {
	chain := newChainFixture(t, "packed")
	f := newExhaustionFixture(t, chain)
	id, data := packTestObject(t, chain)
	const url = "https://fixed.invalid/packed/pack"
	f.transport.serve(url, data)
	f.attach(t, f.receiver, packTestOffer(chain, id, url))

	require.NoError(t, f.run())
	require.True(t, f.installed(), "the part installed from its pack")
	require.Equal(t, 1, f.transport.total(), "one request for the whole pack")
	require.Zero(t, f.transport.open.Load(), "every response is closed")
	installs := f.logs.lines(t, "remote cache part installed")
	require.Len(t, installs, 1)
	line := installs[0]
	require.EqualValues(t, 1, line.int(t, "requests"))
	require.EqualValues(t, len(chain.blobs), line.int(t, "packed"), "both layers came from the pack")
	require.EqualValues(t, len(data), line.int(t, "bytes"), "the install read the whole pack")
}

// A pack whose bytes don't match its ID fails the install as a content
// failure, which exhausts the source, so the part is computed instead. The
// pack is read whole, once per layer at most: a layer whose fetch starts
// after its sibling's read of the pack failed reads the pack itself, so
// under load the chain's two layers can make two requests.
func TestOfferInstallFromCorruptPack(t *testing.T) {
	chain := newChainFixture(t, "corrupt-pack")
	f := newExhaustionFixture(t, chain)
	id, data := packTestObject(t, chain)
	const url = "https://fixed.invalid/corrupt-pack/pack"
	f.transport.serve(url, data)
	f.transport.fault(url, contentTestFault{corrupt: true, stallAfter: -1})
	f.attach(t, f.receiver, packTestOffer(chain, id, url))

	require.ErrorIs(t, f.run(), errRenewalFallbackRan)
	require.False(t, f.installed())
	f.transport.mu.Lock()
	ranges := slices.Clone(f.transport.ranges)
	f.transport.mu.Unlock()
	require.NotEmpty(t, ranges, "the pack was read")
	require.LessOrEqual(t, len(ranges), len(chain.blobs), "once per layer at most")
	for _, r := range ranges {
		require.Equal(t, "bytes=0-", r, "each request reads the whole pack")
	}
	require.EqualValues(t, 1, f.demand.revision, "the pack's failure exhausted the source")
	failed := f.logs.lines(t, "remote cache part install failed")
	require.Len(t, failed, 1)
	require.Equal(t, "content", failed[0].fields["outcome"])
}

// An expired pack address renews through the demand's renewal, which names
// the chain's packs, and the pack is then read once from the renewed
// address.
func TestOfferInstallFromPackRenews(t *testing.T) {
	fixture := newChainFixture(t, "renew-pack")
	id, data := packTestObject(t, fixture)
	const url = "https://renewed.invalid/renew-pack/pack"
	transport := newContentTestTransport()
	transport.serve(url, data)
	source := NewPartContentSource(transport)
	taken := renewalConsumer(t, attachTestBridge(t, source), func(request *RenewalRequest) RenewalReply {
		addresses := map[digest.Digest]BlobAddress{}
		for _, layer := range request.Layers {
			addresses[layer.Descriptor.Digest] = BlobAddress{URL: url}
		}
		return RenewalReply{ID: request.ID, Chain: request.Chain, Addresses: addresses}
	})
	packs := map[digest.Digest]digest.Digest{fixture.digest(0): id, fixture.digest(1): id}
	offer := PersistedPartOffer{Chain: OfferedChain{Layers: fixture.chain.Layers, RenewalKey: "key", Packs: packs}}
	store := testutil.NewStore(t)
	provider := source.Provider(t.Context(), offer, &PartDemandState{target: PersistedPartAddress{Part: "snapshot"}})
	ref, err := store.Manager.ImportChain(t.Context(), &snapshots.ExportChain{Layers: fixture.chain.Layers, Provider: provider})
	require.NoError(t, err)
	testutil.CheckFile(t, ref, "lower.txt", "lower bytes renew-pack")
	testutil.CheckFile(t, ref, "upper.txt", "upper bytes renew-pack")
	require.NoError(t, ref.Release(context.Background()))
	requests := taken()
	require.Len(t, requests, 1)
	require.Equal(t, packs, requests[0].Packs, "the renewal names the chain's packs")
	require.Equal(t, 1, transport.count(url))
	require.Zero(t, transport.open.Load())
}
