package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// firstUseTestSet is a bundle the Cloud sends a fresh engine, decoded as its
// encoding carries it, and the Cloud's numbers of its three records.
type firstUseTestSet struct {
	bundle         ValueBundle
	good, bad, dep uint64
}

// firstUseTestCloud has an engine compute three results, a record "fu-bad"
// whose payload names its dependency "fu-dep", and "fu-good", which proves a
// part complete, and export the two roots to a Cloud. It returns the set the
// Cloud sends a fresh engine for them.
func firstUseTestCloud(t *testing.T) firstUseTestSet {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	depRes := persistedListTestResult(t, actx, a, asrv, "fu-dep", &transferTestValue{Text: "dep"})
	goodRes := persistedListTestResult(t, actx, a, asrv, "fu-good", &transferTestValue{Text: "ready"})
	badRes := persistedListTestResult(t, actx, a, asrv, "fu-bad", &transferTestValue{Text: "bad", Child: uint64(depRes.cacheSharedResult().id)})
	transferTestDependency(a, actx, badRes, depRes)
	exported := exportTestBundle(t, actx, a, goodRes, badRes)

	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", exported)
	require.NoError(t, err)
	numbers := map[uint64]uint64{}
	for _, value := range reply.Values {
		for _, sent := range exported.Values {
			if sent.Ordinal == value.Ordinal {
				numbers[sent.SenderNumber] = value.Number
			}
		}
	}
	set := firstUseTestSet{good: numbers[uint64(goodRes.cacheSharedResult().id)], bad: numbers[uint64(badRes.cacheSharedResult().id)], dep: numbers[uint64(depRes.cacheSharedResult().id)]}
	require.NotZero(t, set.good)
	require.NotZero(t, set.bad)
	require.NotZero(t, set.dep)
	got, err := cloud.StoredBundle(ctx, []uint64{set.good, set.bad}, time.Now().Unix(), nil)
	require.NoError(t, err)
	set.bundle, err = cloneValueBundle(got.Bundle)
	require.NoError(t, err)
	return set
}

// spoil has the bad record's payload name a row it doesn't depend on, the
// good record, and returns the set.
func (set firstUseTestSet) spoil(t *testing.T) firstUseTestSet {
	t.Helper()
	bad := firstUseTestValue(t, set.bundle, set.bad)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(bad.Record.Envelope.ObjectJSON, &payload))
	require.EqualValues(t, set.dep, payload["child"])
	payload["child"] = set.good
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	bad.Record.Envelope.ObjectJSON = raw
	return set
}

// spoilJSON leaves the bad record's payload no longer JSON, and returns the
// set.
func (set firstUseTestSet) spoilJSON(t *testing.T) firstUseTestSet {
	t.Helper()
	bad := firstUseTestValue(t, set.bundle, set.bad)
	payload := bad.Record.Envelope.ObjectJSON
	require.True(t, json.Valid(payload))
	bad.Record.Envelope.ObjectJSON = payload[:len(payload)-1]
	return set
}

// firstUseTestValue returns the bundle's value of the Cloud's entry number.
func firstUseTestValue(t *testing.T, bundle ValueBundle, number uint64) *TransferredValue {
	t.Helper()
	for i := range bundle.Values {
		if bundle.Values[i].SenderNumber == number {
			return &bundle.Values[i]
		}
	}
	t.Fatalf("the bundle carries no value of entry %d", number)
	return nil
}

// firstUseTestLookup looks the field up on the engine and reports whether it
// hit, and the text of the value it got.
func firstUseTestLookup(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) (bool, string) {
	t.Helper()
	_, value, hit := firstUseTestCall(t, ctx, c, srv, field)
	return hit, value.Text
}

// firstUseTestCall looks the field up on the engine, running its call on a
// miss, and returns the result, its value and whether it hit.
func firstUseTestCall(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) (AnyResult, *transferTestValue, bool) {
	t.Helper()
	return firstUseTestCallIn(t, ctx, c, srv, "test-session", field)
}

// firstUseTestCallIn is firstUseTestCall in the session.
func firstUseTestCallIn(t *testing.T, ctx context.Context, c *Cache, srv *Server, session, field string) (AnyResult, *transferTestValue, bool) {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&transferTestValue{}).Type())}
	ran := false
	res, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		ran = true
		return NewResultForCall(&transferTestValue{Text: "ran " + field}, frame)
	})
	require.NoError(t, err)
	value, ok := res.Unwrap().(*transferTestValue)
	require.True(t, ok, "the lookup returns the field's value")
	return res, value, !ran
}

