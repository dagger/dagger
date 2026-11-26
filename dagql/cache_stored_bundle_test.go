package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// storedBundleTestCloud merges an export of a root R, which depends on P,
// into a new Cloud cache, and returns the Cloud's numbers for R and P.
func storedBundleTestCloud(t *testing.T, text string) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	return storedBundleTestCloudIn(t, text, false)
}

// storedBundleTestCloudIn is storedBundleTestCloud, with whole a Cloud whose
// body worker is stopped before the merge, so that its records stay whole.
func storedBundleTestCloudIn(t *testing.T, text string, whole bool) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: text})
	p := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r, p)
	bundle := exportTestBundle(t, actx, a, r)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	if whole {
		storedTestHoldWhole(cloud)
	}
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	var rNumber, pNumber uint64
	for _, value := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal != value.Ordinal {
				continue
			}
			switch sent.SenderNumber {
			case uint64(r.cacheSharedResult().id):
				rNumber = value.Number
			case uint64(p.cacheSharedResult().id):
				pNumber = value.Number
			}
		}
	}
	require.NotZero(t, rNumber)
	require.NotZero(t, pNumber)
	return ctx, cloud, rNumber, pNumber
}

// storedBundleTestEntry returns the Cloud entry with the number.
func storedBundleTestEntry(c *Cache, number uint64) *sharedResult {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.resultsByID[sharedResultID(number)]
}

// A stored bundle carries the records of the roots' closure, named by the
// Cloud's numbers and replacement counts, and each entry's own stored parts,
// expired or not, with the entry as the output's storing entry. A root the
// cache doesn't have is gone, and one known only through holdings has no
// value. The bundle merges into an engine.
func TestStoredBundleCarriesTheClosureAndItsOwnParts(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-x", Number: 3}, holdingOf("holdings-only"))
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	holdingsOnly := uint64(cloud.holderEntriesForTest()[HolderKey{Cache: "cache-x", Number: 3}])
	cloud.egraphMu.RUnlock()
	mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))

	got, err := cloud.StoredBundle(ctx, []uint64{r, 12345, holdingsOnly}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, []uint64{12345}, got.Gone)
	require.Equal(t, []uint64{holdingsOnly}, got.NoValue)
	require.Len(t, got.Bundle.Values, 2, "R and its dependency")
	numbers := map[uint64]bool{}
	for _, value := range got.Bundle.Values {
		numbers[value.SenderNumber] = true
	}
	require.Equal(t, map[uint64]bool{r: true, p: true}, numbers)
	require.Len(t, got.Bundle.Outputs, 1, "R's own part travels though R has expired")
	require.Equal(t, []uint64{r}, got.Donors)
	require.Empty(t, got.Bundle.Outputs[0].Chain.Addresses)
	require.Empty(t, got.Bundle.Outputs[0].Chain.RenewalKey)

	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, got.Bundle)
	require.NoError(t, err, "the bundle merges into an engine")
	require.True(t, reply.Committed)
}

// storedBundleTestClassMember merges another record, of a different recipe,
// into the Cloud and joins it to the entry's class by a content digest. It
// returns the member's number.
func storedBundleTestClassMember(t *testing.T, ctx context.Context, cloud *Cache, entry uint64, field string) uint64 {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, "member")
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	member := reply.Imported()[0].ResultID
	for _, number := range []uint64{entry, member} {
		require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, number)}, testDigest("bundle-class-content")))
	}
	return member
}

// A part the entry's record declares and it doesn't store is borrowed from
// an unexpired entry of its class, which is the output's storing entry; an
// expired member lends nothing.
func TestStoredBundleBorrowsFromAnUnexpiredMember(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	member := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-member")
	stored, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)

	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Outputs, 1)
	require.Equal(t, []uint64{member}, got.Donors, "the donor stores it")
	require.Empty(t, got.Bundle.Outputs[0].Owner.DependencyIDs, "no services, no owner entries")

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, member))
	got, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Empty(t, got.Bundle.Outputs, "an expired member lends nothing")
}

// A bundle reads a class's entries for its parts' donors once in its hold,
// not once for each entry that borrows: a set's file can carry thousands of
// entries of one class that each declare a part only another entry of the
// class stores, and a read of the class for each of them held one read of
// the cache's lock for seconds. A bundle of R and more entries of its class,
// each declaring R's part and storing none, reads R's class once, and each
// borrows the part from the same donor, the lowest-numbered unexpired entry
// of the class that stores it; with that one expired, from the next.
func TestStoredBundleReadsAClassForItsDonorsOnce(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	first := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-member")
	second := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-other-member")
	require.Less(t, first, second)
	for _, donor := range []uint64{first, second} {
		stored, _, err := cloud.SetStoredPart(ctx, donor, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	// More records like R, each joined to R's class by its content.
	const borrowers = 8
	actx, a, asrv := transferTestCache(t)
	var results []AnyResult
	for i := range borrowers {
		results = append(results, persistedListTestResult(t, actx, a, asrv, fmt.Sprintf("bundle-borrower-%d", i), &transferTestValue{Text: fmt.Sprintf("borrower %d", i)}))
	}
	reply, err := cloud.MergeValues(ctx, "cache-d", exportTestBundle(t, actx, a, results...))
	require.NoError(t, err)
	roots := []uint64{r}
	for _, imported := range reply.Imported() {
		roots = append(roots, imported.ResultID)
		require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, imported.ResultID)}, testDigest("bundle-class-content")))
	}
	require.Len(t, roots, borrowers+1)

	for _, tc := range []struct {
		name  string
		donor uint64
	}{
		{"the lowest-numbered member that stores the part", first},
		{"the next, once the lowest has expired", second},
	} {
		if tc.donor == second {
			mergeTestExpire(cloud, storedBundleTestEntry(cloud, first))
		}
		var reads int
		cloud.egraphMu.Lock()
		cloud.testDonorClassRead = func() { reads++ }
		cloud.egraphMu.Unlock()
		got, err := cloud.StoredBundle(ctx, roots, time.Now().Unix(), nil)
		require.NoError(t, err, tc.name)
		var borrowed int
		for i, output := range got.Bundle.Outputs {
			for _, value := range got.Bundle.Values {
				if value.Ordinal == output.Ordinal && slices.Contains(roots, value.SenderNumber) {
					borrowed++
					require.Equal(t, tc.donor, got.Donors[i], tc.name)
				}
			}
		}
		require.Equal(t, borrowers+1, borrowed, "%s: every entry of the class borrows its part", tc.name)
		require.LessOrEqual(t, reads, 2, "%s: R's class is read once, and R's dependency's", tc.name)
	}
}

