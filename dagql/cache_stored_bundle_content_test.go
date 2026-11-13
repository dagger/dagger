package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// storedContentTestWait waits until the Cloud entry's frame carries the
// content digest a span taught it.
func storedContentTestWait(t *testing.T, cloud *Cache, number uint64, content digest.Digest) {
	t.Helper()
	require.Eventually(t, func() bool {
		cloud.egraphMu.RLock()
		var frame entryFrame
		if res := cloud.resultsByID[sharedResultID(number)]; res != nil {
			frame = res.entryFrameLocked()
		}
		cloud.egraphMu.RUnlock()
		decoded, err := frame.decode()
		return err == nil && decoded != nil && decoded.ContentDigest() == content
	}, 5*time.Second, 5*time.Millisecond, "the Cloud gives the entry's frame its taught content digest")
}

// storedContentTestHold holds the Cloud's body worker once it has given the
// frames of its first batch their taught content digests, until release.
// blocked is closed once the worker is held, and batches counts the batches
// the worker has given frames.
func storedContentTestHold(t *testing.T, cloud *Cache) (blocked chan struct{}, release func(), batches *atomic.Int64) {
	t.Helper()
	blocked, released := make(chan struct{}), make(chan struct{})
	var held, freed sync.Once
	batches = &atomic.Int64{}
	release = func() { freed.Do(func() { close(released) }) }
	t.Cleanup(release)
	cloud.testTaughtContent = func(int) {
		held.Do(func() {
			close(blocked)
			<-released
		})
		batches.Add(1)
	}
	return blocked, release, batches
}

// storedContentTestTeach has the Cloud learn, from engine cache-a's span,
// the content digest of a's entry number.
func storedContentTestTeach(t *testing.T, ctx context.Context, cloud *Cache, number uint64, content digest.Digest) {
	t.Helper()
	_, _, err := cloud.UpdateRemoteHolding(ctx, HolderKey{Cache: "cache-a", Number: number}, RemoteHoldingUpdate{ContentDigest: content})
	require.NoError(t, err)
}

// storedContentTestSend has engine A compute R and export it to a Cloud, and
// the Cloud learn R's content digests, in order, before the export (A teaches
// them, so R's frame carries them) or after (from A's later spans, as the
// service reads a lazy span or a call span that reports one; all but the
// first while the Cloud's body worker is busy). It returns the record the
// Cloud sends a fresh engine for R, as JSON, and whether that engine, whose
// own result S of another recipe has R's last content, holds R and S in one
// class.
func storedContentTestSend(t *testing.T, after bool, contents ...digest.Digest) (string, bool) {
	t.Helper()
	content := contents[len(contents)-1]
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "content-root", &transferTestValue{Text: "x"})
	if !after {
		for _, content := range contents {
			require.NoError(t, a.TeachContentDigest(actx, r, content))
		}
	}
	a.egraphMu.RLock()
	recipe := r.cacheSharedResult().recipeKeys[0]
	aNumber := uint64(r.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	blocked, release, batches := storedContentTestHold(t, cloud)
	reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, actx, a, r))
	require.NoError(t, err)
	imported := reply.Imported()
	require.Len(t, imported, 1)
	cloudNumber := imported[0].ResultID
	if after {
		storedContentTestTeach(t, ctx, cloud, aNumber, contents[0])
		<-blocked
		for _, content := range contents[1:] {
			storedContentTestTeach(t, ctx, cloud, aNumber, content)
		}
		release()
		if len(contents) > 1 {
			// The digests taught while the worker was held are its next batch.
			require.Eventually(t, func() bool { return batches.Load() >= 2 }, 5*time.Second, 5*time.Millisecond, "the worker gives the frame the digests taught while it was held")
		}
		storedContentTestWait(t, cloud, cloudNumber, content)
	} else {
		require.Zero(t, cloud.TaughtContentStats().Queued, "a record whose frame carries its content digest is never queued for a rewrite")
	}
	require.Contains(t, cloud.EquivalentEntries(content.String()), cloudNumber, "the Cloud's class has R's content digest")

	got, err := cloud.StoredBundle(ctx, []uint64{cloudNumber}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Values, 1)

	bctx, b, bsrv := transferTestCache(t)
	_, err = b.MergeValues(bctx, cloudCacheID, got.Bundle)
	require.NoError(t, err)
	installed := b.EquivalentEntries(recipe.String())
	require.Len(t, installed, 1, "B holds the Cloud's R")
	b.egraphMu.RLock()
	received := b.resultsByID[sharedResultID(installed[0])].loadResultCall()
	b.egraphMu.RUnlock()
	sent, err := json.Marshal(received)
	require.NoError(t, err)
	s := persistedListTestResult(t, bctx, b, bsrv, "content-other", &transferTestValue{Text: "x"})
	require.NoError(t, b.TeachContentDigest(bctx, s, content))
	b.egraphMu.RLock()
	sRecipe := s.cacheSharedResult().recipeKeys[0]
	b.egraphMu.RUnlock()
	rClass := b.EquivalentEntries(recipe.String())
	require.NotEmpty(t, rClass, "B holds the Cloud's R")
	return string(sent), slices.Equal(b.EquivalentEntries(sRecipe.String()), rClass)
}

