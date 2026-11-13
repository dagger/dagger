package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// storedSizesTestMeasure measures a bundle's roots as the service does: the
// values up to each root's ordinal, beyond those before it, each encoded.
func storedSizesTestMeasure(t *testing.T, bundle ValueBundle) []StoredSize {
	t.Helper()
	var sizes []StoredSize
	counted := 0
	for _, root := range bundle.Roots {
		var size StoredSize
		for ; counted < int(root.Ordinal); counted++ {
			data, err := json.Marshal(bundle.Values[counted])
			require.NoError(t, err)
			size.Records++
			size.Bytes += int64(len(data))
		}
		sizes = append(sizes, size)
	}
	return sizes
}

// storedSizesTestClose requires got to have want's records, and bytes within
// a few bytes a record of want's: a bundle numbers references by ordinal,
// StoredSizes by entry number.
func storedSizesTestClose(t *testing.T, want, got []StoredSize) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].Records, got[i].Records, "root %d records", i)
		require.InDelta(t, want[i].Bytes, got[i].Bytes, float64(8*want[i].Records), "root %d bytes", i)
	}
}

// StoredSizes measures what a stored bundle of the roots carries, root by
// root, without building it: P alone, then what R adds beyond it; the same
// roots in one bundle; and nothing for a root it lacks or one already
// measured.
func TestStoredSizesMeasureWhatTheBundleCarries(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")

	built, err := cloud.StoredBundle(ctx, []uint64{p, r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err := cloud.StoredSizes(ctx, []uint64{p, r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)
	require.Equal(t, 1, got[0].Records)
	require.Equal(t, 1, got[1].Records)

	built, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err = cloud.StoredSizes(ctx, []uint64{r, 12345, r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	want := storedSizesTestMeasure(t, built.Bundle)
	storedSizesTestClose(t, append(want, StoredSize{}, StoredSize{}), got)
	require.Equal(t, 2, got[0].Records)

	entry := storedBundleTestEntry(cloud, r)
	cloud.egraphMu.RLock()
	size := storedTestSize(entry)
	cloud.egraphMu.RUnlock()
	require.NotNil(t, size, "the record's size is kept with it")
}

// With a recipient that holds P, R's closure is R and P's held row, as the
// bundle StoredBundle builds with the same HeldBy.
func TestStoredSizesPriceHeldEntriesAsHeldRows(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	heldTestHold(t, ctx, cloud, "cache-b", 7, p, 0)
	held := &HeldBy{Cache: "cache-b"}

	built, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, []uint64{p}, built.Held)
	got, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), held)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)

	full, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, full[0].Records, got[0].Records)
	require.Less(t, got[0].Bytes, full[0].Bytes, "a held row costs less than P's record")
}

// A root whose kept call needs an input the bundle adds is priced with that
// input, as StoredBundle carries it; a recipient that holds the input is sent
// no copy of it, and the root isn't priced with it.
func TestStoredSizesPriceCallInputs(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	for name, held := range map[string]*HeldBy{
		"added": nil,
		"held":  {Cache: "cache-b"},
	} {
		if held != nil {
			heldTestHold(t, test.ctx, test.cloud, "cache-b", 7, test.receiverNumber, 0)
		}
		built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
		require.NoError(t, err)
		if held == nil {
			require.Equal(t, []uint64{test.receiverNumber}, built.CallInputs, name)
			require.Equal(t, []int{0, 0}, built.RootOwners, "the input travels with the root whose call needs it")
		} else {
			require.Empty(t, built.CallInputs, name)
		}
		var want StoredSize
		for _, value := range built.Bundle.Values {
			data, err := json.Marshal(value)
			require.NoError(t, err)
			want.Records++
			want.Bytes += int64(len(data))
		}
		got, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
		require.NoError(t, err)
		storedSizesTestClose(t, []StoredSize{want}, got)
	}
}