// A stored bundle judges a donor's expiry at the reader's clock, not its
// own: a member whose value expires an hour from the wall clock lends its
// part before that moment and not from it.
func TestStoredBundleBorrowsAtTheReadersClock(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	member := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-member")
	stored, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	expiresAt := time.Now().Add(time.Hour).Unix()
	entry := storedBundleTestEntry(cloud, member)
	cloud.egraphMu.Lock()
	entry.expiresAtUnix = expiresAt
	cloud.egraphMu.Unlock()

	got, err := cloud.StoredBundle(ctx, []uint64{r}, expiresAt-1, nil)
	require.NoError(t, err)
	require.Equal(t, []uint64{member}, got.Donors, "the member hasn't expired at the reader's moment")
	got, err = cloud.StoredBundle(ctx, []uint64{r}, expiresAt, nil)
	require.NoError(t, err)
	require.Empty(t, got.Bundle.Outputs, "the member has expired at the reader's moment, though not by the wall clock")
}

// A borrowed part's services are mapped to rows of the bundle's closure of
// the same class, and its owner lists exactly those rows; a part with a
// service the closure holds nothing of is left out.
func TestStoredBundleBorrowsThroughThePlacementRule(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	member := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-member")
	// The member's part names a service: first R's dependency P, which the
	// closure holds, then an entry outside it.
	outside, _ := mergeTestSource(t, "outside-service", "service")
	reply, err := cloud.MergeValues(ctx, "cache-c", outside)
	require.NoError(t, err)
	outsideNumber := reply.Imported()[0].ResultID
	for _, tc := range []struct {
		name    string
		service uint64
		placed  bool
	}{
		{"a service the closure holds", p, true},
		{"a service outside the closure", outsideNumber, false},
	} {
		part := testLiveOffer()
		part.Value.Services = []TransferredServiceBinding{{ServiceResultID: tc.service, Hostname: "svc"}}
		part.Owner.DependencyIDs = []uint64{tc.service}
		stored, _, err := cloud.SetStoredPart(ctx, member, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
		require.NoError(t, err, tc.name)
		if !tc.placed {
			require.Empty(t, got.Bundle.Outputs, tc.name)
			continue
		}
		require.Len(t, got.Bundle.Outputs, 1, tc.name)
		var pOrdinal TransferOrdinal
		for _, value := range got.Bundle.Values {
			if value.SenderNumber == p {
				pOrdinal = value.Ordinal
			}
		}
		output := got.Bundle.Outputs[0]
		require.Equal(t, uint64(pOrdinal), output.Value.Services[0].ServiceResultID, tc.name)
		require.Equal(t, []uint64{uint64(pOrdinal)}, output.Owner.DependencyIDs, tc.name)
	}
}

// A stored bundle racing a merge that replaces an expired stored record
// carries either the old record with its own part or the new record without
// it, never a mix: a record and its outputs are copied in one hold, and the
// replacement takes the lock for writing.
func TestStoredBundleNeverMixesAReplacement(t *testing.T) {
	t.Parallel()
	for range 50 {
		ctx, cloud, r, _ := storedBundleTestCloud(t, "old")
		stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
		fresh, _ := mergeTestSource(t, "bundle-root", "new")

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cloud.MergeValues(ctx, "cache-b", fresh)
			require.NoError(t, err)
		}()
		got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
		wg.Wait()
		require.NoError(t, err)
		var rootValue TransferredValue
		for _, value := range got.Bundle.Values {
			if value.SenderNumber == r {
				rootValue = value
			}
		}
		record, err := json.Marshal(sentRecord(t, rootValue).Envelope)
		require.NoError(t, err)
		if rootValue.SenderReplacements == 0 {
			require.Contains(t, string(record), "old")
			require.Len(t, got.Bundle.Outputs, 1, "the old record with its own part")
		} else {
			require.Contains(t, string(record), "new")
			require.Empty(t, got.Bundle.Outputs, "the new record, whose part isn't stored")
		}
		require.Equal(t, r, rootValue.SenderNumber, "the entry is replaced, never retired")
	}
}

// storedBundleTestChain merges an export of a chain of n entries, each
// depending on the one before, into a new Cloud cache whose records stay
// whole, and returns the Cloud's number for the chain's top.
func storedBundleTestChain(t *testing.T, n int) (context.Context, *Cache, uint64) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	prev := persistedListTestResult(t, actx, a, asrv, "chain-0", &transferTestValue{Text: "0"})
	for i := 1; i < n; i++ {
		next := persistedListTestResult(t, actx, a, asrv, fmt.Sprintf("chain-%d", i), &transferTestValue{Text: fmt.Sprint(i)})
		transferTestDependency(a, actx, next, prev)
		prev = next
	}
	bundle := exportTestBundle(t, actx, a, prev)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	storedTestHoldWhole(cloud)
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	for _, value := range reply.Values {
		if value.Ordinal == bundle.Roots[0].Ordinal {
			return ctx, cloud, value.Number
		}
	}
	t.Fatal("the merge has no row for the chain's top")
	return nil, nil, 0
}

