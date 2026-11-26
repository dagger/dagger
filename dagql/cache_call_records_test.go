package dagql

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// callRecordTest is an engine cache, cache-a, whose call "outer" returned an
// entry it already had, "inner", and a Cloud that stored inner from cache-a's
// export.
type callRecordTest struct {
	ctx      context.Context
	producer *Cache
	frame    *ResultCall
	inner    AnyResult
	cloud    *Cache
	// number is the Cloud's number for inner.
	number uint64
}

// callRecordTestFrame is a call of field returning a transferred test value,
// which has a part a Cloud can store.
func callRecordTestFrame(field string) *ResultCall {
	return &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&transferTestValue{}).Type())}
}

func newCallRecordTest(t *testing.T, cloudCtx context.Context, cloud *Cache) *callRecordTest {
	t.Helper()
	return newCallRecordTestOf(t, cloudCtx, cloud, callRecordTestFrame("outer"))
}

// newCallRecordTestOf is newCallRecordTest with the call frame.
func newCallRecordTestOf(t *testing.T, cloudCtx context.Context, cloud *Cache, frame *ResultCall) *callRecordTest {
	t.Helper()
	ctx, producer, srv := transferTestCache(t)
	test := &callRecordTest{ctx: ctx, producer: producer, frame: frame, cloud: cloud}
	test.inner = persistedListTestResult(t, ctx, producer, srv, "inner", &transferTestValue{Text: "inner"})
	got, err := producer.GetOrInitCall(ctx, "test-session", srv,
		&CallRequest{ResultCall: test.frame, IsPersistable: true},
		func(context.Context) (AnyResult, error) { return test.inner, nil })
	require.NoError(t, err)
	require.Equal(t, test.inner.cacheSharedResult().id, got.cacheSharedResult().id, "outer returned inner's entry")
	numbers := storedPersistenceTestMerge(t, cloudCtx, cloud, exportTestBundle(t, ctx, producer, test.inner))
	require.Len(t, numbers, 1)
	test.number = numbers[0]
	return test
}

// span is the holding cache-a's span of the outer call reports: the call's
// request, its term and its encoding, on inner's entry, with expiry.
func (test *callRecordTest) span(t *testing.T, withCall bool, expiresAtUnix int64) RemoteHolding {
	t.Helper()
	recipe, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	request, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	self, _, err := test.frame.selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	desc := RemoteHolding{Recipe: recipe, Request: request, Field: "outer", Term: &RemoteTerm{Self: self}, ExpiresAtUnix: expiresAtUnix}
	if withCall {
		pb, err := test.frame.callPB(test.producer)
		require.NoError(t, err)
		desc.Call, err = pb.Encode()
		require.NoError(t, err)
	}
	return desc
}

func (test *callRecordTest) attach(t *testing.T, cloudCtx context.Context, desc RemoteHolding) {
	t.Helper()
	key := HolderKey{Cache: "cache-a", Number: uint64(test.inner.cacheSharedResult().id)}
	_, err := test.cloud.AttachRemoteHolding(cloudCtx, key, desc)
	require.NoError(t, err)
}

// freshHits merges the bundle into a new engine cache and reports whether the
// outer call hits there.
func (test *callRecordTest) freshHits(t *testing.T, bundle ValueBundle) bool {
	t.Helper()
	ctx, fresh, srv := transferTestCache(t)
	_, err := fresh.MergeValues(ctx, CloudCacheID, bundle)
	require.NoError(t, err)
	inner, err := fresh.GetOrInitCall(ctx, "test-session", srv,
		&CallRequest{ResultCall: callRecordTestFrame("inner"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			t.Fatal("the returned entry itself was not merged")
			return nil, nil
		})
	require.NoError(t, err)
	require.True(t, inner.HitCache())
	ran := false
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv,
		&CallRequest{ResultCall: test.frame.clone(), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "ran again"}, test.frame)
		})
	require.NoError(t, err)
	return !ran
}

// A call that returned an existing entry hits on a fresh engine when the
// Cloud sends the call its span reported with the entry; without the call,
// the entry arrives and the call misses.
func TestCallRecordHitsOnAFreshEngine(t *testing.T) {
	t.Parallel()
	for name, withCall := range map[string]bool{"with the call": true, "without it": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cloud := storedPartTestCache(t, WithBlobStore())
			test := newCallRecordTest(t, ctx, cloud)
			test.attach(t, ctx, test.span(t, withCall, 0))
			built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
			require.NoError(t, err)
			if withCall {
				require.Len(t, built.Bundle.Calls, 1)
			} else {
				require.Empty(t, built.Bundle.Calls)
			}
			require.Equal(t, withCall, test.freshHits(t, built.Bundle))
		})
	}
}

// The call is the stored entry's, not the producing engine's: it is still
// sent once the engine is gone.
func TestCallRecordOutlivesItsEngine(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	test.attach(t, ctx, test.span(t, true, 0))
	candidates := cloud.ReleaseRemoteCache(ctx, "cache-a").Candidates
	_, _, err := cloud.CollectRemoteHoldings(ctx, candidates, nil)
	require.NoError(t, err)
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.True(t, test.freshHits(t, built.Bundle))
}

// The call is saved with the Cloud's pool and sent after a restore.
func TestCallRecordSurvivesARestore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	test := newCallRecordTest(t, ctx, cloud)
	test.attach(t, ctx, test.span(t, true, 0))
	stored, _, err := cloud.SetStoredPart(ctx, test.number, storedBenchPart(1), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	test.cloud = cloud
	require.True(t, test.freshHits(t, built.Bundle))
}

// A stored entry that never expires takes the expiry of a call that returned
// it, as a local cache gives an entry the earliest expiry of its calls, and
// the bundle carries that expiry: a fresh engine doesn't keep the call past
// it.
func TestCallRecordGivesTheEntryItsExpiry(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	expires := time.Now().Add(time.Hour).Unix()
	test.attach(t, ctx, test.span(t, true, expires))
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	var got int64
	for _, value := range built.Bundle.Values {
		if value.SenderNumber == test.number {
			got = value.ExpiresAtUnix
		}
	}
	require.Equal(t, expires, got)
}

// The Cloud keeps one call per request digest: a second report of the same
// call adds nothing.
func TestCallRecordIsKeptOnce(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	test.attach(t, ctx, test.span(t, true, 0))
	test.attach(t, ctx, test.span(t, true, 0))
	entry := storedBundleTestEntry(cloud, test.number)
	cloud.egraphMu.RLock()
	calls := len(entry.keptCallsLocked())
	cloud.egraphMu.RUnlock()
	require.Equal(t, 1, calls)
}

// perClientTestFrame is the call "outer" as client makes it: keyed to the
// client, as a call with cachePerClient is, and with no extra digest.
func perClientTestFrame(client string) *ResultCall {
	frame := callRecordTestFrame("outer")
	frame.ImplicitInputs = []*ResultCallArg{{Name: PerClientInput.Name, Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: client}}}
	return frame
}

