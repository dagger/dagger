package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A transfer that lands on a row the engine's image already holds keeps the
// image's value: the merge leaves the sender's offers of the row's parts out,
// since the image supplies them from the engine's own disk, and the engine
// reads the row from its image. It neither downloads the row nor computes it.
//
// Engine A starts without the image's entries, pulls one of the images the
// image's entries hold, and exports it. Engine B starts as the engine ships,
// with the entries, and imports A's export without A's blobs, so a download
// from A's offer can't succeed.
func (RemoteCacheTransferSuite) TestTransferOntoImageEntry(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	start := func(volume *core.CacheVolume, withs ...func(*core.Container) *core.Container) *dagger.Client {
		ctr := devEngineContainerWithStateKey(outer, "image-entry-state-"+rand.Text(), append(withs, func(ctr *core.Container) *core.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})...)
		ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
		upstream := devEngineContainerAsService(ctr)
		tunnel, err := core.NewQuery(outer).Host().Tunnel(upstream).Start(ctx)
		require.NoError(t, err)
		endpoint, err := tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		client, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithWorkdir(t.TempDir()), dagger.WithLogOutput(testutil.ClientLogOutput(t)))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, stopNestedEngine(ctx, &client, &upstream, &tunnel))
		})
		return client
	}
	aVolume := core.NewQuery(outer).CacheVolume("image-entry-a-" + rand.Text())
	bVolume := core.NewQuery(outer).CacheVolume("image-entry-b-" + rand.Text())
	a, b := start(aVolume, withoutImageEntries), start(bVolume)

	// B's row for the smallest image pull its image's entries hold, whose
	// parts only the image offers.
	var before transferFixtureReport
	require.NoError(t, transferFixture(ctx, b, "report", "", []string{}, &before))
	var held *dagql.TransferFixtureRow
	var heldSize int64
	var pulls []string
	for i := range before.Rows {
		row := &before.Rows[i]
		if row.Call == nil || row.Call.Field != "from" || row.Call.Type == nil || row.Call.Type.NamedType != "Container" {
			continue
		}
		size, imageOnly := imageOfferSize(row.AttachedOffers)
		pulls = append(pulls, fmt.Sprintf("row %d: image held %t, %d offers, image only %t", row.ResultID, row.ImageHeld, len(row.AttachedOffers), imageOnly))
		if row.ImageHeld && imageOnly && (held == nil || size < heldSize) {
			held, heldSize = row, size
		}
	}
	require.NotNil(t, held, "B's image entries hold an image pull whose layers only the image offers: %v", pulls)
	var address string
	for _, arg := range held.Call.Args {
		if arg.Name == "address" && arg.Value != nil {
			address = arg.Value.StringValue
		}
	}
	require.NotEmpty(t, address)
	t.Logf("image-held row %d: from(%q), %d bytes", held.ResultID, address, heldSize)

	// A pulls the same image itself and exports it.
	aCtr, err := core.NewQuery(a).Container().From(address).Sync(ctx)
	require.NoError(t, err)
	aEntries, err := aCtr.Rootfs().Entries(ctx)
	require.NoError(t, err)
	aID, err := aCtr.ID(ctx)
	require.NoError(t, err)
	var exported []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, a, "image-entry.json", []string{string(aID)}, []string{string(aID)}, &exported))
	require.NotEmpty(t, exported)
	// B gets the bundle and not A's blobs.
	_, err = core.NewQuery(outer).Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", rand.Text()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/image-entry.json /destination/bundles/; test -z \"$(find /destination/blobs -type f 2>/dev/null)\""}).Sync(ctx)
	require.NoError(t, err)

	var imported []transferFixtureMapping
	require.NoError(t, transferFixture(ctx, b, "import", "image-entry.json", []string{}, &imported))
	var landed uint64
	for _, mapping := range imported {
		if mapping.Root {
			landed = mapping.ResultID
		}
	}
	require.Equal(t, held.ResultID, landed, "A's row lands on the image's row")

	// The row keeps exactly the image's offers: none of A's replaces or joins them.
	var after transferFixtureReport
	require.NoError(t, transferFixture(ctx, b, "report", "", []string{}, &after))
	var kept *dagql.TransferFixtureRow
	for i := range after.Rows {
		if after.Rows[i].ResultID == held.ResultID {
			kept = &after.Rows[i]
		}
	}
	require.NotNil(t, kept)
	require.True(t, kept.ImageHeld, "the image still holds the row")
	require.ElementsMatch(t, offerChains(held.AttachedOffers), offerChains(kept.AttachedOffers), "the image's row keeps the image's offers")

	// Reading the row on B gives A's content, from the image: no download, no
	// pull, no recomputation.
	bEntries, err := core.NewQuery(b).Container().From(address).Rootfs().Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, aEntries, bEntries)
	var read transferFixtureReport
	require.NoError(t, transferFixture(ctx, b, "report", "", []string{}, &read))
	counts := map[string]int{}
	for _, event := range read.Parts {
		if event.ResultID == held.ResultID && event.Address.Part == "fs" {
			counts[event.Kind]++
		}
	}
	require.Positive(t, counts["provider-read"], "B reads the row's filesystem from the image: %v", counts)
	require.Zero(t, counts["lazy-enter"], "B never computes the row: %v", counts)
}