// A stored bundle's build is a read in pieces: each entry it walks and each
// it copies is a step, between which it can let egraphMu go, so that a
// writer waits for one piece of the build rather than all of it. Read in
// pieces of one step, a bundle of a chain of twelve entries lets the lock go
// at least once for each entry walked and each copied, and, as nothing
// writes meanwhile, carries what a build that never let it go carries, at
// the same position.
func TestStoredBundleIsAReadInPieces(t *testing.T) {
	t.Parallel()
	const depth = 12
	ctx, cloud, top := storedBundleTestChain(t, depth)
	now := time.Now().Unix()
	held, err := cloud.StoredBundle(ctx, []uint64{top}, now, nil)
	require.NoError(t, err)
	require.Len(t, held.Bundle.Values, depth)

	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() { yields++ }
	pieces, err := cloud.StoredBundle(ctx, []uint64{top}, now, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, yields, 2*depth, "a step for each entry walked and each copied")
	require.Equal(t, held.Position, pieces.Position)
	require.Equal(t, string(mustTransferJSON(t, held.Bundle)), string(mustTransferJSON(t, pieces.Bundle)), "what one hold carries")
}

// A stored bundle's build gives way to a writer that waits for egraphMu:
// once it has held the lock for a piece's time with a writer waiting, it
// lets the lock go at its next step, and the writer takes it while the
// build is under way, not once the build ends. Here the writer comes as the
// build copies the chain's first record, and the copy holds the lock past a
// piece's time.
func TestStoredBundleGivesWayToAWaitingWriter(t *testing.T) {
	t.Parallel()
	const depth = 12
	ctx, cloud, top := storedBundleTestChain(t, depth)
	wrote := make(chan struct{})
	started := false
	cloud.testStoredTransferNormalized = func() {
		if started {
			return
		}
		started = true
		go func() {
			cloud.egraphMu.Lock()
			cloud.egraphMu.Unlock()
			close(wrote)
		}()
		for !cloud.egraphMu.Waiting() {
			runtime.Gosched()
		}
		time.Sleep(storedBundlePieceHold)
	}
	between := false
	cloud.testReadPieceYielded = func() {
		select {
		case <-wrote:
			between = true
		case <-time.After(10 * time.Second):
		}
	}
	got, err := cloud.StoredBundle(ctx, []uint64{top}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Values, depth)
	require.True(t, started, "the writer came during the build")
	require.True(t, between, "the writer took the lock between two of the build's pieces")
	<-wrote
}

// A write while a stored bundle's build gives way that has an entry the
// build walked name an entry the walk didn't reach has the bundle built
// again in one hold, as the cache then stands. Here, read in pieces of one
// step, a part is stored on R after the walk of R and P and before R's copy,
// owned by Q, outside R's closure: the bundle carries Q and the part, as a
// build after the write does.
func TestStoredBundleIsBuiltAgainWhenAWriteMovesAnEntryPastItsWalk(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloudIn(t, "root", true)
	source, _ := mergeTestSource(t, "bundle-owner", "owner")
	reply, err := cloud.MergeValues(ctx, "cache-b", source)
	require.NoError(t, err)
	q := reply.Imported()[0].ResultID

	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() {
		// The first two steps walk R and P, and the third begins the copy.
		if yields++; yields != 3 {
			return
		}
		part := testLiveOffer()
		part.Owner.DependencyIDs = []uint64{q}
		stored, _, err := cloud.SetStoredPart(ctx, r, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	now := time.Now().Unix()
	got, err := cloud.StoredBundle(ctx, []uint64{r}, now, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cloud.StoredBundlesBuiltAgain(), "built again in one hold")
	var senders []uint64
	for _, value := range got.Bundle.Values {
		senders = append(senders, value.SenderNumber)
	}
	require.Contains(t, senders, q, "the part's owner")
	require.Len(t, got.Bundle.Outputs, 1, "the part")

	cloud.testReadPieceYielded = nil
	after, err := cloud.StoredBundle(ctx, []uint64{r}, now, nil)
	require.NoError(t, err)
	require.Equal(t, string(mustTransferJSON(t, after.Bundle)), string(mustTransferJSON(t, got.Bundle)), "what a build after the write carries")
}

// A replacement while a stored bundle's build gives way that drops a
// dependency the walk followed has the bundle built again in one hold: the
// row the walk placed for the dependency would be outside the roots'
// closure. Here, read in pieces of one step, R, expired, is replaced by a
// record with no dependency after P's copy and before R's: the bundle
// carries R's new record alone, as a build after the replacement does.
func TestStoredBundleIsBuiltAgainWhenAReplacementDropsWhatItWalked(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloudIn(t, "old", true)
	mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
	fresh, _ := mergeTestSource(t, "bundle-root", "new")
	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() {
		// The first two steps walk R and P, the third copies P, and the
		// fourth copies R.
		if yields++; yields != 4 {
			return
		}
		_, err := cloud.MergeValues(ctx, "cache-b", fresh)
		require.NoError(t, err)
	}
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cloud.StoredBundlesBuiltAgain(), "built again in one hold")
	require.Len(t, got.Bundle.Values, 1, "R alone")
	require.Equal(t, r, got.Bundle.Values[0].SenderNumber)
	require.Positive(t, got.Bundle.Values[0].SenderReplacements, "R's new record")
	require.Contains(t, string(sentRecord(t, got.Bundle.Values[0]).Envelope.ObjectJSON), "new")
}

// A replacement while a stored bundle's build gives way of a record the
// build has already copied has the bundle built again in one hold: a record
// copied after it may name the entry for its new record, and the bundle
// would carry the old one. Here, read in pieces of one step, P, expired, is
// replaced in place after its copy and before R's: the bundle carries P's
// new record, as a build after the replacement does.
func TestStoredBundleIsBuiltAgainWhenARecordItCopiedIsReplaced(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloudIn(t, "root", true)
	mergeTestExpire(cloud, storedBundleTestEntry(cloud, p))
	fresh, _ := mergeTestSource(t, "bundle-dep", "new dep")
	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() {
		if yields++; yields != 4 {
			return
		}
		_, err := cloud.MergeValues(ctx, "cache-b", fresh)
		require.NoError(t, err)
	}
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cloud.StoredBundlesBuiltAgain(), "built again in one hold")
	var dep TransferredValue
	for _, value := range got.Bundle.Values {
		if value.SenderNumber == p {
			dep = value
		}
	}
	require.Positive(t, dep.SenderReplacements, "P's new record")
	require.Contains(t, string(sentRecord(t, dep).Envelope.ObjectJSON), "new dep")
}

