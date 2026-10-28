package dagql

import (
	"context"
	"path/filepath"
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
	ctx, producer, srv := transferTestCache(t)
	test := &callRecordTest{ctx: ctx, producer: producer, frame: callRecordTestFrame("outer"), cloud: cloud}
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
			built, err := cloud.StoredBundle(ctx, []uint64{test.number}, nil)
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
	candidates := cloud.ReleaseRemoteCache(ctx, "cache-a")
	_, err := cloud.CollectRemoteHoldings(ctx, candidates)
	require.NoError(t, err)
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, nil)
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
	stored, err := cloud.SetStoredPart(ctx, test.number, storedBenchPart(1), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, nil)
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
	built, err := cloud.StoredBundle(ctx, []uint64{test.number}, nil)
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
	calls := len(entry.calls)
	cloud.egraphMu.RUnlock()
	require.Equal(t, 1, calls)
}

// A span whose encoded call isn't the call it reports is refused.
func TestCallRecordRefusesAMismatchedCall(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, ctx, cloud)
	desc := test.span(t, true, 0)
	desc.Request = digest.FromString("another request")
	key := HolderKey{Cache: "cache-a", Number: uint64(test.inner.cacheSharedResult().id)}
	_, err := cloud.AttachRemoteHolding(ctx, key, desc)
	require.ErrorContains(t, err, "encodes call")
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
	receiver AnyResult
	// receiverNumber is the Cloud's number for A.
	receiverNumber uint64
}

func newReceiverCallTest(t *testing.T) *receiverCallTest {
	t.Helper()
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	ctx, producer, srv := transferTestCache(t)
	test := &receiverCallTest{callRecordTest: &callRecordTest{ctx: ctx, producer: producer, frame: receiverTestCall("a"), cloud: cloud}}
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
		if storedBundleTestEntry(cloud, number).loadResultCall().Field == "receiver" {
			test.receiverNumber = number
		} else {
			test.number = number
		}
	}
	test.attach(t, cloudCtx, test.span(t, true, 0))
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
			built, err := test.cloud.StoredBundle(test.ctx, roots(test), nil)
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

// A call whose input the Cloud doesn't store, and the recipient doesn't hold,
// is not sent: the recipient couldn't place it. It is counted.
func TestCallRecordWithAnUnplaceableInputIsNotSent(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	cloud.egraphMu.Lock()
	receiver := cloud.resultsByID[sharedResultID(test.receiverNumber)]
	cloud.unindexRecipesLocked(receiver)
	cloud.egraphMu.Unlock()
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, nil)
	require.NoError(t, err)
	require.Empty(t, built.Bundle.Calls)
	require.Equal(t, 1, built.UnsendableCalls)
}

// Two engines made the same call on their own equivalent receivers, A and B,
// and the Cloud stores both and keeps both calls. For a recipient that holds
// B, the bundle sends the call on B, which it can place without anything
// added; f(A) alone would teach it nothing about B.
func TestCallRecordPrefersAnInputTheRecipientHolds(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	// cache-b's export of its receiver B and its call on it.
	bctx, b, bsrv := transferTestCache(t)
	inner := persistedListTestResult(t, bctx, b, bsrv, "inner", &transferTestValue{Text: "inner"})
	receiverB, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: receiverTestFrame("b"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("b"))
		})
	require.NoError(t, err)
	callB := receiverTestCall("b")
	_, err = b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: callB, IsPersistable: true},
		func(context.Context) (AnyResult, error) { return inner, nil })
	require.NoError(t, err)
	reply, err := cloud.MergeValues(test.ctx, "cache-b", exportTestBundle(t, bctx, b, inner, receiverB))
	require.NoError(t, err)
	require.Len(t, reply.Imported(), 2)
	bTest := &callRecordTest{ctx: bctx, producer: b, frame: callB, inner: inner, cloud: cloud}
	desc := bTest.span(t, true, 0)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: uint64(inner.cacheSharedResult().id)}, desc)
	require.NoError(t, err)
	entry := storedBundleTestEntry(cloud, test.number)
	cloud.egraphMu.RLock()
	require.Len(t, entry.calls, 2)
	cloud.egraphMu.RUnlock()

	// The recipient holds B.
	recipeB, err := receiverTestFrame("b").deriveRecipeDigest(nil)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-fresh", Number: 1}, RemoteHolding{Recipe: recipeB, Request: recipeB, Field: "receiver"})
	require.NoError(t, err)
	built, err := cloud.StoredBundle(test.ctx, []uint64{test.number}, &HeldBy{Cache: "cache-fresh", Now: time.Now().Unix()})
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.Empty(t, built.CallInputs)
	encodedB, err := func() (string, error) {
		pb, err := callB.callPB(b)
		if err != nil {
			return "", err
		}
		return pb.Encode()
	}()
	require.NoError(t, err)
	require.Equal(t, encodedB, built.Bundle.Calls[0].Call)
}
