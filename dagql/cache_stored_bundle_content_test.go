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
		queued, _, _ := cloud.TaughtContentStats()
		require.Zero(t, queued, "a record whose frame carries its content digest is never queued for a rewrite")
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