// A prune while a stored bundle's build gives way that drops the value of an
// entry the build walked, before its copy, has the bundle built again in one
// hold, which finds the entry with no value. Here, read in pieces of one
// step, R's and P's values are dropped after P's copy and before R's.
func TestStoredBundleIsBuiltAgainWhenAValueItWalkedIsDropped(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloudIn(t, "root", true)
	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() {
		if yields++; yields != 4 {
			return
		}
		_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
		require.NoError(t, err)
		require.True(t, removed)
		require.Equal(t, 2, dropped)
	}
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cloud.StoredBundlesBuiltAgain(), "built again in one hold")
	require.Equal(t, []uint64{r}, got.NoValue, "R has no value")
	require.Empty(t, got.Bundle.Values)
}

// A prune and a merge that fills the entry again while a stored bundle's
// build gives way, of a value the build has already copied, have the bundle
// built again in one hold, as a replacement does, though neither counts as
// one. Here, read in pieces of one step, R's and P's values are dropped and
// filled again by another engine's export after P's copy and before R's: R
// is copied as it was filled, and the bundle would carry P's old value
// beside it. It carries both new values, as a build after the merge does.
func TestStoredBundleIsBuiltAgainWhenAValueItCopiedIsDroppedAndFilledAgain(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloudIn(t, "root", true)
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: "again"})
	dep := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep again"})
	transferTestDependency(a, actx, root, dep)
	again := exportTestBundle(t, actx, a, root)
	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() {
		if yields++; yields != 4 {
			return
		}
		_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
		require.NoError(t, err)
		require.True(t, removed)
		require.Equal(t, 2, dropped)
		reply, err := cloud.MergeValues(ctx, "cache-b", again)
		require.NoError(t, err)
		require.Equal(t, r, reply.Imported()[0].ResultID, "the same entry")
	}
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cloud.StoredBundlesBuiltAgain(), "built again in one hold")
	texts := map[uint64]string{}
	for _, value := range got.Bundle.Values {
		texts[value.SenderNumber] = string(sentRecord(t, value).Envelope.ObjectJSON)
	}
	require.Contains(t, texts[r], "again")
	require.Contains(t, texts[p], "dep again", "P's new value")
}

// A write while a stored bundle's build gives way that changes an entry the
// build walked only within what the bundle carries leaves the build going:
// the entry is copied as the write left it. The bundle's position is the
// cache's as the build began, so the entry reads as changed since it
// (ChangedSince), as the write came after. Here, read in pieces of one step,
// a part owned by P is stored on R between R's walk and its copy.
func TestStoredBundleCopiesAnEntryAsAWriteLeftIt(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloudIn(t, "root", true)
	cloud.readPieceSize = 1
	yields := 0
	cloud.testReadPieceYielded = func() {
		if yields++; yields != 3 {
			return
		}
		part := testLiveOffer()
		part.Owner.DependencyIDs = []uint64{p}
		stored, _, err := cloud.SetStoredPart(ctx, r, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Zero(t, cloud.StoredBundlesBuiltAgain(), "not built again")
	require.Len(t, got.Bundle.Outputs, 1, "R's part, stored after R's walk")
	require.Equal(t, []bool{true}, cloud.ChangedSince([]uint64{r}, got.Position), "R changed since the bundle's position")
}

// A stored bundle carries an entry's current record and call: a build after a
// content digest changes the call, or after a merge replaces the record,
// carries the new one, though an earlier build normalized the old.
func TestStoredBundleCarriesTheCurrentRecord(t *testing.T) {
	t.Parallel()
	for _, compressed := range []bool{false, true} {
		t.Run(storedTestState(compressed), func(t *testing.T) {
			t.Parallel()
			ctx, cloud, r, _ := storedBundleTestCloudIn(t, "old", !compressed)
			settle := func() {
				if compressed {
					storedTestCompress(cloud)
				}
			}
			settle()
			rootRecord := func() PersistedRecord {
				t.Helper()
				got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
				require.NoError(t, err)
				for _, value := range got.Bundle.Values {
					if value.SenderNumber == r {
						return sentRecord(t, value)
					}
				}
				t.Fatal("the bundle has no record of R")
				return PersistedRecord{}
			}
			require.Contains(t, string(rootRecord().Envelope.ObjectJSON), "old")

			content := testDigest("current-record-content")
			require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, r)}, content))
			require.Contains(t, rootRecord().Call.ExtraDigests, call.ExtraDigest{Digest: content, Label: call.ExtraDigestLabelContent}, "the new call")

			mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
			res := storedBundleTestEntry(cloud, r)
			cloud.egraphMu.RLock()
			oldBody := res.stored
			cloud.egraphMu.RUnlock()
			fresh, _ := mergeTestSource(t, "bundle-root", "new")
			_, err := cloud.MergeValues(ctx, "cache-b", fresh)
			require.NoError(t, err)
			cloud.egraphMu.RLock()
			kept, body := res.storedTransfer.Load(), res.stored
			cloud.egraphMu.RUnlock()
			require.Nil(t, kept, "the replacement releases the old normalized record")
			if oldBody != nil {
				require.NotSame(t, oldBody, body, "the replacement releases the old compressed record")
			}
			settle()
			require.Contains(t, string(rootRecord().Envelope.ObjectJSON), "new", "the new record")
		})
	}
}

