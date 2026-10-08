package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// mergeTestSource publishes a root in a sending cache and exports it. It
// returns the bundle and the root's recipe.
func mergeTestSource(t *testing.T, field, text string) (ValueBundle, digest.Digest) {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, field, &transferTestValue{Text: text})
	c.egraphMu.RLock()
	recipe := root.cacheSharedResult().recipeKeys[0]
	c.egraphMu.RUnlock()
	return exportTestBundle(t, ctx, c, root), recipe
}

// mergeTestExpire sets res's own expiry an hour back. Requires no lock.
func mergeTestExpire(c *Cache, res *sharedResult) {
	c.egraphMu.Lock()
	res.expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()
}

// Merging the same bundle twice creates nothing the second time: every record
// lands on the entry the first merge left, and the replies name the same
// numbers.
func TestMergeValuesIsIdempotent(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "value")
	bctx, b, _ := transferTestCache(t)
	first, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	b.egraphMu.RLock()
	entries := len(b.resultsByID)
	b.egraphMu.RUnlock()
	second, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	b.egraphMu.RLock()
	after := len(b.resultsByID)
	b.egraphMu.RUnlock()
	require.Equal(t, entries, after, "the second merge creates nothing")
	require.Equal(t, first.Imported(), second.Imported())
	require.Len(t, second.Values, len(first.Values))
	for i := range first.Values {
		require.Equal(t, first.Values[i].Number, second.Values[i].Number)
	}
}

// A record lands on its recipe's current entry. An unexpired value is kept:
// the same recipe means an interchangeable value.
func TestMergeValuesKeepsAnUnexpiredEntry(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(local.cacheSharedResult().id), reply.Imported()[0].ResultID)
	b.egraphMu.RLock()
	row := local.cacheSharedResult()
	imported, hasValue := row.imported, row.hasValue
	key, cloud := row.cloudHoldingLocked()
	b.egraphMu.RUnlock()
	require.False(t, imported, "the entry keeps its own computed value")
	require.True(t, hasValue)
	require.Equal(t, HolderKey{Cache: cloudCacheID, Number: bundle.Values[0].SenderNumber}, key, "and learns the Cloud's copy")
	require.False(t, cloud.unstored)
}

// An expired entry that nothing uses takes the record in place: it keeps its
// number and its retention edge, its value now comes from the record, and its
// replacement count goes up.
func TestMergeValuesReplacesAnExpiredEntryNothingUses(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
	row := local.cacheSharedResult()
	b.egraphMu.RLock()
	_, retained := b.persistedEdgesByResult[row.id]
	b.egraphMu.RUnlock()
	require.True(t, retained, "a persistable result keeps its retention edge")
	mergeTestExpire(b, row)

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(row.id), reply.Imported()[0].ResultID, "the same number")
	require.Equal(t, uint64(1), reply.Values[0].Replacements)
	b.egraphMu.RLock()
	imported, envelope, replacements := row.imported, row.persistedEnvelope, row.replacements
	current := b.currentEntryForRecipeLocked(reply.Values[0].recipeForTest(b))
	b.egraphMu.RUnlock()
	require.True(t, imported, "the value now comes from the record")
	require.NotNil(t, envelope)
	require.Equal(t, uint64(1), replacements)
	require.Same(t, row, current)
	b.egraphMu.RLock()
	payloadBytes := row.payloadBytes
	b.egraphMu.RUnlock()
	require.Equal(t, persistedEnvelopePayloadBytes(envelope), payloadBytes, "the row counts the record's envelope")
	total, sum := cacheTestPayloadTotalConsistent(b)
	require.Equal(t, sum, total)
}

// An expired entry that a session still uses is retired: it leaves the
// recipe index and keeps serving that session, and a new entry stores the
// record.
func TestMergeValuesRetiresAnExpiredEntryInUse(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
	row := local.cacheSharedResult()
	mergeTestExpire(b, row)

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	number := reply.Imported()[0].ResultID
	require.NotEqual(t, uint64(row.id), number, "a new entry")
	require.Zero(t, reply.Values[0].Replacements)
	b.egraphMu.RLock()
	current := b.currentEntryForRecipeLocked(reply.Values[0].recipeForTest(b))
	stillThere := b.resultsByID[row.id] == row
	oldReplacements := row.replacements
	b.egraphMu.RUnlock()
	require.Equal(t, number, uint64(current.id), "the new entry is the recipe's")
	require.True(t, stillThere, "the retired entry serves its session")
	require.Zero(t, oldReplacements)
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
}

// When both the entry's value and the record have expired, the entry keeps
// its value. An expired dependency travels with a live root this way.
func TestMergeValuesKeepsAnExpiredEntryForAnExpiredRecord(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "root", &transferTestValue{Text: "root"})
	dep := persistedListTestResult(t, actx, a, asrv, "dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, root, dep)
	bundle := exportTestBundle(t, actx, a, root)
	past := time.Now().Add(-time.Hour).Unix()
	depOrdinal := TransferOrdinal(0)
	for i := range bundle.Values {
		if bundle.Values[i].SenderNumber == uint64(dep.cacheSharedResult().id) {
			bundle.Values[i].ExpiresAtUnix = past
			depOrdinal = bundle.Values[i].Ordinal
		}
	}
	require.NotZero(t, depOrdinal)

	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "dep", &transferTestValue{Text: "local dep"})
	mergeTestExpire(b, local.cacheSharedResult())
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	for _, value := range reply.Values {
		if value.Ordinal == depOrdinal {
			require.Equal(t, uint64(local.cacheSharedResult().id), value.Number, "the expired entry keeps its value")
			require.Zero(t, value.Replacements)
		}
	}
	b.egraphMu.RLock()
	imported := local.cacheSharedResult().imported
	b.egraphMu.RUnlock()
	require.False(t, imported)
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
}

// An expired root is answered expired and skipped, with whatever only it
// needs; the other roots are merged.
func TestMergeValuesAnswersAnExpiredRoot(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	live := persistedListTestResult(t, actx, a, asrv, "live", &transferTestValue{Text: "live"})
	old := persistedListTestResult(t, actx, a, asrv, "old", &transferTestValue{Text: "old"})
	bundle := exportTestBundle(t, actx, a, live, old)
	var oldOrdinal TransferOrdinal
	for i := range bundle.Values {
		if bundle.Values[i].SenderNumber == uint64(old.cacheSharedResult().id) {
			bundle.Values[i].ExpiresAtUnix = time.Now().Add(-time.Hour).Unix()
			oldOrdinal = bundle.Values[i].Ordinal
		}
	}
	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Roots, 2)
	for _, root := range reply.Roots {
		if root.Ordinal == oldOrdinal {
			require.True(t, root.Expired)
			require.Zero(t, root.Number)
		} else {
			require.False(t, root.Expired)
			require.True(t, root.Retained)
		}
	}
	require.Len(t, reply.Values, 1, "only the live root is merged")
	require.Len(t, reply.Imported(), 1)
}

// The reply reads every target as the commit left it, with the cache's
// generation and its clock at the commit.
func TestMergeValuesReplyReadsTheCommit(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "value")
	bctx, b, _ := transferTestCache(t)
	before := time.Now().UnixNano()
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	after := time.Now().UnixNano()
	require.NoError(t, err)
	require.Equal(t, b.Identity().Generation, reply.Generation)
	require.GreaterOrEqual(t, reply.EngineTimeUnixNano, before)
	require.LessOrEqual(t, reply.EngineTimeUnixNano, after)
	require.Len(t, reply.Values, 1)
	value := reply.Values[0]
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(value.Number)]
	expires, replacements := row.expiresAtUnix, row.replacements
	_, retained := b.persistedEdgesByResult[row.id]
	b.egraphMu.RUnlock()
	require.Equal(t, expires, value.ExpiresAtUnix)
	require.Equal(t, replacements, value.Replacements)
	require.True(t, retained)
	require.True(t, reply.Roots[0].Retained)
}

// recipeForTest returns the recipe of the entry the merged value names.
// Requires egraphMu.
func (v MergedValue) recipeForTest(c *Cache) digest.Digest {
	res := c.resultsByID[sharedResultID(v.Number)]
	return res.recipeKeys[0]
}

// mergeTestOffered publishes a root in a sending cache with the Cloud's offer
// of its snapshot part, and exports it: the bundle carries the offer.
func mergeTestOffered(t *testing.T, field string) ValueBundle {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, field, &transferTestValue{Text: "pending"})
	out, err := c.testOfferParts(ctx, root, []PersistedPartOffer{testLiveOffer()})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	bundle := exportTestBundle(t, ctx, c, root)
	require.Len(t, bundle.Values[0].Record.Envelope.PendingOffers, 1)
	return bundle
}

// mergeTestOffers returns the number of the Cloud's offers on res.
func mergeTestOffers(c *Cache, res *sharedResult) int {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return len(res.testPartOffers())
}

// An engine admits the bundle's parts by what each target keeps (5.1
// point 3). A new entry stores the record and keeps its offers; an unexpired
// record's offers go to the value an existing entry keeps; an expired
// record's offers go to an unexpired value not at all, and to an expired one
// only where it lacks one. A part complete locally takes no offer.
func TestMergeValuesAdmitsOffersByTheKeptValue(t *testing.T) {
	t.Parallel()
	t.Run("new entry", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestOffered(t, "root")
		bctx, b, _ := transferTestCache(t)
		reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		b.egraphMu.RLock()
		row := b.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
		b.egraphMu.RUnlock()
		require.Equal(t, 1, mergeTestOffers(b, row))
		require.Len(t, reply.Values[0].OfferedParts, 1, "the reply reports the offered part")
	})
	t.Run("unexpired record, kept value", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestOffered(t, "root")
		bctx, b, bsrv := transferTestCache(t)
		local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "pending"})
		_, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.Equal(t, 1, mergeTestOffers(b, local.cacheSharedResult()))
	})
	t.Run("expired record, unexpired value", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestOffered(t, "root")
		// A dependency's record may have expired while its root lives; a
		// root's own expired record is skipped whole. Stand the record in as
		// a dependency of a live root of another recipe.
		bundle = mergeTestUnderLiveRoot(t, bundle)
		bctx, b, bsrv := transferTestCache(t)
		local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "pending"})
		_, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.Zero(t, mergeTestOffers(b, local.cacheSharedResult()))
	})
	t.Run("expired record, expired value", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestOffered(t, "root")
		bundle = mergeTestUnderLiveRoot(t, bundle)
		bctx, b, bsrv := transferTestCache(t)
		local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "pending"})
		mergeTestExpire(b, local.cacheSharedResult())
		_, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.Equal(t, 1, mergeTestOffers(b, local.cacheSharedResult()), "the part it lacked")
		require.NoError(t, b.ReleaseSession(bctx, "test-session"))
	})
	t.Run("complete locally", func(t *testing.T) {
		t.Parallel()
		bundle := mergeTestOffered(t, "root")
		bctx, b, bsrv := transferTestCache(t)
		local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "complete-snapshot"}}})
		row := local.cacheSharedResult()
		b.completePartKeys(bctx, row)
		reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.Zero(t, mergeTestOffers(b, row), "a complete part takes no offer")
		require.Len(t, reply.Values[0].Parts, 1, "the reply reports it complete")
		require.Empty(t, reply.Values[0].OfferedParts)
	})
}

// mergeTestUnderLiveRoot turns a one-record bundle's root into an expired
// dependency of a new live root, a record of another recipe.
func mergeTestUnderLiveRoot(t *testing.T, bundle ValueBundle) ValueBundle {
	t.Helper()
	require.Len(t, bundle.Values, 1)
	dep := bundle.Values[0]
	dep.ExpiresAtUnix = time.Now().Add(-time.Hour).Unix()
	root := TransferredValue{Ordinal: 2, SenderNumber: dep.SenderNumber + 1000, DependencyIDs: []uint64{1}}
	root.Record = dep.Record
	root.Record.ResultID = 2
	if root.Record.Envelope.ResultID != 0 {
		root.Record.Envelope.ResultID = 2
	}
	root.Record.Envelope.PendingOffers = nil
	call := dep.Record.Call.clone()
	call.Field = "liveRoot"
	root.Record.Call = call
	bundle.Values = []TransferredValue{dep, root}
	bundle.Roots = []TransferredRoot{{Ordinal: 2}}
	return bundle
}

// A Cloud entry known only through holdings has no value: a merge stores the
// record in it. The entry keeps its number and its holdings, and its
// replacement count stays, since no value was replaced. The sending engine's
// holding of it is created, and the merge returns it for collection with the
// entry's recipe.
func TestMergeValuesStoresARecordOnTheCloud(t *testing.T) {
	t.Parallel()
	bundle, recipe := mergeTestSource(t, "root", "value")
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	other := HolderKey{Cache: "cache-x", Number: 9}
	_, err := cloud.AttachRemoteHolding(ctx, other, RemoteHolding{Recipe: recipe, Field: "root", TypeName: "transferTestValue"})
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	entry, _ := cloud.holdingLocked(other)
	noValue := entry.noValueLocked()
	cloud.egraphMu.RUnlock()
	require.True(t, noValue)

	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(entry.id), reply.Imported()[0].ResultID)
	sender := HolderKey{Cache: "cache-a", Number: bundle.Values[0].SenderNumber}
	require.Contains(t, reply.Change.Candidates, sender)
	require.Contains(t, reply.Change.Recipes, recipe)
	require.Equal(t, []HolderKey{sender, other}, cloud.EquivalentHolders(recipe.String()), "the recipe digest names the entry's class")
	cloud.egraphMu.RLock()
	stored, replacements := entry.persistedEnvelope != nil, entry.replacements
	_, keeps := entry.holders[other]
	_, holds := entry.holders[sender]
	noValue = entry.noValueLocked()
	cloud.egraphMu.RUnlock()
	require.True(t, stored)
	require.False(t, noValue)
	require.Zero(t, replacements)
	require.True(t, keeps, "the entry keeps its holdings")
	require.True(t, holds, "and the sending engine's holding")
}

