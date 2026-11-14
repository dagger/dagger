package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// changedSinceTestBuild builds a stored bundle of the roots.
func changedSinceTestBuild(t *testing.T, cloud *Cache, roots ...uint64) StoredBundleResult {
	t.Helper()
	built, err := cloud.StoredBundle(t.Context(), roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	return built
}

func changedSinceTestOne(t *testing.T, cloud *Cache, number, position uint64) bool {
	t.Helper()
	changed := cloud.ChangedSince([]uint64{number}, position)
	require.Len(t, changed, 1)
	return changed[0]
}

// changedSinceTestRecords returns, by Cloud entry number, what a bundle
// carries of each record: its value, with the rows it names by ordinal named
// by entry number, its outputs, and the calls that return it.
func changedSinceTestRecords(t *testing.T, bundle ValueBundle) map[uint64]string {
	t.Helper()
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range bundle.Values {
		numbers[value.Ordinal] = value.SenderNumber
	}
	parts := map[uint64][]string{}
	add := func(number uint64, kind string, v any) {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		parts[number] = append(parts[number], kind+string(raw))
	}
	for _, value := range bundle.Values {
		number := value.SenderNumber
		value.Ordinal = 0
		deps := make([]uint64, len(value.DependencyIDs))
		for i, dep := range value.DependencyIDs {
			deps[i] = numbers[TransferOrdinal(dep)]
		}
		value.DependencyIDs = deps
		add(number, "value:", value)
	}
	for _, out := range bundle.Outputs {
		number := numbers[out.Ordinal]
		out.Ordinal = 0
		add(number, "output:", out)
	}
	for _, call := range bundle.Calls {
		parts[numbers[call.Value]] = append(parts[numbers[call.Value]], "call:"+call.Call)
	}
	records := map[uint64]string{}
	for number, p := range parts {
		slices.Sort(p)
		records[number] = strings.Join(p, "\n")
	}
	return records
}

// changedSinceTestRebuild builds the roots' bundle again and requires that
// ChangedSince reports at before's position every record whose bytes the
// new build differs in, or that it no longer carries, but the records before
// carried partially (StoredBundleResult.Partial), which a reader never takes
// as current. It returns the records that differ.
func changedSinceTestRebuild(t *testing.T, cloud *Cache, before StoredBundleResult, roots ...uint64) []uint64 {
	t.Helper()
	after := changedSinceTestBuild(t, cloud, roots...)
	was, now := changedSinceTestRecords(t, before.Bundle), changedSinceTestRecords(t, after.Bundle)
	var differ []uint64
	for number, record := range was {
		if now[number] != record {
			differ = append(differ, number)
		}
	}
	slices.Sort(differ)
	for _, number := range differ {
		if slices.Contains(before.Partial, number) {
			continue
		}
		require.True(t, changedSinceTestOne(t, cloud, number, before.Position), "entry %d's record differs in a build now, and is reported changed", number)
	}
	return differ
}

// changedSinceTestJoined waits until the body worker has given both entries'
// frames the content digest that joins them in one class
// (storedBundleTestClassMember): the join is a write of its own, which a
// test of a later write mustn't race.
func changedSinceTestJoined(t *testing.T, cloud *Cache, numbers ...uint64) {
	t.Helper()
	for _, number := range numbers {
		storedContentTestWait(t, cloud, number, testDigest("bundle-class-content"))
	}
}

// changedSinceTestNumbers returns a's entry numbers of the results.
func changedSinceTestNumbers(a *Cache, results ...AnyResult) []uint64 {
	a.egraphMu.RLock()
	defer a.egraphMu.RUnlock()
	var numbers []uint64
	for _, res := range results {
		numbers = append(numbers, uint64(res.cacheSharedResult().id))
	}
	return numbers
}

// A stored bundle's position tells, for each record it carries, whether a
// write since changed what a bundle of it would carry: its record, expiry,
// stored parts, kept calls or frame, a part it borrows from an entry of its
// class, or its value being dropped. Each case builds the bundle again after
// the write and requires every record that differs to be reported. A holding
// added and removed, or a write to another entry, changes nothing of it. An
// entry that is gone has changed.
func TestChangedSinceAStoredBundle(t *testing.T) {
	t.Parallel()

	t.Run("nothing changed", func(t *testing.T) {
		t.Parallel()
		_, cloud, r, p := storedBundleTestCloud(t, "root")
		before := changedSinceTestBuild(t, cloud, r)
		require.Equal(t, []bool{false, false}, cloud.ChangedSince([]uint64{r, p}, before.Position))
		require.Empty(t, changedSinceTestRebuild(t, cloud, before, r))
		require.Equal(t, before.Position, cloud.BundlePosition())
	})

	t.Run("a part stored on the entry, with no new dependency", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, p := storedBundleTestCloud(t, "root")
		before := changedSinceTestBuild(t, cloud, r)
		stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r))
		require.False(t, changedSinceTestOne(t, cloud, p, before.Position), "its dependency's own record and parts didn't change")
		require.Greater(t, cloud.BundlePosition(), before.Position)
	})

	t.Run("a part stored on its dependency", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, p := storedBundleTestCloud(t, "root")
		before := changedSinceTestBuild(t, cloud, r)
		stored, _, err := cloud.SetStoredPart(ctx, p, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		require.Equal(t, []uint64{p}, changedSinceTestRebuild(t, cloud, before, r))
		require.False(t, changedSinceTestOne(t, cloud, r, before.Position), "the root's own record and parts didn't change: the dependency is a record of its own")
	})

	t.Run("a part stored on an entry of its class, which the bundle borrows", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		member := storedBundleTestClassMember(t, ctx, cloud, r, "changed-since-member")
		changedSinceTestJoined(t, cloud, r, member)
		before := changedSinceTestBuild(t, cloud, r)
		require.Empty(t, before.Bundle.Outputs)
		stored, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r), "the build now borrows the member's part")
	})

	t.Run("stored parts dropped", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		part := storedBenchPart(1)
		stored, _, err := cloud.SetStoredPart(ctx, r, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		before := changedSinceTestBuild(t, cloud, r)
		dropped, _, err := cloud.DropStoredParts(part.Chain.Layers[0].Descriptor.Digest)
		require.NoError(t, err)
		require.Len(t, dropped, 1)
		require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r))
	})

	t.Run("the record replaced", func(t *testing.T) {
		t.Parallel()
		bundle, _ := mergeTestSource(t, "changed-since-replaced", "x")
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
		require.NoError(t, err)
		number := reply.Values[0].Number
		mergeTestExpire(cloud, storedBundleTestEntry(cloud, number))
		before := changedSinceTestBuild(t, cloud, number)
		reply, err = cloud.MergeValues(ctx, "cache-a", bundle)
		require.NoError(t, err)
		require.Equal(t, number, reply.Values[0].Number)
		require.Equal(t, uint64(1), reply.Values[0].Replacements, "replaced in place")
		require.Equal(t, []uint64{number}, changedSinceTestRebuild(t, cloud, before, number))
	})

	t.Run("a call kept on it, its expiry unchanged", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		test := newCallRecordTest(t, ctx, cloud)
		before := changedSinceTestBuild(t, cloud, test.number)
		test.attach(t, ctx, test.span(t, true, 0))
		require.Equal(t, []uint64{test.number}, changedSinceTestRebuild(t, cloud, before, test.number), "the build now carries the call")
	})

	t.Run("its expiry moved by a call", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		test := newCallRecordTest(t, ctx, cloud)
		before := changedSinceTestBuild(t, cloud, test.number)
		test.attach(t, ctx, test.span(t, false, time.Now().Add(time.Hour).Unix()))
		require.Equal(t, []uint64{test.number}, changedSinceTestRebuild(t, cloud, before, test.number))
	})

	t.Run("its frame given a taught content digest by the body worker", func(t *testing.T) {
		t.Parallel()
		actx, a, asrv := transferTestCache(t)
		first := persistedListTestResult(t, actx, a, asrv, "changed-since-first", &transferTestValue{Text: "first"})
		r := persistedListTestResult(t, actx, a, asrv, "changed-since-taught", &transferTestValue{Text: "x"})
		aNumbers := changedSinceTestNumbers(a, first, r)
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		numbers := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, first, r))
		require.Len(t, numbers, 2)
		var number uint64
		for _, n := range numbers {
			if storedTestFrame(t, cloud, storedBundleTestEntry(cloud, n)).Field == "changed-since-taught" {
				number = n
			}
		}
		require.NotZero(t, number)
		// The worker gives the first entry its digest, then is held, so
		// the second's waits behind it while the bundle is built.
		blocked, release, _ := storedContentTestHold(t, cloud)
		storedContentTestTeach(t, ctx, cloud, aNumbers[0], testDigest("changed-since-first-content"))
		<-blocked
		content := testDigest("changed-since-taught-content")
		storedContentTestTeach(t, ctx, cloud, aNumbers[1], content)
		before := changedSinceTestBuild(t, cloud, number)
		release()
		storedContentTestWait(t, cloud, number, content)
		require.Equal(t, []uint64{number}, changedSinceTestRebuild(t, cloud, before, number), "the build now carries the digest in the frame")
	})

	t.Run("its value dropped by a prune", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, p := storedBundleTestCloud(t, "root")
		before := changedSinceTestBuild(t, cloud, r)
		report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
		require.NoError(t, err)
		require.Equal(t, 2, report.DroppedValues)
		require.Equal(t, []bool{true, true}, cloud.ChangedSince([]uint64{r, p}, before.Position))
	})

	t.Run("another engine's holding added, then released", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, p := storedBundleTestCloud(t, "root")
		before := changedSinceTestBuild(t, cloud, r)
		cloud.egraphMu.RLock()
		recipe := storedBundleTestEntry(cloud, r).recipeKeys[0]
		cloud.egraphMu.RUnlock()
		key := HolderKey{Cache: "cache-b", Number: 7}
		_, err := cloud.AttachRemoteHolding(ctx, key, RemoteHolding{Recipe: recipe, Request: recipe})
		require.NoError(t, err)
		require.Equal(t, []bool{false, false}, cloud.ChangedSince([]uint64{r, p}, before.Position), "a hit's holding changes no bundle")
		_, _, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-b").Candidates)
		require.NoError(t, err)
		require.Equal(t, []bool{false, false}, cloud.ChangedSince([]uint64{r, p}, before.Position), "nor does its engine leaving")
		require.Empty(t, changedSinceTestRebuild(t, cloud, before, r))
	})

	t.Run("a write to another entry", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		other := storedPruneTestRoot(t, ctx, cloud, "changed-since-other")
		before := changedSinceTestBuild(t, cloud, r)
		stored, _, err := cloud.SetStoredPart(ctx, other, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		require.True(t, changedSinceTestOne(t, cloud, other, before.Position))
		require.False(t, changedSinceTestOne(t, cloud, r, before.Position))
		require.Empty(t, changedSinceTestRebuild(t, cloud, before, r))
	})

	t.Run("an entry that is gone", func(t *testing.T) {
		t.Parallel()
		_, cloud, r, _ := storedBundleTestCloud(t, "root")
		before := changedSinceTestBuild(t, cloud, r)
		require.True(t, changedSinceTestOne(t, cloud, 12345, before.Position))
	})
}

