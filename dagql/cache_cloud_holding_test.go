package dagql

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testCloudNumber is the Cloud entry number the tests' offers name.
const testCloudNumber = 7000

// testPartOffers returns the Cloud's offers of res's parts by part address
// key, as the tests read them. Callers hold egraphMu, or read after the cache
// has settled.
func (res *sharedResult) testPartOffers() map[string]*partOffer {
	offers := map[string]*partOffer{}
	for key, offer := range res.partOffersLocked() {
		offers[key] = offer
	}
	return offers
}

// testOfferParts offers parts as OfferParts does, each naming the tests'
// Cloud entry, which stores no record: the holding it leaves keeps no terms.
func (c *Cache) testOfferParts(ctx context.Context, receiver AnyResult, offers []PersistedPartOffer) ([]OfferDisposition, error) {
	items := make([]CloudPartOffer, len(offers))
	for i, offer := range offers {
		items[i] = CloudPartOffer{Offer: offer, CloudNumber: testCloudNumber}
	}
	return c.OfferParts(ctx, receiver, items)
}

// testAttachPartOfferLocked attaches offer to res as an offer from the tests'
// Cloud entry does: res notes that Cloud copy first. Requires egraphMu.
func (c *Cache) testAttachPartOfferLocked(res *sharedResult, address PersistedPartAddress, offer *partOffer) error {
	if res != nil {
		res.noteCloudCopyLocked(testCloudNumber, false, 0)
	}
	return c.attachPartOfferLocked(res, address, offer)
}

// testReplacePartOfferLocked replaces res's offer at address as an offer from
// the tests' Cloud entry does. Requires egraphMu.
func (c *Cache) testReplacePartOfferLocked(ctx context.Context, res *sharedResult, address PersistedPartAddress, offer *partOffer) (collectionQueue, error) {
	if res != nil {
		res.noteCloudCopyLocked(testCloudNumber, false, 0)
	}
	return c.replacePartOfferLocked(ctx, res, address, offer)
}

// testOfferPartLocked attaches an offer of res's snapshot part, owned by
// dependency, under res's Cloud holding of the tests' Cloud entry. Requires
// egraphMu.
func testOfferPartLocked(t *testing.T, ctx context.Context, c *Cache, res *sharedResult, dependency *sharedResult) {
	t.Helper()
	res.noteCloudCopyLocked(testCloudNumber, false, 0)
	record := PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory"}, Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(dependency.id)}}}
	owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
	require.NoError(t, err)
	require.NoError(t, c.attachPartOfferLocked(res, record.Address, &partOffer{record: record, owner: owner}))
}

// An engine's Cloud holding owns nothing. An entry the Cloud holds a copy
// of, with the Cloud's offer of its part, retained and expired, is replaced in
// place like any entry only its retention edge owns; when the replacement's
// attachment fails, the entry is collected like any failed publication, with
// no retention edge and no index key.
func TestCacheFailedReplacementWithACloudHoldingIsCollected(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	frame := &ResultCall{Kind: ResultCallKindField, Field: "cloud-holding-failed-replacement", Type: NewResultCallType((&captureTestValue{}).Type())}
	publish := func(session string, value *captureTestValue) (AnyResult, error) {
		return c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame.clone())
		})
	}

	old, err := publish("a", &captureTestValue{text: "old"})
	require.NoError(t, err)
	oldID := old.cacheSharedResult().id
	ownerDepFrame := cacheTestIntCall("cloud-holding-offer-owner")
	ownerDep, err := c.GetOrInitCall(ctx, "a", noopTypeResolver{}, &CallRequest{ResultCall: ownerDepFrame, IsPersistable: true}, ValueFunc(cacheTestIntResult(ownerDepFrame, 1)))
	require.NoError(t, err)
	c.egraphMu.Lock()
	testOfferPartLocked(t, ctx, c, old.cacheSharedResult(), ownerDep.cacheSharedResult())
	owners := old.cacheSharedResult().incomingOwnershipCount
	c.egraphMu.Unlock()
	require.NoError(t, c.ReleaseSession(ctx, "a"))
	c.egraphMu.RLock()
	require.EqualValues(t, 1, c.resultsByID[oldID].incomingOwnershipCount, "only the retention edge owns the entry: the Cloud holding owns nothing")
	require.Len(t, c.resultsByID[oldID].testPartOffers(), 1)
	c.egraphMu.RUnlock()
	require.Positive(t, owners)
	currentEntryTestExpire(c, old)

	_, err = publish("b", &captureTestValue{text: "new", attach: func(context.Context) error {
		return errors.New("attachment failed")
	}})
	require.ErrorContains(t, err, "attachment failed")
	require.NoError(t, c.ReleaseSession(ctx, "b"))
	c.egraphMu.RLock()
	_, registered := c.resultsByID[oldID]
	_, retained := c.persistedEdgesByResult[oldID]
	c.egraphMu.RUnlock()
	require.False(t, registered, "the failed entry is collected")
	require.False(t, retained, "its retention edge is gone")
	require.Zero(t, currentEntryTestIndexed(t, c, frame), "and its index key")
}