// The Cloud replaces an expired stored record in place, whatever holds the
// entry: the entry keeps its number, its count goes up, and its stored parts,
// which belonged to the old record, are dropped.
func TestMergeValuesReplacesAnExpiredCloudRecord(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "value")
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	first, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	number := first.Imported()[0].ResultID
	stored, err := cloud.SetStoredPart(ctx, number, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	cloud.egraphMu.RLock()
	entry := cloud.resultsByID[sharedResultID(number)]
	cloud.egraphMu.RUnlock()
	mergeTestExpire(cloud, entry)

	second, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, number, second.Imported()[0].ResultID)
	require.Equal(t, uint64(1), second.Values[0].Replacements)
	cloud.egraphMu.RLock()
	storedParts := len(entry.storedParts)
	cloud.egraphMu.RUnlock()
	require.Zero(t, storedParts, "the old record's stored parts are dropped")
}

// mergeTestThroughTheCloud merges an engine's export into a new Cloud cache,
// and returns the bundle the Cloud sends on: the same records, named by the
// Cloud's numbers.
func mergeTestThroughTheCloud(t *testing.T, exported ValueBundle) (context.Context, *Cache, ValueBundle) {
	t.Helper()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	stored, err := cloud.MergeValues(ctx, "cache-a", exported)
	require.NoError(t, err)
	sent := exported
	sent.Values = append([]TransferredValue(nil), exported.Values...)
	for i := range sent.Values {
		for _, value := range stored.Values {
			if value.Ordinal == sent.Values[i].Ordinal {
				sent.Values[i].SenderNumber = value.Number
			}
		}
	}
	return ctx, cloud, sent
}

// mergeTestHolding returns cache's holding on the entry of key.
func mergeTestHolding(t *testing.T, cloud *Cache, key HolderKey) RemoteHoldingInfo {
	t.Helper()
	info, ok := cloud.RemoteEntryInfo(key)
	require.True(t, ok, "holding %v exists", key)
	for _, holding := range info.Holdings {
		if holding.Key == key {
			return holding
		}
	}
	t.Fatalf("no holding %v on its entry", key)
	return RemoteHoldingInfo{}
}

// The Cloud applies an engine's merged reply: the engine's holding of each
// record's Cloud entry is created, with the engine entry's actual
// dependencies and replacement count, and the root's retention at the reply's
// time. The holdings the reply created are candidates, and a collection pass
// keeps them all: the root is retained, and its dependency is its dependent's.
func TestApplyMergedReplyCreatesTheEngineHoldings(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "root", &transferTestValue{Text: "root"})
	dep := persistedListTestResult(t, actx, a, asrv, "dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, root, dep)
	ctx, cloud, sent := mergeTestThroughTheCloud(t, exportTestBundle(t, actx, a, root))
	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, sent)
	require.NoError(t, err)

	change, err := cloud.ApplyMergedReply(ctx, "cache-b", sent, reply)
	require.NoError(t, err)
	require.Len(t, change.Candidates, 2)
	collected, err := cloud.CollectRemoteHoldings(ctx, change.Candidates)
	require.NoError(t, err)
	require.Empty(t, collected)
	for _, value := range reply.Values {
		info, ok := cloud.RemoteEntryInfo(HolderKey{Cache: "cache-b", Number: value.Number})
		require.True(t, ok)
		var h RemoteHoldingInfo
		for _, holding := range info.Holdings {
			if holding.Key.Cache == "cache-b" {
				h = holding
			}
		}
		require.Equal(t, value.Deps, h.Deps, "the engine entry's own dependencies")
		require.Equal(t, value.Replacements, h.Replacements)
		if value.Ordinal == reply.Roots[0].Ordinal {
			require.True(t, h.Retained)
		}
	}
}

// A merge landing on a cached nil result keeps it: the nil result is still a
// hit afterwards.
func TestMergeValuesLandingOnANilResultKeepsIt(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "value")
	bctx, b, _ := transferTestCache(t)
	frame := &ResultCall{Kind: ResultCallKindField, Field: "root", Type: NewResultCallType((&transferTestValue{}).Type())}
	calls := 0
	nothing := func(context.Context) (AnyResult, error) { calls++; return nil, nil }
	first, err := b.GetOrInitCall(bctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, nothing)
	require.NoError(t, err)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(first.cacheSharedResult().id), reply.Imported()[0].ResultID, "the merge lands on the nil result")
	second, err := b.GetOrInitCall(bctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, nothing)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "the nil result is still a hit")
	require.True(t, second.HitCache())
}

// A target whose dependency attachment is still open is waited on: the merge
// changes nothing until it settles, then decides again. A clean attachment's
// entry is kept. A failed one rolls its entry back and out of the recipe
// index, so the merge creates a fresh entry, and nothing of the merge holds
// the failed one.
func TestMergeValuesSettlesAttachmentsFirst(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "failed"}[fail], func(t *testing.T) {
			t.Parallel()
			actx, a := storedPartTestCache(t)
			sent := storedPartTestPublish(t, actx, a, "a", "settle-first", &captureTestValue{text: "sent"})
			bundle := exportTestBundle(t, actx, a, sent)

			bctx, b := storedPartTestCache(t)
			frame := &ResultCall{Kind: ResultCallKindField, Field: "settle-first", Type: NewResultCallType((&captureTestValue{}).Type())}
			attachStarted, attachRelease := make(chan struct{}), make(chan struct{})
			var attachErr error
			if fail {
				attachErr = errors.New("attachment failed")
			}
			local := &captureTestValue{text: "local", attach: func(context.Context) error {
				close(attachStarted)
				<-attachRelease
				return attachErr
			}}
			published := make(chan error, 1)
			go func() {
				_, err := b.GetOrInitCall(bctx, "b", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
					return NewResultForCall(local, frame.clone())
				})
				published <- err
			}()
			<-attachStarted
			waited := make(chan sharedResultID, 1)
			b.testMergeWaitsOnAttachment = func(res *sharedResult) { waited <- res.id }
			type outcome struct {
				reply MergeReply
				err   error
			}
			merged := make(chan outcome, 1)
			go func() {
				reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
				merged <- outcome{reply, err}
			}()
			localID := <-waited
			b.egraphMu.RLock()
			_, cloud := b.resultsByID[localID].cloudHoldingLocked()
			b.egraphMu.RUnlock()
			require.Nil(t, cloud, "the waiting merge changed nothing")
			close(attachRelease)

			pubErr := <-published
			out := <-merged
			require.NoError(t, out.err)
			number := out.reply.Imported()[0].ResultID
			if !fail {
				require.NoError(t, pubErr)
				require.Equal(t, uint64(localID), number, "the settled entry is kept")
				return
			}
			require.ErrorIs(t, pubErr, attachErr)
			require.NotEqual(t, uint64(localID), number, "a fresh entry stores the record")
			require.NoError(t, b.ReleaseSession(bctx, "b"))
			b.egraphMu.RLock()
			_, failedStays := b.resultsByID[localID]
			b.egraphMu.RUnlock()
			require.False(t, failedStays, "nothing of the merge holds the failed entry")
		})
	}
}

// Value state follows the replacement count, whatever order its observations
// arrive in. R depends on P and Q, whose holdings only R owns, and P' has a
// session of its own. The engine then replaces R's value with one that
// depends on P' and Q. Five observations can arrive in any of their 120
// orders, each followed by a collection pass: a lazy span of the old value, the
// replacing call span, a lazy span of the new value, an offered reply about the
// new value, and a late record of the old value from an export. Every order
// ends with R at count 1, its new value's parts, depending on P' and Q, and P
// collected; Q is never collected.
func TestHeldValueStateInEveryOrder(t *testing.T) {
	t.Parallel()
	const r, p, q, pNew = 1, 2, 3, 4
	part := func(name string) []PersistedPartAddress { return []PersistedPartAddress{{Part: PartKey(name)}} }
	type event struct {
		name  string
		apply func(*testing.T, *Cache) []HolderKey
	}
	key := func(n uint64) HolderKey { return HolderKey{Cache: "cache-a", Number: n} }
	update := func(update RemoteHoldingUpdate) func(*testing.T, *Cache) []HolderKey {
		return func(t *testing.T, c *Cache) []HolderKey {
			change, _, err := c.UpdateRemoteHolding(t.Context(), key(r), update)
			require.NoError(t, err)
			return change.Candidates
		}
	}
	events := []event{
		{"old lazy span", update(RemoteHoldingUpdate{Parts: part("old"), Deps: []uint64{p, q}})},
		{"replacing call span", func(t *testing.T, c *Cache) []HolderKey {
			h := holdingOf("r")
			h.Replacements, h.Deps = 1, []uint64{pNew, q}
			change, err := c.AttachRemoteHolding(t.Context(), key(r), h)
			require.NoError(t, err)
			return change.Candidates
		}},
		{"new lazy span", update(RemoteHoldingUpdate{Replacements: 1, Parts: part("new"), Deps: []uint64{pNew, q}})},
		{"offered reply", func(t *testing.T, c *Cache) []HolderKey {
			change, _, err := c.ApplyHeldValueState(t.Context(), key(r), HeldValueState{Replacements: 1, Deps: []uint64{pNew, q}, OfferedParts: part("offered")})
			require.NoError(t, err)
			return change.Candidates
		}},
		{"late old export", func(t *testing.T, c *Cache) []HolderKey {
			change, _, err := c.ApplyHeldValueState(t.Context(), key(r), HeldValueState{Deps: []uint64{p, q}})
			require.NoError(t, err)
			return change.Candidates
		}},
	}
	var permute func([]int, int, func([]int))
	permute = func(order []int, k int, visit func([]int)) {
		if k == len(order) {
			visit(order)
			return
		}
		for i := k; i < len(order); i++ {
			order[k], order[i] = order[i], order[k]
			permute(order, k+1, visit)
			order[k], order[i] = order[i], order[k]
		}
	}
	orders := 0
	permute([]int{0, 1, 2, 3, 4}, 0, func(order []int) {
		orders++
		cloud := newCloudCache(t)
		a := newCloudApplier(t, cloud)
		a.call(callRow{key: key(pNew), session: "p-new", holding: holdingOf("p-new")})
		a.call(callRow{key: key(p), holding: holdingOf("p")})
		a.call(callRow{key: key(q), holding: holdingOf("q")})
		a.call(callRow{key: key(r), session: "r", holding: holdingOf("r", p, q)})
		require.Empty(t, a.collect())
		for _, i := range order {
			collected, err := cloud.CollectRemoteHoldings(t.Context(), events[i].apply(t, cloud))
			require.NoError(t, err)
			require.NotContains(t, collected, key(q), "order %v: Q is never collected", order)
		}
		h := requireHolding(t, cloud, key(r))
		require.Equal(t, uint64(1), h.Replacements, "order %v", order)
		require.Equal(t, []uint64{q, pNew}, h.Deps, "order %v", order)
		require.Empty(t, h.UnknownDeps, "order %v", order)
		require.Equal(t, part("new"), h.Parts, "order %v", order)
		require.Equal(t, part("offered"), h.OfferedParts, "order %v", order)
		requireNoHolding(t, cloud, key(p))
	})
	require.Equal(t, 120, orders)
}

// A gone answer removes the named holding outright, whatever owned it: its
// session hold and its dependents' edges, which become unknown numbers. It
// returns the holdings it depended on, and the next collection pass collects
// those that nothing else owns.
func TestCollectRemoteHoldingForAGoneAnswer(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	key := func(n uint64) HolderKey { return HolderKey{Cache: "cache-a", Number: n} }
	a.call(callRow{key: key(2), holding: holdingOf("dep")})
	a.call(callRow{key: key(1), session: "s", holding: holdingOf("gone", 2)})
	a.call(callRow{key: key(3), session: "s", holding: holdingOf("parent", 1)})
	require.Empty(t, a.collect())

	candidates, found, err := cloud.CollectRemoteHolding(t.Context(), key(1))
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []HolderKey{key(2)}, candidates)
	requireNoHolding(t, cloud, key(1))
	require.Equal(t, []uint64{1}, requireHolding(t, cloud, key(3)).UnknownDeps, "the dependent's edge is an unknown number now")

	collected, err := cloud.CollectRemoteHoldings(t.Context(), candidates)
	require.NoError(t, err)
	require.Equal(t, []HolderKey{key(2)}, collected, "the dependency only it owned")
	_, found, err = cloud.CollectRemoteHolding(t.Context(), key(1))
	require.NoError(t, err)
	require.False(t, found, "an answer about a holding already gone changes nothing")
}

