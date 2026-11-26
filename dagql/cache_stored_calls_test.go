package dagql

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// bundleCallsOf is what StoredBundle(roots, now, held) decides of the calls
// kept on each root, read off the bundle it builds: the roots a call it
// sends has as its value, and the roots it counts partial.
func bundleCallsOf(t *testing.T, c *Cache, roots []uint64, now int64, held *HeldBy) (sent, partial []uint64) {
	t.Helper()
	built, err := c.StoredBundle(t.Context(), roots, now, held)
	require.NoError(t, err)
	number := map[TransferOrdinal]uint64{}
	for _, v := range built.Bundle.Values {
		number[v.Ordinal] = v.SenderNumber
	}
	for _, root := range roots {
		if slices.ContainsFunc(built.Bundle.Calls, func(call TransferredCall) bool { return number[call.Value] == root }) {
			sent = append(sent, root)
		}
		if slices.Contains(built.Partial, root) {
			partial = append(partial, root)
		}
	}
	return sent, partial
}

// heldRoots is a recipient that holds the entries numbered roots, whatever
// has changed of them, and whose check made the calls calls names.
func heldRoots(calls func(digest.Digest) bool, roots ...uint64) *HeldBy {
	return &HeldBy{
		Also: func(number uint64) (uint64, bool) {
			return math.MaxUint64, slices.Contains(roots, number)
		},
		Calls: calls,
	}
}

// StoredCalls answers, for a root the recipient holds, what a bundle of it
// decides of the calls kept on it: Sent when the recipient's check made a
// call it can place, Partial when it made one no entry can place, neither
// when its check made none, by its calls or by the terms it names.
func TestStoredCallsAnswersWhatABundleDecidesOfAHeldRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		calls         func(request digest.Digest) bool
		terms         bool
		dropReceiver  bool
		sent, partial bool
	}{
		{name: "a call its check made", calls: func(digest.Digest) bool { return true }, sent: true},
		{name: "a call another check made", calls: func(digest.Digest) bool { return false }},
		{name: "a term its check made", calls: func(digest.Digest) bool { return false }, terms: true, sent: true},
		{name: "a call its check made that nothing can place", calls: func(digest.Digest) bool { return true }, dropReceiver: true, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			test := newReceiverCallTest(t)
			cloud := test.cloud
			if tc.dropReceiver {
				// The receiver's value goes, as a prune takes it: no entry of its
				// class stores a record of it any more.
				cloud.egraphMu.Lock()
				_, err := cloud.dropValueLocked(test.ctx, cloud.resultsByID[sharedResultID(test.receiverNumber)])
				cloud.egraphMu.Unlock()
				require.NoError(t, err)
			}
			held := heldRoots(tc.calls, test.number)
			if tc.terms {
				entry := storedBundleTestEntry(cloud, test.number)
				cloud.egraphMu.RLock()
				for _, kept := range entry.keptCallsLocked() {
					held.Terms = append(held.Terms, RemoteTerm{Self: kept.id.self, Inputs: slices.Clone(kept.id.inputs)})
				}
				cloud.egraphMu.RUnlock()
				require.Len(t, held.Terms, 1)
			}
			now := time.Now().Unix()
			got, err := cloud.StoredCalls(test.ctx, []uint64{test.number}, now, held)
			require.NoError(t, err)
			require.Empty(t, got.NotHeld, "the recipient holds the root")
			require.Equal(t, tc.sent, slices.Contains(got.Sent, test.number), "sent")
			require.Equal(t, tc.partial, slices.Contains(got.Partial, test.number), "partial")

			sent, partial := bundleCallsOf(t, cloud, []uint64{test.number}, now, held)
			require.Equal(t, sent, got.Sent, "sent as the bundle sends it")
			require.Equal(t, partial, got.Partial, "partial as the bundle counts it")
		})
	}
}

// A root the recipient doesn't hold goes in full in a bundle, which walks
// below it: StoredCalls leaves it to a bundle's build, as NotHeld. A root
// the cache has no entry for is in no list, as a bundle carries nothing of
// it.
func TestStoredCallsLeavesARootTheRecipientDoesntHoldToABundle(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	got, err := test.cloud.StoredCalls(test.ctx, []uint64{test.number, math.MaxUint32}, time.Now().Unix(), &HeldBy{Calls: func(digest.Digest) bool { return true }})
	require.NoError(t, err)
	require.Equal(t, []uint64{test.number}, got.NotHeld)
	require.Empty(t, got.Sent)
	require.Empty(t, got.Partial)
}

// A recipient whose bundles decide a root's calls from what the rest of the
// bundle carries is refused.
func TestStoredCallsRefusesARecipientABundleAloneAnswers(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	for name, held := range map[string]*HeldBy{
		"parts":   {Parts: func(uint64) bool { return false }},
		"through": {Through: func(uint64) bool { return false }},
		"whole":   {Whole: true},
	} {
		_, err := test.cloud.StoredCalls(test.ctx, []uint64{test.number}, time.Now().Unix(), held)
		require.Error(t, err, name)
	}
}

// StoredCalls takes its recipient's term keys again once it has let the
// lock go to a writer, as a key taken before a union names a class root
// that has moved. The recipient names inner's call by its term alone. A
// writer that comes after the first answer merges the receiver's class into
// another of a higher rank, which keeps its own root: the second answer for
// the same root still finds the term.
func TestStoredCallsTakesTermKeysAgainOnceItLetsTheLockGo(t *testing.T) {
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
	held := heldRoots(func(digest.Digest) bool { return false }, test.number)
	held.Terms = []RemoteTerm{term}

	merged := make(chan bool, 1)
	cloud.testBatchAnswered = func(i int) {
		if i != 0 {
			return
		}
		go func() {
			cloud.egraphMu.Lock()
			defer cloud.egraphMu.Unlock()
			input := cloud.egraphDigestToClass[term.Inputs[0].String()]
			before := cloud.eqClassRootLocked(input)
			other := cloud.ensureEqClassForDigestLocked(test.ctx, fmt.Sprintf("sha256:other-%d", time.Now().UnixNano()))
			cloud.egraphRanks[other] = cloud.egraphRanks[before] + 1
			cloud.mergeEqClassesLocked(test.ctx, before, other)
			merged <- cloud.eqClassRootLocked(input) != before
		}()
		require.Eventually(t, cloud.egraphMu.Waiting, 10*time.Second, time.Millisecond, "the writer waits")
	}
	defer func() { cloud.testBatchAnswered = nil }()
	got, err := cloud.StoredCalls(test.ctx, []uint64{test.number, test.number}, time.Now().Unix(), held)
	require.NoError(t, err)
	require.True(t, <-merged, "the receiver's class root moved between the two answers")
	require.Equal(t, []uint64{test.number, test.number}, got.Sent, "the term is found in both holds")
}