// Pricing chooses the calls a bundle chooses, in the order the bundle adds
// them. The Cloud stores inner, with two calls of one term kept on it, on
// cache-a's receiver A and cache-b's equivalent receiver B; C, stored after
// inner, with a call kept on it that needs one of the receivers; and R, over
// inner and C. R's bundle takes inner's calls first, finds neither receiver
// carried, and adds the receiver of the call that sorts first; then C's call
// adds its own receiver, unless that was it. Whichever sorts first, one of
// the two cases has the bundle carry both receivers. R's price is the whole
// bundle.
func TestStoredSizesChooseCallsInTheBundlesOrder(t *testing.T) {
	t.Parallel()
	for _, client := range []string{"a", "b"} {
		t.Run("C on "+client, func(t *testing.T) {
			t.Parallel()
			test := newReceiverCallTest(t)
			cloud := test.cloud
			bctx, b, bsrv := transferTestCache(t)
			call := func(frame *ResultCall, value func() (AnyResult, error)) AnyResult {
				t.Helper()
				res, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: frame, IsPersistable: true},
					func(context.Context) (AnyResult, error) { return value() })
				require.NoError(t, err)
				return res
			}
			returns := func(res AnyResult) func() (AnyResult, error) {
				return func() (AnyResult, error) { return res, nil }
			}
			receiver := func(client string) AnyResult {
				return call(receiverTestFrame(client), func() (AnyResult, error) {
					return NewResultForCall(&transferTestValue{Text: "receiver"}, receiverTestFrame(client))
				})
			}
			inner := persistedListTestResult(t, bctx, b, bsrv, "inner", &transferTestValue{Text: "inner"})
			receiverB := receiver("b")
			call(receiverTestCall("b"), returns(inner))
			receiver(client)
			c := persistedListTestResult(t, bctx, b, bsrv, "c", &transferTestValue{Text: "c"})
			callC := callRecordTestFrame("outerC")
			callC.Receiver = &ResultCallRef{Call: receiverTestFrame(client)}
			call(callC, returns(c))
			r := persistedListTestResult(t, bctx, b, bsrv, "r", &transferTestValue{Text: "r"})
			for _, dep := range []AnyResult{inner, c} {
				require.NoError(t, b.AddExplicitDependency(bctx, r, dep, "test"))
			}
			_, err := cloud.MergeValues(test.ctx, "cache-b", exportTestBundle(t, bctx, b, r))
			require.NoError(t, err)
			_, err = cloud.MergeValues(test.ctx, "cache-b", exportTestBundle(t, bctx, b, receiverB))
			require.NoError(t, err)
			for _, kept := range []*callRecordTest{
				{ctx: bctx, producer: b, frame: receiverTestCall("b"), inner: inner, cloud: cloud},
				{ctx: bctx, producer: b, frame: callC, inner: c, cloud: cloud},
			} {
				_, err := cloud.AttachRemoteHolding(test.ctx, HolderKey{Cache: "cache-b", Number: uint64(kept.inner.cacheSharedResult().id)}, kept.span(t, true, 0))
				require.NoError(t, err)
			}
			numbers := map[string]uint64{}
			cloud.egraphMu.RLock()
			for id, res := range cloud.resultsByID {
				if frame, _ := res.entryFrameLocked().decode(); frame != nil && (frame.Field == "r" || frame.Field == "c") {
					numbers[frame.Field] = uint64(id)
				}
			}
			cloud.egraphMu.RUnlock()
			require.Less(t, test.number, numbers["c"], "inner's calls come first in R's bundle")
			require.Len(t, storedBundleTestEntry(cloud, test.number).calls, 2)

			built, err := cloud.StoredBundle(test.ctx, []uint64{numbers["r"]}, time.Now().Unix(), nil)
			require.NoError(t, err)
			require.Len(t, built.Bundle.Calls, 2, "one call on inner, one on C")
			require.NotEmpty(t, built.CallInputs)
			var want StoredSize
			for _, value := range built.Bundle.Values {
				data, err := json.Marshal(value)
				require.NoError(t, err)
				want.Records++
				want.Bytes += int64(len(data))
			}
			got, err := cloud.StoredSizes(test.ctx, []uint64{numbers["r"]}, time.Now().Unix(), nil)
			require.NoError(t, err)
			storedSizesTestClose(t, []StoredSize{want}, got)
		})
	}
}