// An export merged on the Cloud creates the sender's holdings, with their
// dependencies at the records' replacement counts, even for records no span
// named. A row's holding that named one of them as an unknown number attaches
// to it. The collection pass that follows collects the export's holdings that
// nothing owns; the Cloud's stored root keeps its entry either way.
func TestMergeValuesOnTheCloudCreatesTheSendersHoldings(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "root", &transferTestValue{Text: "root"})
	typedef := persistedListTestResult(t, actx, a, asrv, "typedef", &transferTestValue{Text: "type definition"})
	transferTestDependency(a, actx, root, typedef)
	bundle := exportTestBundle(t, actx, a, root)
	rootNumber, typedefNumber := uint64(root.cacheSharedResult().id), uint64(typedef.cacheSharedResult().id)

	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	applier := newCloudApplier(t, cloud)
	// A span named another entry depending on the type definition, which no
	// span names.
	applier.call(callRow{key: HolderKey{"cache-a", 50}, session: "s", holding: holdingOf("user", typedefNumber)})
	require.Empty(t, applier.collect())
	require.Equal(t, []uint64{typedefNumber}, requireHolding(t, cloud, HolderKey{"cache-a", 50}).UnknownDeps)

	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	user := requireHolding(t, cloud, HolderKey{"cache-a", 50})
	require.Empty(t, user.UnknownDeps)
	require.Equal(t, []uint64{typedefNumber}, user.Deps, "the unknown number attached to the new holding")
	require.Equal(t, []uint64{typedefNumber}, requireHolding(t, cloud, HolderKey{"cache-a", rootNumber}).Deps)

	collected, err := cloud.CollectRemoteHoldings(ctx, reply.Change.Candidates)
	require.NoError(t, err)
	require.Equal(t, []HolderKey{{"cache-a", rootNumber}}, collected, "the type definition's holding is its user's")
	cloud.egraphMu.RLock()
	_, stored := cloud.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
	cloud.egraphMu.RUnlock()
	require.True(t, stored, "the stored root keeps its entry")
}

// B holds a retained R that depends on a retired P; P's recipe's current entry
// is P'. A merged bundle with R -> P lands R on B's R, which keeps its value,
// and the reply reports R's actual dependency, P, not P'.
func TestMergeValuesReportsAKeptEntrysOwnDependencies(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	sentR := persistedListTestResult(t, actx, a, asrv, "r", &transferTestValue{Text: "r"})
	sentP := persistedListTestResult(t, actx, a, asrv, "p", &transferTestValue{Text: "p"})
	transferTestDependency(a, actx, sentR, sentP)
	bundle := exportTestBundle(t, actx, a, sentR)

	bctx, b, bsrv := transferTestCache(t)
	r := persistedListTestResult(t, bctx, b, bsrv, "r", &transferTestValue{Text: "local r"})
	p := persistedListTestResult(t, bctx, b, bsrv, "p", &transferTestValue{Text: "local p"})
	transferTestDependency(b, bctx, r, p)
	// P expires while R uses it, and a new P' takes its recipe: P is retired.
	mergeTestExpire(b, p.cacheSharedResult())
	pNew := persistedListTestResult(t, bctx, b, bsrv, "p", &transferTestValue{Text: "local p'"})
	require.NotEqual(t, p.cacheSharedResult().id, pNew.cacheSharedResult().id)

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	for _, value := range reply.Values {
		if value.Number == uint64(r.cacheSharedResult().id) {
			require.Equal(t, []uint64{uint64(p.cacheSharedResult().id)}, value.Deps, "R's own dependency, the retired P")
			return
		}
	}
	t.Fatalf("the merge did not land on B's R: %+v", reply.Values)
}

// valueStateEvent is one engine event of the protocol design's value-state
// model (section 5.4) on entry R, after the Cloud applied the call span that
// published it.
type valueStateEvent struct {
	kind   string // complete, share, offer, hit, ttl, decode, export, merge, replace
	part   string
	expiry int64
	deps   []uint64 // replace's new dependencies; decode's one
}

// valueStateObs is one observation the engine reports of R's value state.
type valueStateObs struct {
	tag      string
	count    uint64
	complete []string
	offered  []string
	expiry   int64
	deps     []uint64
}

// valueStateFinal is R's final state on the engine, with which of P and Q
// still have a holding.
type valueStateFinal struct {
	count    uint64
	complete []string
	offered  []string
	expiry   int64
	deps     []uint64
	held     []uint64
}

const (
	vsR, vsP, vsQ, vsPNew, vsS = 1, 2, 3, 4, 5
)

func vsEarlierExpiry(a, b int64) int64 {
	if a != 0 && b != 0 {
		return min(a, b)
	}
	return max(a, b)
}

// valueStateEngine is the model's engine: R starts at count 0 depending on P
// and Q, which only R owns; P' and S are owned elsewhere. It returns the
// observations the history reports and the engine's final state.
func valueStateEngine(history []valueStateEvent) (valueStateFinal, []valueStateObs) {
	count, expiry := uint64(0), int64(0)
	parts := map[string]string{}
	deps := map[uint64]bool{vsP: true, vsQ: true}
	alive := map[uint64]bool{vsP: true, vsQ: true, vsPNew: true, vsS: true}
	ownedByR := map[uint64]bool{vsP: true, vsQ: true}
	depList := func() []uint64 {
		var list []uint64
		for d := range deps {
			list = append(list, d)
		}
		slices.Sort(list)
		return list
	}
	partsIn := func(state string) []string {
		var names []string
		for name, s := range parts {
			if s == state {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		return names
	}
	var obs []valueStateObs
	hit := func() valueStateObs {
		return valueStateObs{tag: "hit", count: count, complete: partsIn("complete"), expiry: expiry, deps: depList()}
	}
	for _, ev := range history {
		switch ev.kind {
		case "complete", "share":
			parts[ev.part] = "complete"
			obs = append(obs, valueStateObs{tag: ev.kind, count: count, complete: []string{ev.part}, deps: depList()})
		case "offer":
			if _, ok := parts[ev.part]; !ok {
				parts[ev.part] = "offered"
				obs = append(obs, valueStateObs{tag: "offer", count: count, offered: []string{ev.part}, deps: depList()})
			}
		case "hit":
			obs = append(obs, hit())
		case "ttl":
			expiry = vsEarlierExpiry(expiry, ev.expiry)
			obs = append(obs, hit())
		case "decode":
			if alive[ev.deps[0]] {
				deps[ev.deps[0]] = true
				obs = append(obs, hit())
			}
		case "export":
			obs = append(obs, valueStateObs{tag: "export", count: count, deps: depList()})
		case "merge":
			obs = append(obs, valueStateObs{tag: "merge", count: count, complete: partsIn("complete"), offered: partsIn("offered"), expiry: expiry, deps: depList()})
		case "replace":
			next := map[uint64]bool{}
			for _, d := range ev.deps {
				if alive[d] {
					next[d] = true
				}
			}
			for d := range deps {
				if !next[d] && ownedByR[d] {
					alive[d] = false
				}
			}
			count, parts, expiry, deps = count+1, map[string]string{}, ev.expiry, next
			obs = append(obs, valueStateObs{tag: "replace", count: count, expiry: expiry, deps: depList()})
		}
	}
	final := valueStateFinal{count: count, complete: partsIn("complete"), offered: partsIn("offered"), expiry: expiry, deps: depList()}
	for _, d := range []uint64{vsP, vsQ} {
		if alive[d] {
			final.held = append(final.held, d)
		}
	}
	return final, obs
}

// valueStateCloud applies observations to a fresh Cloud cache holding R, P, Q,
// P' and S as the model starts, each through the operation its report takes,
// with a collection pass after each. It returns R's final
// state.
func valueStateCloud(t *testing.T, observations []valueStateObs) valueStateFinal {
	t.Helper()
	ctx := t.Context()
	key := func(n uint64) HolderKey { return HolderKey{Cache: "cache-a", Number: n} }
	// Expiries are the model's small numbers after a day from now.
	base := time.Now().Add(24 * time.Hour).Unix()
	at := func(e int64) int64 {
		if e == 0 {
			return 0
		}
		return base + e
	}
	addresses := func(names []string) []PersistedPartAddress {
		var list []PersistedPartAddress
		for _, name := range names {
			list = append(list, PersistedPartAddress{Part: PartKey(name)})
		}
		return list
	}
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	a.call(callRow{key: key(vsPNew), session: "elsewhere", holding: holdingOf("p-new")})
	a.call(callRow{key: key(vsS), session: "elsewhere", holding: holdingOf("s")})
	a.call(callRow{key: key(vsP), holding: holdingOf("p")})
	a.call(callRow{key: key(vsQ), holding: holdingOf("q")})
	a.call(callRow{key: key(vsR), session: "r", holding: holdingOf("r", vsP, vsQ)})
	require.Empty(t, a.collect())
	for _, o := range observations {
		var change RemoteChange
		var err error
		switch o.tag {
		case "complete", "share":
			// A lazy span, or a share event.
			change, _, err = cloud.UpdateRemoteHolding(ctx, key(vsR), RemoteHoldingUpdate{Replacements: o.count, Parts: addresses(o.complete), Deps: o.deps})
		case "hit", "replace":
			// A call span.
			h := holdingOf("r")
			h.Replacements, h.ExpiresAtUnix, h.Deps, h.Parts = o.count, at(o.expiry), o.deps, addresses(o.complete)
			change, err = cloud.AttachRemoteHolding(ctx, key(vsR), h)
		default:
			// An offered or merged reply, or an export record.
			change, _, err = cloud.ApplyHeldValueState(ctx, key(vsR), HeldValueState{
				Replacements:  o.count,
				Deps:          o.deps,
				Parts:         addresses(o.complete),
				OfferedParts:  addresses(o.offered),
				ExpiresAtUnix: at(o.expiry),
			})
		}
		require.NoError(t, err)
		_, err = cloud.CollectRemoteHoldings(ctx, change.Candidates)
		require.NoError(t, err)
	}
	h := requireHolding(t, cloud, key(vsR))
	require.Empty(t, h.UnknownDeps)
	names := func(list []PersistedPartAddress) []string {
		var out []string
		for _, address := range list {
			out = append(out, string(address.Part))
		}
		slices.Sort(out)
		return out
	}
	final := valueStateFinal{count: h.Replacements, complete: names(h.Parts), offered: names(h.OfferedParts), deps: h.Deps}
	if h.ExpiresAtUnix != 0 {
		final.expiry = h.ExpiresAtUnix - base
	}
	for _, d := range []uint64{vsP, vsQ} {
		if _, ok := cloud.RemoteEntryInfo(key(d)); ok {
			final.held = append(final.held, d)
		}
	}
	return final
}

// forEachOrder visits every order of n items.
func forEachOrder(n int, visit func([]int)) {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	var permute func(int)
	permute = func(k int) {
		if k == n {
			visit(order)
			return
		}
		for i := k; i < n; i++ {
			order[k], order[i] = order[i], order[k]
			permute(k + 1)
			order[k], order[i] = order[i], order[k]
		}
	}
	permute(0)
}

// requireValueStateOrders checks that the Cloud's value state for R ends as
// the engine's in every order the history's observations arrive in, and
// returns the engine's final state.
func requireValueStateOrders(t *testing.T, history []valueStateEvent) valueStateFinal {
	t.Helper()
	want, obs := valueStateEngine(history)
	orders := 0
	forEachOrder(len(obs), func(order []int) {
		orders++
		arrived := make([]valueStateObs, len(order))
		for i, j := range order {
			arrived[i] = obs[j]
		}
		require.Equal(t, want, valueStateCloud(t, arrived), "history %v, order %v", history, order)
	})
	factorial := 1
	for i := 2; i <= len(obs); i++ {
		factorial *= i
	}
	require.Equal(t, factorial, orders)
	return want
}

// The Cloud's value state for R ends as the engine's, in every order its
// observations arrive in, for the design model's five named histories: R's
// count, complete and offered parts, expiry and dependencies, which of P and Q
// still have a holding, and no unknown number. Each engine final state is the
// one the design's model gives.
func TestHeldValueStateNamedHistories(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		history []valueStateEvent
		want    valueStateFinal
	}{
		{"fs completes, the value expires and is replaced, then the new value's fs completes", []valueStateEvent{
			{kind: "complete", part: "fs"}, {kind: "hit"}, {kind: "replace", expiry: 300, deps: []uint64{vsP, vsQ}}, {kind: "complete", part: "fs"}, {kind: "hit"},
		}, valueStateFinal{count: 1, complete: []string{"fs"}, expiry: 300, deps: []uint64{vsP, vsQ}, held: []uint64{vsP, vsQ}}},
		{"the Cloud's offer is accepted, then the value is replaced and the offer dropped", []valueStateEvent{
			{kind: "offer", part: "fs"}, {kind: "merge"}, {kind: "replace", deps: []uint64{vsP, vsQ}}, {kind: "hit"},
		}, valueStateFinal{count: 1, deps: []uint64{vsP, vsQ}, held: []uint64{vsP, vsQ}}},
		{"a time limit lowers the old value's expiry, then a replacement brings a later one", []valueStateEvent{
			{kind: "ttl", expiry: 100}, {kind: "replace", expiry: 400, deps: []uint64{vsP, vsQ}}, {kind: "ttl", expiry: 500}, {kind: "complete", part: "fs"},
		}, valueStateFinal{count: 1, complete: []string{"fs"}, expiry: 400, deps: []uint64{vsP, vsQ}, held: []uint64{vsP, vsQ}}},
		{"R is replaced and depends on P' and Q; old spans and an old export arrive late", []valueStateEvent{
			{kind: "hit"}, {kind: "export"}, {kind: "share", part: "fs"}, {kind: "replace", deps: []uint64{vsPNew, vsQ}}, {kind: "complete", part: "fs"}, {kind: "hit"},
		}, valueStateFinal{count: 1, complete: []string{"fs"}, deps: []uint64{vsQ, vsPNew}, held: []uint64{vsQ}}},
		{"R is replaced and still depends on P and Q; an offer on the new value arrives before or after", []valueStateEvent{
			{kind: "replace", deps: []uint64{vsP, vsQ}}, {kind: "offer", part: "fs"},
		}, valueStateFinal{count: 1, offered: []string{"fs"}, deps: []uint64{vsP, vsQ}, held: []uint64{vsP, vsQ}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, requireValueStateOrders(t, tc.history))
		})
	}
}