// firstUseTestReplyParts returns the complete parts the merge reply reports
// for the bundle's row of the Cloud's entry number.
func firstUseTestReplyParts(t *testing.T, bundle ValueBundle, reply MergeReply, number uint64) []PersistedPartAddress {
	t.Helper()
	ordinal := firstUseTestValue(t, bundle, number).Ordinal
	for _, value := range reply.Values {
		if value.Ordinal == ordinal {
			return value.Parts
		}
	}
	t.Fatalf("the reply has no value of row %d", ordinal)
	return nil
}

// firstUseTestEntry returns the engine's entry of the merge reply's value of
// the bundle's row for the Cloud's entry number.
func firstUseTestEntry(t *testing.T, bundle ValueBundle, reply MergeReply, number uint64) uint64 {
	t.Helper()
	ordinal := firstUseTestValue(t, bundle, number).Ordinal
	for _, value := range reply.Values {
		if value.Ordinal == ordinal {
			return value.Number
		}
	}
	t.Fatalf("the reply has no value of row %d", ordinal)
	return 0
}

// A set from the Cloud lands whole though one record's payload is bad: its
// records are installed from their calls and offers, their payloads left
// for their first use. Every other record hits with its value; the bad one,
// refused at its first use, misses, so its call runs and its caller gets the
// call's result; and its entry is then unknown to a hold, so an export that
// names it answers that the engine no longer has it.
func TestMergedSetWithOneBadPayloadMissesOnlyThatRecord(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	require.NotNil(t, firstUseTestValue(t, set.bundle, set.bad).Parts, "the Cloud's value carries its parts")
	set = set.spoil(t)

	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err, "the set lands")
	require.True(t, reply.Committed)
	badEntry := firstUseTestEntry(t, set.bundle, reply, set.bad)

	hit, text := firstUseTestLookup(t, ctx, engine, srv, "fu-good")
	require.True(t, hit, "a good record hits")
	require.Equal(t, "ready", text)
	hit, text = firstUseTestLookup(t, ctx, engine, srv, "fu-dep")
	require.True(t, hit, "the bad record's dependency hits")
	require.Equal(t, "dep", text)

	_, release, err := engine.HoldEntry(ctx, badEntry)
	require.NoError(t, err, "before its first use, the bad record's entry is held as any")
	require.NoError(t, release(ctx))
	hit, text = firstUseTestLookup(t, ctx, engine, srv, "fu-bad")
	require.False(t, hit, "the bad record misses at its first use")
	require.Equal(t, "ran fu-bad", text, "its caller gets the call's result")
	_, _, err = engine.HoldEntry(ctx, badEntry)
	require.ErrorIs(t, err, ErrUnknownEntry, "the bad record's entry is gone for an export that names it")
}

// A record from the Cloud whose payload isn't JSON is a bad payload like any
// other: its set lands, and only that record, refused at its first use,
// misses.
func TestMergedSetWithOnePayloadNotJSONMissesOnlyThatRecord(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t).spoilJSON(t)

	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err, "the set lands")
	require.True(t, reply.Committed)

	hit, text := firstUseTestLookup(t, ctx, engine, srv, "fu-good")
	require.True(t, hit, "a good record hits")
	require.Equal(t, "ready", text)
	hit, text = firstUseTestLookup(t, ctx, engine, srv, "fu-bad")
	require.False(t, hit, "the record whose payload isn't JSON misses at its first use")
	require.Equal(t, "ran fu-bad", text, "its caller gets the call's result")
	require.Equal(t, int64(1), engine.FirstUse().Refused)
}