// A bundle built later has a later position, at which nothing before it has
// changed.
func TestChangedSinceALaterBundle(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	first := changedSinceTestBuild(t, cloud, r)
	stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	second := changedSinceTestBuild(t, cloud, r)
	require.Greater(t, second.Position, first.Position)
	require.True(t, changedSinceTestOne(t, cloud, r, first.Position))
	require.False(t, changedSinceTestOne(t, cloud, r, second.Position))
}

// A record the build carries without a kept call it would send once the
// call's input has a stored record is partial: no write to the record's own
// entry tells when that input is stored, so a reader that keeps the bundle
// can't take the record as current. A record carried whole isn't partial.
func TestStoredBundlePartialRecords(t *testing.T) {
	t.Parallel()

	t.Run("a kept call whose input has no stored record", func(t *testing.T) {
		t.Parallel()
		test := newReceiverCallTest(t)
		cloud := test.cloud
		whole := changedSinceTestBuild(t, cloud, test.number)
		require.Len(t, whole.Bundle.Calls, 1)
		require.Empty(t, whole.Partial)

		cloud.egraphMu.Lock()
		receiver := cloud.resultsByID[sharedResultID(test.receiverNumber)]
		cloud.unindexRecipesLocked(receiver)
		cloud.egraphMu.Unlock()
		partial := changedSinceTestBuild(t, cloud, test.number)
		require.Empty(t, partial.Bundle.Calls)
		require.Equal(t, 1, partial.UnsendableCalls)
		require.Equal(t, []uint64{test.number}, partial.Partial)
	})

	t.Run("a whole record", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		test := newCallRecordTest(t, ctx, cloud)
		test.attach(t, ctx, test.span(t, true, 0))
		built := changedSinceTestBuild(t, cloud, test.number)
		require.Len(t, built.Bundle.Calls, 1)
		require.Empty(t, built.Partial)
	})
}