// storedTestState names a test's storage state of the Cloud's records.
func storedTestState(compressed bool) string {
	if compressed {
		return "compressed"
	}
	return "whole"
}

// storedBundleTestKept reports whether the entry with the number keeps a
// normalized record, or a compressed one.
func storedBundleTestKept(c *Cache, number uint64) bool {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	return res.storedTransfer.Load() != nil || res.stored != nil
}

// A prune that drops a value releases the record a build kept for it,
// normalized or compressed, and the record the entry later takes again is the
// one a build carries.
func TestStoredBundleRecordGoesWithThePrunedValue(t *testing.T) {
	t.Parallel()
	for _, compressed := range []bool{false, true} {
		t.Run(storedTestState(compressed), func(t *testing.T) {
			t.Parallel()
			ctx, cloud, r, p := storedBundleTestCloudIn(t, "root", !compressed)
			if compressed {
				storedTestCompress(cloud)
			}
			_, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
			require.NoError(t, err)
			require.True(t, storedBundleTestKept(cloud, r))
			require.True(t, storedBundleTestKept(cloud, p))

			_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
			require.NoError(t, err)
			require.True(t, removed)
			require.Equal(t, 2, dropped)
			require.False(t, storedBundleTestKept(cloud, r), "released with R's value")
			require.False(t, storedBundleTestKept(cloud, p), "released with P's value")
			got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
			require.NoError(t, err)
			require.Equal(t, []uint64{r}, got.NoValue)

			actx, a, asrv := transferTestCache(t)
			root := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: "again"})
			dep := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
			transferTestDependency(a, actx, root, dep)
			reply, err := cloud.MergeValues(ctx, "cache-b", exportTestBundle(t, actx, a, root))
			require.NoError(t, err)
			require.Equal(t, r, reply.Imported()[0].ResultID, "the same entry")
			if compressed {
				storedTestCompress(cloud)
			}
			got, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
			require.NoError(t, err)
			for _, value := range got.Bundle.Values {
				if value.SenderNumber == r {
					require.Contains(t, string(sentRecord(t, value).Envelope.ObjectJSON), "again")
				}
			}
		})
	}
}

// A restored Cloud builds the bundle it built before it was saved: a restore
// starts with no normalized record and normalizes the restored ones.
func TestStoredBundleAfterARestore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "restored-root", &transferTestValue{Text: "root"})
	p := persistedListTestResult(t, actx, a, asrv, "restored-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r, p)
	root := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r))[0]
	stored, _, err := cloud.SetStoredPart(ctx, root, storedBenchPart(1), 0)
	require.NoError(t, err)
	require.True(t, stored)
	before, err := cloud.StoredBundle(ctx, []uint64{root}, time.Now().Unix(), nil)
	require.NoError(t, err)
	// The restored Cloud is its cache's next start, and keeps its write
	// count and its stamps.
	before.Bundle.SenderCache = ""
	want, err := json.Marshal(before)
	require.NoError(t, err)
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	cloud.egraphMu.RLock()
	normalized := cloud.resultsByID[sharedResultID(root)].storedTransfer.Load() != nil
	cloud.egraphMu.RUnlock()
	require.False(t, normalized, "a restore starts with no normalized record")
	after, err := cloud.StoredBundle(ctx, []uint64{root}, time.Now().Unix(), nil)
	require.NoError(t, err)
	after.Bundle.SenderCache = ""
	got, err := json.Marshal(after)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}

// A stored record that names an entry of its closure that isn't one of its
// dependencies is refused, though relocation finds that entry an ordinal: a
// build checks every reference against the record's dependencies.
func TestStoredBundleRefusesAReferenceToAnIndirectEntry(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "indirect-root", &transferTestValue{Text: "root"})
	mid := persistedListTestResult(t, actx, a, asrv, "indirect-mid", &transferTestValue{Text: "mid"})
	leaf := persistedListTestResult(t, actx, a, asrv, "indirect-leaf", &transferTestValue{Text: "leaf"})
	transferTestDependency(a, actx, root, mid)
	transferTestDependency(a, actx, mid, leaf)
	bundle := exportTestBundle(t, actx, a, root)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	// The test replaces R's record in place, which a build reads only while
	// the record is whole: the body worker stops before the merge, so that it
	// can't compress the record first.
	storedTestHoldWhole(cloud)
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range reply.Values {
		numbers[value.Ordinal] = value.Number
	}
	var r, leafNumber uint64
	for _, value := range bundle.Values {
		switch value.SenderNumber {
		case uint64(root.cacheSharedResult().id):
			r = numbers[value.Ordinal]
		case uint64(leaf.cacheSharedResult().id):
			leafNumber = numbers[value.Ordinal]
		}
	}
	require.NotZero(t, r)
	require.NotZero(t, leafNumber)
	_, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)

	// R's record now names the leaf, which only its dependency depends on.
	payload, err := json.Marshal(&transferTestValue{Text: "root", Child: leafNumber})
	require.NoError(t, err)
	cloud.egraphMu.Lock()
	res := cloud.resultsByID[sharedResultID(r)]
	env := *res.persistedEnvelope
	env.ObjectJSON = payload
	res.persistedEnvelope = &env
	cloud.egraphMu.Unlock()
	_, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.ErrorContains(t, err, "is not a direct dependency")
}

