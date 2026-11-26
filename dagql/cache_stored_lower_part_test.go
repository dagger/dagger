package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// splitPartTest is a Cloud that stores R over T over inner, and A, with
// cache-a's call outer on its receiver A, returning inner, kept on inner: a
// bundle of R carries A for that call, as a root of its own.
type splitPartTest struct {
	*receiverCallTest
	cloudCtx context.Context
	// root, top and inner are the Cloud's numbers for R, T and inner.
	root, top uint64
}

func newSplitPartTest(t *testing.T) *splitPartTest {
	t.Helper()
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	ctx, producer, srv := transferTestCache(t)
	test := &splitPartTest{receiverCallTest: &receiverCallTest{callRecordTest: &callRecordTest{ctx: ctx, producer: producer, frame: receiverTestCall("a"), cloud: cloud}, srv: srv}, cloudCtx: cloudCtx}
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
	top := persistedListTestResult(t, ctx, producer, srv, "top", &transferTestValue{Text: "top"})
	transferTestDependency(producer, ctx, top, test.inner)
	root := persistedListTestResult(t, ctx, producer, srv, "root", &transferTestValue{Text: "root"})
	transferTestDependency(producer, ctx, root, top)
	reply, err := cloud.MergeValues(cloudCtx, "cache-a", exportTestBundle(t, ctx, producer, root, receiver))
	require.NoError(t, err)
	for _, value := range reply.Values {
		switch storedTestFrame(t, cloud, storedBundleTestEntry(cloud, value.Number)).Field {
		case "root":
			test.root = value.Number
		case "top":
			test.top = value.Number
		case "inner":
			test.number = value.Number
		case "receiver":
			test.receiverNumber = value.Number
		}
	}
	require.NotZero(t, test.root)
	require.NotZero(t, test.top)
	require.NotZero(t, test.number)
	require.NotZero(t, test.receiverNumber)
	test.attach(t, cloudCtx, test.span(t, true, 0))
	return test
}

// recipient is an engine cache whose client "b" makes its own receiver, as
// freshReceiverHits makes one, that merges the Cloud's bundles in turn; the
// Cloud records each entry it lands as a holding of cache, as the engine's
// spans would report it.
type splitPartRecipient struct {
	test   *splitPartTest
	cache  CacheID
	ctx    context.Context
	engine *Cache
	srv    *Server
	// landed are the Cloud entries whose records the engine merged, and
	// numbers the engine's entry for each Cloud entry a merge sent it.
	landed  []uint64
	numbers map[uint64]uint64
}

func (test *splitPartTest) recipient(t *testing.T, cache CacheID) *splitPartRecipient {
	t.Helper()
	ctx, engine, srv := transferTestCache(t)
	_, err := engine.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: receiverTestFrame("b"), IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame("b"))
		})
	require.NoError(t, err)
	return &splitPartRecipient{test: test, cache: cache, ctx: ctx, engine: engine, srv: srv, numbers: map[uint64]uint64{}}
}

