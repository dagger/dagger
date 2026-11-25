package dagql

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// imageTestDir writes chain's blobs where an engine image's directory holds
// them, and returns the directory.
func imageTestDir(t *testing.T, chain chainFixture) string {
	t.Helper()
	dir := t.TempDir()
	for i, blob := range chain.blobs {
		name := filepath.Join(dir, ImageBlobPath(chain.digest(i)))
		require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
		require.NoError(t, os.WriteFile(name, blob, 0o644))
	}
	return dir
}

// imageTestOffer offers chain with every blob addressed in the image.
func imageTestOffer(chain chainFixture) PersistedPartOffer {
	addresses := map[digest.Digest]BlobAddress{}
	for i := range chain.blobs {
		addresses[chain.digest(i)] = ImageBlobAddress(chain.digest(i))
	}
	return PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory", Path: "/"}, Chain: OfferedChain{Layers: chain.chain.Layers, Addresses: addresses}}
}

// attachImage attaches offer to row as the image's merge does: row notes its
// image copy, and the offer goes into that holding.
func (f *exhaustionFixture) attachImage(t *testing.T, row AnyResult, offer PersistedPartOffer) {
	t.Helper()
	f.cache.egraphMu.Lock()
	row.cacheSharedResult().noteImageCopyLocked(1)
	owner, err := f.cache.newOfferOwnerLocked(f.ctx, offer.Owner)
	if err == nil {
		err = f.cache.attachPartOfferLocked(row.cacheSharedResult(), imageCacheID, offer.Address, &partOffer{record: offer, owner: owner})
	}
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
}

// An image offer's part installs from the blobs in the image's directory,
// with no request to any address, and the entry takes no Cloud holding.
func TestImageOfferInstallsFromTheImage(t *testing.T) {
	chain := newChainFixture(t, "image")
	f := newExhaustionFixture(t, chain)
	require.NoError(t, f.cache.SetImageContent(imageTestDir(t, chain)))
	f.attachImage(t, f.receiver, imageTestOffer(chain))

	require.NoError(t, f.run())
	require.True(t, f.installed(), "the part installed from the image")
	require.Zero(t, f.transport.total(), "nothing was read over HTTP")
	f.cache.egraphMu.RLock()
	_, cloud := f.receiver.cacheSharedResult().cloudHoldingLocked()
	_, image := f.receiver.cacheSharedResult().imageHoldingLocked()
	f.cache.egraphMu.RUnlock()
	require.Nil(t, cloud, "no Cloud holding")
	require.NotNil(t, image, "the image holds the entry")
	require.True(t, f.cache.FromImage(f.receiver))
}

// Without the image's directory, an image offer supplies nothing: the part is
// computed instead.
func TestImageOfferNeedsTheImageDirectory(t *testing.T) {
	chain := newChainFixture(t, "no-image")
	f := newExhaustionFixture(t, chain)
	f.attachImage(t, f.receiver, imageTestOffer(chain))

	require.ErrorIs(t, f.run(), errRenewalFallbackRan)
	require.False(t, f.installed())
	require.Zero(t, f.transport.total())
}

// A blob the image holds with other bytes than the offer names fails its
// install, and the part is computed instead.
func TestImageOfferRefusesAShortBlob(t *testing.T) {
	chain := newChainFixture(t, "short-image")
	f := newExhaustionFixture(t, chain)
	dir := imageTestDir(t, chain)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ImageBlobPath(chain.digest(1))), []byte("short"), 0o644))
	require.NoError(t, f.cache.SetImageContent(dir))
	f.attachImage(t, f.receiver, imageTestOffer(chain))

	require.ErrorIs(t, f.run(), errRenewalFallbackRan)
	require.False(t, f.installed())
}

// imageTestLayerOffer is an offer of one layer, addressed by address.
func imageTestLayerOffer(address func(digest.Digest) BlobAddress) PersistedPartOffer {
	d := digest.FromString("image test layer")
	layer := snapshots.ExportLayer{Descriptor: ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayer, Digest: d, Size: 16}}
	return PersistedPartOffer{
		Address: PersistedPartAddress{Part: "snapshot"},
		Value:   SnapshotValue{Kind: "directory", Path: "/"},
		Chain:   OfferedChain{Layers: []snapshots.ExportLayer{layer}, Addresses: map[digest.Digest]BlobAddress{d: address(d)}},
	}
}