// BenchmarkChangedSince reads, for a file of test-base's whole set after four
// pushes, 75,188 records, which changed since its build, a tenth of them
// having changed: what the service's watcher reads for each file an engine
// restored, each time the Cloud cache's position has moved.
func BenchmarkChangedSince(b *testing.B) {
	const records = 75_188
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	numbers := make([]uint64, records)
	for i := range numbers {
		id := sharedResultID(1 + 3*i)
		res := &sharedResult{id: id}
		if i%10 == 0 {
			res.changedAt = 2
		}
		c.resultsByID[id] = res
		numbers[i] = uint64(id)
	}
	// Entries other files carry, as a whole pull request's Cloud cache has.
	for i := range 10 * records {
		id := sharedResultID(2 + 3*i)
		c.resultsByID[id] = &sharedResult{id: id}
	}
	b.ResetTimer()
	for b.Loop() {
		changed := c.ChangedSince(numbers, 1)
		if !changed[0] || changed[1] {
			b.Fatal("wrong answer")
		}
	}
}

// StampCauses names the write that last changed each entry's bundle: a part
// stored on it, one stored on an entry of its class, a kept call of a new
// term, and gone for an entry that is gone.
func TestStampCausesNameTheWrite(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	member := storedBundleTestClassMember(t, ctx, cloud, r, "stamp-cause-member")
	changedSinceTestJoined(t, cloud, r, member)
	stored, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.Equal(t, map[string]int{"part stored": 1}, cloud.StampCauses([]uint64{member}))
	causes := cloud.StampCauses([]uint64{r})
	require.Len(t, causes, 1)
	for cause := range causes {
		require.True(t, strings.HasSuffix(cause, " (class)"), "a write to its class member, not to r: %s", cause)
	}
	require.Equal(t, map[string]int{"gone": 1}, cloud.StampCauses([]uint64{12345}))

	cctx, ccloud := storedPartTestCache(t, WithBlobStore())
	test := newCallRecordTest(t, cctx, ccloud)
	test.attach(t, cctx, test.span(t, true, 0))
	require.Equal(t, map[string]int{"kept call, new term": 1}, ccloud.StampCauses([]uint64{test.number}))
}

