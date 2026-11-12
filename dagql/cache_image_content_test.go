package dagql

import (
	"context"
	"os"
	"path/filepath"
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
		err = f.cache.attachPartOfferLocked(row.cacheSharedResult(), offer.Address, &partOffer{record: offer, owner: owner})
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

// mergeTestImageOffered is a bundle of one root whose record carries one
// pending offer of offer.
func mergeTestImageOffered(t *testing.T, field string, offer PersistedPartOffer) ValueBundle {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, field, &transferTestValue{Text: "pending"})
	out, err := c.testOfferParts(ctx, root, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	bundle := exportTestBundle(t, ctx, c, root)
	require.Len(t, bundle.Values[0].Record.Envelope.PendingOffers, 1)
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
	running.lazyMu.Lock()
	running.lazyWhole.attempt = &lazyEvalAttempt{done: make(chan struct{}), waiters: 1}
	running.lazyMu.Unlock()
	stuck.lazyMu.Lock()
	stuck.lazyWhole.syncPending = true
	stuck.lazyMu.Unlock()
	t.Cleanup(func() {
		running.lazyMu.Lock()
		running.lazyWhole.attempt = nil
		running.lazyMu.Unlock()
		stuck.lazyMu.Lock()
		stuck.lazyWhole.syncPending = false
		stuck.lazyMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := c.ExportImageBundle(ctx, []SelectedValueOutput{{Result: rows.first, Address: PersistedPartAddress{Part: "snapshot"}}}, config.RefConfig{}, nil)
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	require.NoError(t, ctx.Err(), "the export failed at once, without waiting")
	require.ErrorContains(t, err, "export image bundle: 1 of 1 results left out")
}