// Prices are never less than what a bundle of the first roots carries, where
// what the whole bundle carries changes which call it chooses. The Cloud
// stores P, with calls of one term kept on it on equivalent receivers X and
// Y, X's sorting first; and Q, over Y, with calls of another term kept on it
// on Z and X, Z's sorting first, Z large. X, Y and Z are each another
// engine's, so each is its own entry. Priced alone, P adds X. A bundle of P
// and Q places P's call on Y, which Q carries, adds no X, and so adds Z for
// Q's call.
func TestStoredSizesNeverPriceLessThanTheBundle(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	cloud := test.cloud
	type engine struct {
		ctx context.Context
		c   *Cache
		srv *Server
	}
	a := engine{test.ctx, test.producer, test.srv}
	b, c := engine{}, engine{}
	b.ctx, b.c, b.srv = transferTestCache(t)
	c.ctx, c.c, c.srv = transferTestCache(t)
	call := func(e engine, frame *ResultCall, value func() (AnyResult, error)) AnyResult {
		t.Helper()
		res, err := e.c.GetOrInitCall(e.ctx, "test-session", e.srv, &CallRequest{ResultCall: frame, IsPersistable: true},
			func(context.Context) (AnyResult, error) { return value() })
		require.NoError(t, err)
		return res
	}
	returns := func(res AnyResult) func() (AnyResult, error) {
		return func() (AnyResult, error) { return res, nil }
	}
	receiver := func(e engine, client, text string) AnyResult {
		return call(e, receiverTestFrame(client), func() (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: text}, receiverTestFrame(client))
		})
	}
	merge := func(e engine, cache CacheID, roots ...AnyResult) {
		t.Helper()
		_, err := cloud.MergeValues(a.ctx, cache, exportTestBundle(t, e.ctx, e.c, roots...))
		require.NoError(t, err)
	}
	keep := func(e engine, cache CacheID, frame *ResultCall, inner AnyResult) {
		t.Helper()
		kept := &callRecordTest{ctx: e.ctx, producer: e.c, frame: frame, inner: inner, cloud: cloud}
		_, err := cloud.AttachRemoteHolding(a.ctx, HolderKey{Cache: cache, Number: uint64(inner.cacheSharedResult().id)}, kept.span(t, true, 0))
		require.NoError(t, err)
	}
	callOn := func(field, client string) *ResultCall {
		frame := callRecordTestFrame(field)
		frame.Receiver = &ResultCallRef{Call: receiverTestFrame(client)}
		return frame
	}

	// P is inner, with cache-a's call on its receiver A and cache-b's on
	// its receiver B.
	inner := persistedListTestResult(t, b.ctx, b.c, b.srv, "inner", &transferTestValue{Text: "inner"})
	receivers := map[string]AnyResult{"a": test.receiver, "b": receiver(b, "b", "receiver")}
	merge(b, "cache-b", receivers["b"])
	call(b, receiverTestCall("b"), returns(inner))
	keep(b, "cache-b", receiverTestCall("b"), inner)
	alone, err := cloud.StoredBundle(a.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, alone.CallInputs, 1)
	x, y := "a", "b"
	if alone.CallInputs[0] != test.receiverNumber {
		x, y = y, x
	}
	engines := map[string]engine{"a": a, "b": b}
	ye := engines[y]

	// Z, cache-c's receiver, whose call g sorts before g on X.
	recipe := func(frame *ResultCall) digest.Digest {
		t.Helper()
		dig, err := frame.deriveRecipeDigest(ye.c)
		require.NoError(t, err)
		return dig
	}
	z := ""
	for i := 0; z == ""; i++ {
		if client := fmt.Sprint("z", i); recipe(callOn("g", client)) < recipe(callOn("g", x)) {
			z = client
		}
	}
	merge(c, "cache-c", receiver(c, z, strings.Repeat("z", 64<<10)))

	// Q, over Y, on Y's engine, with its calls g on Z and on X.
	q := persistedListTestResult(t, ye.ctx, ye.c, ye.srv, "q", &transferTestValue{Text: "q"})
	require.NoError(t, ye.c.AddExplicitDependency(ye.ctx, q, receivers[y], "test"))
	call(ye, callOn("g", z), returns(q))
	call(ye, callOn("g", x), returns(q))
	merge(ye, CacheID("cache-"+y), q)
	keep(ye, "cache-q1", callOn("g", z), q)
	keep(ye, "cache-q2", callOn("g", x), q)
	var qNumber uint64
	cloud.egraphMu.RLock()
	for id, res := range cloud.resultsByID {
		if frame, _ := res.entryFrameLocked().decode(); frame != nil && frame.Field == "q" {
			qNumber = uint64(id)
		}
	}
	cloud.egraphMu.RUnlock()
	require.Len(t, storedBundleTestEntry(cloud, qNumber).calls, 2)

	built, err := cloud.StoredBundle(a.ctx, []uint64{test.number, qNumber}, time.Now().Unix(), nil)
	require.NoError(t, err)
	var want StoredSize
	for _, value := range built.Bundle.Values {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		want.Records++
		want.Bytes += int64(len(data))
	}
	require.Greater(t, want.Bytes, int64(64<<10), "the bundle carries Z")
	require.Len(t, built.CallInputs, 2, "X for P's call, Z for Q's")
	require.Equal(t, []int{0, 1, 0, 1}, built.RootOwners, "X travels with P, Z with Q")
	got, err := cloud.StoredSizes(a.ctx, []uint64{test.number, qNumber}, time.Now().Unix(), nil)
	require.NoError(t, err)
	var priced StoredSize
	for _, size := range got {
		priced.Records += size.Records
		priced.Bytes += size.Bytes
	}
	require.GreaterOrEqual(t, priced.Records, want.Records)
	require.GreaterOrEqual(t, priced.Bytes+int64(8*want.Records), want.Bytes, "a size differs from the bundle's encoding by the digits of references")
}

