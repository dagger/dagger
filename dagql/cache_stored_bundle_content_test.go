package dagql

import (
	"context"
	"encoding/json"
	"slices"
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
		frame := storedBundleTestEntry(cloud, number).entryFrameLocked()
		cloud.egraphMu.RUnlock()
		decoded, err := frame.decode()
		return err == nil && decoded != nil && decoded.ContentDigest() == content
	}, 5*time.Second, 5*time.Millisecond, "the Cloud gives the entry's frame its taught content digest")
}

// storedContentTestSend has engine A compute R and export it to a Cloud, and
// the Cloud learn R's content digest before the export (A teaches it, so R's
// frame carries it) or after (from A's later span, as the service reads a
// lazy span or a call span that reports it). It returns the record the Cloud
// sends a fresh engine for R, as JSON, and whether that engine, whose own
// result S of another recipe has the same content, holds R and S in one class.
func storedContentTestSend(t *testing.T, after bool) (string, bool) {
	t.Helper()
	content := testDigest("content-of-x")
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "content-root", &transferTestValue{Text: "x"})
	if !after {
		require.NoError(t, a.TeachContentDigest(actx, r, content))
	}
	a.egraphMu.RLock()
	recipe := r.cacheSharedResult().recipeKeys[0]
	aNumber := uint64(r.cacheSharedResult().id)
	a.egraphMu.RUnlock()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, actx, a, r))
	require.NoError(t, err)
	imported := reply.Imported()
	require.Len(t, imported, 1)
	cloudNumber := imported[0].ResultID
	if after {
		_, _, err := cloud.UpdateRemoteHolding(ctx, HolderKey{Cache: "cache-a", Number: aNumber}, RemoteHoldingUpdate{ContentDigest: content})
		require.NoError(t, err)
		storedContentTestWait(t, cloud, cloudNumber, content)
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
	before, beforeJoined := storedContentTestSend(t, false)
	after, afterJoined := storedContentTestSend(t, true)
	require.True(t, beforeJoined, "taught before the export, R and S are one class on B")
	require.True(t, afterJoined, "learned after the export, R and S are one class on B, as on the Cloud")
	require.JSONEq(t, before, after, "the fresh engine gets the frame it gets when R's frame carried the digest at the export")
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
		if storedBundleTestEntry(cloud, number).loadResultCall().Field == "receiver" {
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