// A content digest the Cloud learns for an entry after its record arrived,
// from a later span of the engine that computed it, goes on the entry's frame
// as TeachContentDigest put it on that engine's: the record the Cloud sends
// is the record it sends when the engine had taught the digest before its
// export, and a fresh engine whose own result has that content digest holds
// the Cloud's record in its class, as one engine that computed both does.
func TestStoredBundleCarriesAContentDigestLearnedAfterTheRecord(t *testing.T) {
	t.Parallel()
	content := testDigest("content-of-x")
	before, beforeJoined := storedContentTestSend(t, false, content)
	after, afterJoined := storedContentTestSend(t, true, content)
	require.True(t, beforeJoined, "taught before the export, R and S are one class on B")
	require.True(t, afterJoined, "learned after the export, R and S are one class on B, as on the Cloud")
	require.JSONEq(t, before, after, "the fresh engine gets the frame it gets when R's frame carried the digest at the export")
}

// Content digests the Cloud learns for an entry one after another, the later
// ones while its body worker is busy, go on the entry's frame in that order,
// as a run of TeachContentDigest calls puts them on an engine's: a digest
// replaced by a later one is still on the frame the Cloud sends, as
// replaced-content.
func TestStoredBundleCarriesContentDigestsLearnedInTurn(t *testing.T) {
	t.Parallel()
	first, second, third := testDigest("content-first"), testDigest("content-second"), testDigest("content-third")
	for name, contents := range map[string][]digest.Digest{
		"three":       {first, second, third},
		"back to one": {first, second, first},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			before, beforeJoined := storedContentTestSend(t, false, contents...)
			after, afterJoined := storedContentTestSend(t, true, contents...)
			require.True(t, beforeJoined)
			require.True(t, afterJoined)
			require.JSONEq(t, before, after, "the fresh engine gets the frame the engine's own run of teaches left")
		})
	}
}

// An entry whose record is replaced, once expired, by a new record from an
// engine keeps the content digest a span taught it: the new record's frame
// takes it too.
func TestStoredBundleKeepsATaughtContentDigestAcrossAReplacement(t *testing.T) {
	t.Parallel()
	content := testDigest("content-of-replaced")
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "replaced-root", &transferTestValue{Text: "x"})
	a.egraphMu.RLock()
	aNumber := uint64(r.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	bundle := exportTestBundle(t, actx, a, r)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number := storedPersistenceTestMerge(t, ctx, cloud, bundle)[0]
	storedContentTestTeach(t, ctx, cloud, aNumber, content)
	storedContentTestWait(t, cloud, number, content)

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, number))
	second, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, number, second.Imported()[0].ResultID)
	require.Equal(t, uint64(1), second.Values[0].Replacements, "the expired record was replaced")
	storedContentTestWait(t, cloud, number, content)
}