// The engine image ships its entries' blobs uncompressed: the image's own
// layer compresses them for download, and an engine applies each with no
// decompression the first time it uses it. Every layer the bundle offers,
// a pulled image's included, is a tar, and its blob is the tar itself.
func (RemoteCacheTransferSuite) TestImageEntriesAreUncompressed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	raw, err := devEngineContainer(c).File(distconsts.EngineContainerImageEntriesDir + "/bundle.json").Contents(ctx)
	require.NoError(t, err)
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal([]byte(raw), &bundle))
	layers := 0
	for _, output := range bundle.Outputs {
		if output.Chain == nil {
			continue
		}
		for _, layer := range output.Chain.Layers {
			layers++
			d := layer.Descriptor.Digest
			require.Equal(t, "application/vnd.oci.image.layer.v1.tar", layer.Descriptor.MediaType, "layer %s", d)
			// The blob is the layer's tar itself: any uncompressed digest it
			// names is its own.
			if diffID, ok := layer.Descriptor.Annotations["containerd.io/uncompressed"]; ok {
				require.Equal(t, d.String(), diffID, "layer %s", d)
			}
		}
	}
	require.NotZero(t, layers, "the bundle offers layers")
}

// imageOfferSize is the size of offers' layers, and whether there are any and
// the image's directory addresses every one of them. An offer of a part
// without layers, such as a container's metadata, offers no blob.
func imageOfferSize(offers []dagql.PersistedPartOffer) (int64, bool) {
	var size int64
	layered := false
	for _, offer := range offers {
		for _, layer := range offer.Chain.Layers {
			layered = true
			d := layer.Descriptor.Digest
			if offer.Chain.Addresses[d].URL != dagql.ImageBlobAddress(d).URL {
				return 0, false
			}
			size += layer.Descriptor.Size
		}
	}
	return size, layered
}

// offerChain is what an offer offers: its part and its chain's layers at
// their addresses.
type offerChain struct {
	Part      string
	Layers    string
	Addresses string
}

func offerChains(offers []dagql.PersistedPartOffer) []offerChain {
	chains := make([]offerChain, 0, len(offers))
	for _, offer := range offers {
		var layers, addresses string
		for _, layer := range offer.Chain.Layers {
			d := layer.Descriptor.Digest
			layers += d.String() + " "
			addresses += offer.Chain.Addresses[d].URL + " "
		}
		chains = append(chains, offerChain{Part: string(offer.Address.Part), Layers: layers, Addresses: addresses})
	}
	return chains
}