// storedCheckValue's family has a codec that normalizes a payload without
// validating it, as a family's codec may: only its validation refuses a
// payload whose text is "invalid", only its output mapping one whose text is
// "unmapped", and it maps one whose text is "unaddressed" to an output with
// no part. The cache never decodes its records.
type storedCheckValue struct{}

func (*storedCheckValue) Type() *ast.Type {
	return &ast.Type{NamedType: "StoredCheckValue", NonNull: true}
}

type storedCheckCodec struct{}

func (storedCheckCodec) VisitPersistedReferences(v PersistedPayloadVisit, _ PersistedRefVisitor) (json.RawMessage, error) {
	return v.Payload, nil
}
func (storedCheckCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}
func (storedCheckCodec) ValidateForeign(v PersistedPayloadVisit) error {
	if strings.Contains(string(v.Payload), `"invalid"`) {
		return fmt.Errorf("invalid payload")
	}
	return nil
}
func (storedCheckCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	if strings.Contains(string(v.Payload), `"unmapped"`) {
		return nil, fmt.Errorf("unmapped payload")
	}
	if strings.Contains(string(v.Payload), `"unaddressed"`) {
		return []CapturedCodecOutput{{Address: PersistedPartAddress{OutputPath: v.Path}, State: "pending"}}, nil
	}
	return nil, nil
}
func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.StoredCheck", Typed: (*storedCheckValue)(nil), Visitor: storedCheckCodec{}, Transfer: storedCheckCodec{}})
}

// A stored record whose payload its codec refuses, can't map to its outputs,
// or maps to an output with no part, is refused, though the codec normalizes
// it: a build validates and maps each record's payloads when it normalizes
// the record. The test replaces the record in place, which a build reads
// only while the record is whole, so the Cloud's records stay whole.
func TestStoredBundleRefusesAPayloadItsCodecRefuses(t *testing.T) {
	t.Parallel()
	for text, refusal := range map[string]string{"invalid": "invalid payload", "unmapped": "unmapped payload", "unaddressed": "empty part"} {
		ctx, cloud, r, _ := storedBundleTestCloudIn(t, "root", true)
		_, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
		require.NoError(t, err, text)
		cloud.egraphMu.Lock()
		res := cloud.resultsByID[sharedResultID(r)]
		env := *res.persistedEnvelope
		env.ObjectCodec = "dagql_test.StoredCheck"
		env.ObjectJSON = json.RawMessage(`{"text":"` + text + `"}`)
		res.persistedEnvelope = &env
		cloud.egraphMu.Unlock()
		_, err = cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
		require.ErrorContains(t, err, refusal, text)
	}
}

// A payload its codec validated stays valid once its family's visitor
// relocates its references, which validateStoredBundle relies on: a stored
// bundle's records are validated before they are relocated. Every family the
// dagql tests register with a transfer codec has a sample, and the samples
// with references have them moved.
func TestRelocatedTransferPayloadsStayValid(t *testing.T) {
	samples := map[string]struct {
		payload string
		refs    int
	}{
		"dagql_test.Transfer":      {`{"text":"relocated","child":5}`, 1},
		"dagql_test.StoredBench":   {`{"text":"relocated","child":5,"fields":[{"name":"f","description":"d","type":"String!"}]}`, 1},
		"dagql_test.InlineCapture": {`{"text":"relocated"}`, 0},
		"dagql_test.StatePart":     {`{}`, 0},
		"dagql_test.StateTwoPart":  {`{}`, 0},
		"dagql_test.PartTest":      {`{"name":"relocated"}`, 0},
		"dagql_test.PartReuse":     {`{"name":"relocated"}`, 0},
		"dagql_test.StoredCheck":   {`{"text":"relocated"}`, 0},
		"dagql_test.DescribedPart": {`{"done":true}`, 0},
		"dagql_test.ExportChain":   {`{"snapshotID":"relocated","file":"/f"}`, 0},
		"dagql_test.HeldPart":      {`{"snapshotID":"relocated","file":"/f"}`, 0},
		"dagql_test.NormChild":     {`{"text":"relocated","child":5}`, 1},
		"dagql_test.MarkSingle":    {`{"parts":["a"]}`, 0},
		"dagql_test.MarkBatch":     {`{"parts":["a"]}`, 0},
		"dagql_test.PlanDelegate":  {`{"parent":7}`, 0},
		"dagql_test.SyncDelegate":  {`{}`, 0},
		"dagql_test.DecodeChain":   {`{"text":"relocated","child":5}`, 1},
		"dagql_test.ReadPayload":   {`{"text":"relocated"}`, 0},
	}
	for _, family := range PersistedObjectFamilies() {
		// core's families, which this package's external tests register,
		// are core's test's to cover.
		if family.Transfer == nil || !strings.HasPrefix(family.Name, "dagql_test.") {
			continue
		}
		sample, ok := samples[family.Name]
		require.True(t, ok, "family %s has no sample", family.Name)
		v := PersistedPayloadVisit{Version: persistedResultEnvelopeVersion, Payload: json.RawMessage(sample.payload)}
		foreign, err := family.Transfer.NormalizeForeign(v)
		require.NoError(t, err, family.Name)
		v.Payload = foreign.JSON
		require.NoError(t, family.Transfer.ValidateForeign(v), family.Name)
		// A payload validation accepts is in its normal form, relocated or
		// not (PersistedTransferCodec).
		again, err := family.Transfer.NormalizeForeign(v)
		require.NoError(t, err, family.Name)
		require.Equal(t, string(v.Payload), string(again.JSON), "%s: a normal payload normalizes to itself", family.Name)
		moved := 0
		relocated, err := family.Visitor.VisitPersistedReferences(v, func(ref *PersistedRef) error {
			if ref.RecipeID == nil && ref.ResultID != 0 {
				ref.ResultID += 1000
				moved++
			}
			return nil
		})
		require.NoError(t, err, family.Name)
		require.Equal(t, sample.refs, moved, family.Name)
		if moved > 0 {
			require.NotEqual(t, string(v.Payload), string(relocated), family.Name)
		}
		v.Payload = relocated
		require.NoError(t, family.Transfer.ValidateForeign(v), "%s, relocated", family.Name)
		again, err = family.Transfer.NormalizeForeign(v)
		require.NoError(t, err, family.Name)
		require.Equal(t, string(relocated), string(again.JSON), "%s: a relocated payload is still normal", family.Name)
	}
}