// A root that a call of an earlier root needs travels with that root, as
// its price says: the Cloud stores inner, with a call kept on it on the
// receiver A, and A; a bundle of inner and A chooses inner's call before
// A's closure is carried, adds A for it, and counts A with inner. Each
// root's price is what travels with it.
func TestStoredBundleCarriesALaterRootWithTheCallThatNeedsIt(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	roots := []uint64{test.number, test.receiverNumber}
	built, err := test.cloud.StoredBundle(test.ctx, roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1)
	require.Empty(t, built.CallInputs, "A is a root it was asked for")
	require.Equal(t, []int{0, 0}, built.RootOwners, "A travels with inner, whose call needs it")
	want := make([]StoredSize, len(roots))
	for _, value := range built.Bundle.Values {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		want[0].Records++
		want[0].Bytes += int64(len(data))
	}
	got, err := test.cloud.StoredSizes(test.ctx, roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	storedSizesTestClose(t, want, got)
}

// A stored part that names another entry adds a dependency to its entry
// without replacing the record: the entry's size is measured again.
func TestStoredSizesMeasureAgainAfterAPartAddsADependency(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	_, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)

	source, _ := mergeTestSource(t, "sizes-owner", "owner")
	reply, err := cloud.MergeValues(ctx, "cache-b", source)
	require.NoError(t, err)
	q := reply.Imported()[0].ResultID
	part := testLiveOffer()
	part.Owner.DependencyIDs = []uint64{q}
	stored, _, err := cloud.SetStoredPart(ctx, r, part, 0)
	require.NoError(t, err)
	require.True(t, stored)

	built, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)
	require.Equal(t, 3, got[0].Records, "R, P and the part's owner")

	entry := storedBundleTestEntry(cloud, r)
	cloud.egraphMu.RLock()
	kept, ok := storedValueMeasuredLocked(entry)
	miss := storedSizeMissLocked(entry)
	cloud.egraphMu.RUnlock()
	require.True(t, ok, "R's size is kept for its current state")
	cloud.egraphMu.RLock()
	if entry.stored != nil {
		entry.stored.size.Store(nil)
	} else {
		entry.storedTransfer.Load().size.Store(nil)
	}
	cloud.egraphMu.RUnlock()
	fresh, err := cloud.measureStoredValue(miss)
	require.NoError(t, err)
	require.Equal(t, fresh.size.bytes, kept, "the kept size is R's with its new dependency")
}

// StoredSizes normalizes and encodes an unmeasured value with the graph lock
// free: a measure never holds egraphMu, and each first measurement counts. A
// measure proves the lock free the way a writer finds it: a write lock taken
// within ten seconds. The cache's own background work may hold the lock for
// a moment, which a bare TryLock would take for the measure's.
func TestStoredSizesMeasureOutsideTheLock(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	var free, held int
	cloud.testStoredSizeMeasure = func() {
		locked := make(chan struct{})
		go func() {
			// Taking the write lock is the check: it is empty on purpose.
			cloud.egraphMu.Lock()
			cloud.egraphMu.Unlock() //nolint:staticcheck // SA2001: the lock taken is the check
			close(locked)
		}()
		select {
		case <-locked:
			free++
		case <-time.After(10 * time.Second):
			held++
		}
	}
	before := cloud.StoredSizeEncodes()
	got, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, 2, got[0].Records)
	require.Equal(t, 2, free, "R and P are measured with the lock free")
	require.Zero(t, held)
	require.Equal(t, before+2, cloud.StoredSizeEncodes())

	again, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, got, again)
	require.Equal(t, 2, free, "measured sizes are kept")
	require.Equal(t, before+2, cloud.StoredSizeEncodes())
}