// An entry whose record is replaced while the body worker gives its old
// frame a taught content digest keeps the digest: the new record's frame
// takes it too.
func TestStoredBundleKeepsATaughtContentDigestWhenTheRecordIsReplacedAsItIsGiven(t *testing.T) {
	t.Parallel()
	content := testDigest("content-of-raced")
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "raced-root", &transferTestValue{Text: "x"})
	a.egraphMu.RLock()
	aNumber := uint64(r.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	bundle := exportTestBundle(t, actx, a, r)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	given, proceed := make(chan struct{}), make(chan struct{})
	var once, freed sync.Once
	release := func() { freed.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	cloud.testTaughtContentGiven = func() {
		once.Do(func() {
			close(given)
			<-proceed
		})
	}
	number := storedPersistenceTestMerge(t, ctx, cloud, bundle)[0]
	storedContentTestTeach(t, ctx, cloud, aNumber, content)
	<-given

	// The worker has given the old frame the digest and not yet recorded
	// it: the expired record is replaced now.
	mergeTestExpire(cloud, storedBundleTestEntry(cloud, number))
	second, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(1), second.Values[0].Replacements, "the expired record was replaced")
	release()
	storedContentTestWait(t, cloud, number, content)
}

// storedContentTestRoot has engine cache-a compute a root with no content
// digest and returns its export and its number on a.
func storedContentTestRoot(t *testing.T, field string) (ValueBundle, uint64) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, field, &transferTestValue{Text: "x"})
	a.egraphMu.RLock()
	aNumber := uint64(r.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	return exportTestBundle(t, actx, a, r), aNumber
}

// storedContentTestFrameDigests returns every digest the frame of the Cloud's
// entry carries: its content digest and the ones it replaced.
func storedContentTestFrameDigests(t *testing.T, cloud *Cache, number uint64) []digest.Digest {
	t.Helper()
	cloud.egraphMu.RLock()
	frame := cloud.resultsByID[sharedResultID(number)].entryFrameLocked()
	cloud.egraphMu.RUnlock()
	decoded, err := frame.decode()
	require.NoError(t, err)
	require.NotNil(t, decoded)
	var digests []digest.Digest
	for _, extra := range decoded.ExtraDigests {
		digests = append(digests, extra.Digest)
	}
	return digests
}

// An entry a prune left with no value, whose holding keeps it, takes a later
// export's record without the content digests spans taught it before the
// prune. They named the bytes of the run it held, and the record may be
// another engine's run of the recipe, with other bytes: the Cloud would send
// that run's bytes labelled with the first run's content, and a fresh engine
// would take them for anything of that content. Until runs are named, the
// Cloud can't tell another run from the same one, so a prune gives up its
// taught digests whichever run fills the entry again.
func TestStoredBundleDropsTaughtContentDigestsWhenAPrunedEntryStoresAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// fill is the export that fills the pruned entry again, from its
		// sender, with that sender's number for the recipe.
		fill func(t *testing.T, a ValueBundle, aNumber uint64) (CacheID, ValueBundle, uint64)
	}{
		{"another engine's run", func(t *testing.T, _ ValueBundle, _ uint64) (CacheID, ValueBundle, uint64) {
			b, bNumber := storedContentTestRoot(t, "pruned-root")
			return "cache-b", b, bNumber
		}},
		{"the same run", func(t *testing.T, a ValueBundle, aNumber uint64) (CacheID, ValueBundle, uint64) {
			return "cache-a", a, aNumber
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before, after := testDigest("content-before-the-prune"), testDigest("content-after-the-prune")
			bundle, aNumber := storedContentTestRoot(t, "pruned-root")
			ctx, cloud := storedPartTestCache(t, WithBlobStore())
			number := storedPersistenceTestMerge(t, ctx, cloud, bundle)[0]
			storedContentTestTeach(t, ctx, cloud, aNumber, before)
			storedContentTestWait(t, cloud, number, before)

			_, removed, _, err := cloud.removePrunedEdge(ctx, sharedResultID(number))
			require.NoError(t, err)
			require.True(t, removed)
			from, fill, fillNumber := tc.fill(t, bundle, aNumber)
			reply, err := cloud.MergeValues(ctx, from, fill)
			require.NoError(t, err)
			require.Equal(t, number, reply.Imported()[0].ResultID, "the pruned entry takes the record")
			// The filling run's holding teaches its own content; once the
			// frame has it, the body worker has given the frame everything
			// the entry was taught.
			_, _, err = cloud.UpdateRemoteHolding(ctx, HolderKey{Cache: from, Number: fillNumber}, RemoteHoldingUpdate{ContentDigest: after})
			require.NoError(t, err)
			storedContentTestWait(t, cloud, number, after)
			require.NotContains(t, storedContentTestFrameDigests(t, cloud, number), before,
				"the frame carries no content digest taught for the bytes the prune took")
		})
	}
}