// An engine's Cloud holding owns nothing: an entry the Cloud offers a part of
// goes when its session ends, as it would with no offer, and the holding is in
// none of the indexes of engine caches' holdings.
func TestCloudHoldingOwnsNothing(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	frame := &ResultCall{Kind: ResultCallKindField, Field: "cloud-holding-owns-nothing", Type: NewResultCallType((&transferTestValue{}).Type())}
	receiver, err := c.GetOrInitCall(ctx, "session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "pending"}, frame)
	})
	require.NoError(t, err)
	id := receiver.cacheSharedResult().id
	out, err := c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer(), CloudNumber: 11}})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	c.egraphMu.RLock()
	key, cloud := receiver.cacheSharedResult().cloudHoldingLocked()
	owners := receiver.cacheSharedResult().incomingOwnershipCount
	indexed := len(c.holderEntries) + len(c.remoteCaches)
	c.egraphMu.RUnlock()
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 11}, key)
	require.NotNil(t, cloud)
	require.EqualValues(t, 1, owners, "only the session owns the entry")
	require.Zero(t, indexed)

	require.NoError(t, c.ReleaseSession(ctx, "session"))
	c.egraphMu.RLock()
	_, registered := c.resultsByID[id]
	c.egraphMu.RUnlock()
	require.False(t, registered, "the entry goes with its session")
}

// Each offer names the Cloud counterpart of the entry it lands on. The
// entry's one Cloud holding takes the latest one named, with that copy's
// expiry, as when a service restart renumbers the Cloud's entries, and keeps
// its offers.
func TestOfferRekeysTheCloudHolding(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	row := receiver.cacheSharedResult()
	first, second := time.Now().Add(time.Hour).Unix(), time.Now().Add(2*time.Hour).Unix()
	holding := func() (HolderKey, int64, int, int) {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		key, cloud := row.cloudHoldingLocked()
		clouds := 0
		for k := range row.holders {
			if k.Cache == cloudCacheID {
				clouds++
			}
		}
		return key, cloud.expiresAtUnix, len(row.testPartOffers()), clouds
	}

	out, err := c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer(), CloudNumber: 10, CloudStored: true, CloudExpiresAtUnix: first}})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	key, expires, offers, clouds := holding()
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 10}, key)
	require.Equal(t, first, expires)
	require.Equal(t, 1, offers)
	require.Equal(t, 1, clouds)

	out, err = c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer(), CloudNumber: 12, CloudStored: true, CloudExpiresAtUnix: second}})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	key, expires, offers, clouds = holding()
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 12}, key, "the holding names the latest Cloud entry")
	require.Equal(t, second, expires)
	require.Equal(t, 1, offers, "and keeps its offer")
	require.Equal(t, 1, clouds, "an engine entry has one Cloud holding")
}

// An offer that names no Cloud entry is refused, and leaves no holding.
func TestOfferPartsRefusesAnOfferWithNoCloudEntry(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	out, err := c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer()}})
	require.Error(t, err)
	require.Equal(t, OfferInvalid, out[0].Outcome)
	c.egraphMu.RLock()
	_, cloud := receiver.cacheSharedResult().cloudHoldingLocked()
	c.egraphMu.RUnlock()
	require.Nil(t, cloud)
}