// The same over random histories of the model's events, fewer and shorter
// than the design's so the test stays quick: 300 histories of one to
// five events, over two parts and four dependencies, every order of each.
func TestHeldValueStateRandomHistories(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(9, 9))
	pick := func(options ...string) string { return options[rng.IntN(len(options))] }
	part := func() string { return pick("fs", "mount:/src") }
	events := []func() valueStateEvent{
		func() valueStateEvent { return valueStateEvent{kind: "complete", part: part()} },
		func() valueStateEvent { return valueStateEvent{kind: "share", part: part()} },
		func() valueStateEvent { return valueStateEvent{kind: "offer", part: part()} },
		func() valueStateEvent { return valueStateEvent{kind: "hit"} },
		func() valueStateEvent {
			return valueStateEvent{kind: "ttl", expiry: []int64{100, 200, 300}[rng.IntN(3)]}
		},
		func() valueStateEvent {
			return valueStateEvent{kind: "decode", deps: []uint64{[]uint64{vsQ, vsS}[rng.IntN(2)]}}
		},
		func() valueStateEvent { return valueStateEvent{kind: "export"} },
		func() valueStateEvent { return valueStateEvent{kind: "merge"} },
		func() valueStateEvent {
			deps := [][]uint64{{vsP, vsQ}, {vsPNew, vsQ}, {vsPNew}, {vsP, vsS}}[rng.IntN(4)]
			return valueStateEvent{kind: "replace", expiry: []int64{0, 400, 500}[rng.IntN(3)], deps: deps}
		},
	}
	for range 300 {
		history := make([]valueStateEvent, 1+rng.IntN(5))
		for i := range history {
			history[i] = events[rng.IntN(len(events))]()
		}
		requireValueStateOrders(t, history)
	}
}

// Merge's targets come from the recipe index only. A session
// that lacks the current entry's requirements publishes its own entry of the
// recipe without indexing it; a record of that recipe still lands on the
// indexed entry, and the unindexed one learns nothing of the Cloud's copy.
func TestMergeValuesTargetsOnlyTheIndexedEntry(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	bctx, b, bsrv := transferTestCache(t)
	publish := func(ctx context.Context, session, text string) AnyResult {
		t.Helper()
		value := &transferTestValue{Text: text}
		frame := func() *ResultCall {
			return &ResultCall{Kind: ResultCallKindField, Field: "root", Type: NewResultCallType(value.Type())}
		}
		res, err := b.GetOrInitCall(ctx, session, bsrv, &CallRequest{ResultCall: frame(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame())
		})
		require.NoError(t, err)
		return res
	}
	handle := cacheTestVolatileSessionResourceHandle("MERGE_TARGET_LATE_DEP")
	aCtx := currentEntryTestSession(bctx, b, "session-a")
	require.NoError(t, b.BindSessionResource(aCtx, "session-a", "session-a-client", handle, "a"))
	indexed := publish(aCtx, "session-a", "indexed")
	leaf, err := cacheTestSessionResourceLeaf(aCtx, handle)
	require.NoError(t, err)
	dep, err := b.AttachResult(aCtx, "session-a", bsrv, leaf)
	require.NoError(t, err)
	require.NoError(t, b.AddExplicitDependency(aCtx, indexed, dep, "test_late_dep"))
	cCtx := currentEntryTestSession(bctx, b, "session-c")
	unindexed := publish(cCtx, "session-c", "unindexed")
	indexedRow, unindexedRow := indexed.cacheSharedResult(), unindexed.cacheSharedResult()
	require.NotEqual(t, indexedRow.id, unindexedRow.id, "session C lacks the requirements")

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(indexedRow.id), reply.Imported()[0].ResultID, "the record lands on the indexed entry")
	b.egraphMu.RLock()
	current := b.currentEntryForRecipeLocked(reply.Values[0].recipeForTest(b))
	_, indexedCloud := indexedRow.cloudHoldingLocked()
	_, unindexedCloud := unindexedRow.cloudHoldingLocked()
	b.egraphMu.RUnlock()
	require.Same(t, indexedRow, current)
	require.NotNil(t, indexedCloud)
	require.Nil(t, unindexedCloud, "the unindexed entry learns nothing")
	require.NoError(t, b.ReleaseSession(aCtx, "session-a"))
	require.NoError(t, b.ReleaseSession(cCtx, "session-c"))
}

// An expired entry that another entry depends on is retired by a merge too,
// while both are still encoded envelopes: the dependent's edge is a use.
// The record gets a new entry, the dependent still decodes
// against the retired entry's value, and a later merge of the record lands on
// the new entry: a retired entry is never a target.
func TestMergeValuesRetiresAnExpiredEntryADependentUses(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	oldDep := persistedListTestResult(t, actx, a, asrv, "merge-dependency", String("old"))
	list := persistedListTestResult(t, actx, a, asrv, "merge-dependent", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{oldDep}})
	listBundle := exportTestBundle(t, actx, a, list)
	cctx, sender, csrv := transferTestCache(t)
	newDep := persistedListTestResult(t, cctx, sender, csrv, "merge-dependency", String("new"))
	depBundle := exportTestBundle(t, cctx, sender, newDep)

	bctx, b, bsrv := transferTestCache(t)
	listReply, err := b.MergeValues(bctx, cloudCacheID, listBundle)
	require.NoError(t, err)
	listID := sharedResultID(listReply.Imported()[0].ResultID)
	b.egraphMu.RLock()
	var depID sharedResultID
	for id := range b.resultsByID[listID].deps {
		depID = id
	}
	depRow := b.resultsByID[depID]
	b.egraphMu.RUnlock()
	require.NotZero(t, depID)
	require.Nil(t, depRow.loadPayloadState().self, "the dependent and its dependency are still envelopes")
	mergeTestExpire(b, depRow)

	reply, err := b.MergeValues(bctx, cloudCacheID, depBundle)
	require.NoError(t, err)
	freshID := sharedResultID(reply.Imported()[0].ResultID)
	require.NotEqual(t, depID, freshID, "a new entry")
	b.egraphMu.RLock()
	require.Empty(t, depRow.recipeKeys, "the dependency is retired")
	require.Zero(t, depRow.replacements)
	require.Equal(t, freshID, b.entriesByRecipe[depRow.recipeKeysForTest(t, b)])
	b.egraphMu.RUnlock()

	res, err := b.LoadResultByResultID(bctx, "hit-session", bsrv, uint64(listID))
	require.NoError(t, err)
	item, err := res.Unwrap().(Enumerable).NthValue(1, nil)
	require.NoError(t, err)
	require.Equal(t, depID, item.cacheSharedResult().id, "the dependent decodes against the retired entry")
	require.Equal(t, String("old"), item.Unwrap())

	again, err := b.MergeValues(bctx, cloudCacheID, depBundle)
	require.NoError(t, err)
	require.Equal(t, uint64(freshID), again.Imported()[0].ResultID, "the next merge lands on the current entry")
	require.NoError(t, b.ReleaseSession(bctx, "hit-session"))
}

// An offered reply names a holding the Cloud already has. One about a holding
// that doesn't exist, collected in between, is dropped: it creates nothing
// and changes nothing.
func TestOfferedReplyAboutAMissingHoldingChangesNothing(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	a.call(callRow{key: HolderKey{"cache-a", 2}, session: "s", holding: holdingOf("dep")})
	require.Empty(t, a.collect())
	cloud.egraphMu.RLock()
	entries := len(cloud.resultsByID)
	cloud.egraphMu.RUnlock()

	change, found, err := cloud.ApplyHeldValueState(t.Context(), HolderKey{"cache-a", 1}, HeldValueState{
		Replacements: 1,
		Deps:         []uint64{2},
		OfferedParts: []PersistedPartAddress{{Part: "fs"}},
	})
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, change.Candidates)
	require.Empty(t, change.Recipes)
	requireNoHolding(t, cloud, HolderKey{"cache-a", 1})
	require.Zero(t, requireHolding(t, cloud, HolderKey{"cache-a", 2}).Dependents, "no edge to the dependency")
	cloud.egraphMu.RLock()
	require.Equal(t, entries, len(cloud.resultsByID))
	unknown := len(cloud.remoteCaches["cache-a"].unknownDependents)
	cloud.egraphMu.RUnlock()
	require.Zero(t, unknown, "no unknown number waits")
}

// B prunes a root it merged: the drop's time is later than the merged
// reply's commit, so the drop wins over the reply's retention and the
// collection pass collects the holdings the reply created. A merged reply read
// after that prune recreates the root's holding with its stale retention, as
// 5.4 accepts.
func TestApplyMergedReplyThenAPruneDrop(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "root", &transferTestValue{Text: "root"})
	dep := persistedListTestResult(t, actx, a, asrv, "dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, root, dep)
	ctx, cloud, sent := mergeTestThroughTheCloud(t, exportTestBundle(t, actx, a, root))
	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, sent)
	require.NoError(t, err)
	rootKey := HolderKey{Cache: "cache-b", Number: reply.Roots[0].Number}
	change, err := cloud.ApplyMergedReply(ctx, "cache-b", sent, reply)
	require.NoError(t, err)
	collected, err := cloud.CollectRemoteHoldings(ctx, change.Candidates)
	require.NoError(t, err)
	require.Empty(t, collected)
	require.True(t, mergeTestHolding(t, cloud, rootKey).Retained)

	report, err := b.Prune(bctx, []CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	var drop *CacheRetentionDrop
	for i := range report.DroppedEdges {
		if report.DroppedEdges[i].ResultID == rootKey.Number {
			drop = &report.DroppedEdges[i]
		}
	}
	require.NotNil(t, drop, "the prune drops the merged root's edge")
	require.Greater(t, drop.DroppedAt.UnixNano(), reply.EngineTimeUnixNano, "the drop is later than the commit")
	candidates, err := cloud.ObserveRemoteRetention(ctx, rootKey, RetentionObservation{Generation: b.Identity().Generation, EngineTimeUnixNano: drop.DroppedAt.UnixNano()})
	require.NoError(t, err)
	collected, err = cloud.CollectRemoteHoldings(ctx, candidates)
	require.NoError(t, err)
	require.Len(t, collected, 2, "the root's holding and its dependency's")

	change, err = cloud.ApplyMergedReply(ctx, "cache-b", sent, reply)
	require.NoError(t, err)
	collected, err = cloud.CollectRemoteHoldings(ctx, change.Candidates)
	require.NoError(t, err)
	require.Empty(t, collected)
	require.True(t, mergeTestHolding(t, cloud, rootKey).Retained, "a late reply recreates the stale retention")
}

// The Cloud applies the merged reply of the R -> retired P case: R's holding
// depends on B's retired P, which the Cloud holds nothing of, so the number
// waits as an unknown dependency; P's record landed on the current P', whose
// holding nothing owns and the collection pass collects.
func TestApplyMergedReplyKeepsAKeptEntrysOwnDependencies(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	sentR := persistedListTestResult(t, actx, a, asrv, "r", &transferTestValue{Text: "r"})
	sentP := persistedListTestResult(t, actx, a, asrv, "p", &transferTestValue{Text: "p"})
	transferTestDependency(a, actx, sentR, sentP)
	ctx, cloud, sent := mergeTestThroughTheCloud(t, exportTestBundle(t, actx, a, sentR))

	bctx, b, bsrv := transferTestCache(t)
	r := persistedListTestResult(t, bctx, b, bsrv, "r", &transferTestValue{Text: "local r"})
	p := persistedListTestResult(t, bctx, b, bsrv, "p", &transferTestValue{Text: "local p"})
	transferTestDependency(b, bctx, r, p)
	mergeTestExpire(b, p.cacheSharedResult())
	pNew := persistedListTestResult(t, bctx, b, bsrv, "p", &transferTestValue{Text: "local p'"})
	reply, err := b.MergeValues(bctx, cloudCacheID, sent)
	require.NoError(t, err)

	change, err := cloud.ApplyMergedReply(ctx, "cache-b", sent, reply)
	require.NoError(t, err)
	collected, err := cloud.CollectRemoteHoldings(ctx, change.Candidates)
	require.NoError(t, err)
	pNewKey := HolderKey{Cache: "cache-b", Number: uint64(pNew.cacheSharedResult().id)}
	require.Equal(t, []HolderKey{pNewKey}, collected, "P' is nobody's dependency")
	h := mergeTestHolding(t, cloud, HolderKey{Cache: "cache-b", Number: uint64(r.cacheSharedResult().id)})
	require.Empty(t, h.Deps)
	require.Equal(t, []uint64{uint64(p.cacheSharedResult().id)}, h.UnknownDeps, "R's own dependency, the retired P")
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
}