// A stored bundle is its caller's copy: changing its records and outputs in
// place reaches neither the stored parts it was built from, which keep their
// addresses and renewal keys, nor the next bundle built from the same entries.
// The root depends on a scalar, whose payload relocation passes through.
func TestStoredBundleIsTheCallersCopy(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "copied-root", &transferTestValue{Text: "root"})
	transferTestDependency(a, actx, root, persistedListTestResult(t, actx, a, asrv, "copied-scalar", String("scalar")))
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, actx, a, root))
	require.NoError(t, err)
	r := reply.Imported()[0].ResultID
	part := storedBenchPart(1)
	stored, _, err := cloud.SetStoredPart(ctx, r, part, 0)
	require.NoError(t, err)
	require.True(t, stored)
	first, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, first.Bundle.Outputs, 1)
	want, err := json.Marshal(first.Bundle)
	require.NoError(t, err)

	scribbleTestValue(reflect.ValueOf(&first.Bundle).Elem(), map[uintptr]bool{})
	second, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	got, err := json.Marshal(second.Bundle)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
	cloud.egraphMu.RLock()
	kept := storedPartTestOffers(storedBundleTestEntry(cloud, r).storedParts)
	cloud.egraphMu.RUnlock()
	key, err := partAddressKey(part.Address)
	require.NoError(t, err)
	require.Equal(t, map[string]PersistedPartOffer{key: part}, kept)
}

// scribbleTestValue overwrites, in place, every exported field v reaches: the
// elements of its slices, the values of its maps and the targets of its
// pointers.
func scribbleTestValue(v reflect.Value, seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		scribbleTestValue(v.Elem(), seen)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				scribbleTestValue(v.Field(i), seen)
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			scribbleTestValue(v.Index(i), seen)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(key))
			scribbleTestValue(elem, seen)
			v.SetMapIndex(key, elem)
		}
	case reflect.String:
		v.SetString("scribbled")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	}
}

// storedPartServiceTestCloud merges a second copy of R, from cache-b, into
// storedBundleTestCloud's Cloud. Its record depends on a service entry S that
// R's doesn't: R keeps its own record, and only cache-b's holding keeps S. It
// returns the numbers of R and S.
func storedPartServiceTestCloud(t *testing.T) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	ctx, cloud, r, p := storedBundleTestCloud(t, "first")
	bctx, b, bsrv := transferTestCache(t)
	br := persistedListTestResult(t, bctx, b, bsrv, "bundle-root", &transferTestValue{Text: "second"})
	bs := persistedListTestResult(t, bctx, b, bsrv, "different-service", &transferTestValue{Text: "service"})
	transferTestDependency(b, bctx, br, bs)
	bundle := exportTestBundle(t, bctx, b, br)
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	var service uint64
	for _, row := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal != row.Ordinal {
				continue
			}
			switch sent.SenderNumber {
			case uint64(br.cacheSharedResult().id):
				require.Equal(t, r, row.Number)
				require.Equal(t, []uint64{p}, row.Deps, "R keeps its own record")
			case uint64(bs.cacheSharedResult().id):
				service = row.Number
			}
		}
	}
	require.NotZero(t, service)
	return ctx, cloud, r, service
}

// storedPartNaming is a part whose service, and so its owner, is the entry
// with the number.
func storedPartNaming(number uint64) PersistedPartOffer {
	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: number, Hostname: "db"}}
	part.Owner.DependencyIDs = []uint64{number}
	return part
}

// storedPartTestDeps returns the dependencies of the entry with the number.
func storedPartTestDeps(c *Cache, number uint64) []uint64 {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var deps []uint64
	for _, id := range c.resultsByID[sharedResultID(number)].deps {
		deps = append(deps, uint64(id))
	}
	slices.Sort(deps)
	return deps
}