// An entry restored from a save, whose expired record is then replaced by a
// new one from an engine, keeps the content digest a span taught it before
// the save: the new frame takes the old frame's.
func TestCloudRestoreKeepsATaughtContentDigestAcrossAReplacement(t *testing.T) {
	t.Parallel()
	content := testDigest("content-before-restore")
	bundle, aNumber := storedContentTestRoot(t, "restored-root")
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	number := storedPersistenceTestMerge(t, ctx, cloud, bundle)[0]
	storedContentTestTeach(t, ctx, cloud, aNumber, content)
	storedContentTestWait(t, cloud, number, content)
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	mergeTestExpire(cloud, storedBundleTestEntry(cloud, number))
	second, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, number, second.Imported()[0].ResultID)
	require.Equal(t, uint64(1), second.Values[0].Replacements, "the expired record was replaced")
	storedContentTestWait(t, cloud, number, content)
}

// storedContentTestRestored has a Cloud saved at a new path take engine
// cache-a's export of a root, learn the root's content digest from a's span,
// give it to the frame and close, and returns the Cloud restored from the
// save, with the export, a's number and the Cloud's. a exports the record
// again to the restored Cloud, as an engine does after a restart: a holding
// keeps no span's teach across a restart, and its new one takes a's spans.
func storedContentTestRestored(t *testing.T, field string, content digest.Digest) (context.Context, *Cache, ValueBundle, uint64, uint64) {
	t.Helper()
	bundle, aNumber := storedContentTestRoot(t, field)
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	number := storedPersistenceTestMerge(t, ctx, cloud, bundle)[0]
	storedContentTestTeach(t, ctx, cloud, aNumber, content)
	storedContentTestWait(t, cloud, number, content)
	require.NoError(t, cloud.Close(ctx))
	ctx, cloud = storedPersistenceTestCloud(t, path)
	t.Cleanup(func() { require.NoError(t, cloud.Close(cacheTestContext(context.Background()))) })
	require.Equal(t, []uint64{number}, storedPersistenceTestMerge(t, ctx, cloud, bundle), "the restored entry takes a's export")
	return ctx, cloud, bundle, aNumber, number
}

// A restored entry taught a new content digest, whose expired record is then
// replaced, keeps the digest taught before the restore: the new frame has it
// as replaced-content, as the frame it replaced did.
func TestCloudRestoreKeepsAnEarlierTaughtDigestWhenALaterOneIsTaught(t *testing.T) {
	t.Parallel()
	first, second := testDigest("content-saved"), testDigest("content-after-restore")
	ctx, cloud, bundle, aNumber, number := storedContentTestRestored(t, "restored-taught-root", first)
	storedContentTestTeach(t, ctx, cloud, aNumber, second)
	storedContentTestWait(t, cloud, number, second)

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, number))
	replaced, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(1), replaced.Values[0].Replacements, "the expired record was replaced")
	storedContentTestWait(t, cloud, number, second)
	require.Contains(t, storedTestFrame(t, cloud, storedBundleTestEntry(cloud, number)).ExtraDigests,
		call.ExtraDigest{Digest: first, Label: call.ExtraDigestLabelReplacedContent}, "the digest taught before the restore")
}