// MeasuredStoredSizes never measures: a root whose closure has an unmeasured
// value is answered unmeasured, with nothing normalized or encoded, and
// later roots are measured as if it weren't there.
func TestMeasuredStoredSizesNeverMeasure(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	before := cloud.StoredSizeEncodes()
	got, err := cloud.MeasuredStoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, []StoredSize{{Unmeasured: true}}, got)
	require.Equal(t, before, cloud.StoredSizeEncodes(), "nothing encoded")
	require.Nil(t, storedBundleTestEntry(cloud, r).storedTransfer.Load(), "nothing normalized")

	alone, err := cloud.StoredSizes(ctx, []uint64{p}, time.Now().Unix(), nil)
	require.NoError(t, err)
	encodes := cloud.StoredSizeEncodes()
	got, err = cloud.MeasuredStoredSizes(ctx, []uint64{r, p}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, []StoredSize{{Unmeasured: true}, alone[0]}, got, "P is measured beyond the measured roots before it")
	require.Equal(t, encodes, cloud.StoredSizeEncodes())

	measured, err := cloud.StoredSizes(ctx, []uint64{p, r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err = cloud.MeasuredStoredSizes(ctx, []uint64{p, r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, measured, got, "once measured, the sizes are StoredSizes'")
}

// A measure that ends after the cache dropped the value it measured keeps
// nothing: the dropped record isn't brought back onto the entry.
func TestStoredSizesKeepNothingOfADroppedValue(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	entry := storedBundleTestEntry(cloud, r)
	dropped := false
	cloud.testStoredSizeMeasure = func() {
		if dropped {
			return
		}
		dropped = true
		cloud.egraphMu.Lock()
		defer cloud.egraphMu.Unlock()
		_, err := cloud.dropValueLocked(ctx, entry)
		require.NoError(t, err)
	}
	_, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.True(t, dropped)
	require.Nil(t, entry.storedTransfer.Load(), "the dropped value's record isn't kept")
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Nil(t, entry.stored, "nor its compressed record")
}

// A held row's size is computed, not encoded, and is its encoding's length.
func TestHeldRowSizeIsItsEncodingLength(t *testing.T) {
	t.Parallel()
	for _, number := range []uint64{1, 9, 10, 12345, 1<<64 - 1} {
		for _, replacements := range []uint64{0, 1, 77, 1<<64 - 1} {
			for _, expires := range []int64{0, 1, -5, 1_760_000_000, -1 << 63} {
				for _, recipe := range []digest.Digest{"xxh3:2d06800538d394c2", heldRowSizeDigest, digest.FromString("x")} {
					data, err := json.Marshal(TransferredValue{Ordinal: TransferOrdinal(number), SenderNumber: number, SenderReplacements: replacements, Record: PersistedRecord{ResultID: number}, ExpiresAtUnix: expires, Held: recipe})
					require.NoError(t, err)
					require.Equal(t, int64(len(data)), heldRowSize(number, replacements, expires, recipe), "number %d, replacements %d, expiry %d, recipe %s", number, replacements, expires, recipe)
				}
			}
		}
	}
}

// A build and a measure that normalize the same record at once leave its one
// kept record measured: the build, publishing after the measure, keeps the
// measured record rather than replacing it with its own.
func TestStoredSizesSurviveABuildNormalizingAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloudIn(t, "root", true)
	normalized, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	cloud.testStoredTransferNormalized = func() {
		once.Do(func() {
			close(normalized)
			<-release
		})
	}
	built := make(chan error, 1)
	go func() {
		_, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
		built <- err
	}()
	<-normalized
	_, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-built)

	encodes := cloud.StoredSizeEncodes()
	got, err := cloud.MeasuredStoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.False(t, got[0].Unmeasured, "the build kept the measured record")
	require.Equal(t, 2, got[0].Records)
	require.Equal(t, encodes, cloud.StoredSizeEncodes())
}