// The Cloud holding survives a clean restart: the entry comes back with the
// Cloud counterpart's number, whether it stores a record and that record's
// expiry, and its saved offers attach to that holding.
func TestCloudHoldingSurvivesRestart(t *testing.T) {
	t.Parallel()
	for _, stored := range []bool{true, false} {
		t.Run(map[bool]string{true: "stored", false: "unstored"}[stored], func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "cache.db")
			ctx, c, srv := persistedListTestCache(t, path)
			srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
			id := receiver.cacheSharedResult().id
			expires := time.Now().Add(time.Hour).Unix()
			out, err := c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer(), CloudNumber: 42, CloudStored: stored, CloudExpiresAtUnix: expires}})
			require.NoError(t, err)
			require.Equal(t, OfferAccepted, out[0].Outcome)
			require.NoError(t, c.ReleaseSession(ctx, "test-session"))
			require.NoError(t, c.Close(context.Background()))

			_, restored := reopenTransferTestCache(t, path)
			restored.egraphMu.RLock()
			row := restored.resultsByID[id]
			require.NotNil(t, row)
			key, cloud := row.cloudHoldingLocked()
			offers := row.testPartOffers()
			restored.egraphMu.RUnlock()
			require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 42}, key)
			require.Equal(t, !stored, cloud.unstored)
			if stored {
				require.Equal(t, expires, cloud.expiresAtUnix)
			} else {
				require.Zero(t, cloud.expiresAtUnix, "an unstored counterpart has no record to expire")
			}
			require.Len(t, offers, 1)
		})
	}
}

// A bundle names each value by its number in the sending cache. An engine that
// imports it keeps that number as each entry's Cloud holding, which stores the
// record, with the record's expiry, and the offers the bundle carries attach
// to it. The sender's own Cloud holding, which stores nothing here, doesn't
// travel.
func TestImportValuesKeepsTheSendersNumber(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	receiver := persistedListTestResult(t, actx, a, asrv, "receiver", &transferTestValue{Text: "pending"})
	out, err := a.testOfferParts(actx, receiver, []PersistedPartOffer{testLiveOffer()})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	bundle := exportTestBundle(t, actx, a, receiver)
	require.Len(t, bundle.Values, 1)
	require.Equal(t, uint64(receiver.cacheSharedResult().id), bundle.Values[0].SenderNumber)

	bctx, b, _ := transferTestCache(t)
	mappingReply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	mapping := mappingReply.Imported()
	require.NoError(t, err)
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(mapping[0].ResultID)]
	key, cloud := row.cloudHoldingLocked()
	offers := row.testPartOffers()
	b.egraphMu.RUnlock()
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: bundle.Values[0].SenderNumber}, key)
	require.False(t, cloud.unstored, "a merged record is stored")
	require.Equal(t, bundle.Values[0].ExpiresAtUnix, cloud.expiresAtUnix)
	require.Len(t, offers, 1)

	bundle.Values[0].SenderNumber = 0
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.ErrorContains(t, err, "no sender number")
}

// The Cloud's operations on engine caches' holdings refuse the key an engine
// keeps its Cloud holding under: that holding owns nothing, and only merges
// and offers keep it.
func TestAttachRemoteHoldingRefusesTheCloudKey(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	_, err := cloud.AttachRemoteHolding(t.Context(), HolderKey{Cache: cloudCacheID, Number: 1}, holdingOf("cloud-key"))
	require.ErrorContains(t, err, "not an engine cache")
}

// A class keeps its terms while any value in it has not expired, the entry's
// own or another cache's copy: an entry known only through holdings keeps
// them for an unexpired copy, and loses them once every copy has expired.
func TestHeldCopyKeepsTheClassTerms(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()
	for _, tc := range []struct {
		name    string
		expires int64
		kept    bool
	}{
		{"unexpired", now + 3600, true},
		{"no-expiry", 0, true},
		{"expired", now - 3600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cloud := newCloudCache(t)
			a := newCloudApplier(t, cloud)
			held := holdingOf("held-" + tc.name)
			held.ExpiresAtUnix = tc.expires
			a.call(callRow{key: HolderKey{"cache-a", 1}, session: "s", holding: held})
			cloud.egraphMu.RLock()
			class := cloud.eqClassRootLocked(cloud.egraphDigestToClass[held.Recipe.String()])
			kept := cloud.hasUnexpiredResultForOutputEqClassLocked(class, now)
			cloud.egraphMu.RUnlock()
			require.Equal(t, tc.kept, kept)
		})
	}
}