// A restored entry whose record a prune drops while an engine's holding keeps
// the entry gives up the content digest taught before the restore with the
// value, as an entry that was never saved does
// (TestStoredBundleDropsTaughtContentDigestsWhenAPrunedEntryStoresAgain): a
// later export's record takes only what spans teach it after.
func TestCloudRestoreDropsATaughtDigestWhenAPrunedEntryStoresAgain(t *testing.T) {
	t.Parallel()
	saved, later := testDigest("content-saved-pruned"), testDigest("content-after-the-prune")
	ctx, cloud, bundle, aNumber, number := storedContentTestRestored(t, "restored-pruned-root", saved)
	_, removed, _, err := cloud.removePrunedEdge(ctx, sharedResultID(number))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, []uint64{number}, storedPersistenceTestMerge(t, ctx, cloud, bundle), "the same entry takes the record again")
	storedContentTestTeach(t, ctx, cloud, aNumber, later)
	storedContentTestWait(t, cloud, number, later)
	require.NotContains(t, storedContentTestFrameDigests(t, cloud, number), saved, "the digest saved for the dropped value")
}

// A content digest the Cloud learned for an entry, still waiting for the
// body worker when the Cloud closes, is on the entry's frame after a restore:
// the save keeps frames, not the digests waiting for them.
func TestCloudSaveKeepsAContentDigestTaughtAsItCloses(t *testing.T) {
	t.Parallel()
	first, last := testDigest("content-before-close"), testDigest("content-at-close")
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	blocked, release, _ := storedContentTestHold(t, cloud)
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "closing-root", &transferTestValue{Text: "x"})
	a.egraphMu.RLock()
	aNumber := uint64(r.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	number := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r))[0]
	storedContentTestTeach(t, ctx, cloud, aNumber, first)
	<-blocked
	storedContentTestTeach(t, ctx, cloud, aNumber, last)

	closed := make(chan error, 1)
	go func() { closed <- cloud.Close(ctx) }()
	require.Eventually(t, func() bool {
		w := cloud.storedBodies
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.closed
	}, 5*time.Second, 5*time.Millisecond, "the Cloud stops its body worker")
	release()
	require.NoError(t, <-closed)

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	require.Equal(t, last, storedTestFrame(t, cloud, storedBundleTestEntry(cloud, number)).ContentDigest())
}

// lateContentReceiverFrame is receiverTestFrame without its content digest:
// a receiver whose content its engine learns only once it is evaluated, as a
// directory's is, and reports in a later span.
func lateContentReceiverFrame(client string) *ResultCall {
	frame := receiverTestFrame(client)
	frame.ExtraDigests = nil
	return frame
}