// A measure of a value whose dependencies changed while it measured keeps
// nothing, so it can't replace the size measured for the new dependencies.
func TestStoredSizesKeepNoMeasureOfAnOlderState(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	source, _ := mergeTestSource(t, "sizes-later-owner", "owner")
	reply, err := cloud.MergeValues(ctx, "cache-b", source)
	require.NoError(t, err)
	q := reply.Imported()[0].ResultID
	_, err = cloud.StoredSizes(ctx, []uint64{q}, time.Now().Unix(), nil)
	require.NoError(t, err)

	// The newer measure calls the hook too, so it is guarded before it nests.
	changed := false
	cloud.testStoredSizeMeasure = func() {
		if changed {
			return
		}
		changed = true
		part := testLiveOffer()
		part.Owner.DependencyIDs = []uint64{q}
		stored, _, err := cloud.SetStoredPart(ctx, r, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		_, err = cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
		require.NoError(t, err)
	}
	_, err = cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.True(t, changed)

	encodes := cloud.StoredSizeEncodes()
	got, err := cloud.MeasuredStoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.False(t, got[0].Unmeasured, "the size measured for the new dependencies stays")
	require.Equal(t, 3, got[0].Records, "R, P and the part's owner")
	require.Equal(t, encodes, cloud.StoredSizeEncodes())
}

// A root's price counts what its outputs add to a merge's message as the
// wire carries them (ShareChainLayers): each stored part without its layers,
// each layer the bundle lists once, and an allowance for each blob's
// download address, which the bundle gets when it is sent. Before addresses,
// that is never less than the bundle's outputs and layers encode to, and more
// only by each output's renewal key, which signing adds too, and the digits
// of its references.
func TestStoredSizesPriceOutputsAsTheWireCarriesThem(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	base := []snapshots.ExportLayer{storedLayersTestLayer("price-base-0", "the base"), storedLayersTestLayer("price-base-1", "the base")}
	for i, number := range []uint64{r, p} {
		chain := append(slices.Clone(base), storedLayersTestLayer(fmt.Sprint("price-top-", i), "the part's own"))
		packs := map[digest.Digest]digest.Digest{base[0].Descriptor.Digest: testDigest("price-pack")}
		stored, _, err := cloud.SetStoredPart(ctx, number, storedLayersTestPart("snapshot", chain, packs), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	sizes, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	built, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Outputs, 2)
	shared, err := ShareChainLayers(built.Bundle)
	require.NoError(t, err)
	outputs, err := json.Marshal(shared.Outputs)
	require.NoError(t, err)
	layers, err := json.Marshal(shared.Layers)
	require.NoError(t, err)
	carried := int64(len(outputs) + len(layers))
	addresses := int64(4) * storedBlobAddressAllowance // two base layers and two tops
	priced := sizes[0].OutputBytes - addresses
	require.GreaterOrEqual(t, priced, carried, "never less than the wire carries")
	require.LessOrEqual(t, priced, carried+2*storedOutputReferenceSlack, "more only by renewal keys and the digits of references")
}

// A root's lower half is what a merge over the message limit sends ahead of
// it, as the service's split of a refused merge does: the entries that carry
// the first half of the records below the root, those of them that no other
// of them depends on; never the root, and never an entry the recipient holds.
// It also says how many records, but held rows, the root's closure carries.
func TestStoredLowerHalfIsTheFirstHalfBelowTheRoot(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "half-root", &transferTestValue{Text: "root"})
	var chain []AnyResult
	for i := range 8 {
		dep := persistedListTestResult(t, actx, a, asrv, fmt.Sprint("half-dep-", i), &transferTestValue{Text: fmt.Sprint("dep ", i)})
		if i > 0 {
			transferTestDependency(a, actx, dep, chain[i-1])
		}
		chain = append(chain, dep)
	}
	transferTestDependency(a, actx, root, chain[7])
	bundle := exportTestBundle(t, actx, a, root)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	number := map[sharedResultID]uint64{}
	for _, value := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal == value.Ordinal {
				number[sharedResultID(sent.SenderNumber)] = value.Number
			}
		}
	}
	cloudOf := func(res AnyResult) uint64 { return number[res.cacheSharedResult().id] }

	lower, carried, err := cloud.StoredLowerHalf(ctx, cloudOf(root), time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, []uint64{cloudOf(chain[3])}, lower, "the top of the chain's lower four")
	require.Equal(t, 9, carried)

	// The recipient holds the chain's lowest two: what's below the root is
	// the six above them, and their lower half the next three.
	held := &HeldBy{Cache: "cache-b"}
	for i, res := range chain[:2] {
		heldTestHold(t, ctx, cloud, "cache-b", uint64(100+i), cloudOf(res), 0)
	}
	lower, carried, err = cloud.StoredLowerHalf(ctx, cloudOf(root), time.Now().Unix(), held)
	require.NoError(t, err)
	require.Equal(t, []uint64{cloudOf(chain[4])}, lower)
	require.Equal(t, 7, carried, "the root and the six above the held rows")
}