// stampTestMember merges another record, of a different recipe, into the
// Cloud, with a stored part, without joining it to any class. It returns
// the member's number.
func stampTestMember(t *testing.T, ctx context.Context, cloud *Cache, field string) uint64 {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, "member")
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	member := reply.Imported()[0].ResultID
	ok, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, ok)
	return member
}

// stampTestTeach gives entries' frames a content digest, by the body worker,
// and waits for each.
func stampTestTeach(t *testing.T, ctx context.Context, cloud *Cache, content string, numbers ...uint64) {
	t.Helper()
	for _, number := range numbers {
		require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, number)}, testDigest(content)))
	}
	for _, number := range numbers {
		storedContentTestWait(t, cloud, number, testDigest(content))
	}
}

// A class mate that joins a record's class when the body worker gives its
// frame a taught content digest, and stores a part the record declares,
// changes what a bundle of the record carries: the record borrows the part.
// The worker's write is a write of its own, with no later write behind it,
// and the record is reported changed as soon as it ends.
func TestChangedSinceAJoinByTheBodyWorker(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	member := stampTestMember(t, ctx, cloud, "stamp-join-mate")
	// The record's own frame takes the digest before the build, so only the
	// mate's taking it, the join, comes after.
	stampTestTeach(t, ctx, cloud, "stamp-join", r)
	before := changedSinceTestBuild(t, cloud, r)
	require.Empty(t, before.Bundle.Outputs)
	stampTestTeach(t, ctx, cloud, "stamp-join", member)
	require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r), "the record now borrows the mate's part")
}