// mergeTestImageOffered is a bundle of one root sent with one part, offer,
// as an output with its chain, as an image's bundle carries its parts.
func mergeTestImageOffered(t *testing.T, field string, offer PersistedPartOffer) ValueBundle {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, field, &transferTestValue{Text: "pending"})
	out, err := c.testOfferParts(ctx, root, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	bundle := cloudTestBundle(t, ctx, c, root)
	require.Len(t, bundle.Outputs, 1)
	return bundle
}

// The image's merge gives its entries an image holding, with the image's
// offers and no Cloud holding. Their offers aren't saved, and the engine
// never exports them.
func TestMergeValuesFromTheImage(t *testing.T) {
	t.Parallel()
	bundle := mergeTestImageOffered(t, "root", imageTestLayerOffer(ImageBlobAddress))
	ctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(ctx, ImageCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Roots, 1)
	require.True(t, reply.Roots[0].Retained)
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
	_, cloud := row.cloudHoldingLocked()
	_, image := row.imageHoldingLocked()
	offers := len(row.testPartOffers())
	saved, savedErr := row.pendingOffersLocked()
	noValue := row.noValueLocked()
	b.egraphMu.RUnlock()
	require.Nil(t, cloud, "no Cloud holding")
	require.NotNil(t, image, "an image holding")
	require.Equal(t, 1, offers, "the image's offer")
	require.NoError(t, savedErr)
	require.Empty(t, saved, "the image's offers aren't saved")
	require.False(t, noValue, "the image holding owns nothing and leaves the record its value")
	require.True(t, b.FromImage(Result[Typed]{shared: row}))
}

// A merge's addresses belong to its sender: the image's bundle addresses its
// blobs in the image, and a bundle from the Cloud addresses none there.
func TestMergeValuesChecksTheSendersAddresses(t *testing.T) {
	t.Parallel()
	http := func(digest.Digest) BlobAddress { return BlobAddress{URL: "https://fixed.invalid/blob"} }
	t.Run("the image's bundle names a URL", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestImageOffered(t, "root", imageTestLayerOffer(http))
		ctx, b, _ := transferTestCache(t)
		_, err := b.MergeValues(ctx, ImageCacheID, bundle)
		require.ErrorContains(t, err, "outside the image")
	})
	t.Run("the Cloud's bundle names the image", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestImageOffered(t, "root", imageTestLayerOffer(ImageBlobAddress))
		bundle.Addresses = map[digest.Digest]BlobAddress{digest.FromString("x"): ImageBlobAddress(digest.FromString("x"))}
		ctx, b, _ := transferTestCache(t)
		_, err := b.MergeValues(ctx, CloudCacheID, bundle)
		require.ErrorContains(t, err, "in the engine image")
	})
}

// The Cloud's offer of a part the image supplies is left out: the entry keeps
// the image's offer and takes no Cloud holding from it.
func TestCloudOfferOfAnImageEntryIsLeftOut(t *testing.T) {
	t.Parallel()
	bundle := mergeTestImageOffered(t, "root", imageTestLayerOffer(ImageBlobAddress))
	ctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(ctx, ImageCacheID, bundle)
	require.NoError(t, err)
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
	b.egraphMu.RUnlock()

	cloudOffer := imageTestLayerOffer(func(digest.Digest) BlobAddress { return BlobAddress{URL: "https://fixed.invalid/blob"} })
	out, err := b.testOfferParts(ctx, Result[Typed]{shared: row}, []PersistedPartOffer{cloudOffer})
	require.NoError(t, err)
	require.Equal(t, OfferUnavailable, out[0].Outcome)
	b.egraphMu.RLock()
	_, cloud := row.cloudHoldingLocked()
	offers := row.testPartOffers()
	b.egraphMu.RUnlock()
	require.Nil(t, cloud, "no Cloud holding")
	require.Len(t, offers, 1)
	for _, offer := range offers {
		require.True(t, imageOfferLayers(&offer.record), "the image's offer stays")
	}
}

// A record the image ships without a part has no content of its own on the
// engine: the Cloud's offer of its part is taken as any entry's is, and a
// read installs the part from the Cloud's address without running the step.
func TestCloudOfferInstallsOnAnImageRecordWithoutAPart(t *testing.T) {
	chain := newChainFixture(t, "image-record")
	f := newExhaustionFixture(t, chain)
	f.cache.egraphMu.Lock()
	f.receiver.cacheSharedResult().noteImageCopyLocked(1)
	f.cache.egraphMu.Unlock()
	require.True(t, f.cache.FromImage(f.receiver), "the image ships the record")

	out, err := f.cache.testOfferParts(f.ctx, f.receiver, []PersistedPartOffer{exhaustionOffer(chain, "", true)})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome, "the Cloud's offer is taken")
	require.NoError(t, f.run())
	require.True(t, f.installed(), "the part installed from the Cloud's offer")
	require.NotZero(t, f.transport.total(), "its blobs were read from the Cloud's address")
	require.Zero(t, f.operation.runs.Load(), "the step didn't run")
	require.True(t, f.cache.FromImage(f.receiver), "the entry is still the image's")
}