// Offer owners are relocated to the targets: an offer whose owner names a
// dependency's record names, after the merge, the entry that record landed
// on, here B's own entry of the recipe under its own number, which the offer
// now owns.
func TestMergeValuesRelocatesOfferOwnersToTheTargets(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "root", &transferTestValue{Text: "pending"})
	dep := persistedListTestResult(t, actx, a, asrv, "dep", String("dep"))
	transferTestOffer(t, a, actx, root, dep)
	bundle := exportTestBundle(t, actx, a, root)

	bctx, b, bsrv := transferTestCache(t)
	for i := range 3 {
		persistedListTestResult(t, bctx, b, bsrv, fmt.Sprintf("other-%d", i), String("other"))
	}
	local := persistedListTestResult(t, bctx, b, bsrv, "dep", String("dep")).cacheSharedResult()
	require.NotEqual(t, dep.cacheSharedResult().id, local.id)
	b.egraphMu.RLock()
	before := local.incomingOwnershipCount
	b.egraphMu.RUnlock()

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	b.egraphMu.RLock()
	offers := b.resultsByID[sharedResultID(reply.Imported()[0].ResultID)].testPartOffers()
	after := local.incomingOwnershipCount
	b.egraphMu.RUnlock()
	require.Len(t, offers, 1)
	for _, offer := range offers {
		require.Equal(t, []uint64{uint64(local.id)}, offer.owner.record.DependencyIDs, "the owner names B's entry")
	}
	require.Equal(t, before+1, after, "which the offer owns")
}

// A publication racing a merge of the same recipe ends with one entry: the
// merge commits while the publication computes, and the publication adopts
// the merged entry.
func TestMergeValuesThenAPublicationAdopts(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "sent")
	bctx, b, bsrv := transferTestCache(t)
	value := &transferTestValue{Text: "local"}
	frame := func() *ResultCall {
		return &ResultCall{Kind: ResultCallKindField, Field: "root", Type: NewResultCallType(value.Type())}
	}
	started, finish := make(chan struct{}), make(chan struct{})
	published := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: frame(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
			close(started)
			<-finish
			return NewResultForCall(value, frame())
		})
		published <- currentEntryTestOutcome{res, err}
	}()
	<-started
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	close(finish)
	out := <-published
	require.NoError(t, out.err)
	require.Equal(t, reply.Imported()[0].ResultID, uint64(out.res.cacheSharedResult().id), "the publication adopts the merged entry")
	b.egraphMu.RLock()
	recipe := reply.Values[0].recipeForTest(b)
	entries := 0
	for _, res := range b.resultsByID {
		if frame := res.loadResultCall(); frame != nil && frame.Field == "root" && res.recipeKeysForTest(t, b) == recipe {
			entries++
		}
	}
	b.egraphMu.RUnlock()
	require.Equal(t, 1, entries, "one entry of the recipe")
}

// Two bundles share an expired dependency P. The Cloud stores P's fs after
// the first; the second brings P's record again, expired too, and not the
// part. P keeps its record and its stored fs: when both have expired, the
// entry keeps its own.
func TestMergeValuesKeepsAnExpiredDependencysStoredPart(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	p := persistedListTestResult(t, actx, a, asrv, "p", &transferTestValue{Text: "p"})
	r1 := persistedListTestResult(t, actx, a, asrv, "r1", &transferTestValue{Text: "r1"})
	r2 := persistedListTestResult(t, actx, a, asrv, "r2", &transferTestValue{Text: "r2"})
	transferTestDependency(a, actx, r1, p)
	transferTestDependency(a, actx, r2, p)
	expireP := func(bundle ValueBundle) (ValueBundle, TransferOrdinal) {
		var ordinal TransferOrdinal
		for i := range bundle.Values {
			if bundle.Values[i].SenderNumber == uint64(p.cacheSharedResult().id) {
				bundle.Values[i].ExpiresAtUnix = time.Now().Add(-time.Hour).Unix()
				ordinal = bundle.Values[i].Ordinal
			}
		}
		require.NotZero(t, ordinal)
		return bundle, ordinal
	}
	numberOf := func(reply MergeReply, ordinal TransferOrdinal) uint64 {
		for _, value := range reply.Values {
			if value.Ordinal == ordinal {
				return value.Number
			}
		}
		t.Fatalf("no value for ordinal %d", ordinal)
		return 0
	}
	first, firstP := expireP(exportTestBundle(t, actx, a, r1))
	second, secondP := expireP(exportTestBundle(t, actx, a, r2))

	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", first)
	require.NoError(t, err)
	number := numberOf(reply, firstP)
	stored, err := cloud.SetStoredPart(ctx, number, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)

	reply, err = cloud.MergeValues(ctx, "cache-a", second)
	require.NoError(t, err)
	require.Equal(t, number, numberOf(reply, secondP), "P lands on the same entry")
	cloud.egraphMu.RLock()
	entry := cloud.resultsByID[sharedResultID(number)]
	storedParts, replacements := len(entry.storedParts), entry.replacements
	cloud.egraphMu.RUnlock()
	require.Equal(t, 1, storedParts, "P keeps its stored fs")
	require.Zero(t, replacements)
}

// A null record merged into a cold engine is a hit: a call of a nullable type
// that returned nothing is exported as an absent value, and B's first call of
// the recipe after the merge runs nothing and returns nothing.
func TestMergeValuesNullRecordIntoAColdEngineHits(t *testing.T) {
	t.Parallel()
	frame := func() *ResultCall {
		f := &ResultCall{Kind: ResultCallKindField, Field: "maybe", Type: NewResultCallType((&transferTestValue{}).Type())}
		f.Type.NonNull = false
		return f
	}
	calls := 0
	nothing := func(context.Context) (AnyResult, error) { calls++; return nil, nil }
	actx, a, _ := transferTestCache(t)
	sent, err := a.GetOrInitCall(actx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame(), IsPersistable: true}, nothing)
	require.NoError(t, err)
	bundle := exportTestBundle(t, actx, a, sent)
	require.Len(t, bundle.Values, 1)

	bctx, b, bsrv := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	hit, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: frame(), IsPersistable: true}, nothing)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "only A ran the call")
	require.True(t, hit.HitCache())
	require.Equal(t, reply.Imported()[0].ResultID, uint64(hit.cacheSharedResult().id))
	// An absent value decodes as persistence rebuilds it after a restart: an
	// invalid DynamicNullable of the declared type (persistedAbsentValue).
	absent, ok := hit.Unwrap().(DynamicNullable)
	require.True(t, ok, "got %T", hit.Unwrap())
	require.False(t, absent.Valid, "and it returns nothing")
}

// The Cloud admits none of a bundle's parts (5.1 point 3): the entry stores
// the record without its offers, stores a part only once it has the bytes
// (SetStoredPart), and the sending engine's holding gets no parts from the
// record.
func TestMergeValuesOnTheCloudAdmitsNoParts(t *testing.T) {
	t.Parallel()
	bundle := mergeTestOffered(t, "root")
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Empty(t, reply.Values[0].Parts)
	require.Empty(t, reply.Values[0].OfferedParts)
	cloud.egraphMu.RLock()
	entry := cloud.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
	offers, storedParts := len(entry.testPartOffers()), len(entry.storedParts)
	pending := len(entry.persistedEnvelope.PendingOffers)
	cloud.egraphMu.RUnlock()
	require.Zero(t, offers)
	require.Zero(t, storedParts)
	require.Zero(t, pending, "the stored record carries no offer")
	h := mergeTestHolding(t, cloud, HolderKey{Cache: "cache-a", Number: bundle.Values[0].SenderNumber})
	require.Empty(t, h.Parts)
	require.Empty(t, h.OfferedParts)
}

// mergeTestTwoValues publishes P's recipe twice in a sending cache: an old
// value, which a live list R holds, and which then expires and is retired;
// then the current P' of the recipe. It exports R and P' in one bundle, whose
// records are P, R -> P and P' in dependency order. With expireFresh, P' has
// expired too, so a live list of both is exported instead, with records P,
// P' and the list. Each value of P may carry the Cloud's offer of its
// snapshot part, with the given path.
func mergeTestTwoValues(t *testing.T, oldOffer, freshOffer string, expireFresh bool) (ValueBundle, *sharedResult, *sharedResult) {
	t.Helper()
	ctx, a, srv := transferTestCache(t)
	offer := func(res AnyResult, path string) {
		if path == "" {
			return
		}
		o := testLiveOffer()
		o.Value.Path = path
		out, err := a.testOfferParts(ctx, res, []PersistedPartOffer{o})
		require.NoError(t, err)
		require.Equal(t, OfferAccepted, out[0].Outcome)
	}
	old := persistedListTestResult(t, ctx, a, srv, "two-values", &transferTestValue{Text: "old value"})
	offer(old, oldOffer)
	parent := persistedListTestResult(t, ctx, a, srv, "two-values-parent", DynamicResultArrayOutput{Elem: &transferTestValue{}, Values: []AnyResult{old}})
	mergeTestExpire(a, old.cacheSharedResult())
	frame := old.cacheSharedResult().loadResultCall().clone()
	fresh, err := a.GetOrInitCall(ctx, "fresh-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "fresh value"}, frame)
	})
	require.NoError(t, err)
	require.NotEqual(t, old.cacheSharedResult().id, fresh.cacheSharedResult().id, "P is retired")
	offer(fresh, freshOffer)
	roots := []AnyResult{parent, fresh}
	if expireFresh {
		both := persistedListTestResult(t, ctx, a, srv, "two-values-both", DynamicResultArrayOutput{Elem: &transferTestValue{}, Values: []AnyResult{old, fresh}})
		mergeTestExpire(a, fresh.cacheSharedResult())
		roots = []AnyResult{both}
	}
	bundle := exportTestBundle(t, ctx, a, roots...)
	require.Len(t, bundle.Values, 3)
	t.Cleanup(func() { require.NoError(t, a.ReleaseSession(ctx, "fresh-session")) })
	return bundle, old.cacheSharedResult(), fresh.cacheSharedResult()
}

// mergeTestOrdinalOf returns the bundle ordinal of the sending cache's entry.
func mergeTestOrdinalOf(t *testing.T, bundle ValueBundle, sent *sharedResult) TransferOrdinal {
	t.Helper()
	for _, value := range bundle.Values {
		if value.SenderNumber == uint64(sent.id) {
			return value.Ordinal
		}
	}
	t.Fatalf("no record of sender entry %d", sent.id)
	return 0
}

// mergeTestValueOf returns the reply's value for a bundle ordinal.
func mergeTestValueOf(t *testing.T, reply MergeReply, ordinal TransferOrdinal) MergedValue {
	t.Helper()
	for _, value := range reply.Values {
		if value.Ordinal == ordinal {
			return value
		}
	}
	t.Fatalf("no reply value for ordinal %d", ordinal)
	return MergedValue{}
}

// A bundle can carry two records of one recipe: a retired, expired P that a
// live R depends on, and the current P'. The recipe is decided once, by its
// best record, the first unexpired one: the recipe's entry stores the record
// of P', with its expiry; P's record lands on the same entry and adds none of
// its parts to that unexpired value; and R's dependency is that entry. On the
// Cloud, the sending engine's holdings of both P and P' sit on it.
func TestMergeValuesDecidesARecipeByItsBestRecord(t *testing.T) {
	t.Parallel()
	for _, cloud := range []bool{false, true} {
		t.Run(map[bool]string{false: "engine", true: "cloud"}[cloud], func(t *testing.T) {
			t.Parallel()
			bundle, old, fresh := mergeTestTwoValues(t, "/old", "", false)
			var (
				ctx  context.Context
				b    *Cache
				from = cloudCacheID
			)
			if cloud {
				ctx, b = storedPartTestCache(t, WithBlobStore())
				from = "cache-a"
			} else {
				ctx, b, _ = transferTestCache(t)
			}
			reply, err := b.MergeValues(ctx, from, bundle)
			require.NoError(t, err)
			oldValue := mergeTestValueOf(t, reply, mergeTestOrdinalOf(t, bundle, old))
			freshValue := mergeTestValueOf(t, reply, mergeTestOrdinalOf(t, bundle, fresh))
			require.Equal(t, freshValue.Number, oldValue.Number, "one entry for the recipe")
			require.Zero(t, freshValue.ExpiresAtUnix, "it stores the unexpired record of P'")
			b.egraphMu.RLock()
			stored, err := json.Marshal(b.resultsByID[sharedResultID(freshValue.Number)].persistedEnvelope)
			b.egraphMu.RUnlock()
			require.NoError(t, err)
			require.Contains(t, string(stored), "fresh value")
			for _, value := range reply.Values {
				if value.Number != freshValue.Number {
					require.Equal(t, []uint64{freshValue.Number}, value.Deps, "R's dependency is the recipe's entry")
				}
			}
			b.egraphMu.RLock()
			entry := b.resultsByID[sharedResultID(freshValue.Number)]
			offers := len(entry.testPartOffers())
			b.egraphMu.RUnlock()
			require.Zero(t, offers, "P's expired record adds no parts to the unexpired value")
			if cloud {
				info, ok := b.RemoteEntryInfo(HolderKey{Cache: "cache-a", Number: uint64(fresh.id)})
				require.True(t, ok)
				var keys []HolderKey
				for _, h := range info.Holdings {
					keys = append(keys, h.Key)
				}
				require.ElementsMatch(t, []HolderKey{{"cache-a", uint64(old.id)}, {"cache-a", uint64(fresh.id)}}, keys)
			}
		})
	}
}