// An offer of a part the engine already has is answered already complete, and
// still records the Cloud counterpart it names: a re-offer after a service
// restart renumbered the Cloud's entries re-keys the holding even though the
// part needs no offer, and takes the counterpart's stored flag and expiry.
// The part may be complete in the entry's record, or settled in its gate.
func TestOfferAlreadyCompleteRecordsTheCloudCounterpart(t *testing.T) {
	t.Parallel()
	for _, settled := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "gate"}[settled], func(t *testing.T) {
			t.Parallel()
			ctx, c, srv := transferTestCache(t)
			receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "complete-snapshot"}}})
			row := receiver.cacheSharedResult()
			if settled {
				key, err := partAddressKey(testLiveOffer().Address)
				require.NoError(t, err)
				gate := row.partGate.loadOrCreate()
				gate.mu.Lock()
				if gate.outputs == nil {
					gate.outputs = map[string]partOutputState{}
				}
				gate.outputs[key] = partOutputState{phase: PartComplete}
				gate.mu.Unlock()
			}
			for _, counterpart := range []struct {
				number uint64
				stored bool
			}{{10, true}, {12, false}, {14, true}} {
				expires := time.Now().Add(time.Duration(counterpart.number) * time.Hour).Unix()
				out, err := c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer(), CloudNumber: counterpart.number, CloudStored: counterpart.stored, CloudExpiresAtUnix: expires}})
				require.NoError(t, err)
				require.Equal(t, OfferAlreadyComplete, out[0].Outcome)
				c.egraphMu.RLock()
				key, cloud := row.cloudHoldingLocked()
				offers := len(row.testPartOffers())
				c.egraphMu.RUnlock()
				require.Equal(t, HolderKey{Cache: cloudCacheID, Number: counterpart.number}, key)
				require.Equal(t, !counterpart.stored, cloud.unstored)
				if !counterpart.stored {
					expires = 0
				}
				require.Equal(t, expires, cloud.expiresAtUnix)
				require.Zero(t, offers, "a complete part takes no offer")
			}
		})
	}
}

// testClassTermsKept reports whether the term guard keeps the terms of res's
// output classes now.
func testClassTermsKept(t *testing.T, c *Cache, res *sharedResult) bool {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	classes := c.outputEqClassRootsLocked(res.id)
	require.NotEmpty(t, classes)
	now := time.Now().Unix()
	kept := false
	for class := range classes {
		kept = kept || c.hasUnexpiredResultForOutputEqClassLocked(class, now)
	}
	return kept
}

// An engine entry's Cloud holding keeps its class's terms only while its
// counterpart stores a record that has not expired, 0 meaning never. A
// counterpart that stores none, as when the Cloud offers a part it keeps for
// another entry of the class, keeps nothing whatever expiry the offer
// carries. Each message sets the flag again, either way.
func TestCloudStoredDecidesTheClassTerms(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	row := receiver.cacheSharedResult()
	require.True(t, testClassTermsKept(t, c, row), "the entry's own value keeps the terms")
	now := time.Now().Unix()
	c.egraphMu.Lock()
	row.expiresAtUnix = now - 3600
	c.egraphMu.Unlock()
	require.False(t, testClassTermsKept(t, c, row), "an expired value keeps none")

	for _, step := range []struct {
		name    string
		stored  bool
		expires int64
		kept    bool
	}{
		{"unstored", false, now + 3600, false},
		{"stored with no expiry", true, 0, true},
		{"unstored again", false, 0, false},
		{"stored and expired", true, now - 60, false},
		{"stored and unexpired", true, now + 3600, true},
	} {
		out, err := c.OfferParts(ctx, receiver, []CloudPartOffer{{Offer: testLiveOffer(), CloudNumber: 21, CloudStored: step.stored, CloudExpiresAtUnix: step.expires}})
		require.NoError(t, err)
		require.Equal(t, OfferAccepted, out[0].Outcome, step.name)
		require.Equal(t, step.kept, testClassTermsKept(t, c, row), step.name)
	}
}