// The Cloud's merge gives a record the image ships without a part the
// Cloud's part, in its Cloud holding and saved with it, as any entry takes
// one. The result above it, whose part the image ships, keeps the image's
// offer alone.
func TestCloudMergeGivesAnImageRecordWithoutAPartItsPart(t *testing.T) {
	t.Parallel()
	// bundle is a root over a dependency, from a cache that holds address's
	// offer of the root's part, and of the dependency's too when both.
	bundle := func(t *testing.T, address func(digest.Digest) BlobAddress, both bool) ValueBundle {
		t.Helper()
		ctx, c, srv := transferTestCache(t)
		dep := persistedListTestResult(t, ctx, c, srv, "dep", &transferTestValue{Text: "dep"})
		root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "pending"})
		require.NoError(t, c.AddExplicitDependency(ctx, root, dep, "root reads dep"))
		offered := []AnyResult{root}
		if both {
			offered = append(offered, dep)
		}
		for _, r := range offered {
			out, err := c.testOfferParts(ctx, r, []PersistedPartOffer{imageTestLayerOffer(address)})
			require.NoError(t, err)
			require.Equal(t, OfferAccepted, out[0].Outcome)
		}
		return cloudTestBundle(t, ctx, c, root)
	}
	image := bundle(t, ImageBlobAddress, false)
	require.Len(t, image.Values, 2, "the root and its closure's record")
	require.Len(t, image.Outputs, 1, "a part for the root alone")
	cloud := bundle(t, func(digest.Digest) BlobAddress { return BlobAddress{URL: "https://fixed.invalid/blob"} }, true)
	require.Len(t, cloud.Outputs, 2, "a part for each")

	ctx, b, _ := transferTestCache(t)
	_, err := b.MergeValues(ctx, ImageCacheID, image)
	require.NoError(t, err)
	entries := b.ImageEntries()
	require.Len(t, entries, 2)
	var root, dep *sharedResult
	b.egraphMu.RLock()
	for _, n := range entries {
		row := b.resultsByID[sharedResultID(n)]
		if len(row.testPartOffers()) > 0 {
			root = row
		} else {
			dep = row
		}
	}
	b.egraphMu.RUnlock()
	require.NotNil(t, root, "the image ships the root's part")
	require.NotNil(t, dep, "the image ships the dependency's record without a part")

	_, err = b.MergeValues(ctx, CloudCacheID, cloud)
	require.NoError(t, err)
	b.egraphMu.RLock()
	rootOffers, depOffers := root.testPartOffers(), dep.testPartOffers()
	_, depCloud := dep.cloudHoldingLocked()
	saved, savedErr := dep.pendingOffersLocked()
	b.egraphMu.RUnlock()
	require.Len(t, rootOffers, 1)
	for _, offer := range rootOffers {
		require.True(t, imageOfferLayers(&offer.record), "the root keeps the image's offer")
	}
	require.Len(t, depOffers, 1, "the record takes the Cloud's part")
	for _, offer := range depOffers {
		require.False(t, imageOfferLayers(&offer.record), "the Cloud's offer")
	}
	require.NotNil(t, depCloud, "in its Cloud holding")
	require.NoError(t, savedErr)
	require.Len(t, saved, 1, "saved as any Cloud offer is")
}

// An entry the image ships is found by its content digest and type, and
// only an entry the image ships is: a result of the same content that a
// session made answers no lookup.
func TestImageEntryByContent(t *testing.T) {
	t.Parallel()
	content := digest.FromString("bundled content")
	ctx, a, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, a, srv, "root", &transferTestValue{Text: "bundled"})
	require.NoError(t, a.TeachContentDigest(ctx, root, content))
	bundle := exportTestBundle(t, ctx, a, root)

	_, err := a.ImageEntryByContent(content, "TransferTestValue")
	require.ErrorIs(t, err, ErrUnknownEntry, "a session's result of that content isn't the image's")

	ctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(ctx, ImageCacheID, bundle)
	require.NoError(t, err)
	number, err := b.ImageEntryByContent(content, "TransferTestValue")
	require.NoError(t, err)
	require.Equal(t, reply.Imported()[0].ResultID, number)

	_, err = b.ImageEntryByContent(content, "Directory")
	require.ErrorIs(t, err, ErrUnknownEntry, "another type")
	_, err = b.ImageEntryByContent(digest.FromString("other content"), "TransferTestValue")
	require.ErrorIs(t, err, ErrUnknownEntry, "other content")
}