// A root's lower half counts the records a bundle of the root carries, its
// kept calls' inputs included, as the split of a merge refused as over the
// message limit counts them from the bundle: a merge split before it was
// built and refused after is then judged against the same count. Here inner
// has a call kept on the receiver A, which its bundle carries.
func TestStoredLowerHalfCountsWhatABundleOfTheRootCarries(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	built, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Calls, 1, "the bundle carries inner's call, and A for it")
	_, carried, err := test.cloud.StoredLowerHalf(test.ctx, test.number, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, len(built.Bundle.Values), carried)
}

// A blob that two parts hold in different packs has two download addresses,
// each its pack's (or its own), and the message carries each part's that
// isn't the bundle's own as an address of its chain (ShareChainLayers): the
// price allows for every address the message carries.
func TestStoredSizesAllowForEachAddressOfABlobInDifferentPacks(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	shared := storedLayersTestLayer("packs-shared", "in two packs")
	for i, number := range []uint64{r, p} {
		packs := map[digest.Digest]digest.Digest{}
		if i == 0 {
			packs[shared.Descriptor.Digest] = testDigest("packs-pack")
		}
		stored, _, err := cloud.SetStoredPart(ctx, number, storedLayersTestPart("snapshot", []snapshots.ExportLayer{shared}, packs), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	sizes, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	built, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, built.Bundle.Outputs, 2)
	unsigned, err := ShareChainLayers(built.Bundle)
	require.NoError(t, err)
	outputs, err := json.Marshal(unsigned.Outputs)
	require.NoError(t, err)
	layers, err := json.Marshal(unsigned.Layers)
	require.NoError(t, err)
	addresses := storedSizesTestAddresses(t, built.Bundle)
	require.Equal(t, 2, addresses, "the bundle's and one chain's own")
	require.GreaterOrEqual(t, sizes[0].OutputBytes-int64(len(outputs)+len(layers)), int64(addresses)*storedBlobAddressAllowance)
}

// storedSizesTestAddresses counts the download addresses a merge's message
// carries for the bundle once signed as the service signs it, a packed
// layer's address its pack's and an unpacked one's its own, and its chains'
// layers shared (ShareChainLayers): the bundle's, and its chains' own.
func storedSizesTestAddresses(t *testing.T, bundle ValueBundle) int {
	t.Helper()
	signed := bundle
	signed.Outputs = slices.Clone(signed.Outputs)
	for i := range signed.Outputs {
		chain := *signed.Outputs[i].Chain
		chain.Addresses = map[digest.Digest]BlobAddress{}
		for _, layer := range chain.Layers {
			object := layer.Descriptor.Digest
			if pack, ok := chain.Packs[object]; ok {
				object = pack
			}
			chain.Addresses[layer.Descriptor.Digest] = BlobAddress{URL: "https://store.test/" + object.String()}
		}
		signed.Outputs[i].Chain = &chain
	}
	wire, err := ShareChainLayers(signed)
	require.NoError(t, err)
	addresses := len(wire.Addresses)
	for _, out := range wire.Outputs {
		addresses += len(out.Chain.Addresses)
	}
	return addresses
}

// The address a bundle shares for a blob is the first its outputs name, in
// the bundle's order, each entry's parts by key (storedBundleOutputsLocked):
// the price follows that order. Here one entry's parts a and b name a blob in
// packs A and B, and the next entry's part c names it in B: the bundle shares
// A's address and carries B's on b and on c, three in all, whichever order
// the entry keeps its parts in.
func TestStoredSizesAllowForAddressesInTheBundlesPartOrder(t *testing.T) {
	t.Parallel()
	blob := testDigest("order-blob")
	in := func(part, pack string) storedPart {
		return storedPart{
			held:   PersistedPartOffer{Address: PersistedPartAddress{Part: PartKey(part)}},
			layers: []*storedLayer{{layer: snapshots.ExportLayer{Descriptor: ocispecs.Descriptor{Digest: blob}}, pack: testDigest(pack)}},
		}
	}
	first := &sharedResult{storedParts: map[string]storedPart{"a": in("a", "pack-a"), "b": in("b", "pack-b")}}
	next := &sharedResult{storedParts: map[string]storedPart{"c": in("c", "pack-b")}}
	// A map's order differs from one walk to the next: walk it many times.
	for range 50 {
		walk := &storedSizeWalk{outputs: &storedOutputsSeen{layers: map[*storedLayer]bool{}, blobs: map[digest.Digest]digest.Digest{}}}
		walk.addOutputs(first)
		walk.addOutputs(next)
		require.Equal(t, int64(3)*storedBlobAddressAllowance, walk.size.OutputBytes, "A's address shared, B's on b and on c")
	}
}

// A compressed record's kept size goes with its call: once a taught digest
// replaces the call, pricing measures what the bundle now carries.
func TestStoredSizesOfACompressedRecordFollowItsCall(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	storedTestCompress(cloud)
	_, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, r)}, testDigest("compressed-size-content")))
	built, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)
}