// A set from the Cloud, whose records carry their parts, lands as the same
// set does when its records are prepared as they land: the reply is the
// same, value by value, and each root hits with the same value.
func TestMergedSetRepliesAsPreparedSetDoes(t *testing.T) {
	t.Parallel()
	bundle := firstUseTestCloud(t).bundle
	prepared := bundle
	prepared.Values = append([]TransferredValue(nil), bundle.Values...)
	for i := range prepared.Values {
		prepared.Values[i].Parts = nil
	}
	merge := func(bundle ValueBundle) (MergeReply, map[string]string) {
		ctx, engine, srv := transferTestCache(t)
		reply, err := engine.MergeValues(ctx, cloudCacheID, bundle)
		require.NoError(t, err)
		texts := map[string]string{}
		for _, field := range []string{"fu-good", "fu-bad", "fu-dep"} {
			hit, text := firstUseTestLookup(t, ctx, engine, srv, field)
			require.True(t, hit, "%s hits", field)
			texts[field] = text
		}
		return reply, texts
	}
	wantReply, wantTexts := merge(prepared)
	gotReply, gotTexts := merge(bundle)
	require.Equal(t, wantTexts, gotTexts)
	require.Len(t, gotReply.Values, len(wantReply.Values))
	complete := 0
	for i := range wantReply.Values {
		want, got := wantReply.Values[i], gotReply.Values[i]
		require.Equal(t, want.Ordinal, got.Ordinal)
		require.Equal(t, want.Parts, got.Parts, "row %d's complete parts", want.Ordinal)
		require.Equal(t, want.OfferedParts, got.OfferedParts, "row %d's offered parts", want.Ordinal)
		require.Equal(t, want.Deps, got.Deps, "row %d's dependencies", want.Ordinal)
		require.Equal(t, want.Stored, got.Stored, "row %d stored", want.Ordinal)
		complete += len(got.Parts)
	}
	require.NotZero(t, complete, "the reply reports a complete part")
}

// firstUseTestPrepareAll prepares the payload of every record c holds merged
// unprepared, as each one's first use would.
func firstUseTestPrepareAll(t *testing.T, ctx context.Context, c *Cache) {
	t.Helper()
	c.egraphMu.RLock()
	entries := make([]*sharedResult, 0, len(c.resultsByID))
	for _, res := range c.resultsByID {
		entries = append(entries, res)
	}
	c.egraphMu.RUnlock()
	for _, res := range entries {
		require.NoError(t, c.prepareMergedPayload(ctx, res, workLookup))
		require.False(t, res.holdsUnpreparedPayload())
	}
}

// A cache counts the payloads its calls prepared at their first use and
// those refused, with the time preparing took in all and at the longest:
// the work a restore defers onto the calls that use its records.
func TestFirstUseCountsWhatCallsPrepared(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t).spoil(t)

	ctx, engine, srv := transferTestCache(t)
	_, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	merged := engine.FirstUse()
	require.Equal(t, FirstUseStats{Routes: merged.Routes}, merged, "a merge prepares no payload")
	require.Equal(t, int64(3), merged.Routes["unnamed"].Merged, "it only merges the records")
	for _, field := range []string{"fu-good", "fu-dep", "fu-bad", "fu-good"} {
		firstUseTestLookup(t, ctx, engine, srv, field)
	}
	stats := engine.FirstUse()
	require.Equal(t, int64(2), stats.Prepared, "the two good records, each once")
	require.Equal(t, int64(1), stats.Refused, "and the bad one")
	require.Positive(t, stats.Longest)
	require.GreaterOrEqual(t, stats.Total, stats.Longest)
}

// A record from the Cloud that replaces an expired entry in place is
// prepared at its first use as any merged record is: its payload's
// reference to its dependency names the engine's entry, not the Cloud's.
func TestMergedRecordReplacingAnEntryInPlaceIsPreparedAtItsFirstUse(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, srv := transferTestCache(t)
	// The engine's numbers run apart from the Cloud's.
	for i := range 4 {
		persistedListTestResult(t, ctx, engine, srv, fmt.Sprintf("fu-pad-%d", i), &transferTestValue{Text: "pad"})
	}
	local := persistedListTestResult(t, ctx, engine, srv, "fu-bad", &transferTestValue{Text: "local"})
	require.NoError(t, engine.ReleaseSession(ctx, "test-session"))
	mergeTestExpire(engine, local.cacheSharedResult())

	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(local.cacheSharedResult().id), firstUseTestEntry(t, set.bundle, reply, set.bad), "the record replaces the expired entry in place")
	depEntry := firstUseTestEntry(t, set.bundle, reply, set.dep)
	require.NotEqual(t, set.dep, depEntry, "the engine numbers the dependency apart from the Cloud")

	_, value, hit := firstUseTestCallIn(t, ctx, engine, srv, "later-session", "fu-bad")
	require.True(t, hit, "the record hits")
	require.Equal(t, "bad", value.Text)
	require.Equal(t, depEntry, value.Child, "its reference names the engine's entry of its dependency")
}