// An image export waits only for rows busy with something that ends on its
// own. A result whose closure also reaches a row busy until a later
// evaluation fails the export at once, though a running row comes first.
func TestExportImageBundleFailsOnARowBusyUntilALaterEvaluation(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	rows := newLeftOutTestRows(t, ctx, c, srv)
	late := persistedListTestResult(t, ctx, c, srv, "busy-late", String("late"))
	transferTestDependency(c, ctx, rows.first, late)
	running, stuck := rows.shared.cacheSharedResult(), late.cacheSharedResult()
	require.Less(t, running.id, stuck.id, "the running row is the one the root names")
	running.lazyState().mu.Lock()
	running.lazyState().whole.attempt = &lazyEvalAttempt{done: make(chan struct{}), waiters: 1}
	running.lazyState().mu.Unlock()
	stuck.lazyState().mu.Lock()
	stuck.lazyState().whole.syncPending = true
	stuck.lazyState().mu.Unlock()
	t.Cleanup(func() {
		running.lazyState().mu.Lock()
		running.lazyState().whole.attempt = nil
		running.lazyState().mu.Unlock()
		stuck.lazyState().mu.Lock()
		stuck.lazyState().whole.syncPending = false
		stuck.lazyState().mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := c.ExportImageBundle(ctx, []SelectedValueOutput{{Result: rows.first, Address: PersistedPartAddress{Part: "snapshot"}}}, config.RefConfig{}, nil, nil)
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	require.NoError(t, ctx.Err(), "the export failed at once, without waiting")
	require.ErrorContains(t, err, "export image bundle: 1 of 1 results left out")
}

// The cache snapshot marks the entries the engine image ships: those its
// merge at start gave an image holding, which every engine of the image has.
// An entry the engine made is not marked.
func TestDebugCacheSnapshotMarksImageEntries(t *testing.T) {
	t.Parallel()
	bundle := mergeTestImageOffered(t, "root", imageTestLayerOffer(ImageBlobAddress))
	ctx, b, srv := transferTestCache(t)
	reply, err := b.MergeValues(ctx, ImageCacheID, bundle)
	require.NoError(t, err)
	image := debugSnapshotEntry(t, b, reply.Imported()[0].ResultID)
	require.True(t, image.FromImage, "the image ships the entry")

	own := persistedListTestResult(t, ctx, b, srv, "own", &transferTestValue{Text: "own"})
	entry := debugSnapshotEntry(t, b, uint64(own.cacheSharedResult().id))
	require.False(t, entry.FromImage, "the engine made the entry")
}

// The entries the engine image ships are every entry its merge gave an image
// holding: the result it ships a part for, and the records of its closure,
// which come without parts. An entry the engine made is not one of them.
func TestImageEntries(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	dep := persistedListTestResult(t, ctx, c, srv, "dep", &transferTestValue{Text: "dep"})
	root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "pending"})
	require.NoError(t, c.AddExplicitDependency(ctx, root, dep, "root reads dep"))
	out, err := c.testOfferParts(ctx, root, []PersistedPartOffer{imageTestLayerOffer(ImageBlobAddress)})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	bundle := cloudTestBundle(t, ctx, c, root)
	require.Len(t, bundle.Values, 2, "the root and its closure's record")
	require.Len(t, bundle.Outputs, 1, "a part for the root alone")

	bctx, b, bsrv := transferTestCache(t)
	require.Empty(t, b.ImageEntries(), "no image merged yet")
	reply, err := b.MergeValues(bctx, ImageCacheID, bundle)
	require.NoError(t, err)
	var merged []uint64
	for _, value := range reply.Values {
		merged = append(merged, value.Number)
	}
	slices.Sort(merged)
	merged = slices.Compact(merged)
	require.Len(t, merged, 2)
	own := persistedListTestResult(t, bctx, b, bsrv, "own", &transferTestValue{Text: "own"})
	entries := b.ImageEntries()
	require.Equal(t, merged, entries)
	require.NotContains(t, entries, uint64(own.cacheSharedResult().id), "the engine made the entry")
}