// merge merges the bundle, which must commit, and records what it landed.
func (r *splitPartRecipient) merge(t *testing.T, bundle ValueBundle) {
	t.Helper()
	reply, err := r.engine.MergeValues(r.ctx, CloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	for _, value := range reply.Values {
		sent := bundle.Values[value.Ordinal-1]
		heldTestHold(t, r.test.cloudCtx, r.test.cloud, r.cache, value.Number, sent.SenderNumber, 0)
		r.numbers[sent.SenderNumber] = value.Number
		if sent.Held == "" {
			r.landed = append(r.landed, sent.SenderNumber)
		}
	}
}

// hits reports whether client b's call outer on its own receiver hits the
// engine's inner. A miss runs nothing that the engine keeps.
func (r *splitPartRecipient) hits(t *testing.T) bool {
	t.Helper()
	res, err := r.engine.GetOrInitCall(r.ctx, "test-session", r.srv, &CallRequest{ResultCall: receiverTestCall("b"), IsPersistable: true},
		func(context.Context) (AnyResult, error) { return nil, errors.New("the call missed") })
	if err != nil {
		return false
	}
	number, err := r.engine.PersistedResultID(res)
	require.NoError(t, err)
	require.Equal(t, r.numbers[r.test.number], number, "the call hits the engine's inner")
	return true
}

// bundle builds a bundle of roots for the recipient and checks that its
// price agrees with it.
func (r *splitPartRecipient) bundle(t *testing.T, roots []uint64, held *HeldBy) ValueBundle {
	t.Helper()
	now := time.Now().Unix()
	built, err := r.test.cloud.StoredBundle(r.test.cloudCtx, roots, now, held)
	require.NoError(t, err)
	sizes, err := r.test.cloud.StoredSizes(r.test.cloudCtx, roots, now, held)
	require.NoError(t, err)
	records := 0
	for _, size := range sizes {
		records += size.Records
	}
	require.Equal(t, len(built.Bundle.Values), records, "priced as built")
	return built.Bundle
}

// A split's parts and its root's own merge leave the recipient with what one
// merge of the root gives it: every record, and every call kept on them. R's
// bundle carries R, T and inner, and A for inner's call outer on A, as a root
// of its own. Its lower part, cut so that R's merge carries A within the
// limit, is T over inner. T goes first, as a part of R's merge, which can't
// place inner's call: A goes with R. R's own merge then walks through T,
// held, to inner, and sends inner's call with A, inner going as a held row
// the call alone names. A recipient so merged holds every record and hits
// the call on its own receiver, as one that merged R's whole bundle does.
// R's merge without the walk through, as a held T stood for all below it,
// would leave the call out.
func TestASplitsPartsAndItsRootsMergeGiveWhatOneMergeGives(t *testing.T) {
	t.Parallel()
	test := newSplitPartTest(t)
	cloud, ctx, now := test.cloud, test.cloudCtx, time.Now().Unix()

	one := test.recipient(t, "cache-one")
	whole := one.bundle(t, []uint64{test.root}, nil)
	require.Len(t, whole.Calls, 1)
	one.merge(t, whole)
	require.True(t, one.hits(t), "one merge of R teaches the call")

	// The limit R's merge carries A within, beside R, the call and the held
	// rows of T and inner, and not T: each record's size is what StoredSizes
	// adds for it, in the order that adds each alone.
	order := []uint64{test.receiverNumber, test.number, test.top, test.root}
	sizes, err := cloud.StoredSizes(ctx, order, now, nil)
	require.NoError(t, err)
	size := func(i int) int64 { return sizes[i].Bytes + sizes[i].OutputBytes }
	unlimited, err := cloud.StoredLowerPart(ctx, test.root, now, nil, 1<<40)
	require.NoError(t, err)
	require.Empty(t, unlimited.Lower, "within the limit, nothing goes ahead")
	// R's record names T's held row, and inner's call inner's.
	cloud.egraphMu.RLock()
	row := heldRowSizeLocked(storedBundleTestEntry(cloud, test.top)) + heldRowSizeLocked(storedBundleTestEntry(cloud, test.number))
	cloud.egraphMu.RUnlock()
	limit := size(0) + size(3) + unlimited.CallBytes + row
	lower, err := cloud.StoredLowerPart(ctx, test.root, now, nil, limit)
	require.NoError(t, err)
	require.Equal(t, []uint64{test.top}, lower.Lower, "T, over inner, goes ahead")
	part := []uint64{test.top, test.number}
	slices.Sort(part)
	require.Equal(t, part, lower.Part)
	require.Equal(t, 4, lower.Carried)

	parts := &HeldBy{Cache: "cache-split", Parts: func(root uint64) bool { return root == test.top }}
	alone, err := cloud.StoredSizesAlone(ctx, lower.Lower, now, parts)
	require.NoError(t, err)
	priced, err := cloud.StoredSizes(ctx, lower.Lower, now, parts)
	require.NoError(t, err)
	require.Equal(t, priced, alone, "a part priced alone as StoredSizes prices it")

	through := func(number uint64) bool {
		_, found := slices.BinarySearch(lower.Part, number)
		return found
	}
	for _, walkThrough := range []bool{true, false} {
		split := test.recipient(t, "cache-split")
		if !walkThrough {
			split = test.recipient(t, "cache-held")
			parts = &HeldBy{Cache: "cache-held", Parts: parts.Parts}
		}
		part := split.bundle(t, lower.Lower, parts)
		require.Empty(t, part.Calls, "the part can't place inner's call: A goes with R")
		split.merge(t, part)
		resume := &HeldBy{Cache: split.cache}
		if walkThrough {
			resume.Through = through
		}
		root := split.bundle(t, []uint64{test.root}, resume)
		split.merge(t, root)
		if !walkThrough {
			require.Empty(t, root.Calls, "a held T stands for inner, and its call")
			require.NotContains(t, split.landed, test.receiverNumber, "nor A, which only inner's call carries")
			require.False(t, split.hits(t))
			continue
		}
		require.ElementsMatch(t, one.landed, split.landed, "every record lands")
		require.Len(t, root.Calls, 1, "inner's call")
		require.NotEmpty(t, heldTestRow(t, root, test.number).Held, "inner goes as a held row the call alone names")
		require.NotEmpty(t, heldTestRow(t, root, test.top).Held, "T as a held row R's record names")
		require.True(t, split.hits(t), "the call is taught as one merge teaches it")
	}

	// The calls the merge carries beside its records, as the message encodes
	// them, are counted no less than they encode.
	call, err := json.Marshal(whole.Calls[0])
	require.NoError(t, err)
	require.GreaterOrEqual(t, unlimited.CallBytes, int64(len(call)))
}

// A call kept on an entry the recipient held before the split goes with the
// root's merge too. The recipient holds inner, without A or inner's call, as
// a merge that sent no call left it. R's lower part is then T alone: T goes
// as a part, which reaches inner as a held row and can't place inner's
// call. R's merge walks through T to inner, which it reaches as the held row
// it is, and sends inner's call with A, as one merge of R would.
func TestASplitRootsMergeSendsTheCallsOfWhatItsPartsReachHeld(t *testing.T) {
	t.Parallel()
	test := newSplitPartTest(t)
	cloud, ctx, now := test.cloud, test.cloudCtx, time.Now().Unix()
	split := test.recipient(t, "cache-held-below")
	split.merge(t, split.bundle(t, []uint64{test.number}, &HeldBy{Cache: split.cache, Calls: func(digest.Digest) bool { return false }}))
	require.False(t, split.hits(t), "inner, held without its call")
	held := &HeldBy{Cache: split.cache}

	order := []uint64{test.receiverNumber, test.number, test.top, test.root}
	sizes, err := cloud.StoredSizes(ctx, order, now, nil)
	require.NoError(t, err)
	size := func(i int) int64 { return sizes[i].Bytes + sizes[i].OutputBytes }
	unlimited, err := cloud.StoredLowerPart(ctx, test.root, now, held, 1<<40)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	rowTop := heldRowSizeLocked(storedBundleTestEntry(cloud, test.top))
	rowInner := heldRowSizeLocked(storedBundleTestEntry(cloud, test.number))
	cloud.egraphMu.RUnlock()
	limit := size(0) + size(3) + unlimited.CallBytes + rowTop + rowInner
	lower, err := cloud.StoredLowerPart(ctx, test.root, now, held, limit)
	require.NoError(t, err)
	require.Equal(t, []uint64{test.top}, lower.Lower)
	require.Equal(t, []uint64{test.top}, lower.Part, "inner, held, isn't part of it")

	part := split.bundle(t, lower.Lower, &HeldBy{Cache: split.cache, Parts: func(root uint64) bool { return root == test.top }})
	require.Empty(t, part.Calls, "T's part can't place inner's call")
	split.merge(t, part)
	root := split.bundle(t, []uint64{test.root}, &HeldBy{Cache: split.cache, Through: func(number uint64) bool {
		_, found := slices.BinarySearch(lower.Part, number)
		return found
	}})
	require.Len(t, root.Calls, 1, "inner's call")
	require.NotEmpty(t, heldTestRow(t, root, test.number).Held, "inner as a held row the call names")
	split.merge(t, root)
	require.Contains(t, split.landed, test.receiverNumber, "A, for the call")
	require.True(t, split.hits(t), "the call is taught")
}
