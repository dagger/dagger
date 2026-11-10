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
	"github.com/tidwall/gjson"
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
		client, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithWorkdir(t.TempDir()), dagger.WithLogOutput(testutil.NewTWriter(t)))
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

// The cache export carries every part of a result, whatever its type: an
// exec's container brings its image config, its filesystem and the exec's
// metadata, which the export reads from the result's own record, with no
// list of parts per type.
func (RemoteCacheTransferSuite) TestCacheExportCarriesEveryPart(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"sh", "-c", "echo " + rand.Text() + " > /out"})
	id, err := ctr.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Engine struct {
			LocalCache struct {
				Export struct {
					File struct {
						Contents string `json:"contents"`
					} `json:"file"`
				} `json:"_export"`
			} `json:"localCache"`
		} `json:"engine"`
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $bundle: String!) { engine { localCache { _export(results: [$id]) { file(path: $bundle) { contents } } } } }`,
		Variables: map[string]any{"id": id, "bundle": dagql.BundleFile},
	}, &dagger.Response{Data: &res}))
	raw := res.Engine.LocalCache.Export.File.Contents
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal([]byte(raw), &bundle))
	require.Len(t, bundle.Roots, 1)
	root := bundle.Roots[0].Ordinal
	parts := map[string]bool{}
	for _, output := range bundle.Outputs {
		if output.Ordinal == root && len(output.Address.OutputPath) == 0 {
			parts[string(output.Address.Part)] = true
		}
	}
	require.Equal(t, map[string]bool{"metadata": true, "fs": true, "execMeta": true}, parts)
}

// A cache export carries a result's own data, and what made it as records
// only: a directory an exec wrote brings its snapshot, but not the exec
// container's filesystem, which a lookup of the directory never reads.
func (RemoteCacheTransferSuite) TestCacheExportLeavesWhatMadeAResultAsRecords(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dir := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"sh", "-c", "mkdir /out && echo " + rand.Text() + " > /out/f"}).
		Directory("/out")
	id, err := dir.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Engine struct {
			LocalCache struct {
				Export struct {
					File struct {
						Contents string `json:"contents"`
					} `json:"file"`
				} `json:"_export"`
			} `json:"localCache"`
		} `json:"engine"`
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $bundle: String!) { engine { localCache { _export(results: [$id]) { file(path: $bundle) { contents } } } } }`,
		Variables: map[string]any{"id": id, "bundle": dagql.BundleFile},
	}, &dagger.Response{Data: &res}))
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal([]byte(res.Engine.LocalCache.Export.File.Contents), &bundle))
	require.Len(t, bundle.Roots, 1)
	root := bundle.Roots[0].Ordinal
	execs := 0
	for _, value := range bundle.Values {
		if call := value.Record.Call; call != nil && call.Field == "withExec" {
			execs++
		}
	}
	require.NotZero(t, execs, "the exec that made the directory travels as a record")
	// A part another value shares layers with may be named as a prefix of
	// the directory's chain, which carries no data of its own; only a chain
	// carries layers.
	for _, output := range bundle.Outputs {
		if output.Chain != nil && len(output.Chain.Layers) > 0 {
			require.Equal(t, root, output.Ordinal, "only the directory's own part carries data, not %s of value %d", output.Address.Part, output.Ordinal)
		}
	}
}

// Exporting a Git module source carries its checkout's data: the checkout is
// a result the source's value holds, so the export carries it with no case for
// module sources. Only the source is exported, so nothing else selects a part
// the checkout's data could arrive through.
func (RemoteCacheTransferSuite) TestCacheExportCarriesAModuleSourceCheckout(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ref := "github.com/dagger/dagger-test-modules/top-level@" + vcsTestCaseCommit
	var res struct {
		Engine struct {
			LocalCache struct {
				Export struct {
					File struct {
						Contents string `json:"contents"`
					} `json:"file"`
				} `json:"_export"`
			} `json:"localCache"`
		} `json:"engine"`
	}
	var src struct {
		ModuleSource struct {
			ID string `json:"id"`
		} `json:"moduleSource"`
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($ref: String!) { moduleSource(refString: $ref) { id } }`,
		Variables: map[string]any{"ref": ref},
	}, &dagger.Response{Data: &src}))
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $bundle: String!) { engine { localCache { _export(results: [$id]) { file(path: $bundle) { contents } } } } }`,
		Variables: map[string]any{"id": src.ModuleSource.ID, "bundle": dagql.BundleFile},
	}, &dagger.Response{Data: &res}))
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal([]byte(res.Engine.LocalCache.Export.File.Contents), &bundle))
	require.Len(t, bundle.Roots, 1)
	var rootRecord []byte
	for _, value := range bundle.Values {
		if value.Ordinal == bundle.Roots[0].Ordinal {
			raw, err := json.Marshal(value.Record)
			require.NoError(t, err)
			rootRecord = raw
		}
	}
	require.NotEmpty(t, rootRecord, "the bundle holds the module source's record")
	// The source's record names its checkout by its ordinal in the bundle.
	checkout := findJSONKey(gjson.ParseBytes(rootRecord), "gitUnfilteredContextDirResultID")
	require.True(t, checkout.Exists(), "the module source's record names its checkout: %s", rootRecord)
	carried := false
	for _, output := range bundle.Outputs {
		if uint64(output.Ordinal) == checkout.Uint() && output.Address.Part == "snapshot" && (output.Chain != nil || output.Prefix != nil) {
			carried = true
		}
	}
	require.True(t, carried, "the checkout's snapshot ships with its data")
}

// findJSONKey returns the first value under key anywhere in v, decoding JSON
// strings that hold JSON on the way, as a record's payloads are.
func findJSONKey(v gjson.Result, key string) gjson.Result {
	var found gjson.Result
	var walk func(v gjson.Result)
	walk = func(v gjson.Result) {
		if found.Exists() {
			return
		}
		switch {
		case v.IsObject():
			v.ForEach(func(k, child gjson.Result) bool {
				if k.String() == key {
					found = child
					return false
				}
				walk(child)
				return !found.Exists()
			})
		case v.IsArray():
			v.ForEach(func(_, child gjson.Result) bool {
				walk(child)
				return !found.Exists()
			})
		case v.Type == gjson.String && gjson.Valid(v.String()):
			if inner := gjson.Parse(v.String()); inner.IsObject() || inner.IsArray() {
				walk(inner)
			}
		}
	}
	walk(v)
	return found
}