// A stored-size walk is a read in pieces (readInPieces): it lets egraphMu
// go between entries, every readPieceSteps of them, and takes it again, so a
// writer waiting for the lock gets in, and a walk of a large closure is many
// short holds, not one. It doesn't restart when a write lands between two
// pieces: the rest of the walk reads each entry again, by its number, as the
// write left it, so it may count a closure that grew between two of its
// pieces. Here, with a piece of one entry, a part stored on R between two
// pieces, before R is read, adds its owner to R's closure, and the walk
// counts it. The write takes egraphMu for writing, so it gets in only if the
// walk let the lock go; it is given ten seconds, and the walk goes on either
// way.
func TestStoredSizesGiveUpTheLockBetweenEntries(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	source, _ := mergeTestSource(t, "sizes-owner", "owner")
	reply, err := cloud.MergeValues(ctx, "cache-b", source)
	require.NoError(t, err)
	q := reply.Imported()[0].ResultID

	type stored struct {
		ok  bool
		err error
	}
	written := make(chan stored, 1)
	var yields, holds int
	var late bool
	cloud.readPieceSize = 1
	cloud.testStoredSizesHold = func(time.Duration) { holds++ }
	cloud.testReadPieceYielded = func() {
		yields++
		if yields != 1 {
			return
		}
		go func() {
			part := testLiveOffer()
			part.Owner.DependencyIDs = []uint64{q}
			ok, _, err := cloud.SetStoredPart(ctx, r, part, 0)
			written <- stored{ok, err}
		}()
		select {
		case w := <-written:
			written <- w
		case <-time.After(10 * time.Second):
			late = true
		}
	}
	got, err := cloud.StoredSizes(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Positive(t, yields, "the walk let the lock go")
	w := <-written
	require.NoError(t, w.err)
	require.True(t, w.ok)
	require.False(t, late, "the write got the lock between two pieces")
	require.Equal(t, 3, got[0].Records, "R, P, and the owner the part stored between two pieces added")
	require.Greater(t, holds, yields, "a hold for each piece, and the measures' publish")
}

// A stored-size walk lets egraphMu go on the way back up a closure as well
// as on the way down: an entry's own reading after its dependencies, its
// outputs and its size, is a step too. A chain several pieces deep would
// otherwise read every entry of it on the way back up in one hold, with a
// writer waiting for all of it. Here a chain of twelve entries, with pieces
// of four steps, lets the lock go for each four of the walk's steps down,
// back up, and over the kept calls.
func TestStoredSizesLetTheLockGoUpADeepChain(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	const depth = 12
	prev := persistedListTestResult(t, actx, a, asrv, "chain-0", &transferTestValue{Text: "0"})
	for i := 1; i < depth; i++ {
		next := persistedListTestResult(t, actx, a, asrv, fmt.Sprintf("chain-%d", i), &transferTestValue{Text: fmt.Sprint(i)})
		transferTestDependency(a, actx, next, prev)
		prev = next
	}
	bundle := exportTestBundle(t, actx, a, prev)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	var top uint64
	for _, value := range reply.Values {
		if value.Ordinal == bundle.Roots[0].Ordinal {
			top = value.Number
		}
	}
	require.NotZero(t, top)

	cloud.readPieceSize = 4
	yields := 0
	cloud.testReadPieceYielded = func() { yields++ }
	got, err := cloud.StoredSizes(ctx, []uint64{top}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, depth, got[0].Records)
	require.GreaterOrEqual(t, yields, 3*depth/4, "a piece for each four steps down, back up, and over the kept calls")
}