// Storing a part makes the entries it names the entry's dependencies, as
// installing a part does on an engine: after the engine that sent the service
// leaves, the service stays, and the entry's stored bundle builds, with the
// service among its record's dependencies.
func TestStoredPartKeepsTheEntriesItNames(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, service := storedPartServiceTestCloud(t)
	stored, _, err := cloud.SetStoredPart(ctx, r, storedPartNaming(service), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.NoError(t, cloud.VerifyPoolMeasure(), "the pool's kept measure follows the dependency the part adds")

	_, _, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-b").Candidates, nil)
	require.NoError(t, err)
	require.NotNil(t, storedBundleTestEntry(cloud, service), "the service outlives its sender")
	got, err := cloud.StoredBundle(ctx, []uint64{r}, time.Now().Unix(), nil)
	require.NoError(t, err)
	ordinals := map[uint64]TransferOrdinal{}
	for _, value := range got.Bundle.Values {
		ordinals[value.SenderNumber] = value.Ordinal
	}
	for _, value := range got.Bundle.Values {
		if value.SenderNumber == r {
			require.Contains(t, value.DependencyIDs, uint64(ordinals[service]), "the service is among R's dependencies")
		}
	}
	require.Len(t, got.Bundle.Outputs, 1)
}

// The dependencies a stored part adds belong to the value: replacing it in
// place releases them, and the service, which nothing else owns, is
// collected.
func TestStoredPartDependenciesGoWithTheValue(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, service := storedPartServiceTestCloud(t)
	stored, _, err := cloud.SetStoredPart(ctx, r, storedPartNaming(service), 0)
	require.NoError(t, err)
	require.True(t, stored)
	_, _, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-b").Candidates, nil)
	require.NoError(t, err)
	require.NotNil(t, storedBundleTestEntry(cloud, service))

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
	fresh, _ := mergeTestSource(t, "bundle-root", "new")
	_, err = cloud.MergeValues(ctx, "cache-c", fresh)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	replacements := storedBundleTestEntry(cloud, r).replacements
	cloud.egraphMu.RUnlock()
	require.Equal(t, uint64(1), replacements, "replaced in place")
	require.NotContains(t, storedPartTestDeps(cloud, r), service)
	require.Nil(t, storedBundleTestEntry(cloud, service), "the service is collected")
	require.NoError(t, cloud.VerifyPoolMeasure(), "the pool's kept measure follows the dependency the replacement took away")
}

// A part that names an entry the cache doesn't have, through its owner or a
// service, or one that reaches the storing entry, which would close a cycle,
// is not stored and adds no dependency.
func TestSetStoredPartRefusesWhatItCannotDependOn(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	ownerOnly := testLiveOffer()
	ownerOnly.Owner.DependencyIDs = []uint64{12345}
	serviceOnly := testLiveOffer()
	serviceOnly.Value.Services = []TransferredServiceBinding{{ServiceResultID: 12345, Hostname: "db"}}
	for _, tc := range []struct {
		name  string
		entry uint64
		part  PersistedPartOffer
	}{
		{"an owner entry the cache doesn't have", r, ownerOnly},
		{"a service the cache doesn't have", r, serviceOnly},
		{"the entry's own dependent", p, storedPartNaming(r)},
		{"the entry itself", r, storedPartNaming(r)},
	} {
		deps := storedPartTestDeps(cloud, tc.entry)
		stored, _, err := cloud.SetStoredPart(ctx, tc.entry, tc.part, 0)
		require.NoError(t, err, tc.name)
		require.False(t, stored, tc.name)
		cloud.egraphMu.RLock()
		parts := storedBundleTestEntry(cloud, tc.entry).storedParts
		cloud.egraphMu.RUnlock()
		require.Empty(t, parts, tc.name)
		require.Equal(t, deps, storedPartTestDeps(cloud, tc.entry), tc.name)
	}
}

// A root a merge is told to leave unretained gets no retention edge: the
// Cloud doesn't count it among its stored roots, and its value lives, as
// on an engine, only through what holds it: here its sender's holding,
// until that engine's cache is released. A root merged as usual is a
// stored root, and outlives its sender's holding.
func TestMergeLeavesAnUnretainedRootWithoutAnEdge(t *testing.T) {
	t.Parallel()
	for _, unretained := range []bool{false, true} {
		t.Run(fmt.Sprint("unretained=", unretained), func(t *testing.T) {
			t.Parallel()
			actx, a, asrv := transferTestCache(t)
			r := persistedListTestResult(t, actx, a, asrv, "unretained-root", &transferTestValue{Text: "root"})
			p := persistedListTestResult(t, actx, a, asrv, "unretained-dep", &transferTestValue{Text: "dep"})
			transferTestDependency(a, actx, r, p)
			bundle := exportTestBundle(t, actx, a, r)
			ctx, cloud := storedPartTestCache(t, WithBlobStore())
			prepared, err := cloud.PrepareValueMerge(ctx, "cache-a", bundle)
			require.NoError(t, err)
			if unretained {
				prepared.Unretained(bundle.Roots[0].Ordinal)
			}
			reply, err := cloud.CommitValueMerge(ctx, prepared)
			require.NoError(t, err)
			require.Len(t, reply.Roots, 1)
			number := reply.Roots[0].Number
			require.NotZero(t, number)
			require.Equal(t, !unretained, reply.Roots[0].Retained)

			state, err := cloud.StoredState()
			require.NoError(t, err)
			if unretained {
				require.Empty(t, state.Roots, "no stored root")
			} else {
				require.Equal(t, []uint64{number}, state.Roots)
			}
			info, ok := cloud.EntryInfo(number)
			require.True(t, ok)
			require.True(t, info.Stored, "its record is stored")

			collect := cloud.ReleaseRemoteCache(ctx, "cache-a")
			_, _, err = cloud.CollectRemoteHoldings(ctx, collect.Candidates, nil)
			require.NoError(t, err)
			_, ok = cloud.EntryInfo(number)
			require.Equal(t, !unretained, ok, "an unretained root lives only through what holds it")
		})
	}
}
