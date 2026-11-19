package dagql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// storedClosureTestOf lists a built bundle as StoredClosure lists it: each
// value's number and whether it is a held row, in the bundle's order.
func storedClosureTestOf(bundle ValueBundle) []StoredClosureEntry {
	var entries []StoredClosureEntry
	for _, value := range bundle.Values {
		entries = append(entries, StoredClosureEntry{Number: value.SenderNumber, Held: value.Held != ""})
	}
	return entries
}

// StoredClosure lists what StoredBundle carries, without building it: the
// same entries in the same order, the same held rows, and the same inputs of
// the calls it sends. R over P, alone, with P held, and with R and P asked
// for in either order; and inner, whose kept call takes the receiver as its
// input, alone and with the receiver asked for too.
func TestStoredClosureListsWhatTheBundleCarries(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	calls := newReceiverCallTest(t)
	now := time.Now().Unix()
	heldP := &HeldBy{Cache: "cache-fresh", Also: func(number uint64) bool { return number == p }}
	for _, c := range []struct {
		name  string
		cache *Cache
		roots []uint64
		held  *HeldBy
	}{
		{"R", cloud, []uint64{r}, nil},
		{"R with P held", cloud, []uint64{r}, heldP},
		{"P then R", cloud, []uint64{p, r}, nil},
		{"R then P", cloud, []uint64{r, p}, nil},
		{"a root it lacks", cloud, []uint64{12345, r}, nil},
		{"inner", calls.cloud, []uint64{calls.number}, nil},
		{"inner and its call's input", calls.cloud, []uint64{calls.number, calls.receiverNumber}, nil},
	} {
		built, err := c.cache.StoredBundle(ctx, c.roots, now, c.held)
		require.NoError(t, err, c.name)
		listed, err := c.cache.StoredClosure(ctx, c.roots, now, c.held)
		require.NoError(t, err, c.name)
		require.Equal(t, storedClosureTestOf(built.Bundle), listed.Entries, c.name)
		require.Equal(t, built.CallInputs, listed.CallInputs, c.name)
	}
	listed, err := calls.cloud.StoredClosure(ctx, []uint64{calls.number}, now, nil)
	require.NoError(t, err)
	require.Equal(t, []uint64{calls.receiverNumber}, listed.CallInputs, "inner's kept call takes the receiver")
}

// StoredClosure reads no value: it builds no bundle, and copies, normalizes
// and encodes nothing, so it measures no size either.
func TestStoredClosureBuildsNothing(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	builds, encodes := cloud.StoredBundleBuilds(), cloud.StoredSizeEncodes()
	listed, err := cloud.StoredClosure(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, listed.Entries, 2)
	require.Equal(t, builds, cloud.StoredBundleBuilds(), "no bundle built")
	require.Equal(t, encodes, cloud.StoredSizeEncodes(), "no value measured")
	_, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, builds+1, cloud.StoredBundleBuilds(), "a bundle built is counted")
}

// StoredClosure lets egraphMu go between its pieces, as StoredSizes does: a
// write that comes while it lists gets the lock between two pieces, without
// waiting for the whole list.
func TestStoredClosureLetsTheLockGoBetweenEntries(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	written := make(chan error, 1)
	var yields int
	var late bool
	cloud.readPieceSize = 1
	cloud.testReadPieceYielded = func() {
		yields++
		if yields != 1 {
			return
		}
		go func() {
			w := cloud.HoldRemoteWrites()
			written <- w.Release(ctx)
		}()
		select {
		case err := <-written:
			written <- err
		case <-time.After(10 * time.Second):
			late = true
		}
	}
	listed, err := cloud.StoredClosure(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Positive(t, yields, "the list let the lock go")
	require.NoError(t, <-written)
	require.False(t, late, "the write got the lock between two pieces")
	require.Equal(t, []StoredClosureEntry{{Number: p}, {Number: r}}, listed.Entries)
}

// A bundle built with StoredBundleAlsoRoots is the bundle built again with
// the entries it names, of those it carries in full, as roots after the
// others: R over P, with P's retention lasting longer than P's own value,
// which a root travels with; and inner, whose kept call's input becomes a
// root of its own, and no longer a root for the call alone. An entry it names
// that the recipient holds, or that it doesn't carry, isn't made a root.
func TestStoredBundleAlsoRootsIsTheBundleBuiltAgain(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	calls := newReceiverCallTest(t)
	now := time.Now().Unix()
	// P retained to a later time than its own expiry.
	cloud.egraphMu.Lock()
	res := cloud.resultsByID[sharedResultID(p)]
	res.expiresAtUnix = now + 3600
	cloud.upsertPersistedEdgeLocked(ctx, res, now+7200, false)
	cloud.egraphMu.Unlock()

	names := func(numbers ...uint64) StoredBundleOption {
		return StoredBundleAlsoRoots(func(number uint64) bool {
			for _, n := range numbers {
				if n == number {
					return true
				}
			}
			return false
		})
	}
	same := func(name string, want, got StoredBundleResult) {
		t.Helper()
		require.Equal(t, want.Bundle, got.Bundle, name)
		require.Equal(t, want.Donors, got.Donors, name)
		require.Equal(t, want.CallInputs, got.CallInputs, name)
		require.Equal(t, want.RootOwners, got.RootOwners, name)
		require.Equal(t, want.Held, got.Held, name)
		require.Equal(t, want.Partial, got.Partial, name)
		require.Equal(t, want.UnsendableCalls, got.UnsendableCalls, name)
	}

	again, err := cloud.StoredBundle(ctx, []uint64{r, p}, now, nil)
	require.NoError(t, err)
	once, err := cloud.StoredBundle(ctx, []uint64{r}, now, nil, names(p, 12345))
	require.NoError(t, err)
	same("R, and P below it", again, once)
	require.Len(t, once.Bundle.Roots, 2)
	require.Equal(t, now+7200, once.Bundle.Roots[1].ExpiresAtUnix, "P travels as a root with its retention's expiry")

	heldP := &HeldBy{Cache: "cache-fresh", Also: func(number uint64) bool { return number == p }}
	plain, err := cloud.StoredBundle(ctx, []uint64{r}, now, heldP)
	require.NoError(t, err)
	held, err := cloud.StoredBundle(ctx, []uint64{r}, now, heldP, names(p))
	require.NoError(t, err)
	same("P held", plain, held)

	again, err = calls.cloud.StoredBundle(ctx, []uint64{calls.number, calls.receiverNumber}, now, nil)
	require.NoError(t, err)
	once, err = calls.cloud.StoredBundle(ctx, []uint64{calls.number}, now, nil, names(calls.receiverNumber))
	require.NoError(t, err)
	same("inner, and its call's input", again, once)
	require.Empty(t, once.CallInputs, "the input is a root of its own")
}