// A set from the Cloud, whose records all carry their parts, prepares none
// of them with its payload, so its merge decides its rows once, in its
// commit: deciding them before, to relocate prepared payloads early, would
// serve no record.
func TestMergedSetDecidesItsRowsOnlyInItsCommit(t *testing.T) {
	t.Parallel()
	bundle := firstUseTestCloud(t).bundle
	ctx, c, _ := transferTestCache(t)
	prerelocated := false
	c.testPrerelocated = func(map[uint64]map[uint64]sharedResultID) { prerelocated = true }
	p, err := c.PrepareValueMerge(ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Nil(t, p.prep.early, "the rows aren't decided as the records are prepared")
	require.False(t, prerelocated, "nor again before the commit")
	reply, err := c.CommitValueMerge(ctx, p)
	require.NoError(t, err)
	require.True(t, reply.Committed)
}

// A value computed again over an expired record from the Cloud, before its
// first use, replaces it in place, and leaves nothing of the record to
// prepare: an export of the entry captures the computed value.
func TestComputedValueReplacingAMergedRecordLeavesNothingToPrepare(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	goodEntry := firstUseTestEntry(t, set.bundle, reply, set.good)
	engine.egraphMu.RLock()
	good := engine.resultsByID[sharedResultID(goodEntry)]
	engine.egraphMu.RUnlock()
	require.True(t, good.holdsUnpreparedPayload())
	mergeTestExpire(engine, good)

	res, value, hit := firstUseTestCall(t, ctx, engine, srv, "fu-good")
	require.False(t, hit, "the expired record misses")
	require.Equal(t, "ran fu-good", value.Text)
	require.Same(t, good, res.cacheSharedResult(), "the computed value replaces the record in place")
	require.False(t, good.holdsUnpreparedPayload(), "nothing of the record is left to prepare")

	exported := exportTestBundle(t, ctx, engine, res)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(firstUseTestValue(t, exported, goodEntry).Record.Envelope.ObjectJSON, &payload))
	require.Equal(t, "ran fu-good", payload["text"], "an export captures the computed value")
}

// A record from the Cloud that a capture prepares while a later merge of it
// is between its warming and its commit is replied with the complete parts
// its bundle carried, as when the merge prepared it: preparing a record
// keeps what it proves complete.
func TestMergedRecordPreparedBeforeAMergeCommitsKeepsItsCompleteParts(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, _ := transferTestCache(t)
	first, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	want := firstUseTestReplyParts(t, set.bundle, first, set.good)
	require.NotEmpty(t, want, "the good record proves a part complete")
	engine.egraphMu.RLock()
	good := engine.resultsByID[sharedResultID(firstUseTestEntry(t, set.bundle, first, set.good))]
	engine.egraphMu.RUnlock()

	captured := false
	engine.testBeforeTransferCommit = func() {
		if captured {
			return
		}
		captured = true
		_, err := engine.captureRowRecord(ctx, good)
		require.NoError(t, err)
		require.False(t, good.holdsUnpreparedPayload(), "the capture prepared the record")
	}
	second, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	require.True(t, captured)
	require.Equal(t, want, firstUseTestReplyParts(t, set.bundle, second, set.good))
}

// An engine that closes with records from the Cloud nothing used saves them
// prepared, and leaves out the one whose payload is refused: after a
// restart, a good record hits with its value, and the refused one misses and
// runs its call. The save counts what it prepared and refused.
func TestSaveLeavesOutARecordRefusedAtItsPreparation(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t).spoil(t)
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, engine, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	_, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	require.NoError(t, engine.Close(ctx), "the save stands")
	stats := engine.FirstUse()
	require.Equal(t, int64(2), stats.SavePrepared, "the save prepared the two good records")
	require.Equal(t, int64(1), stats.SaveRefused, "and refused the bad one")
	require.Positive(t, stats.SaveWall)
	require.Zero(t, stats.Prepared+stats.Refused, "no call used a record")

	ctx, restarted, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	require.Equal(t, CachePersistenceResetNone, restarted.PersistenceResetReason())
	hit, text := firstUseTestLookup(t, ctx, restarted, srv, "fu-good")
	require.True(t, hit, "a good record hits after the restart")
	require.Equal(t, "ready", text)
	hit, text = firstUseTestLookup(t, ctx, restarted, srv, "fu-bad")
	require.False(t, hit, "the refused record misses after the restart")
	require.Equal(t, "ran fu-bad", text)
}