// A prune drops its edges one at a time, each in a hold of the graph lock of
// its own, and hands its marks back only once it ends, so a bundle can be
// built between two of its drops. R, A and B share a class, and A and B store
// a part R declares; neither has a holding left. Dropping A's edge collects
// A; a bundle of R built then borrows B's part; dropping B's edge collects B,
// and R's bundle loses the part. R is reported changed since that build as
// soon as the drop's hold ends.
func TestChangedSinceABuildBetweenAPrunesDrops(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	a := stampTestMember(t, ctx, cloud, "stamp-prune-a")
	b := stampTestMember(t, ctx, cloud, "stamp-prune-b")
	stampTestTeach(t, ctx, cloud, "stamp-prune", r, a, b)
	_, _, err := cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-b").Candidates)
	require.NoError(t, err)

	_, removed, _, err := cloud.removePrunedEdge(ctx, sharedResultID(a))
	require.NoError(t, err)
	require.True(t, removed)
	require.Nil(t, storedBundleTestEntry(cloud, a), "A is collected")
	before := changedSinceTestBuild(t, cloud, r)
	require.Len(t, before.Bundle.Outputs, 1, "R borrows B's part")

	_, removed, _, err = cloud.removePrunedEdge(ctx, sharedResultID(b))
	require.NoError(t, err)
	require.True(t, removed)
	require.Nil(t, storedBundleTestEntry(cloud, b), "B is collected")
	require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r), "R's bundle lost the part it borrowed")
}

// narrowTestMember merges another record, of a different recipe, into the
// Cloud, with a stored part if stored, without joining it to any class. It
// returns the member's number and the bundle that made it.
func narrowTestMember(t *testing.T, ctx context.Context, cloud *Cache, field string, stored bool) (uint64, ValueBundle) {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, "member")
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	member := reply.Imported()[0].ResultID
	if stored {
		ok, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, ok)
	}
	return member, bundle
}

// narrowTestJoin joins entries in one class by a content digest, and waits
// for the body worker to have given each its frame.
func narrowTestJoin(t *testing.T, ctx context.Context, cloud *Cache, content string, numbers ...uint64) {
	t.Helper()
	for _, number := range numbers {
		require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, number)}, testDigest(content)))
	}
	for _, number := range numbers {
		storedContentTestWait(t, cloud, number, testDigest(content))
	}
}