// keptCallCount is how many calls the Cloud keeps on its entry number.
func keptCallCount(cloud *Cache, number uint64) int {
	entry := storedBundleTestEntry(cloud, number)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	return len(entry.keptCallsLocked())
}

// A call keyed to the client that made it names its client in its recipe and
// in its term, so no other client can match it: the Cloud doesn't keep it,
// and no bundle sends it. The entry still takes the call's expiry, as a
// local cache's entry takes the earliest of its calls'.
func TestCallRecordKeyedToItsClientIsNotKept(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTestOf(t, ctx, cloud, perClientTestFrame("a"))
	expires := time.Now().Add(time.Hour).Unix()
	test.attach(t, ctx, test.span(t, true, expires))
	require.Zero(t, keptCallCount(cloud, test.number))
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls)
	require.Zero(t, built.UnsendableCalls)
	require.Empty(t, built.Partial)
	var got int64
	for _, value := range built.Bundle.Values {
		if value.SenderNumber == test.number {
			got = value.ExpiresAtUnix
		}
	}
	require.Equal(t, expires, got)
}

// A store saved while the Cloud still kept calls keyed to their client is
// restored without them.
func TestCallRecordKeyedToItsClientIsDroppedOnRestore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	test := newCallRecordTestOf(t, ctx, cloud, perClientTestFrame("a"))
	stored, _, err := cloud.SetStoredPart(ctx, test.number, storedBenchPart(1), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.NoError(t, cloud.Close(ctx))

	// The row such a Cloud saved.
	request, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	pb, err := test.frame.callPB(test.producer)
	require.NoError(t, err)
	encoded, err := pb.Encode()
	require.NoError(t, err)
	db, _, err := prepareCacheDBs(ctx, path, true)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO result_calls (result_id, request_digest, call) VALUES (?, ?, ?)`, test.number, request.String(), encoded)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	require.Zero(t, keptCallCount(cloud, test.number))
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls)
}

// A span whose encoded call isn't the call it reports attaches its holding,
// with the call left out.
func TestCallRecordLeavesOutAMismatchedCall(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	desc := test.span(t, true, 0)
	desc.Request = digest.FromString("another request")
	key := HolderKey{Cache: "cache-b", Number: 7}
	_, err := cloud.AttachRemoteHolding(ctx, key, desc)
	require.NoError(t, err)
	info, ok := cloud.RemoteEntryInfo(key)
	require.True(t, ok, "the holding attached")
	require.Equal(t, test.number, info.Number)
	entry := storedBundleTestEntry(cloud, test.number)
	cloud.egraphMu.RLock()
	calls := len(entry.keptCallsLocked())
	cloud.egraphMu.RUnlock()
	require.Zero(t, calls)
}

// A call whose input is a digest-witnessed string's digest, which no entry
// stands for, is sent: the recipient's own call reproduces the digest.
func TestCallRecordWithAWitnessInputIsSent(t *testing.T) {
	t.Parallel()
	frame := callRecordTestFrame("outer")
	frame.Args = []*ResultCallArg{{Name: "execMD", Value: &ResultCallLiteral{
		Kind:                 ResultCallLiteralKindDigestedString,
		DigestedStringValue:  `{"clientID":"a"}`,
		DigestedStringDigest: digest.FromString("exec metadata"),
	}}}
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTestOf(t, ctx, cloud, frame)
	test.attach(t, ctx, test.span(t, true, 0))
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.Zero(t, built.UnsendableCalls)
	require.True(t, test.freshHits(t, built.Bundle))
}

// bundleRowOf returns the row of the bundle whose sender number is number.
func bundleRowOf(t *testing.T, bundle ValueBundle, number uint64) TransferOrdinal {
	t.Helper()
	for _, v := range bundle.Values {
		if v.SenderNumber == number {
			return v.Ordinal
		}
	}
	t.Fatalf("no row of entry %d", number)
	return 0
}

// receiverTestFrame is a receiver's call as client makes it: its recipe is
// the client's own, and its content is the same for every client, as a
// module's _implementationScoped is.
func receiverTestFrame(client string) *ResultCall {
	frame := callRecordTestFrame("receiver")
	frame.ImplicitInputs = []*ResultCallArg{{Name: "cachePerClient", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: client}}}
	frame.ExtraDigests = []call.ExtraDigest{{Digest: digest.FromString("receiver content"), Label: call.ExtraDigestLabelContent}}
	return frame
}

// receiverTestCall is the call "outer" on the receiver of client.
func receiverTestCall(client string) *ResultCall {
	frame := callRecordTestFrame("outer")
	frame.Receiver = &ResultCallRef{Call: receiverTestFrame(client)}
	return frame
}

// receiverCallTest is cache-a's call outer on its own receiver A, returning
// inner, and a Cloud that stored inner and A and kept the call.
type receiverCallTest struct {
	*callRecordTest
	// srv is cache-a's server.
	srv      *Server
	receiver AnyResult
	// receiverNumber is the Cloud's number for A.
	receiverNumber uint64
}

func newReceiverCallTest(t *testing.T) *receiverCallTest {
	t.Helper()
	return newReceiverCallTestKeeping(t, true)
}

// newReceiverCallTestKeeping is newReceiverCallTest, with the Cloud keeping
// cache-a's call on A only with keepCall.
func newReceiverCallTestKeeping(t *testing.T, keepCall bool) *receiverCallTest {
	t.Helper()
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	ctx, producer, srv := transferTestCache(t)
	test := &receiverCallTest{callRecordTest: &callRecordTest{ctx: ctx, producer: producer, frame: receiverTestCall("a"), cloud: cloud}, srv: srv}
	test.inner = persistedListTestResult(t, ctx, producer, srv, "inner", &transferTestValue{Text: "inner"})
	receiver, err := producer.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestFrame("a"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("a"))
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
	if keepCall {
		test.attach(t, cloudCtx, test.span(t, true, 0))
	}
	return test
}

// freshReceiverHits merges the bundle into a new engine cache whose client
// "b" makes its own receiver, and reports whether b's call on it hits.
func (test *receiverCallTest) freshReceiverHits(t *testing.T, bundle ValueBundle) bool {
	t.Helper()
	ctx, fresh, srv := transferTestCache(t)
	_, err := fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestFrame("b"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("b"))
		})
	require.NoError(t, err)
	_, err = fresh.MergeValues(ctx, CloudCacheID, bundle)
	require.NoError(t, err)
	ran := false
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestCall("b"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "ran again"}, receiverTestCall("b"))
		})
	require.NoError(t, err)
	return !ran
}

// A bundle for a recipient whose check made a term sends a call of that term
// it can place, though the check's own call can't be. Check A made outer on
// its receiver, which the Cloud stores. Check B made outer on its own
// receiver, equal in content: the Cloud knows B's receiver's recipe, in the
// same class as A's by their content, but stores no record of it, as it
// stores one record of a class. So B's own call can't be placed, and the two
// calls are one term, of which the entry keeps A's call alone. B's bundle,
// naming B's call and its term, sends A's call with A's receiver, and a
// fresh engine whose client makes a receiver of its own, under a recipe no
// run made before, hits outer by its term and runs nothing.
func TestACallOfATermItsCheckMadeIsSentThoughItsOwnCantBePlaced(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloudCtx := context.Background()
	inner, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	callB := receiverTestCall("b")
	requestB, err := callB.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	selfB, _, err := callB.selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	pb, err := callB.callPB(test.producer)
	require.NoError(t, err)
	encodedB, err := pb.Encode()
	require.NoError(t, err)
	// B's receiver, reported by its engine with its content, never stored.
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("receiver content")})
	require.NoError(t, err)
	// B's call, which returned inner.
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 2},
		RemoteHolding{Recipe: inner, Request: requestB, Field: "outer", Call: encodedB, Term: &RemoteTerm{Self: selfB}})
	require.NoError(t, err)
	entry := storedBundleTestEntry(test.cloud, test.number)
	test.cloud.egraphMu.RLock()
	kept := len(entry.keptCallsLocked())
	keys := map[string]int{}
	for _, call := range entry.keptCallsLocked() {
		keys[test.cloud.keptCallTermKeyLocked(call)]++
	}
	test.cloud.egraphMu.RUnlock()
	require.Equal(t, 1, kept, "the Cloud keeps one call of the term on inner, A's")
	require.Len(t, keys, 1)

	held := &HeldBy{Calls: func(r digest.Digest) bool { return r == requestB }, Terms: []RemoteTerm{{Self: selfB, Inputs: []digest.Digest{receiverB}}}}
	built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	sizes, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
	require.Len(t, built.Bundle.Calls, 1, "one call of B's term")
	require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs, "with A's receiver, which the Cloud stores")
	require.Zero(t, built.UnsendableCalls)

	ctx, fresh, srv := transferTestCache(t)
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestFrame("c"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("c"))
		})
	require.NoError(t, err)
	_, err = fresh.MergeValues(ctx, CloudCacheID, built.Bundle)
	require.NoError(t, err)
	ran := false
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestCall("c"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "ran again"}, receiverTestCall("c"))
		})
	require.NoError(t, err)
	require.False(t, ran, "a fresh engine's client hits outer on its own receiver by its term")
}

// A call whose input the Cloud knows only by its class is sent, placed by a
// stored entry of that class. Check A made its receiver, which the Cloud
// stores, and no call on it. Check B made outer on its own receiver, equal in
// content: the Cloud knows B's receiver's recipe, in the same class as A's by
// their content, but stores no record of it, as it stores one record of a
// class, and keeps B's call alone on inner. B's bundle sends B's call with A's
// receiver standing for B's, and a fresh engine whose client makes a receiver
// of its own, under a recipe no run made before, puts B's receiver in A's
// class, hits outer by its term and runs nothing. A workspace derived in
// every session, which takes its content's identity, is such a receiver: the
// Cloud stores one of a class, and every other session's read on it was
// carried without its call.
func TestACallWhoseInputTheCloudKnowsOnlyByItsClassIsSent(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTestKeeping(t, false)
	cloudCtx := context.Background()
	inner, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	callB := receiverTestCall("b")
	requestB, err := callB.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	selfB, _, err := callB.selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	pb, err := callB.callPB(test.producer)
	require.NoError(t, err)
	encodedB, err := pb.Encode()
	require.NoError(t, err)
	// B's receiver, reported by its engine with its content, never stored.
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("receiver content")})
	require.NoError(t, err)
	// B's call, which returned inner.
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 2},
		RemoteHolding{Recipe: inner, Request: requestB, Field: "outer", Call: encodedB, Term: &RemoteTerm{Self: selfB}})
	require.NoError(t, err)
	entry := storedBundleTestEntry(test.cloud, test.number)
	test.cloud.egraphMu.RLock()
	kept := len(entry.keptCallsLocked())
	test.cloud.egraphMu.RUnlock()
	require.Equal(t, 1, kept, "the Cloud keeps B's call alone on inner")

	held := &HeldBy{Calls: func(r digest.Digest) bool { return r == requestB }}
	built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	sizes, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
	require.Zero(t, built.UnsendableCalls)
	require.Empty(t, built.Partial, "inner goes with its call")
	require.Len(t, built.Bundle.Calls, 1)
	require.Equal(t, encodedB, built.Bundle.Calls[0].Call, "B's own call")
	require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs, "with A's receiver, standing for B's")
	require.Equal(t, []TransferOrdinal{bundleRowOf(t, built.Bundle, test.receiverNumber)}, built.Bundle.Calls[0].Inputs, "B's receiver is placed by A's row")

	ctx, fresh, srv := transferTestCache(t)
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestFrame("c"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("c"))
		})
	require.NoError(t, err)
	_, err = fresh.MergeValues(ctx, CloudCacheID, built.Bundle)
	require.NoError(t, err)
	ran := false
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestCall("c"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "ran again"}, receiverTestCall("c"))
		})
	require.NoError(t, err)
	require.False(t, ran, "a fresh engine's client hits outer on its own receiver by its term")
}

// Keeping a call and sending it judge its inputs by one rule
// (callPlacementLocked). A call whose input the Cloud knows only by its class
// is one a bundle can send, placed by a stored entry of the class, so keeping
// it stamps its entry with a new term a bundle can send, not with one it
// can't. Check A made its receiver, which the Cloud stores; check B made
// outer on its own receiver, equal in content, which the Cloud knows only by
// its recipe in A's class.
func TestAKeptCallPlacedThroughItsInputsClassIsStampedSendable(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTestKeeping(t, false)
	cloudCtx := context.Background()
	inner, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	callB := receiverTestCall("b")
	requestB, err := callB.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	selfB, _, err := callB.selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	pb, err := callB.callPB(test.producer)
	require.NoError(t, err)
	encodedB, err := pb.Encode()
	require.NoError(t, err)
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("receiver content")})
	require.NoError(t, err)
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 2},
		RemoteHolding{Recipe: inner, Request: requestB, Field: "outer", Call: encodedB, Term: &RemoteTerm{Self: selfB}})
	require.NoError(t, err)
	require.Equal(t, []digest.Digest{requestB}, keptCallRequests(test.cloud, test.number))
	require.Equal(t, map[string]int{StampKeptCallNewTerm.String(): 1}, test.cloud.StampCauses([]uint64{test.number}),
		"B's call is placed by A's receiver, a stored entry of its class")
}

// attachCallOnB has cache-b report outer on its own receiver B, equal in
// content to A's, returning inner, with the expiry its span reports. With
// stored, the Cloud stores B's receiver from cache-b's export; without, it
// only knows B's receiver's recipe, in A's class by their content. It
// returns B's call's request digest and its encoding.
func (test *receiverCallTest) attachCallOnB(t *testing.T, stored bool, expiresAtUnix int64) (digest.Digest, string) {
	t.Helper()
	cloud := test.cloud
	bctx, b, bsrv := transferTestCache(t)
	callB := receiverTestCall("b")
	requestB, err := callB.deriveRecipeDigest(b)
	require.NoError(t, err)
	pb, err := callB.callPB(b)
	require.NoError(t, err)
	encodedB, err := pb.Encode()
	require.NoError(t, err)
	if stored {
		inner := persistedListTestResult(t, bctx, b, bsrv, "inner", &transferTestValue{Text: "inner"})
		receiverB, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: receiverTestFrame("b"), IsPersistable: true},
			func(context.Context) (AnyResult, error) {
				return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("b"))
			})
		require.NoError(t, err)
		_, err = b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: callB, IsPersistable: true},
			func(context.Context) (AnyResult, error) { return inner, nil })
		require.NoError(t, err)
		_, err = cloud.MergeValues(test.ctx, "cache-b", exportTestBundle(t, bctx, b, inner, receiverB))
		require.NoError(t, err)
		bTest := &callRecordTest{ctx: bctx, producer: b, frame: callB, inner: inner, cloud: cloud}
		_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: uint64(inner.cacheSharedResult().id)}, bTest.span(t, true, expiresAtUnix))
		require.NoError(t, err)
		return requestB, encodedB
	}
	inner, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(b)
	require.NoError(t, err)
	selfB, _, err := callB.selfDigestAndInputRefs(b)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("receiver content")})
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: 2},
		RemoteHolding{Recipe: inner, Request: requestB, Field: "outer", Call: encodedB, Term: &RemoteTerm{Self: selfB}, ExpiresAtUnix: expiresAtUnix})
	require.NoError(t, err)
	return requestB, encodedB
}

// keptCallRequests are the request digests of the calls the Cloud keeps on
// its entry number.
func keptCallRequests(cloud *Cache, number uint64) []digest.Digest {
	entry := storedBundleTestEntry(cloud, number)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	return slices.Sorted(maps.Keys(entry.keptCallsLocked()))
}

// A call of a term its entry keeps a call of adds nothing a bundle sends: a
// bundle sends one call of a term, and the cache matches the term, as a
// local cache adds no second term of a term it has. A's call, whose receiver
// the Cloud stores, is kept; B's call of the same term on its own receiver,
// which the Cloud knows in A's class but stores no record of, is not. The
// entry still takes B's call's expiry, the earlier, as a local cache's entry
// takes the earliest of its calls'.
func TestACallOfAKeptTermIsNotKeptAgain(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	requestA, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	expires := time.Now().Add(time.Hour).Unix()
	test.attachCallOnB(t, false, expires)
	require.Equal(t, []digest.Digest{requestA}, keptCallRequests(test.cloud, test.number), "one call of the term: A's")
	built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	var got int64
	for _, value := range built.Bundle.Values {
		if value.SenderNumber == test.number {
			got = value.ExpiresAtUnix
		}
	}
	require.Equal(t, expires, got, "the entry takes B's call's expiry")
}

// A kept call the Cloud can't place, its receiver stored under no recipe of
// its own and in no class, so that no stored entry stands for it, gives way
// to a call of its term whose receiver the Cloud stores: the one call the
// entry keeps of the term is then one a bundle can send, with the receiver
// it names.
func TestAnUnplaceableKeptCallGivesWayToAPlaceableOneOfItsTerm(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	cloud.egraphMu.Lock()
	cloud.unindexRecipesLocked(cloud.resultsByID[sharedResultID(test.receiverNumber)])
	cloud.removeResultOutputEqClassesLocked(sharedResultID(test.receiverNumber))
	cloud.egraphMu.Unlock()
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls, "A's call can't be placed")

	requestB, encodedB := test.attachCallOnB(t, true, 0)
	require.Equal(t, []digest.Digest{requestB}, keptCallRequests(cloud, test.number), "B's call replaces A's")
	built, err = cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.Equal(t, encodedB, built.Bundle.Calls[0].Call)
	require.Len(t, built.CallInputs, 1, "with B's receiver")
	require.Zero(t, built.UnsendableCalls)
}

// A call of a kept term whose receiver the Cloud stores only after the call
// came is sent once a receiver of its class is stored, though no call of the
// term could be placed when it came. A call often comes before its receiver
// is exported. The Cloud stores A's receiver under no recipe of its
// own and in no class, and B's call comes while the Cloud knows B's receiver
// only by its content, in A's class: neither call can be placed, and the
// entry keeps A's, the term's first. cache-b's export then stores B's
// receiver, which places A's call by its class. A bundle for a recipient
// whose check made B's call, naming B's call and its term as a set does,
// sends A's call with B's receiver standing for A's, and a fresh engine hits
// it.
func TestACallOfAKeptTermIsSentOnceAReceiverOfItsClassIsStored(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	cloud.egraphMu.Lock()
	cloud.unindexRecipesLocked(cloud.resultsByID[sharedResultID(test.receiverNumber)])
	cloud.removeResultOutputEqClassesLocked(sharedResultID(test.receiverNumber))
	cloud.egraphMu.Unlock()
	requestA, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	requestB, _ := test.attachCallOnB(t, false, 0)
	require.Equal(t, []digest.Digest{requestA}, keptCallRequests(cloud, test.number), "the term's first call, A's")
	selfB, _, err := receiverTestCall("b").selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	recipeB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	held := &HeldBy{Calls: func(request digest.Digest) bool { return request == requestB }, Terms: []RemoteTerm{{Self: selfB, Inputs: []digest.Digest{recipeB}}}}
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls, "no receiver of the class is stored yet")

	bctx, b, bsrv := transferTestCache(t)
	receiverB, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: receiverTestFrame("b"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("b"))
		})
	require.NoError(t, err)
	_, err = cloud.MergeValues(test.ctx, "cache-b", exportTestBundle(t, bctx, b, receiverB))
	require.NoError(t, err)

	built, err = cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1, "A's call, placed by B's receiver")
	require.Len(t, built.CallInputs, 1, "with B's receiver")
	require.Zero(t, built.UnsendableCalls)
	require.True(t, test.freshReceiverHits(t, built.Bundle))
}

// Two calls kept on an entry as two terms become one term when their inputs'
// classes are joined: B's call was kept while the Cloud knew no class of B's
// receiver, and B's receiver then joined A's class by its content. The next
// call kept on the entry leaves one call of the joined term, A's, whose
// receiver the Cloud stores, as the entry would have kept one had the
// classes been one when B's call came.
func TestAKeptCallLeavesOneCallOfATermAUnionJoined(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	requestA, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	inner, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	encode := func(frame *ResultCall) (digest.Digest, digest.Digest, string) {
		t.Helper()
		request, err := frame.deriveRecipeDigest(test.producer)
		require.NoError(t, err)
		self, _, err := frame.selfDigestAndInputRefs(test.producer)
		require.NoError(t, err)
		pb, err := frame.callPB(test.producer)
		require.NoError(t, err)
		encoded, err := pb.Encode()
		require.NoError(t, err)
		return request, self, encoded
	}
	requestB, selfB, encodedB := encode(receiverTestCall("b"))
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: 2},
		RemoteHolding{Recipe: inner, Request: requestB, Field: "outer", Call: encodedB, Term: &RemoteTerm{Self: selfB}})
	require.NoError(t, err)
	require.ElementsMatch(t, []digest.Digest{requestA, requestB}, keptCallRequests(cloud, test.number), "B's call, its own term")

	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("receiver content")})
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	groups := len(cloud.keptCallTermsLocked(storedBundleTestEntry(cloud, test.number)))
	cloud.egraphMu.RUnlock()
	require.Equal(t, 1, groups, "B's receiver joined A's class: one term")

	// Another call on A's receiver, of another term, returning inner.
	other := callRecordTestFrame("other")
	other.Receiver = &ResultCallRef{Call: receiverTestFrame("a")}
	requestOther, selfOther, encodedOther := encode(other)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-a", Number: 99},
		RemoteHolding{Recipe: inner, Request: requestOther, Field: "other", Call: encodedOther, Term: &RemoteTerm{Self: selfOther}})
	require.NoError(t, err)
	require.ElementsMatch(t, []digest.Digest{requestA, requestOther}, keptCallRequests(cloud, test.number), "one call of the joined term, A's")
}

// A bundle for a recipient that names a term none of whose kept calls is
// one its check made, as when the Cloud keeps another run's call of a term
// the check made, sends that term as a call the recipient can place, and a
// fresh engine with a receiver of its own hits it. A recipient that names
// neither the term nor any of its calls gets no call of it.
func TestABundleSendsATermItsRecipientNamed(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloudCtx := context.Background()
	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	selfB, _, err := receiverTestCall("b").selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	// B's receiver, reported by its engine with its content, never stored:
	// the Cloud knows its recipe, in the class of A's.
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("receiver content")})
	require.NoError(t, err)
	none := func(digest.Digest) bool { return false }
	for name, terms := range map[string][]RemoteTerm{
		"named":     {{Self: selfB, Inputs: []digest.Digest{receiverB}}},
		"not named": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			held := &HeldBy{Calls: none, Terms: terms}
			built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
			require.NoError(t, err)
			sizes, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
			require.NoError(t, err)
			require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
			closure, err := test.cloud.StoredClosure(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
			require.NoError(t, err)
			require.Equal(t, built.CallInputs, closure.CallInputs, "listed as built")
			if terms == nil {
				require.Empty(t, built.Bundle.Calls)
				require.Empty(t, built.CallInputs)
				return
			}
			require.Len(t, built.Bundle.Calls, 1, "the named term's call")
			require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs, "with the receiver the Cloud stores")
			require.True(t, test.freshReceiverHits(t, built.Bundle))
		})
	}
}

// Pricing builds the index of a recipient's named terms once, whatever the
// roots it prices: a set's file prices thousands of roots and names
// thousands of terms, and an index built for each root made each of its
// price reads take 6 to 13 s, against 0.2 to 0.3 s for a file that names
// none. Pricing two roots, building their bundle and listing their closure
// for one recipient builds its index once.
func TestPricingBuildsARecipientsNamedTermsOnce(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	selfB, _, err := receiverTestCall("b").selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	receiverB, err := receiverTestFrame("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	held := &HeldBy{Calls: func(digest.Digest) bool { return false }, Terms: []RemoteTerm{{Self: selfB, Inputs: []digest.Digest{receiverB}}}}
	roots := []uint64{test.number, test.receiverNumber}
	_, err = test.cloud.StoredSizes(test.ctx, roots, time.Now().Unix(), held)
	require.NoError(t, err)
	_, err = test.cloud.StoredBundle(test.ctx, roots, time.Now().Unix(), held)
	require.NoError(t, err)
	_, err = test.cloud.StoredClosure(test.ctx, roots, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, int32(1), held.namedBuilds.Load(), "one index for the recipient")
}

// A walk that lets egraphMu go between entries prices and lists the call of
// a term the recipient names as the bundle sends it, though the class of the
// call's input is merged into another while the walk has let the lock go:
// the term's key and the call's are both taken over the classes as they
// stand.
func TestANamedTermsCallIsPricedThoughItsInputsClassMergesDuringTheWalk(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	entry := storedBundleTestEntry(cloud, test.number)
	cloud.egraphMu.RLock()
	var term RemoteTerm
	for _, kept := range entry.keptCallsLocked() {
		term = RemoteTerm{Self: kept.id.self, Inputs: slices.Clone(kept.id.inputs)}
	}
	cloud.egraphMu.RUnlock()
	require.Len(t, term.Inputs, 1)
	held := &HeldBy{Calls: func(digest.Digest) bool { return false }, Terms: []RemoteTerm{term}}
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)

	// Each time the walk lets the lock go, the input's class is merged into
	// another class of a higher rank, which keeps its own root: the input's
	// class root moves, as it does when a write unions it with a larger class.
	cloud.readPieceSize = 1
	merges := 0
	cloud.testReadPieceYielded = func() {
		cloud.egraphMu.Lock()
		defer cloud.egraphMu.Unlock()
		input := cloud.egraphDigestToClass[term.Inputs[0].String()]
		before := cloud.eqClassRootLocked(input)
		other := cloud.ensureEqClassForDigestLocked(test.ctx, fmt.Sprintf("sha256:other-%d", merges))
		cloud.egraphRanks[other] = cloud.egraphRanks[before] + 1
		cloud.mergeEqClassesLocked(test.ctx, before, other)
		if cloud.eqClassRootLocked(input) != before {
			merges++
		}
	}
	sizes, err := cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Positive(t, merges, "the input's class root moved while the walk had let the lock go")
	require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
	closure, err := cloud.StoredClosure(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, built.CallInputs, closure.CallInputs, "the closure lists the call's input")
}

// A call on a receiver whose recipe is per client hits on a fresh engine with
// a receiver of its own, by its term: the bundle carries the call and the
// producer's receiver, whose content joins the fresh engine's.
func TestCallRecordHitsOverAnEquivalentReceiver(t *testing.T) {
	t.Parallel()
	for name, roots := range map[string]func(*receiverCallTest) []uint64{
		// The bundle carries the receiver already.
		"carried": func(test *receiverCallTest) []uint64 { return []uint64{test.number, test.receiverNumber} },
		// The bundle adds the stored receiver for the call.
		"added": func(test *receiverCallTest) []uint64 { return []uint64{test.number} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			test := newReceiverCallTest(t)
			built, err := test.cloud.StoredBundle(test.ctx, roots(test), time.Now().Unix(), nil)
			require.NoError(t, err)
			require.Len(t, built.Bundle.Calls, 1)
			if name == "added" {
				require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs)
			} else {
				require.Empty(t, built.CallInputs)
			}
			require.True(t, test.freshReceiverHits(t, built.Bundle))
		})
	}
}

// A bundle for a recipient whose check didn't make a kept call sends neither
// the call nor the inputs it would add, and doesn't count it unsendable: an
// engine that ran the check itself would hold neither. For one whose check
// made it, it goes with its input as before, and hits. StoredSizes prices
// each as it is built.
func TestCallRecordOfAnotherCheckIsNotSent(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	request, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	for name, made := range map[string]bool{"made by another check": false, "made by its check": true} {
		t.Run(name, func(t *testing.T) {
			held := &HeldBy{Calls: func(r digest.Digest) bool { return made && r == request }}
			built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
			require.NoError(t, err)
			sizes, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
			require.NoError(t, err)
			require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
			require.Zero(t, built.UnsendableCalls)
			require.Empty(t, built.Partial)
			if !made {
				require.Empty(t, built.Bundle.Calls)
				require.Empty(t, built.CallInputs, "the call's receiver isn't carried")
				require.Len(t, built.Bundle.Values, 1)
				return
			}
			require.Len(t, built.Bundle.Calls, 1)
			require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs)
			require.True(t, test.freshReceiverHits(t, built.Bundle))
		})
	}
}

// A call whose input the Cloud stores no entry of, under its recipe or in
// its class, and the recipient doesn't hold, is not sent: the recipient
// couldn't place it. It is counted, and its entry goes without it.
func TestCallRecordWithAnUnplaceableInputIsNotSent(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	// The receiver's value goes, as a prune takes it: the only entry of its
	// class stores no record any more.
	cloud.egraphMu.Lock()
	receiver := cloud.resultsByID[sharedResultID(test.receiverNumber)]
	_, err := cloud.dropValueLocked(test.ctx, receiver)
	cloud.egraphMu.Unlock()
	require.NoError(t, err)
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls)
	require.Equal(t, 1, built.UnsendableCalls)
	require.Equal(t, []uint64{test.number}, built.Partial)
}

// A call whose input's class holds no stored entry is not placed, and the
// recipient is taught nothing of it. Check A made its receiver, which the
// Cloud stores, and no call on it. Check B made outer on its own receiver of
// other content, which the Cloud knows only as B's engine reported it, in a
// class of its own: no entry of that class is stored. B's bundle carries
// inner without the call, and a fresh engine whose client makes a receiver
// of that other content runs outer.
func TestACallWhoseInputsClassHoldsNoStoredEntryIsNotSent(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTestKeeping(t, false)
	cloudCtx := context.Background()
	other := func(client string) *ResultCall {
		frame := receiverTestFrame(client)
		frame.ExtraDigests = []call.ExtraDigest{{Digest: digest.FromString("other content"), Label: call.ExtraDigestLabelContent}}
		return frame
	}
	otherCall := func(client string) *ResultCall {
		frame := callRecordTestFrame("outer")
		frame.Receiver = &ResultCallRef{Call: other(client)}
		return frame
	}
	inner, err := test.inner.cacheSharedResult().loadResultCall().deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	receiverB, err := other("b").deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	callB := otherCall("b")
	requestB, err := callB.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	selfB, _, err := callB.selfDigestAndInputRefs(test.producer)
	require.NoError(t, err)
	pb, err := callB.callPB(test.producer)
	require.NoError(t, err)
	encodedB, err := pb.Encode()
	require.NoError(t, err)
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 1},
		RemoteHolding{Recipe: receiverB, Field: "receiver", ContentDigest: digest.FromString("other content")})
	require.NoError(t, err)
	_, err = test.cloud.AttachRemoteHolding(cloudCtx, HolderKey{Cache: "cache-b", Number: 2},
		RemoteHolding{Recipe: inner, Request: requestB, Field: "outer", Call: encodedB, Term: &RemoteTerm{Self: selfB}})
	require.NoError(t, err)

	held := &HeldBy{Calls: func(r digest.Digest) bool { return r == requestB }}
	built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	sizes, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
	require.Empty(t, built.Bundle.Calls)
	require.Empty(t, built.CallInputs, "A's receiver, of another class, doesn't go")
	require.Equal(t, 1, built.UnsendableCalls)
	require.Equal(t, []uint64{test.number}, built.Partial)

	ctx, fresh, srv := transferTestCache(t)
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: other("c"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "other receiver"}, other("c"))
		})
	require.NoError(t, err)
	_, err = fresh.MergeValues(ctx, CloudCacheID, built.Bundle)
	require.NoError(t, err)
	ran := false
	_, err = fresh.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: otherCall("c"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "ran"}, otherCall("c"))
		})
	require.NoError(t, err)
	require.True(t, ran, "nothing placed outer for the fresh engine")
}

// Two engines made the same call on their own equivalent receivers, A and B,
// and the Cloud stores both. It keeps A's call alone, the term's first, so a
// recipient that holds B gets A's call with A's receiver added, and still
// hits it by its term: one call of a term costs such a recipient A's record,
// where keeping a call for each stored receiver would grow the entry with
// every session's.
func TestCallRecordOfATermIsSentWithItsOwnInputToARecipientHoldingAnother(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	test.attachCallOnB(t, true, 0)
	requestA, err := test.frame.deriveRecipeDigest(test.producer)
	require.NoError(t, err)
	require.Equal(t, []digest.Digest{requestA}, keptCallRequests(cloud, test.number))

	// The recipient holds B.
	recipeB, err := receiverTestFrame("b").deriveRecipeDigest(nil)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-fresh", Number: 1}, RemoteHolding{Recipe: recipeB, Request: recipeB, Field: "receiver"})
	require.NoError(t, err)
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), &HeldBy{Cache: "cache-fresh"})
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs, "with A's receiver")
	require.True(t, test.freshReceiverHits(t, built.Bundle))
}

// A call a bundle places by a row its recipient holds names that row and
// carries no copy of its record, and the row is no root. A recipient that no
// longer holds the input, as when a set file is restored after the input
// expired, merges the bundle all the same: the call goes untaught, and the
// entry lands. The recipient holds A, the kept call's own receiver.
func TestACallPlacedByARowItsRecipientNoLongerHoldsGoesUntaught(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	recipeA, err := receiverTestFrame("a").deriveRecipeDigest(nil)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-fresh", Number: 1}, RemoteHolding{Recipe: recipeA, Request: recipeA, Field: "receiver"})
	require.NoError(t, err)
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), &HeldBy{Cache: "cache-fresh"})
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	heldA := built.Bundle.Calls[0].Inputs
	require.Len(t, heldA, 1, "the call names the row that places its receiver")
	require.NotEmpty(t, built.Bundle.Values[heldA[0]-1].Held, "a held row: the recipient's own A, no copy of its record")
	for _, root := range built.Bundle.Roots {
		require.NotEqual(t, heldA[0], root.Ordinal, "a held row a call alone names is no root")
	}
	require.Empty(t, built.CallInputs)

	fctx, fresh, fsrv := transferTestCache(t)
	_, err = fresh.MergeValues(fctx, CloudCacheID, built.Bundle)
	require.NoError(t, err, "a held row the call alone names is never stale")
	landed, err := fresh.GetOrInitCall(fctx, "test-session", fsrv, &CallRequest{ResultCall: callRecordTestFrame("inner"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			t.Fatal("inner did not land")
			return nil, nil
		})
	require.NoError(t, err)
	require.True(t, landed.HitCache())
}

// A call's input the bundle carries below another call's input still travels
// as a root of its own, so that it lands if the other expires first. inner
// was returned by f on X and by g on Y, and X depends on Y: whichever call
// the bundle places first carries Y, and both X and Y are roots. StoredClosure
// lists the same call inputs as the bundle.
func TestACallInputBelowAnotherCallsInputIsARootOfItsOwn(t *testing.T) {
	t.Parallel()
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	ctx, producer, srv := transferTestCache(t)
	receiver := func(field, content string) *ResultCall {
		frame := callRecordTestFrame(field)
		frame.ExtraDigests = []call.ExtraDigest{{Digest: digest.FromString(content), Label: call.ExtraDigestLabelContent}}
		return frame
	}
	on := func(field string, recv *ResultCall) *ResultCall {
		frame := callRecordTestFrame(field)
		frame.Receiver = &ResultCallRef{Call: recv}
		return frame
	}
	made := func(frame *ResultCall, text string) AnyResult {
		res, err := producer.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true},
			func(context.Context) (AnyResult, error) {
				return NewResultForCall(&transferTestValue{Text: text}, frame)
			})
		require.NoError(t, err)
		return res
	}
	inner := persistedListTestResult(t, ctx, producer, srv, "inner", &transferTestValue{Text: "inner"})
	x := made(receiver("x", "x content"), "x")
	y := made(receiver("y", "y content"), "y")
	require.NoError(t, producer.AddExplicitDependency(ctx, x, y, "x reads y"))
	f, g := on("f", receiver("x", "x content")), on("g", receiver("y", "y content"))
	for _, frame := range []*ResultCall{f, g} {
		_, err := producer.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true},
			func(context.Context) (AnyResult, error) { return inner, nil })
		require.NoError(t, err)
	}
	numbers := storedPersistenceTestMerge(t, cloudCtx, cloud, exportTestBundle(t, ctx, producer, inner, x, y))
	require.Len(t, numbers, 3)
	byField := map[string]uint64{}
	for _, number := range numbers {
		byField[storedTestFrame(t, cloud, storedBundleTestEntry(cloud, number)).Field] = number
	}
	for _, frame := range []*ResultCall{f, g} {
		test := &callRecordTest{ctx: ctx, producer: producer, frame: frame, inner: inner, cloud: cloud}
		test.attach(t, cloudCtx, test.span(t, true, 0))
	}

	built, err := cloud.StoredBundle(ctx, []uint64{byField["inner"]}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 2)
	require.Equal(t, []uint64{byField["x"], byField["y"]}, built.CallInputs, "f(X) is placed first, carrying Y below X, and Y is still a root of its own")
	roots := map[TransferOrdinal]bool{}
	for _, root := range built.Bundle.Roots {
		roots[root.Ordinal] = true
	}
	require.True(t, roots[bundleRowOf(t, built.Bundle, byField["y"])], "Y is a root, though X's closure carries it")
	closure, err := cloud.StoredClosure(ctx, []uint64{byField["inner"]}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, built.CallInputs, closure.CallInputs, "StoredClosure lists the bundle's call inputs")
	sizes, err := cloud.StoredSizes(ctx, []uint64{byField["inner"]}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, len(built.Bundle.Values), sizes[0].Records, "priced as built")
}

// Keeping a call on a stored entry, or giving it a call's expiry, is a change
// a save writes; a second report of the same call is not.
func TestCallRecordReportsWhatASaveWrites(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	key := HolderKey{Cache: "cache-a", Number: uint64(test.inner.cacheSharedResult().id)}
	change, err := cloud.AttachRemoteHolding(ctx, key, test.span(t, true, 0))
	require.NoError(t, err)
	require.True(t, change.Saved, "a new call")
	change, err = cloud.AttachRemoteHolding(ctx, key, test.span(t, true, 0))
	require.NoError(t, err)
	require.False(t, change.Saved, "the same call again")
}

// A call span with an expiry already past reports a call that has expired:
// read again, or read late from a client the Cloud hasn't seen, it neither
// keeps its call nor moves back the expiry of a fresh value a later export
// stored. A completion with an expiry to come still gives the entry its
// expiry.
func TestCallRecordPastExpiriesLeaveAReplacementAlone(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	old := test.span(t, true, time.Now().Add(-time.Hour).Unix())
	test.attach(t, ctx, old)
	entry := storedBundleTestEntry(cloud, test.number)
	// The value expires, and another engine exports a fresh one.
	cloud.egraphMu.Lock()
	entry.expiresAtUnix = time.Now().Add(-time.Minute).Unix()
	cloud.egraphMu.Unlock()
	bctx, b, srv := transferTestCache(t)
	frame := callRecordTestFrame("inner")
	refreshed, err := b.GetOrInitCall(bctx, "test-session", srv,
		&CallRequest{ResultCall: frame, IsPersistable: true, TTL: 3600},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "refreshed"}, frame)
		})
	require.NoError(t, err)
	_, err = cloud.MergeValues(ctx, "cache-b", exportTestBundle(t, bctx, b, refreshed))
	require.NoError(t, err)
	expiry := func() int64 {
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		return entry.expiresAtUnix
	}
	expires := expiry()
	require.Greater(t, expires, time.Now().Unix(), "the fresh value")

	// The reader reads the old span again.
	test.attach(t, ctx, old)
	require.Equal(t, expires, expiry())

	// A client the Cloud hasn't seen reports, late, a call of the old value.
	other := callRecordTestFrame("outer")
	other.ImplicitInputs = []*ResultCallArg{{Name: "cachePerClient", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "late-client"}}}
	late := test.span(t, true, time.Now().Add(-time.Hour).Unix())
	pb, err := other.callPB(b)
	require.NoError(t, err)
	late.Request = digest.Digest(pb.Digest)
	late.Call, err = pb.Encode()
	require.NoError(t, err)
	test.attach(t, ctx, late)
	cloud.egraphMu.RLock()
	kept := len(entry.keptCallsLocked())
	cloud.egraphMu.RUnlock()
	require.Zero(t, kept, "neither expired call is kept")
	require.Equal(t, expires, expiry(), "its past expiry says nothing of the fresh value")

	// The first client completes the call again, with an earlier expiry.
	renewed := time.Now().Add(time.Minute).Unix()
	test.attach(t, ctx, test.span(t, true, renewed))
	require.Equal(t, renewed, expiry(), "a new completion")
	// And once more, sooner: a call already kept still reports completions.
	sooner := time.Now().Add(30 * time.Second).Unix()
	test.attach(t, ctx, test.span(t, true, sooner))
	require.Equal(t, sooner, expiry(), "a new completion of a kept call")
}

// The first span of a call that has already expired, read late, gives no
// engine the call: a fresh engine runs it.
func TestCallRecordLateFirstCallStaysExpired(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	test.attach(t, ctx, test.span(t, true, time.Now().Add(-time.Second).Unix()))
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls)
	require.False(t, test.freshHits(t, built.Bundle))
}

// A call that reaches an engine which already has the entry, in full or as a
// held row, gives the entry its expiry there too: once that passes, the call
// runs again, as it would where it was made.
func TestCallRecordExpiresOnAnEntryTheEngineHad(t *testing.T) {
	t.Parallel()
	for name, held := range map[string]bool{"full row": false, "held row": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cloud := storedPartTestCache(t, WithBlobStore())
			test := newCallRecordTest(t, ctx, cloud)
			initial, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
			require.NoError(t, err)
			freshCtx, fresh, srv := transferTestCache(t)
			_, err = fresh.MergeValues(freshCtx, CloudCacheID, initial.Bundle)
			require.NoError(t, err)
			var heldBy *HeldBy
			if held {
				own := test.span(t, false, 0)
				own.Request = own.Recipe
				_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "fresh", Number: 1}, own)
				require.NoError(t, err)
				heldBy = &HeldBy{Cache: "fresh"}
			}
			expires := time.Now().Add(2 * time.Second).Unix()
			test.attach(t, ctx, test.span(t, true, expires))
			built, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), heldBy)
			require.NoError(t, err)
			require.Len(t, built.Bundle.Calls, 1)
			if held {
				require.Len(t, built.Held, 1)
			}
			_, err = fresh.MergeValues(freshCtx, CloudCacheID, built.Bundle)
			require.NoError(t, err)
			before, err := fresh.GetOrInitCall(freshCtx, "test-session", srv,
				&CallRequest{ResultCall: test.frame.clone(), IsPersistable: true},
				func(context.Context) (AnyResult, error) {
					t.Fatal("the call was not taught")
					return nil, nil
				})
			require.NoError(t, err)
			require.True(t, before.HitCache())
			time.Sleep(time.Until(time.Unix(expires+1, 0)))
			ran := false
			_, err = fresh.GetOrInitCall(freshCtx, "test-session", srv,
				&CallRequest{ResultCall: test.frame.clone(), IsPersistable: true},
				func(context.Context) (AnyResult, error) {
					ran = true
					return NewResultForCall(&transferTestValue{Text: "ran again"}, test.frame)
				})
			require.NoError(t, err)
			require.True(t, ran, "the call runs again once its expiry passes")
		})
	}
}

// A bundle can carry, below a live root, a row whose Cloud value has expired,
// with its calls. An engine that has its own unexpired value of that recipe
// keeps it, with its own expiry, and isn't taught the calls, which have
// expired: it runs the call again, as a local engine does after its TTL.
func TestCallRecordOnAnExpiredRowLeavesTheEnginesValueAlone(t *testing.T) {
	t.Parallel()
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	ctx, producer, srv := transferTestCache(t)
	frame := callRecordTestFrame("outer")
	inner := persistedListTestResult(t, ctx, producer, srv, "inner", &transferTestValue{Text: "inner"})
	_, err := producer.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true},
		func(context.Context) (AnyResult, error) { return inner, nil })
	require.NoError(t, err)
	parent := persistedListTestResult(t, ctx, producer, srv, "parent", &transferTestValue{Text: "parent"})
	transferTestDependency(producer, ctx, parent, inner)
	numbers := storedPersistenceTestMerge(t, cloudCtx, cloud, exportTestBundle(t, ctx, producer, parent))
	require.Len(t, numbers, 1)
	test := &callRecordTest{ctx: ctx, producer: producer, frame: frame, inner: inner, cloud: cloud}
	test.attach(t, cloudCtx, test.span(t, true, 0))
	// The Cloud's inner expires; its live parent still carries it.
	var innerNumber uint64
	cloud.egraphMu.Lock()
	for id, res := range cloud.resultsByID {
		if call, _ := res.entryFrameLocked().decode(); call != nil && call.Field == "inner" {
			res.expiresAtUnix = time.Now().Add(-time.Minute).Unix()
			innerNumber = uint64(id)
		}
	}
	cloud.egraphMu.Unlock()
	require.NotZero(t, innerNumber)
	built, err := cloud.StoredBundle(cloudCtx, numbers, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)

	// A fresh engine made its own inner minutes ago.
	freshCtx, fresh, freshSrv := transferTestCache(t)
	own := persistedListTestResult(t, freshCtx, fresh, freshSrv, "inner", &transferTestValue{Text: "inner"})
	fresh.egraphMu.RLock()
	before := own.cacheSharedResult().expiresAtUnix
	fresh.egraphMu.RUnlock()
	_, err = fresh.MergeValues(freshCtx, CloudCacheID, built.Bundle)
	require.NoError(t, err)
	fresh.egraphMu.RLock()
	after := own.cacheSharedResult().expiresAtUnix
	fresh.egraphMu.RUnlock()
	require.Equal(t, before, after, "the engine's own value keeps its expiry")
	ran := false
	_, err = fresh.GetOrInitCall(freshCtx, "test-session", freshSrv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return own, nil
		})
	require.NoError(t, err)
	require.True(t, ran, "the expired call isn't taught")
	ownHit, err := fresh.GetOrInitCall(freshCtx, "test-session", freshSrv, &CallRequest{ResultCall: callRecordTestFrame("inner"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			t.Fatal("the engine's own value expired")
			return nil, nil
		})
	require.NoError(t, err)
	require.True(t, ownHit.HitCache())
}

// An engine imported a value before a call gave it a TTL; once the TTL has
// passed, a bundle that carries the value below a live root, with the call,
// doesn't teach the engine the call over the value it kept: it runs the call.
func TestCallRecordExpiredCallIsNotTaughtOnAValueTheEngineKept(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	pctx, producer, producerSrv := transferTestCache(t)
	frame := callRecordTestFrame("outer")
	inner := persistedListTestResult(t, pctx, producer, producerSrv, "inner", &transferTestValue{Text: "inner"})
	_, err := producer.GetOrInitCall(pctx, "test-session", producerSrv,
		&CallRequest{ResultCall: frame, IsPersistable: true},
		func(context.Context) (AnyResult, error) { return inner, nil })
	require.NoError(t, err)
	stored := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, pctx, producer, inner))
	require.Len(t, stored, 1)
	test := &callRecordTest{ctx: pctx, producer: producer, frame: frame, inner: inner, cloud: cloud, number: stored[0]}
	initial, err := cloud.StoredBundle(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	fctx, fresh, srv := transferTestCache(t)
	_, err = fresh.MergeValues(fctx, CloudCacheID, initial.Bundle)
	require.NoError(t, err)
	// A live parent keeps the inner row in its closure after the outer TTL.
	parent := persistedListTestResult(t, test.ctx, test.producer, producerSrv, "parent", &transferTestValue{Text: "parent"})
	transferTestDependency(test.producer, test.ctx, parent, test.inner)
	numbers := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, test.ctx, test.producer, parent))
	require.Len(t, numbers, 1)
	expires := time.Now().Add(2 * time.Second).Unix()
	test.attach(t, ctx, test.span(t, true, expires))
	time.Sleep(time.Until(time.Unix(expires+1, 0)))
	built, err := cloud.StoredBundle(ctx, numbers, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	_, err = fresh.MergeValues(fctx, CloudCacheID, built.Bundle)
	require.NoError(t, err)
	ran := false
	_, err = fresh.GetOrInitCall(fctx, "test-session", srv,
		&CallRequest{ResultCall: test.frame.clone(), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			ran = true
			return NewResultForCall(&transferTestValue{Text: "reran"}, test.frame)
		})
	require.NoError(t, err)
	require.True(t, ran, "a call whose TTL passed must not be taught onto the original never-expiring copy")
}