// The same bundle into an engine whose entry of the recipe has expired and is
// unused: the record of P', the best, replaces its value in place.
func TestMergeValuesBestRecordReplacesAnUnusedExpiredEntry(t *testing.T) {
	t.Parallel()
	bundle, _, fresh := mergeTestTwoValues(t, "/old", "", false)
	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "two-values", &transferTestValue{Text: "local"})
	row := local.cacheSharedResult()
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
	mergeTestExpire(b, row)

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	value := mergeTestValueOf(t, reply, mergeTestOrdinalOf(t, bundle, fresh))
	require.Equal(t, uint64(row.id), value.Number, "replaced in place")
	require.Equal(t, uint64(1), value.Replacements)
	require.Zero(t, value.ExpiresAtUnix)
}

// When every record of a recipe has expired, the first decides and the others
// add only what the entry lacks.
func TestMergeValuesFirstOfExpiredRecordsDecides(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		oldOffer, fresh string
		want            string
	}{
		{"the first's part stays", "/first", "/second", "/first"},
		{"a part the entry lacks is added", "", "/second", "/second"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bundle, old, fresh := mergeTestTwoValues(t, tc.oldOffer, tc.fresh, true)
			bctx, b, _ := transferTestCache(t)
			reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
			require.NoError(t, err)
			oldValue := mergeTestValueOf(t, reply, mergeTestOrdinalOf(t, bundle, old))
			require.Equal(t, oldValue.Number, mergeTestValueOf(t, reply, mergeTestOrdinalOf(t, bundle, fresh)).Number)
			b.egraphMu.RLock()
			entry := b.resultsByID[sharedResultID(oldValue.Number)]
			var paths []string
			for _, offer := range entry.testPartOffers() {
				paths = append(paths, offer.record.Value.Path)
			}
			env := entry.persistedEnvelope
			b.egraphMu.RUnlock()
			require.Equal(t, []string{tc.want}, paths)
			require.NotNil(t, env)
			stored, err := json.Marshal(env)
			require.NoError(t, err)
			require.Contains(t, string(stored), "old value", "the first record decides")
		})
	}
}

// The reply reports the complete parts of a record the merge installs as the
// entry's own spans will: the part probe's LocalComplete over the installed
// record, read under the commit's lock.
func TestMergeValuesReplyReportsAnInstalledRecordsCompleteParts(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "installed-ready-record", "ready")
	ctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Values, 1)
	b.egraphMu.RLock()
	row := b.resultsByID[sharedResultID(reply.Values[0].Number)]
	b.egraphMu.RUnlock()
	expected, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	require.Contains(t, b.completePartKeys(ctx, row), expected, "the committed record proves the part complete")
	require.Contains(t, reply.Values[0].Parts, PersistedPartAddress{Part: "snapshot"}, "and the reply reports it")
}

// An entry the merge creates that nothing owns is collected in the commit,
// and the reply leaves it out. B keeps its R, whose own dependency is its
// retired P; the current entry of P's recipe was pruned. The bundle's P, which
// only the bundle's R needed, gets a new entry that nothing owns.
func TestMergeValuesCollectsAnUnusedCreatedEntry(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	ap := persistedListTestResult(t, actx, a, asrv, "p", String("value"))
	ar := persistedListTestResult(t, actx, a, asrv, "r", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{ap}})
	bundle := exportTestBundle(t, actx, a, ar)
	bctx, b, bsrv := transferTestCache(t)
	old := persistedListTestResult(t, bctx, b, bsrv, "p", String("value"))
	r := persistedListTestResult(t, bctx, b, bsrv, "r", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{old}})
	mergeTestExpire(b, old.cacheSharedResult())
	fresh := persistedListTestResult(t, bctx, b, bsrv, "p", String("value"))
	require.NotEqual(t, old.cacheSharedResult().id, fresh.cacheSharedResult().id)
	_, err := b.LoadResultByResultID(bctx, "reader", bsrv, uint64(r.cacheSharedResult().id))
	require.NoError(t, err)
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
	_, err = b.Prune(bctx, []CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	b.egraphMu.RLock()
	_, freshSurvived := b.resultsByID[fresh.cacheSharedResult().id]
	entries := len(b.resultsByID)
	b.egraphMu.RUnlock()
	require.False(t, freshSurvived, "precondition: only R and its retired P remain")

	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Values, 1, "only R: the created entry was collected")
	require.Equal(t, uint64(r.cacheSharedResult().id), reply.Values[0].Number)
	require.Equal(t, []uint64{uint64(old.cacheSharedResult().id)}, reply.Values[0].Deps)
	b.egraphMu.RLock()
	after := len(b.resultsByID)
	_, indexed := b.entriesByRecipe[old.cacheSharedResult().recipeKeysForTest(t, b)]
	b.egraphMu.RUnlock()
	require.Equal(t, entries, after, "nothing new stays")
	require.False(t, indexed, "and the recipe index names no collected entry")
	require.NoError(t, b.ReleaseSession(bctx, "reader"))
}

// A merge that would close a dependency cycle is refused, like any failed
// check, and changes nothing. On the Cloud, which always replaces, a kept A
// -> B and a new record B -> A would close one. On an engine, the two
// records of one recipe on one entry can: P' -> R, and R -> P lands on the
// entry of P'.
func TestMergeValuesRefusesADependencyCycle(t *testing.T) {
	t.Parallel()
	t.Run("cloud", func(t *testing.T) {
		t.Parallel()
		ctx, a, asrv := transferTestCache(t)
		pa := persistedListTestResult(t, ctx, a, asrv, "cycle-a", String("a"))
		pb := persistedListTestResult(t, ctx, a, asrv, "cycle-b", String("old-b"))
		require.NoError(t, a.AddExplicitDependency(ctx, pa, pb, "test"))
		mergeTestExpire(a, pb.cacheSharedResult())
		first := exportTestBundle(t, ctx, a, pa)
		cctx, cloud := storedPartTestCache(t, WithBlobStore())
		firstReply, err := cloud.MergeValues(cctx, "cache-a", first)
		require.NoError(t, err)
		aNumber := sharedResultID(firstReply.Imported()[0].ResultID)

		bctx, b, bsrv := transferTestCache(t)
		qa := persistedListTestResult(t, bctx, b, bsrv, "cycle-a", String("a"))
		qb := persistedListTestResult(t, bctx, b, bsrv, "cycle-b", String("new-b"))
		require.NoError(t, b.AddExplicitDependency(bctx, qb, qa, "test"))
		second := exportTestBundle(t, bctx, b, qb)

		cloud.egraphMu.RLock()
		var bNumber sharedResultID
		for dep := range cloud.resultsByID[aNumber].deps {
			bNumber = dep
		}
		before, err := json.Marshal(cloud.resultsByID[bNumber].persistedEnvelope)
		require.NoError(t, err)
		cloud.egraphMu.RUnlock()
		_, err = cloud.MergeValues(cctx, "cache-b", second)
		require.ErrorContains(t, err, "cycle")
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		require.Equal(t, map[sharedResultID]struct{}{bNumber: {}}, cloud.resultsByID[aNumber].deps, "the Cloud keeps A -> B")
		require.Empty(t, cloud.resultsByID[bNumber].deps)
		after, err := json.Marshal(cloud.resultsByID[bNumber].persistedEnvelope)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after), "and B's old value")
	})
	t.Run("engine", func(t *testing.T) {
		t.Parallel()
		ctx, a, srv := transferTestCache(t)
		old := persistedListTestResult(t, ctx, a, srv, "cycle-p", String("old"))
		r := persistedListTestResult(t, ctx, a, srv, "cycle-r", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{old}})
		mergeTestExpire(a, old.cacheSharedResult())
		frame := old.cacheSharedResult().loadResultCall().clone()
		fresh, err := a.GetOrInitCall(ctx, "fresh-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(String("fresh"), frame)
		})
		require.NoError(t, err)
		require.NotEqual(t, old.cacheSharedResult().id, fresh.cacheSharedResult().id)
		require.NoError(t, a.AddExplicitDependency(ctx, fresh, r, "test"))
		bundle := exportTestBundle(t, ctx, a, fresh)
		require.Len(t, bundle.Values, 3)

		bctx, b, _ := transferTestCache(t)
		b.egraphMu.RLock()
		entries := len(b.resultsByID)
		b.egraphMu.RUnlock()
		_, err = b.MergeValues(bctx, cloudCacheID, bundle)
		require.ErrorContains(t, err, "cycle")
		b.egraphMu.RLock()
		after := len(b.resultsByID)
		b.egraphMu.RUnlock()
		require.Equal(t, entries, after, "the engine is unchanged")
		require.NoError(t, a.ReleaseSession(ctx, "fresh-session"))
	})
}

// The same rule when one live list holds both values of the recipe, the
// retired, expired one and the current one: the unexpired record is the
// recipe's, into an engine and into the Cloud.
func TestMergeValuesDecidesARecipeHeldTwiceInAList(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, a, srv, "same-recipe-in-a-list", String("old"))
	mergeTestExpire(a, old.cacheSharedResult())
	frame := old.cacheSharedResult().loadResultCall().clone()
	fresh, err := a.GetOrInitCall(ctx, "new-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String("fresh"), frame)
	})
	require.NoError(t, err)
	require.NotEqual(t, old.cacheSharedResult().id, fresh.cacheSharedResult().id)
	root := persistedListTestResult(t, ctx, a, srv, "both-values", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{old, fresh}})
	bundle := exportTestBundle(t, ctx, a, root)
	require.Len(t, bundle.Values, 3)
	freshOrdinal := mergeTestOrdinalOf(t, bundle, fresh.cacheSharedResult())
	for _, cloud := range []bool{false, true} {
		t.Run(map[bool]string{false: "engine", true: "cloud"}[cloud], func(t *testing.T) {
			bctx, b, _ := transferTestCache(t)
			from := cloudCacheID
			if cloud {
				bctx, b = storedPartTestCache(t, WithBlobStore())
				from = "cache-a"
			}
			reply, err := b.MergeValues(bctx, from, bundle)
			require.NoError(t, err)
			value := mergeTestValueOf(t, reply, freshOrdinal)
			require.True(t, value.ExpiresAtUnix == 0 || value.ExpiresAtUnix > time.Now().Unix(), "the unexpired record decides: %+v", value)
		})
	}
	require.NoError(t, a.ReleaseSession(ctx, "new-session"))
}

// Requirements follow the final graph. The export is P (expired, retired), R
// -> P, a session resource H, and the current P' -> H. The merge maps R's
// dependency to the entry of P', which comes after R in the bundle;
// recomputed in the final graph's dependency order, R requires H, and a
// session that hasn't bound H is refused R.
func TestMergeValuesRecomputesRequirementsInFinalGraphOrder(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, a, srv, "order-p", String("old"))
	r := persistedListTestResult(t, ctx, a, srv, "order-r", String("r"))
	require.NoError(t, a.AddExplicitDependency(ctx, r, old, "test"))
	mergeTestExpire(a, old.cacheSharedResult())
	frame := old.cacheSharedResult().loadResultCall().clone()
	newCtx := currentEntryTestSession(ctx, a, "new-session")
	handle := cacheTestVolatileSessionResourceHandle("merge-order")
	require.NoError(t, a.BindSessionResource(newCtx, "new-session", "new-session-client", handle, "bound"))
	fresh, err := a.GetOrInitCall(newCtx, "new-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String("fresh"), frame)
	})
	require.NoError(t, err)
	require.NotEqual(t, old.cacheSharedResult().id, fresh.cacheSharedResult().id)
	leaf, err := cacheTestSessionResourceLeaf(newCtx, handle)
	require.NoError(t, err)
	attachedLeaf, err := a.AttachResult(newCtx, "new-session", srv, leaf)
	require.NoError(t, err)
	require.NoError(t, a.AddExplicitDependency(newCtx, fresh, attachedLeaf, "test"))
	require.NoError(t, cacheRequiredSessionResourcesError(a))
	bundle := exportTestBundle(t, ctx, a, r, fresh)

	bctx, b, bsrv := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	var root uint64
	for _, rr := range reply.Roots {
		if rr.Ordinal == bundle.Roots[0].Ordinal {
			root = rr.Number
		}
	}
	require.NoError(t, cacheRequiredSessionResourcesError(b), "requirements follow the final graph")
	unboundCtx := currentEntryTestSession(bctx, b, "unbound-session")
	_, err = b.LoadResultByResultID(unboundCtx, "unbound-session", bsrv, root)
	require.ErrorContains(t, err, "has not bound the session resources")
	require.NoError(t, a.ReleaseSession(newCtx, "new-session"))
}