// A call on a per-client receiver hits on a fresh engine with a receiver of
// its own of the same content, as on one engine, when the Cloud learned the
// producer's receiver's content only after the export, from the producer's
// later span: Go.module over a workspace whose directory's content the fill's
// engine reported once it had evaluated it. The control is the receiver whose
// frame carried its content at the export (TestCallRecordHitsOverAnEquivalentReceiver).
func TestCallRecordHitsOverAReceiverWhoseContentTheCloudLearnedLater(t *testing.T) {
	t.Parallel()
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	ctx, producer, srv := transferTestCache(t)
	outer := callRecordTestFrame("outer")
	outer.Receiver = &ResultCallRef{Call: lateContentReceiverFrame("a")}
	test := &receiverCallTest{callRecordTest: &callRecordTest{ctx: ctx, producer: producer, frame: outer, cloud: cloud}, srv: srv}
	test.inner = persistedListTestResult(t, ctx, producer, srv, "inner", &transferTestValue{Text: "inner"})
	receiver, err := producer.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: lateContentReceiverFrame("a"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, lateContentReceiverFrame("a"))
		})
	require.NoError(t, err)
	test.receiver = receiver
	_, err = producer.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: test.frame, IsPersistable: true},
		func(context.Context) (AnyResult, error) { return test.inner, nil })
	require.NoError(t, err)
	numbers := storedPersistenceTestMerge(t, cloudCtx, cloud, exportTestBundle(t, ctx, producer, test.inner, receiver))
	require.Len(t, numbers, 2)
	for _, number := range numbers {
		if storedTestFrame(t, cloud, storedBundleTestEntry(cloud, number)).Field == "receiver" {
			test.receiverNumber = number
		} else {
			test.number = number
		}
	}
	test.attach(t, cloudCtx, test.span(t, true, 0))

	// The producer's later span reports its receiver's content.
	content := digest.FromString("receiver content")
	producer.egraphMu.RLock()
	producerReceiver := uint64(receiver.cacheSharedResult().id)
	producer.egraphMu.RUnlock()
	_, _, err = cloud.UpdateRemoteHolding(cloudCtx, HolderKey{Cache: "cache-a", Number: producerReceiver}, RemoteHoldingUpdate{ContentDigest: content})
	require.NoError(t, err)
	require.Contains(t, cloud.EquivalentEntries(content.String()), test.receiverNumber, "the Cloud's class has the receiver's content")
	storedContentTestWait(t, cloud, test.receiverNumber, content)

	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.True(t, test.freshReceiverHits(t, built.Bundle), "b's call on its own receiver of the same content hits")
}

// TaughtContentStats counts what giving frames their taught content digests
// did: a digest the frame already carries is counted carried and queues
// nothing; each digest a frame lacks is queued and rewritten; and a bundle
// built while an entry in it isn't yet recorded as rewritten is counted
// once, and one built after is not.
func TestTaughtContentStatsCountWhatTheRewritesDid(t *testing.T) {
	t.Parallel()
	carried, first, second := testDigest("stats-carried"), testDigest("stats-first"), testDigest("stats-second")
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "stats-carried-root", &transferTestValue{Text: "x"})
	require.NoError(t, a.TeachContentDigest(actx, r, carried))
	r2 := persistedListTestResult(t, actx, a, asrv, "stats-taught-root", &transferTestValue{Text: "y"})
	a.egraphMu.RLock()
	rNumber, r2Number := uint64(r.cacheSharedResult().id), uint64(r2.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	blocked, release, batches := storedContentTestHold(t, cloud)
	storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r))
	number := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r2))[0]

	storedContentTestTeach(t, ctx, cloud, rNumber, carried)
	require.Equal(t, TaughtContentStats{Carried: 1}, cloud.TaughtContentStats(), "a digest the frame carries queues nothing")

	storedContentTestTeach(t, ctx, cloud, r2Number, first)
	<-blocked
	storedContentTestTeach(t, ctx, cloud, r2Number, second)
	_, err := cloud.StoredBundle(ctx, []uint64{number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	release()
	// The worker's second batch has rewritten the frame and recorded it.
	require.Eventually(t, func() bool { return batches.Load() >= 2 }, 5*time.Second, 5*time.Millisecond)
	storedContentTestWait(t, cloud, number, second)
	_, err = cloud.StoredBundle(ctx, []uint64{number}, time.Now().Unix(), nil)
	require.NoError(t, err)

	got := cloud.TaughtContentStats()
	require.Positive(t, got.Took)
	got.Took = 0
	require.Equal(t, TaughtContentStats{Queued: 2, Rewritten: 2, Carried: 1, BundlesWaiting: 1}, got)
}