// A record's bundle reads its class only to borrow a part a class mate
// stores, so a write to a class mate moves the record's position only when
// it can change what the record borrows. A class mate's value replaced while
// it stores no part, or a join with a class that stores none, leaves the
// record current; a class mate's value replaced while it stores a part the
// record borrows, or a join that brings a stored part, doesn't. The rebuild
// judges each.
func TestChangedSinceAClassMatesWrite(t *testing.T) {
	t.Parallel()

	t.Run("a class mate's value replaced, storing no part", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		member, bundle := narrowTestMember(t, ctx, cloud, "narrow-plain-mate", false)
		narrowTestJoin(t, ctx, cloud, "narrow-plain", r, member)
		mergeTestExpire(cloud, storedBundleTestEntry(cloud, member))
		before := changedSinceTestBuild(t, cloud, r)
		reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
		require.NoError(t, err)
		require.Equal(t, uint64(1), reply.Values[0].Replacements, "the mate's value replaced in place")
		require.True(t, changedSinceTestOne(t, cloud, member, before.Position), "the mate itself changed")
		require.False(t, changedSinceTestOne(t, cloud, r, before.Position), "the record borrows nothing from it")
		require.Empty(t, changedSinceTestRebuild(t, cloud, before, r))
	})

	t.Run("a class mate's value replaced, storing a part the record borrows", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		member, bundle := narrowTestMember(t, ctx, cloud, "narrow-lending-mate", true)
		narrowTestJoin(t, ctx, cloud, "narrow-lending", r, member)
		before := changedSinceTestBuild(t, cloud, r)
		require.Len(t, before.Bundle.Outputs, 1, "the record borrows the mate's part")
		mergeTestExpire(cloud, storedBundleTestEntry(cloud, member))
		_, err := cloud.MergeValues(ctx, "cache-b", bundle)
		require.NoError(t, err)
		require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r), "the replacement dropped the part it borrowed")
	})

	t.Run("a join with a class that stores a part", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		member, _ := narrowTestMember(t, ctx, cloud, "narrow-join-lending", true)
		// The record's own frame takes the digest before the build: only the
		// mate's taking it, the join, comes after.
		narrowTestJoin(t, ctx, cloud, "narrow-join-lending", r)
		before := changedSinceTestBuild(t, cloud, r)
		require.Empty(t, before.Bundle.Outputs)
		narrowTestJoin(t, ctx, cloud, "narrow-join-lending", member)
		require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r), "the record now borrows the mate's part")
	})

	t.Run("a join with a class that stores none", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		member, _ := narrowTestMember(t, ctx, cloud, "narrow-join-plain", false)
		narrowTestJoin(t, ctx, cloud, "narrow-join-plain", r)
		before := changedSinceTestBuild(t, cloud, r)
		narrowTestJoin(t, ctx, cloud, "narrow-join-plain", member)
		require.Empty(t, changedSinceTestRebuild(t, cloud, before, r))
		require.False(t, changedSinceTestOne(t, cloud, r, before.Position), "nothing it can borrow came")
	})
}

// narrowTestUnretained merges another record, of a different recipe, into
// the Cloud from sender as an unretained root, with a stored part if stored,
// without joining it to any class: it lives only through sender's holding.
// It returns the member's number.
func narrowTestUnretained(t *testing.T, ctx context.Context, cloud *Cache, sender CacheID, field string, stored bool) uint64 {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, "member")
	prepared, err := cloud.PrepareValueMerge(ctx, sender, bundle)
	require.NoError(t, err)
	prepared.Unretained(bundle.Roots[0].Ordinal)
	reply, err := cloud.CommitValueMerge(ctx, prepared)
	require.NoError(t, err)
	require.Len(t, reply.Roots, 1)
	member := reply.Roots[0].Number
	if stored {
		ok, _, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, ok)
	}
	return member
}

// Two class mates of a record that leave their class in one collection, one
// storing no part and one storing the part the record borrows, change the
// record's bundle whichever leaves first: the second, leaving after the
// first walked the class, still takes the part the record borrows with it.
func TestChangedSinceClassMatesLeaveTogether(t *testing.T) {
	t.Parallel()
	for _, plainFirst := range []bool{true, false} {
		t.Run(fmt.Sprint("plain mate's holding first=", plainFirst), func(t *testing.T) {
			t.Parallel()
			ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
			plain := narrowTestUnretained(t, ctx, cloud, "cache-x", "narrow-leave-plain", false)
			lending := narrowTestUnretained(t, ctx, cloud, "cache-y", "narrow-leave-lending", true)
			narrowTestJoin(t, ctx, cloud, "narrow-leave", r, plain, lending)
			before := changedSinceTestBuild(t, cloud, r)
			require.Len(t, before.Bundle.Outputs, 1, "the record borrows the lending mate's part")

			plainKeys := cloud.ReleaseRemoteCache(ctx, "cache-x").Candidates
			lendingKeys := cloud.ReleaseRemoteCache(ctx, "cache-y").Candidates
			candidates := slices.Concat(plainKeys, lendingKeys)
			if !plainFirst {
				candidates = slices.Concat(lendingKeys, plainKeys)
			}
			_, _, err := cloud.CollectRemoteHoldings(ctx, candidates)
			require.NoError(t, err)
			require.Nil(t, storedBundleTestEntry(cloud, plain), "the plain mate is collected")
			require.Nil(t, storedBundleTestEntry(cloud, lending), "the lending mate is collected")
			require.Equal(t, []uint64{r}, changedSinceTestRebuild(t, cloud, before, r), "the record's bundle lost the part it borrowed")
		})
	}
}