// The graph check counts only the offers merge admits. A retired, expired P
// holds an offer whose owner is the current P'; R -> P -(offer)-> P' is
// acyclic. The merge maps P and P' to one entry, whose value is that of P', and
// P's expired offer is not admitted there: it closes no cycle, and the merge
// succeeds with no offer on the entry.
func TestMergeValuesChecksOnlyTheAdmittedOffers(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, a, srv, "discarded-offer-p", &transferTestValue{Text: "old"})
	root := persistedListTestResult(t, ctx, a, srv, "discarded-offer-r", DynamicResultArrayOutput{Elem: &transferTestValue{}, Values: []AnyResult{old}})
	mergeTestExpire(a, old.cacheSharedResult())
	frame := old.cacheSharedResult().loadResultCall().clone()
	fresh, err := a.GetOrInitCall(ctx, "fresh-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "fresh"}, frame)
	})
	require.NoError(t, err)
	require.NotEqual(t, old.cacheSharedResult().id, fresh.cacheSharedResult().id)
	offer := testLiveOffer()
	offer.Owner.DependencyIDs = []uint64{uint64(fresh.cacheSharedResult().id)}
	out, err := a.testOfferParts(ctx, old, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	bundle := exportTestBundle(t, ctx, a, root)
	require.Len(t, bundle.Values, 3)

	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err, "the expired record's offer is not admitted, so it closes no cycle")
	current := mergeTestValueOf(t, reply, mergeTestOrdinalOf(t, bundle, fresh.cacheSharedResult()))
	require.Zero(t, current.ExpiresAtUnix)
	b.egraphMu.RLock()
	offers := len(b.resultsByID[sharedResultID(current.Number)].testPartOffers())
	b.egraphMu.RUnlock()
	require.Zero(t, offers)
	require.NoError(t, a.ReleaseSession(ctx, "fresh-session"))
}

// mergeTestOfferOn attaches the Cloud's offer of res's snapshot part, with the
// given path, owned by owners.
func mergeTestOfferOn(t *testing.T, ctx context.Context, c *Cache, res AnyResult, path string, owners ...AnyResult) {
	t.Helper()
	offer := testLiveOffer()
	offer.Value.Path = path
	for _, owner := range owners {
		offer.Owner.DependencyIDs = append(offer.Owner.DependencyIDs, uint64(owner.cacheSharedResult().id))
	}
	out, err := c.testOfferParts(ctx, res, []PersistedPartOffer{offer})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
}

// The graph check reads the offers the commit leaves, so an offer admission
// discards adds no edge. The receiver has live X and Y -> X; the bundle's X
// record carries an offer owned by Y, from an expired record or for a part
// complete there.
func TestMergeValuesChecksNoDiscardedOffer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		expired  bool
		complete bool
	}{
		{"an expired record's offer on a live entry", true, false},
		{"an offer for a part complete there", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, a, srv := transferTestCache(t)
			x := persistedListTestResult(t, ctx, a, srv, "discard-x", &transferTestValue{Text: "pending"})
			y := persistedListTestResult(t, ctx, a, srv, "discard-y", String("y"))
			mergeTestOfferOn(t, ctx, a, x, "/sent", y)
			root := persistedListTestResult(t, ctx, a, srv, "discard-root", String("root"))
			require.NoError(t, a.AddExplicitDependency(ctx, root, x, "test"))
			if tc.expired {
				mergeTestExpire(a, x.cacheSharedResult())
			}
			bundle := exportTestBundle(t, ctx, a, root)

			bctx, b, bsrv := transferTestCache(t)
			value := &transferTestValue{Text: "pending"}
			if tc.complete {
				value = &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "complete-snapshot"}}}
			}
			keptX := persistedListTestResult(t, bctx, b, bsrv, "discard-x", value)
			if tc.complete {
				b.completePartKeys(bctx, keptX.cacheSharedResult())
			}
			keptY := persistedListTestResult(t, bctx, b, bsrv, "discard-y", String("y"))
			require.NoError(t, b.AddExplicitDependency(bctx, keptY, keptX, "test"))
			_, err := b.MergeValues(bctx, cloudCacheID, bundle)
			require.NoError(t, err, "the offer is not admitted, so X -> Y is not in the final graph")
			require.Zero(t, mergeTestOffers(b, keptX.cacheSharedResult()))
		})
	}
}

// An offer the merge admits replaces the entry's own: the replaced offer's
// owner edge is gone from the final graph. The receiver's live X holds an
// offer owned by Y; the bundle admits an offer on Y owned by X. With the
// bundle's X record replacing X's offer by one Y doesn't own, there is no
// cycle and the merge succeeds. Without it, X -> Y -> X is a real cycle, and
// the merge is refused.
func TestMergeValuesChecksTheOffersTheCommitLeaves(t *testing.T) {
	t.Parallel()
	for _, replaces := range []bool{true, false} {
		t.Run(map[bool]string{true: "replaced", false: "real cycle"}[replaces], func(t *testing.T) {
			t.Parallel()
			ctx, a, srv := transferTestCache(t)
			x := persistedListTestResult(t, ctx, a, srv, "leaves-x", &transferTestValue{Text: "pending"})
			y := persistedListTestResult(t, ctx, a, srv, "leaves-y", &transferTestValue{Text: "pending"})
			if replaces {
				mergeTestOfferOn(t, ctx, a, x, "/new")
			}
			mergeTestOfferOn(t, ctx, a, y, "/sent", x)
			root := persistedListTestResult(t, ctx, a, srv, "leaves-root", String("root"))
			require.NoError(t, a.AddExplicitDependency(ctx, root, x, "test"))
			require.NoError(t, a.AddExplicitDependency(ctx, root, y, "test"))
			bundle := exportTestBundle(t, ctx, a, root)

			bctx, b, bsrv := transferTestCache(t)
			keptX := persistedListTestResult(t, bctx, b, bsrv, "leaves-x", &transferTestValue{Text: "pending"})
			keptY := persistedListTestResult(t, bctx, b, bsrv, "leaves-y", &transferTestValue{Text: "pending"})
			mergeTestOfferOn(t, bctx, b, keptX, "/old", keptY)
			_, err := b.MergeValues(bctx, cloudCacheID, bundle)
			if !replaces {
				require.ErrorContains(t, err, "cycle")
				return
			}
			require.NoError(t, err)
			b.egraphMu.RLock()
			var paths []string
			for _, offer := range keptX.cacheSharedResult().testPartOffers() {
				paths = append(paths, offer.record.Value.Path)
			}
			b.egraphMu.RUnlock()
			require.Equal(t, []string{"/new"}, paths, "X's offer was replaced")
			require.Equal(t, 1, mergeTestOffers(b, keptY.cacheSharedResult()))
		})
	}
}

// The commit applies the offer graph it checked, not the rows' intermediate
// ones: offers the final set replaces are retired before the final offers
// attach. The receiver keeps Z -> Y and Y's offer owned by X; the bundle
// brings X's offer owned by Z and Y's new offer with no owner, X's row first.
// The final graph X -> Z -> Y is acyclic, and Y's old edge to X must not
// refuse X's offer on the way.
func TestMergeValuesAppliesTheCheckedOfferGraph(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	z := persistedListTestResult(t, ctx, a, srv, "applied-z", String("z"))
	x := persistedListTestResult(t, ctx, a, srv, "applied-x", &transferTestValue{Text: "pending"})
	y := persistedListTestResult(t, ctx, a, srv, "applied-y", &transferTestValue{Text: "pending"})
	mergeTestOfferOn(t, ctx, a, x, "/new-x", z)
	mergeTestOfferOn(t, ctx, a, y, "/new-y")
	bundle := exportTestBundle(t, ctx, a, x, y)
	require.Less(t, mergeTestOrdinalOf(t, bundle, x.cacheSharedResult()), mergeTestOrdinalOf(t, bundle, y.cacheSharedResult()))

	bctx, b, bsrv := transferTestCache(t)
	keptX := persistedListTestResult(t, bctx, b, bsrv, "applied-x", &transferTestValue{Text: "pending"})
	keptY := persistedListTestResult(t, bctx, b, bsrv, "applied-y", &transferTestValue{Text: "pending"})
	keptZ := persistedListTestResult(t, bctx, b, bsrv, "applied-z", String("z"))
	require.NoError(t, b.AddExplicitDependency(bctx, keptZ, keptY, "test"))
	mergeTestOfferOn(t, bctx, b, keptY, "/old-y", keptX)

	_, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err, "the checked final graph is acyclic")
	require.Equal(t, []string{"/new-x"}, mergeTestOfferPaths(b, keptX.cacheSharedResult()))
	require.Equal(t, []string{"/new-y"}, mergeTestOfferPaths(b, keptY.cacheSharedResult()))
}

// The same through a recipe's best record: the sender's retired X owns Y's
// offer, and its current X' brings an offer that replaces the receiver's X
// offer owned by Y. The final graph is Y -> X, whatever order the rows take.
func TestMergeValuesAppliesTheCheckedOfferGraphOfABestRecord(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	oldX := persistedListTestResult(t, ctx, a, srv, "applied-best-x", &transferTestValue{Text: "old"})
	mergeTestExpire(a, oldX.cacheSharedResult())
	frame := oldX.cacheSharedResult().loadResultCall().clone()
	freshX, err := a.GetOrInitCall(ctx, "fresh-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "fresh"}, frame)
	})
	require.NoError(t, err)
	require.NotEqual(t, oldX.cacheSharedResult().id, freshX.cacheSharedResult().id)
	y := persistedListTestResult(t, ctx, a, srv, "applied-best-y", &transferTestValue{Text: "pending"})
	mergeTestOfferOn(t, ctx, a, y, "/sent-y", oldX)
	mergeTestOfferOn(t, ctx, a, freshX, "/new-x")
	bundle := exportTestBundle(t, ctx, a, y, freshX)
	require.Len(t, bundle.Values, 3)

	bctx, b, bsrv := transferTestCache(t)
	keptX := persistedListTestResult(t, bctx, b, bsrv, "applied-best-x", &transferTestValue{Text: "pending"})
	keptY := persistedListTestResult(t, bctx, b, bsrv, "applied-best-y", &transferTestValue{Text: "pending"})
	mergeTestOfferOn(t, bctx, b, keptX, "/old-x", keptY)

	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err, "the final graph Y -> X is acyclic")
	require.Equal(t, []string{"/new-x"}, mergeTestOfferPaths(b, keptX.cacheSharedResult()))
	b.egraphMu.RLock()
	for _, offer := range keptY.cacheSharedResult().testPartOffers() {
		require.Equal(t, []uint64{uint64(keptX.cacheSharedResult().id)}, offer.owner.record.DependencyIDs)
	}
	b.egraphMu.RUnlock()
	require.Len(t, mergeTestOfferPaths(b, keptY.cacheSharedResult()), 1)
	require.NoError(t, a.ReleaseSession(ctx, "fresh-session"))
}

// mergeTestOfferPaths returns the paths of the Cloud's offers on res.
func mergeTestOfferPaths(c *Cache, res *sharedResult) []string {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var paths []string
	for _, offer := range res.testPartOffers() {
		paths = append(paths, offer.record.Value.Path)
	}
	slices.Sort(paths)
	return paths
}

// Two unexpired records of one recipe, which a sending session that lacked
// the current entry's requirements can leave, both admit an offer for the
// same part of the entry they land on. Only the final one attaches: the first
// is owned by Y, which depends on X in the receiver, and would close X -> Y ->
// X even briefly.
func TestMergeValuesAttachesOnlyTheFinalOfferOfAPart(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	frame := func() *ResultCall {
		return &ResultCall{Kind: ResultCallKindField, Field: "final-offer-x", Type: NewResultCallType((&transferTestValue{}).Type())}
	}
	publish := func(ctx context.Context, session, text string) AnyResult {
		t.Helper()
		res, err := a.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: text}, frame())
		})
		require.NoError(t, err)
		return res
	}
	handle := cacheTestVolatileSessionResourceHandle("final-offer")
	aCtx := currentEntryTestSession(ctx, a, "session-a")
	require.NoError(t, a.BindSessionResource(aCtx, "session-a", "session-a-client", handle, "a"))
	first := publish(aCtx, "session-a", "first")
	leaf, err := cacheTestSessionResourceLeaf(aCtx, handle)
	require.NoError(t, err)
	dep, err := a.AttachResult(aCtx, "session-a", srv, leaf)
	require.NoError(t, err)
	require.NoError(t, a.AddExplicitDependency(aCtx, first, dep, "test"))
	cCtx := currentEntryTestSession(ctx, a, "session-c")
	second := publish(cCtx, "session-c", "second")
	require.NotEqual(t, first.cacheSharedResult().id, second.cacheSharedResult().id, "both unexpired")
	y := persistedListTestResult(t, ctx, a, srv, "final-offer-y", &transferTestValue{Text: "pending"})
	mergeTestOfferOn(t, ctx, a, first, "/first", y)
	mergeTestOfferOn(t, ctx, a, second, "/second")
	bundle := exportTestBundle(t, ctx, a, first, second, y)
	require.Less(t, mergeTestOrdinalOf(t, bundle, first.cacheSharedResult()), mergeTestOrdinalOf(t, bundle, second.cacheSharedResult()))

	bctx, b, bsrv := transferTestCache(t)
	keptX := persistedListTestResult(t, bctx, b, bsrv, "final-offer-x", &transferTestValue{Text: "pending"})
	keptY := persistedListTestResult(t, bctx, b, bsrv, "final-offer-y", &transferTestValue{Text: "pending"})
	require.NoError(t, b.AddExplicitDependency(bctx, keptY, keptX, "test"))
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, []string{"/second"}, mergeTestOfferPaths(b, keptX.cacheSharedResult()), "only the final offer of the part")
	require.NoError(t, a.ReleaseSession(aCtx, "session-a"))
	require.NoError(t, a.ReleaseSession(cCtx, "session-c"))
}