// A cache names its longest first-use preparation: the record's recipe, its
// payload's size, and its time split into preparing the payload and waiting
// for the entry's payload lock, here held by a reader for a while, to
// install it.
func TestFirstUseNamesItsLongestPreparation(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	engine.egraphMu.RLock()
	good := engine.resultsByID[sharedResultID(firstUseTestEntry(t, set.bundle, reply, set.good))]
	engine.egraphMu.RUnlock()
	recipe, err := good.loadResultCall().deriveRecipeDigest(engine)
	require.NoError(t, err)
	good.payloadMu.RLock()
	bytes := persistedEnvelopePayloadBytes(good.persistedEnvelope)
	good.payloadMu.RUnlock()
	require.Positive(t, bytes)

	// A reader takes the entry's payload lock just as the preparation is
	// about to install, and holds it a while.
	const hold = 100 * time.Millisecond
	released := make(chan struct{})
	engine.testFirstUseInstall = func(res *sharedResult) {
		if res != good {
			return
		}
		res.payloadMu.RLock()
		go func() {
			time.Sleep(hold)
			res.payloadMu.RUnlock()
			close(released)
		}()
	}
	hit, _ := firstUseTestLookup(t, ctx, engine, srv, "fu-good")
	<-released
	require.True(t, hit, "the lookup hits")

	stats := engine.FirstUse()
	require.Equal(t, int64(1), stats.Prepared)
	require.Equal(t, recipe.String(), stats.LongestRecipe, "the longest preparation names its record's recipe")
	require.Equal(t, bytes, stats.LongestBytes, "and its payload's size")
	require.GreaterOrEqual(t, stats.LongestLockWait, hold/2, "the wait for the payload lock is told apart")
	require.Equal(t, stats.Longest, stats.LongestPrepare+stats.LongestLockWait)
}

// Sharing queues a pass for a class only when one member could receive and
// another could donate: a record merged from the Cloud, alone in its output
// class or beside another record merged and untouched, has no donor, so no
// pass is queued, which would only probe it and so prepare its payload. Once
// a value computed on the engine joins the class, the class is queued.
func TestSharingQueuesAClassOnlyWhenItCouldHaveADonor(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)
	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	local := persistedListTestResult(t, ctx, engine, srv, "fu-local", &transferTestValue{Text: "computed here"})
	require.NoError(t, engine.EnableSnapshotSharing())

	engine.egraphMu.Lock()
	good := engine.resultsByID[sharedResultID(firstUseTestEntry(t, set.bundle, reply, set.good))]
	other := engine.resultsByID[sharedResultID(firstUseTestEntry(t, set.bundle, reply, set.dep))]
	classes := engine.outputEqClassesForResultLocked(good.id)
	alone := 0
	for class := range classes {
		if len(engine.snapshotShareMembersLocked(engine.findEqClassLocked(class))) == 1 {
			alone++
		}
	}
	engine.queueSnapshotShareRowLocked(ctx, good)
	queuedAlone := len(engine.sharePending)
	for class := range classes {
		engine.addResultOutputEqClassLocked(other.id, class)
	}
	engine.queueSnapshotShareRowLocked(ctx, good)
	queuedUntouched := len(engine.sharePending)
	// Read before a donor joins: once the class is queued and the lock let
	// go, the sharing worker may prepare the record.
	unprepared := good.holdsUnpreparedPayload()
	for class := range classes {
		engine.addResultOutputEqClassLocked(local.cacheSharedResult().id, class)
	}
	engine.queueSnapshotShareRowLocked(ctx, good)
	queuedWithDonor := len(engine.sharePending)
	engine.egraphMu.Unlock()

	require.NotZero(t, alone, "the merged record is alone in an output class")
	require.Zero(t, queuedAlone, "a class of one merged record queues no pass")
	require.Zero(t, queuedUntouched, "nor does a class of merged records none of which holds a part here")
	require.Equal(t, len(classes), queuedWithDonor, "a class with a value computed here is queued")
	require.True(t, unprepared, "until then nothing prepared the merged record")
}