// A part the Cloud borrows from another entry of its class names two Cloud
// entries. The offer's renewal key is the donor's, whose chain holds the
// bytes; the counterpart is the Cloud entry of the engine entry the offer
// lands on, and here it stores no record. The holding is keyed by the
// counterpart, and renewal asks with the offer's own key.
func TestBorrowedOfferRenewsByItsOwnKey(t *testing.T) {
	chain := newChainFixture(t, "borrowed")
	f := newExhaustionFixture(t, chain)
	taken := renewalConsumer(t, attachTestBridge(t, f.cache.PartContentSource()), renewChain(chain))
	out, err := f.cache.OfferParts(f.ctx, f.receiver, []CloudPartOffer{{Offer: exhaustionOffer(chain, "cloud-entry-99", false), CloudNumber: 21}})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	row := f.receiver.cacheSharedResult()
	f.cache.egraphMu.RLock()
	key, cloud := row.cloudHoldingLocked()
	f.cache.egraphMu.RUnlock()
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 21}, key, "the holding names the counterpart, not the donor")
	require.True(t, cloud.unstored)

	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.Zero(t, f.operation.runs.Load())
	requests := taken()
	require.Len(t, requests, 1)
	require.Equal(t, "cloud-entry-99", requests[0].RenewalKey, "renewal names the donor's chain")
}

// An offer whose address resolves to a referenced row that already has the
// part is answered already complete at the final gate check, and that row
// records the counterpart the offer names. The receiver keeps its own
// counterpart, and nothing takes an offer or an ownership unit.
func TestOfferAlreadyCompleteOnAReferencedRowRecordsItsCounterpart(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	leaf := persistedListTestResult(t, ctx, c, srv, "leaf", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "complete-snapshot"}}})
	leafRow := leaf.cacheSharedResult()
	ran := false
	require.NoError(t, c.RunLazyTask(ctx, leaf, "lazy:referenced-complete", LazyTaskSpec{Body: func(ctx context.Context) error {
		return c.partHostFor(leafRow).RunNative(ctx, LazyGroupWhole, []PartKey{"snapshot"}, func(context.Context) error { ran = true; return nil })
	}}))
	require.True(t, ran)
	key, err := partAddressKey(testLiveOffer().Address)
	require.NoError(t, err)
	gate := leafRow.partGate.loadOrCreate()
	gate.mu.Lock()
	phase := gate.outputs[key].phase
	gate.mu.Unlock()
	require.Equal(t, PartComplete, phase, "the native evaluation settled the leaf's part")

	list := persistedListTestResult(t, ctx, c, srv, "list", DynamicResultArrayOutput{Elem: &transferTestValue{}, Values: []AnyResult{leaf}})
	listRow := list.cacheSharedResult()
	listExpires := time.Now().Add(time.Hour).Unix()
	c.egraphMu.Lock()
	// An earlier message named the receiver's own counterpart.
	listRow.noteCloudCopyLocked(30, true, listExpires)
	leafOwners, listOwners := leafRow.incomingOwnershipCount, listRow.incomingOwnershipCount
	c.egraphMu.Unlock()

	offer := testLiveOffer()
	offer.Address.OutputPath = PersistedRefPath{}.Field("items").Index(0)
	expires := time.Now().Add(2 * time.Hour).Unix()
	out, err := c.OfferParts(ctx, list, []CloudPartOffer{{Offer: offer, CloudNumber: 42, CloudStored: true, CloudExpiresAtUnix: expires}})
	require.NoError(t, err)
	require.Equal(t, OfferAlreadyComplete, out[0].Outcome)

	c.egraphMu.RLock()
	leafKey, leafCloud := leafRow.cloudHoldingLocked()
	listKey, listCloud := listRow.cloudHoldingLocked()
	offers := len(leafRow.testPartOffers()) + len(listRow.testPartOffers())
	owners := len(c.offerOwners)
	gotLeafOwners, gotListOwners := leafRow.incomingOwnershipCount, listRow.incomingOwnershipCount
	c.egraphMu.RUnlock()
	require.NotNil(t, leafCloud, "the referenced row records the counterpart")
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 42}, leafKey)
	require.False(t, leafCloud.unstored)
	require.Equal(t, expires, leafCloud.expiresAtUnix)
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: 30}, listKey, "the receiver keeps its own counterpart")
	require.False(t, listCloud.unstored)
	require.Equal(t, listExpires, listCloud.expiresAtUnix)
	require.Zero(t, offers, "a complete part takes no offer")
	require.Zero(t, owners)
	require.Equal(t, leafOwners, gotLeafOwners)
	require.Equal(t, listOwners, gotListOwners)
}