// A record that references two records of one recipe, which land on one
// entry, takes that entry once: it has one dependency, the entry counts one
// unit of ownership from it, and each of its references names one of its
// dependencies. The merge validates records at their provisional numbers,
// where the two are distinct; relocation must keep what that validation
// proved.
func TestMergeValuesRelocationKeepsReferencesWithinDependencies(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, a, srv, "relocated-twice", String("old"))
	mergeTestExpire(a, old.cacheSharedResult())
	frame := old.cacheSharedResult().loadResultCall().clone()
	fresh, err := a.GetOrInitCall(ctx, "new-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String("fresh"), frame)
	})
	require.NoError(t, err)
	root := persistedListTestResult(t, ctx, a, srv, "both-relocated", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{old, fresh}})
	bundle := exportTestBundle(t, ctx, a, root)
	require.Len(t, bundle.Values, 3)
	rootOrdinal := mergeTestOrdinalOf(t, bundle, root.cacheSharedResult())

	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	b.egraphMu.RLock()
	defer b.egraphMu.RUnlock()
	r := b.resultsByID[sharedResultID(mergeTestValueOf(t, reply, rootOrdinal).Number)]
	require.Len(t, r.deps, 1, "both records land on one entry")
	for id := range r.deps {
		require.Equal(t, 1, int(b.resultsByID[id].incomingOwnershipCount), "the root owns the entry once")
	}
	refs := 0
	_, err = VisitEncodedReferences(PersistedRecord{ResultID: uint64(r.id), Envelope: *r.persistedEnvelope, Call: r.loadResultCall()}, func(ref *PersistedRef) error {
		if ref.RecipeID == nil && (ref.Kind == PersistedRefChild || ref.Kind == PersistedRefCall) {
			refs++
			require.Contains(t, r.deps, sharedResultID(ref.ResultID), "reference %s", ref.Path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, refs, "both items reference the entry")
	require.NoError(t, a.ReleaseSession(ctx, "new-session"))
}

// An offer whose owner lists two records of one recipe, which land on one
// entry, is admitted with that entry once in its owner, whether the offer's
// receiver is created by the merge or is the engine's own entry.
func TestMergeValuesOfferOwnerNamingOneEntryTwice(t *testing.T) {
	t.Parallel()
	ctx, a, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, a, srv, "owner-twice", String("old"))
	mergeTestExpire(a, old.cacheSharedResult())
	frame := old.cacheSharedResult().loadResultCall().clone()
	fresh, err := a.GetOrInitCall(ctx, "new-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String("fresh"), frame)
	})
	require.NoError(t, err)
	root := persistedListTestResult(t, ctx, a, srv, "owned-twice", &transferTestValue{Text: "pending"})
	mergeTestOfferOn(t, ctx, a, root, "/", old, fresh)
	bundle := exportTestBundle(t, ctx, a, root)
	require.Len(t, bundle.Values, 3, "the root and the owner's two records")
	rootOrdinal := mergeTestOrdinalOf(t, bundle, root.cacheSharedResult())
	freshOrdinal := mergeTestOrdinalOf(t, bundle, fresh.cacheSharedResult())

	for _, own := range []bool{false, true} {
		t.Run(map[bool]string{false: "a created receiver", true: "the engine's own receiver"}[own], func(t *testing.T) {
			bctx, b, bsrv := transferTestCache(t)
			var mine uint64
			if own {
				mine = uint64(persistedListTestResult(t, bctx, b, bsrv, "owned-twice", &transferTestValue{Text: "pending"}).cacheSharedResult().id)
			}
			reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
			require.NoError(t, err)
			receiver := mergeTestValueOf(t, reply, rootOrdinal).Number
			if own {
				require.Equal(t, mine, receiver, "the record lands on the engine's own entry")
			}
			entry := mergeTestValueOf(t, reply, freshOrdinal).Number
			b.egraphMu.RLock()
			defer b.egraphMu.RUnlock()
			offers := b.resultsByID[sharedResultID(receiver)].testPartOffers()
			require.Len(t, offers, 1)
			for _, offer := range offers {
				require.Equal(t, []uint64{entry}, offer.record.Owner.DependencyIDs, "the entry once")
			}
		})
	}
	require.NoError(t, a.ReleaseSession(ctx, "new-session"))
}

// A record the merge installs names the entry that takes it, whether the
// merge creates that entry, stores the record on an entry with no value,
// replaces an expired value in place or retires an entry in use; the entry
// then exports. A record that lands on an entry the cache has names a number
// other than its provisional one, so its relocation can't be skipped.
func TestMergeValuesInstalledRecordNamesItsEntry(t *testing.T) {
	t.Parallel()
	for _, tc := range []string{"created", "stored on an entry with no value", "replacing an expired value", "retiring an entry in use"} {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			bundle, recipe := mergeTestSource(t, "root", "sent")
			var (
				ctx  context.Context
				into *Cache
				from = cloudCacheID
			)
			switch tc {
			case "stored on an entry with no value":
				ctx, into = storedPartTestCache(t, WithBlobStore())
				from = "cache-a"
				_, err := into.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-x", Number: 9}, RemoteHolding{Recipe: recipe, Field: "root", TypeName: "transferTestValue"})
				require.NoError(t, err)
			default:
				bctx, b, bsrv := transferTestCache(t)
				ctx, into = bctx, b
				if tc != "created" {
					local := persistedListTestResult(t, bctx, b, bsrv, "root", &transferTestValue{Text: "local"})
					if tc == "replacing an expired value" {
						require.NoError(t, b.ReleaseSession(bctx, "test-session"))
					}
					mergeTestExpire(b, local.cacheSharedResult())
				}
			}
			reply, err := into.MergeValues(ctx, from, bundle)
			require.NoError(t, err)
			number := reply.Imported()[0].ResultID
			into.egraphMu.RLock()
			res := into.resultsByID[sharedResultID(number)]
			envelope, frame := *res.persistedEnvelope, res.loadResultCall()
			into.egraphMu.RUnlock()
			require.Equal(t, number, envelope.ResultID, "the record names its entry")
			_, err = normalizeTransferRecord(PersistedRecord{ResultID: number, Envelope: envelope, Call: frame})
			require.NoError(t, err, "the entry exports")
		})
	}
}

// mergeTestReadList reads every item of list, one position at a time.
func mergeTestReadList(t *testing.T, ctx context.Context, list AnyResult) []string {
	t.Helper()
	var got []string
	for nth := 1; nth <= list.Unwrap().(Enumerable).Len(); nth++ {
		item, err := list.NthValue(ctx, nth)
		require.NoError(t, err)
		got = append(got, item.Unwrap().(String).String())
	}
	return got
}

// A merge keeps the cache's own value for a list recipe and moves the
// bundle's references to it, so an item read from the bundle's list value
// comes to name the cache's list. Reads of that list must still return its
// own items, and the imported item still loads as the value it was.
func TestMergeValuesKeptListReadsItsOwnItems(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	source := persistedListTestResult(t, actx, a, asrv, "merge-kept-list", NewStringArray("a", "b", "c"))
	sourceItem, err := source.NthValue(actx, 1)
	require.NoError(t, err)
	bundle := exportTestBundle(t, actx, a, sourceItem)

	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "merge-kept-list", NewStringArray("c", "a", "b"))
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	imported, err := b.LoadResultByResultID(bctx, "test-session", bsrv, reply.Imported()[0].ResultID)
	require.NoError(t, err)
	frame, err := imported.ResultCall()
	require.NoError(t, err)
	require.Equal(t, uint64(local.cacheSharedResult().id), frame.Receiver.ResultID)
	require.Equal(t, "a", imported.Unwrap().(String).String())

	require.Equal(t, []string{"c", "a", "b"}, mergeTestReadList(t, bctx, local))
}

// A bundle can carry a list and an item read from another value of the list's
// recipe: the cache that exported it had merged the item beside its own
// list, as above. A cache that takes both reads the list's own items.
func TestMergeValuesTransferredItemOfAnotherValueIsNotRead(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	source := persistedListTestResult(t, actx, a, asrv, "merge-mixed-list", NewStringArray("a", "b", "c"))
	sourceItem, err := source.NthValue(actx, 1)
	require.NoError(t, err)

	bctx, b, bsrv := transferTestCache(t)
	persistedListTestResult(t, bctx, b, bsrv, "merge-mixed-list", NewStringArray("c", "a", "b"))
	reply, err := b.MergeValues(bctx, cloudCacheID, exportTestBundle(t, actx, a, sourceItem))
	require.NoError(t, err)
	mixedItem, err := b.LoadResultByResultID(bctx, "test-session", bsrv, reply.Imported()[0].ResultID)
	require.NoError(t, err)
	mixed := exportTestBundle(t, bctx, b, mixedItem)

	cctx, c, csrv := transferTestCache(t)
	reply, err = c.MergeValues(cctx, cloudCacheID, mixed)
	require.NoError(t, err)
	item, err := c.LoadResultByResultID(cctx, "test-session", csrv, reply.Imported()[0].ResultID)
	require.NoError(t, err)
	frame, err := item.ResultCall()
	require.NoError(t, err)
	list, err := c.LoadResultByResultID(cctx, "test-session", csrv, frame.Receiver.ResultID)
	require.NoError(t, err)
	require.Equal(t, "a", item.Unwrap().(String).String())

	require.Equal(t, []string{"c", "a", "b"}, mergeTestReadList(t, cctx, list))
}

// A recorded item can be retained past its reading session by a field that
// returns it, and gain that field's expiry. Once it has expired and nothing
// uses it, a merge replaces its value in place with an item of another value
// of the list's recipe. The list it was read from must not read that value.
func TestMergeValuesReplacedItemLeavesItsList(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	source := persistedListTestResult(t, actx, a, asrv, "merge-replaced-item", NewStringArray("a", "b", "c"))
	sourceItem, err := source.NthValue(actx, 1)
	require.NoError(t, err)
	bundle := exportTestBundle(t, actx, a, sourceItem)

	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "merge-replaced-item", NewStringArray("c", "a", "b"))
	item, err := local.NthValue(bctx, 1)
	require.NoError(t, err)
	retain := NodeFunc("mergeReplacedItem", func(context.Context, ObjectResult[*persistCodecRoot], struct{}) (Result[String], error) {
		return Result[String]{shared: item.cacheSharedResult()}, nil
	})
	retain.Spec.TTL = 3600
	retain.Spec.IsPersistable = true
	Fields[*persistCodecRoot]{retain}.Install(bsrv)
	retained, err := bsrv.Root().Select(bctx, bsrv, Selector{Field: "mergeReplacedItem"})
	require.NoError(t, err)
	row := item.cacheSharedResult()
	require.Same(t, row, retained.cacheSharedResult())

	reader := srvToContext(currentEntryTestSession(bctx, b, "reader"), bsrv)
	local, err = b.LoadResultByResultID(reader, "reader", bsrv, uint64(local.cacheSharedResult().id))
	require.NoError(t, err)
	require.NoError(t, b.ReleaseSession(bctx, "test-session"))
	mergeTestExpire(b, row)
	reply, err := b.MergeValues(reader, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(row.id), reply.Imported()[0].ResultID)
	b.egraphMu.RLock()
	replacements := row.replacements
	b.egraphMu.RUnlock()
	require.Equal(t, uint64(1), replacements)

	require.Equal(t, []string{"c", "a", "b"}, mergeTestReadList(t, reader, local))
}

// The Cloud keeps its own value for a list recipe when an engine's export of
// an item of another value arrives, so the item comes to name the Cloud's
// list. An engine that takes both from the Cloud reads the list's own items.
func TestMergeValuesItemThroughTheCloudIsNotRead(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	source := persistedListTestResult(t, actx, a, asrv, "merge-cloud-item", NewStringArray("a", "b", "c"))
	sourceItem, err := source.NthValue(actx, 1)
	require.NoError(t, err)
	bctx, b, bsrv := transferTestCache(t)
	local := persistedListTestResult(t, bctx, b, bsrv, "merge-cloud-item", NewStringArray("c", "a", "b"))

	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	_, err = cloud.MergeValues(cloudCtx, "cache-b", exportTestBundle(t, bctx, b, local))
	require.NoError(t, err)
	reply, err := cloud.MergeValues(cloudCtx, "cache-a", exportTestBundle(t, actx, a, sourceItem))
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	cloudItem := cloud.resultsByID[sharedResultID(reply.Imported()[0].ResultID)]
	cloud.egraphMu.RUnlock()

	cctx, c, csrv := transferTestCache(t)
	reply, err = c.MergeValues(cctx, cloudCacheID, exportTestBundle(t, cloudCtx, cloud, Result[Typed]{shared: cloudItem}))
	require.NoError(t, err)
	item, err := c.LoadResultByResultID(cctx, "test-session", csrv, reply.Imported()[0].ResultID)
	require.NoError(t, err)
	frame, err := item.ResultCall()
	require.NoError(t, err)
	list, err := c.LoadResultByResultID(cctx, "test-session", csrv, frame.Receiver.ResultID)
	require.NoError(t, err)
	require.Equal(t, "a", item.Unwrap().(String).String())

	require.Equal(t, []string{"c", "a", "b"}, mergeTestReadList(t, cctx, list))
}
