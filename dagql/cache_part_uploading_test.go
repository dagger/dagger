package dagql

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startUpload starts an upload of the part at address for the entry numbered
// number (StartPartUploads), which must start, and returns the start's change.
func startUpload(t *testing.T, c *Cache, number uint64, address PersistedPartAddress) RemoteChange {
	t.Helper()
	starts, change := c.StartPartUploads([]PartStartQuery{{PartQuery: PartQuery{Number: number, Address: address}}}, time.Now().Unix())
	require.Len(t, starts, 1)
	require.NoError(t, starts[0].Err)
	require.True(t, starts[0].Started, "the upload of %d's part starts: %+v", number, starts[0])
	return change
}

// An upload of a part in progress for one entry changes every class mate's
// answer (PartUploading): its start names the class, and so does its end.
func TestAnUploadsStartAndEndNameItsClassMates(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	mount := PersistedPartAddress{Part: "mount:/start"}
	started := startUpload(t, h.cloud, h.m, mount)
	require.True(t, h.cloud.PartUploading(h.m, mount))
	require.Contains(t, started.Entries, h.m)
	require.Contains(t, started.Entries, h.r)
	ended, err := h.cloud.EndPartUploads([]PartUpload{{Number: h.m, Address: mount}})
	require.NoError(t, err)
	require.False(t, h.cloud.PartUploading(h.m, mount))
	require.Contains(t, ended.Entries, h.m)
	require.Contains(t, ended.Entries, h.r)
}

// uploadJoinTest is a Cloud cache with two classes, of contents a and b: a1
// and a2 in one, b in the other, each an engine's holding with no value and
// no part, so nothing but an upload can hold a part for either class.
type uploadJoinTest struct {
	ctx        context.Context
	cloud      *Cache
	a1, a2, bk HolderKey
}

func newUploadJoinTest(t *testing.T) *uploadJoinTest {
	t.Helper()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	u := &uploadJoinTest{ctx: ctx, cloud: cloud, a1: HolderKey{"cache-c", 1}, a2: HolderKey{"cache-c", 2}, bk: HolderKey{"cache-c", 3}}
	u.attach(t, u.a1, "join-a1", "join-content-a")
	u.attach(t, u.a2, "join-a2", "join-content-a")
	u.attach(t, u.bk, "join-b", "join-content-b")
	class, ok := cloud.EntryClassEntries(u.number(t, u.a1))
	require.True(t, ok)
	require.Contains(t, class.Entries, u.number(t, u.a2), "a1 and a2 share a class")
	require.NotContains(t, class.Entries, u.number(t, u.bk), "b is in a class of its own")
	return u
}

func (u *uploadJoinTest) attach(t *testing.T, key HolderKey, field, content string) RemoteChange {
	t.Helper()
	change, err := u.cloud.AttachRemoteHolding(u.ctx, key, RemoteHolding{Recipe: testDigest(field), Request: testDigest(field), Field: field, TypeName: "Directory", ContentDigest: testDigest(content)})
	require.NoError(t, err)
	return change
}

func (u *uploadJoinTest) number(t *testing.T, key HolderKey) uint64 {
	t.Helper()
	held, ok := u.cloud.RemoteHoldingInfo(key)
	require.True(t, ok)
	return held.Number
}

// join teaches a1's holding b's content, which joins the two classes, and
// returns the write's change.
func (u *uploadJoinTest) join(t *testing.T) RemoteChange {
	t.Helper()
	change, found, err := u.cloud.UpdateRemoteHolding(u.ctx, u.a1, RemoteHoldingUpdate{ContentDigest: testDigest("join-content-b")})
	require.NoError(t, err)
	require.True(t, found)
	class, ok := u.cloud.EntryClassEntries(u.number(t, u.bk))
	require.True(t, ok)
	require.Contains(t, class.Entries, u.number(t, u.a2), "the classes joined")
	return change
}

var uploadJoinPart = PersistedPartAddress{Part: "fs"}

// A join of two classes brings an upload in progress on one side to the
// other side's entries: the join names them, though no entry of either side
// stores a part or marks one absent. Without the upload, it names none of
// them.
func TestChangeReportsAJoinNamesASideWhenTheOtherUploads(t *testing.T) {
	t.Parallel()
	for _, uploading := range []bool{true, false} {
		u := newUploadJoinTest(t)
		if uploading {
			startUpload(t, u.cloud, u.number(t, u.bk), uploadJoinPart)
		}
		change := u.join(t)
		if uploading {
			require.Contains(t, change.Entries, u.number(t, u.a2), "a2 now answers from b's upload")
		} else {
			require.NotContains(t, change.Entries, u.number(t, u.a2), "nothing a2 answers changed")
		}
	}
}

// startQueries are queries to start, none only to answer.
func startQueries(queries ...PartQuery) []PartStartQuery {
	out := make([]PartStartQuery, len(queries))
	for i, q := range queries {
		out[i].PartQuery = q
	}
	return out
}

// A batch answers its parts in order: a part only to answer counts the starts
// before it in the batch and none after, and starts nothing. Here a1's part
// is answered before a2's start and after it, in one batch, and a part to
// answer whose class uploads it says so, the entry that uploads it named.
func TestAStartsBatchAnswersInOrderAndStartsNothingOnlyToAnswer(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	a1, a2 := u.number(t, u.a1), u.number(t, u.a2)
	answer := PartStartQuery{PartQuery: PartQuery{Number: a1, Address: uploadJoinPart}, AnswerOnly: true}
	starts, _ := u.cloud.StartPartUploads([]PartStartQuery{answer, {PartQuery: PartQuery{Number: a2, Address: uploadJoinPart}}, answer}, time.Now().Unix())
	require.Len(t, starts, 3)
	require.False(t, starts[0].Started, "a part only to answer starts nothing")
	require.Zero(t, starts[0].UploadedBy, "before a2's start nothing uploads it")
	require.True(t, starts[1].Started)
	require.False(t, starts[2].Started)
	require.Equal(t, a2, starts[2].UploadedBy, "after a2's start, its upload answers")
	require.Equal(t, PartUploading, starts[2].State)
	require.Equal(t, map[uint64]int{a2: 1}, u.cloud.UploadCounts())
}

// A start names its entry's class as it counts the upload, in the same hold:
// a join after it names the other side, the join seeing the count.
func TestAJoinAfterAnUploadsStartIsNamed(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	b := u.number(t, u.bk)
	named := startUpload(t, u.cloud, b, uploadJoinPart)
	require.Equal(t, []uint64{b}, named.Entries, "b's class is b alone before the join")
	joined := u.join(t)
	require.Contains(t, joined.Entries, u.number(t, u.a2))
}

// One upload serves a part's class: a start for an entry whose class has an
// upload of the part in progress, the entry itself included, is refused,
// naming the entry that uploads it, and counts nothing. A class joined since
// is the class: an upload on one side refuses a start on the other.
func TestAStartIsRefusedWhileItsClassUploadsThePart(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	a1, a2, b := u.number(t, u.a1), u.number(t, u.a2), u.number(t, u.bk)
	now := time.Now().Unix()
	startUpload(t, u.cloud, a2, uploadJoinPart)
	starts, change := u.cloud.StartPartUploads(startQueries(PartQuery{Number: a1, Address: uploadJoinPart}, PartQuery{Number: a2, Address: uploadJoinPart}, PartQuery{Number: b, Address: uploadJoinPart}), now)
	require.Len(t, starts, 3)
	for i, n := range []uint64{a1, a2} {
		require.NoError(t, starts[i].Err)
		require.False(t, starts[i].Started, "%d's class uploads the part", n)
		require.Equal(t, a2, starts[i].UploadedBy)
		require.Equal(t, PartUploading, starts[i].State)
		require.Equal(t, a2, starts[i].Donor)
	}
	require.True(t, starts[2].Started, "b is in a class of its own")
	require.Zero(t, starts[2].UploadedBy)
	require.Equal(t, []uint64{b}, change.Entries, "only what started is named")
	require.False(t, u.cloud.PartUploading(a1, uploadJoinPart), "a refused start counts nothing")
	require.Equal(t, map[uint64]int{a2: 1, b: 1}, u.cloud.UploadCounts())

	_, err := u.cloud.EndPartUploads([]PartUpload{{Number: b, Address: uploadJoinPart}})
	require.NoError(t, err)
	u.join(t)
	starts, _ = u.cloud.StartPartUploads(startQueries(PartQuery{Number: b, Address: uploadJoinPart}), now)
	require.False(t, starts[0].Started, "the join brought a2's upload to b's class")
	require.Equal(t, a2, starts[0].UploadedBy)
}

// A start batch lets a writer that comes during it in between two parts,
// as a batch read does (giveWayLocked): the writer waits for the part in
// progress, not the batch. Each part's answer, check and count stand whole
// in one hold, so what lands while the batch has let the lock go, a start of
// another reader's included, is seen by the batch's later parts. Here a
// writer comes after the batch's first part, b's; while the batch has let
// go, another reader starts a2's part, and the batch's a1, a2's class mate,
// is refused for it.
func TestAStartBatchGivesWayBetweenParts(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	a1, a2, b := u.number(t, u.a1), u.number(t, u.a2), u.number(t, u.bk)
	var answered, in atomic.Int64
	in.Store(-1)
	done := make(chan struct{})
	u.cloud.testBatchAnswered = func(i int) {
		answered.Store(int64(i + 1))
		if i != 0 {
			return
		}
		go func() {
			defer close(done)
			u.cloud.egraphMu.Lock()
			in.Store(answered.Load())
			u.cloud.egraphMu.Unlock()
		}()
		require.Eventually(t, u.cloud.egraphMu.Waiting, 10*time.Second, time.Millisecond, "the writer waits")
	}
	// While the batch has let the lock go, the writer, waiting or about to,
	// takes it, and then another reader starts a2's part.
	u.cloud.testBatchGaveWay = func() {
		require.Eventually(t, func() bool { return in.Load() >= 0 }, 10*time.Second, time.Millisecond, "the writer gets in")
		u.cloud.testBatchAnswered, u.cloud.testBatchGaveWay = nil, nil
		startUpload(t, u.cloud, a2, uploadJoinPart)
	}
	starts, _ := u.cloud.StartPartUploads(startQueries(PartQuery{Number: b, Address: uploadJoinPart}, PartQuery{Number: a1, Address: uploadJoinPart}), time.Now().Unix())
	require.EqualValues(t, 1, in.Load(), "the writer got in after b's part, before a1's")
	<-done
	require.Len(t, starts, 2)
	require.True(t, starts[0].Started, "b's upload starts")
	require.False(t, starts[1].Started, "a1's is refused: a2's started while the batch let go")
	require.Equal(t, a2, starts[1].UploadedBy)
}

// Starts of class mates' parts refuse each other whatever their holds: of
// many started at once, from readers each holding the graph for reading, one
// starts.
func TestStartsOfClassMatesRefuseEachOther(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	a1, a2 := u.number(t, u.a1), u.number(t, u.a2)
	now := time.Now().Unix()
	var wg sync.WaitGroup
	results := make(chan PartStart, 64)
	for i := range 64 {
		n := a1
		if i%2 == 1 {
			n = a2
		}
		wg.Go(func() {
			starts, _ := u.cloud.StartPartUploads(startQueries(PartQuery{Number: n, Address: uploadJoinPart}), now)
			results <- starts[0]
		})
	}
	wg.Wait()
	close(results)
	started := 0
	for r := range results {
		require.NoError(t, r.Err)
		if r.Started {
			started++
		}
	}
	require.Equal(t, 1, started, "one upload serves the class")
	counts := u.cloud.UploadCounts()
	require.Equal(t, 1, counts[a1]+counts[a2])
}

// A part stored in its class isn't started: the start answers it stored, as
// AvailableParts does, and counts nothing. One the cache doesn't know isn't
// started either, and the rest of the batch is.
func TestAStartAnswersAsAvailablePartsAndStartsOnlyWhatIsNeeded(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	mount := PersistedPartAddress{Part: "mount:/start"}
	queries := []PartQuery{{Number: h.m, Address: h.snapshot}, {Number: 1 << 40, Address: mount}, {Number: h.m, Address: mount}}
	answers := h.cloud.AvailableParts(queries, h.now)
	starts, change := h.cloud.StartPartUploads(startQueries(queries...), h.now)
	require.Len(t, starts, 3)
	require.Equal(t, answers[0].PartAvailability, starts[0].PartAvailability, "stored, by R")
	require.Equal(t, PartStored, starts[0].State)
	require.False(t, starts[0].Started)
	require.Error(t, starts[1].Err, "an entry the cache doesn't have")
	require.False(t, starts[1].Started)
	require.Equal(t, answers[2].PartAvailability, starts[2].PartAvailability, "unavailable, as answered before the start")
	require.True(t, starts[2].Started)
	require.Contains(t, change.Entries, h.r, "the start names M's class")
	require.Equal(t, map[uint64]int{h.m: 1}, h.cloud.UploadCounts())
}

// An entry with an upload in progress, and nothing else that holds a part,
// leaving its class takes the upload from its class mates: its leaving names
// them. Here a1 has no value and goes with its engine's last holding. Without
// the upload, its leaving names none of them.
func TestALeavingOfAnUploadingEntryNamesItsClassMates(t *testing.T) {
	t.Parallel()
	for _, uploading := range []bool{true, false} {
		u := newUploadJoinTest(t)
		a1, a2 := u.number(t, u.a1), u.number(t, u.a2)
		if uploading {
			startUpload(t, u.cloud, a1, uploadJoinPart)
		}
		change, found, err := u.cloud.CollectRemoteHolding(u.ctx, u.a1)
		require.NoError(t, err)
		require.True(t, found)
		u.cloud.egraphMu.RLock()
		gone := u.cloud.resultsByID[sharedResultID(a1)] == nil
		u.cloud.egraphMu.RUnlock()
		require.True(t, gone, "a1 went with its last holding")
		if uploading {
			require.Contains(t, change.Entries, a2, "a2 no longer answers from a1's upload")
		} else {
			require.NotContains(t, change.Entries, a2, "nothing a2 answers changed")
		}
	}
}

// An upload's end, before a join, leaves nothing for the join to bring: the
// end names the class as it is, and the join names no entry of the other
// side.
func TestAJoinAfterAnUploadsEndNamesNothingOfIt(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	b := u.number(t, u.bk)
	startUpload(t, u.cloud, b, uploadJoinPart)
	ended, err := u.cloud.EndPartUploads([]PartUpload{{Number: b, Address: uploadJoinPart}})
	require.NoError(t, err)
	require.False(t, u.cloud.PartUploading(b, uploadJoinPart))
	require.Equal(t, []uint64{b}, ended.Entries, "b's class is b alone at the end")
	joined := u.join(t)
	require.NotContains(t, joined.Entries, u.number(t, u.a2), "the upload ended before the join")
}

// An uploading entry that leaves its class around its upload's end has its
// old class mates named either way: a leaving before the end sees the count,
// and the end, finding the entry gone, names nothing; a leaving after the
// end needs to name nothing, the end having named the class. The end names
// and uncounts in one read, so no leaving lands between the two. Here a1 has
// no value and goes with its engine's last holding.
func TestALeavingAroundAnUploadsEndNamesItsClassMates(t *testing.T) {
	t.Parallel()
	for _, before := range []bool{true, false} {
		u := newUploadJoinTest(t)
		a1, a2 := u.number(t, u.a1), u.number(t, u.a2)
		startUpload(t, u.cloud, a1, uploadJoinPart)
		end := func() RemoteChange {
			change, err := u.cloud.EndPartUploads([]PartUpload{{Number: a1, Address: uploadJoinPart}})
			require.NoError(t, err)
			return change
		}
		leave := func() RemoteChange {
			change, found, err := u.cloud.CollectRemoteHolding(u.ctx, u.a1)
			require.NoError(t, err)
			require.True(t, found)
			return change
		}
		var named []uint64
		if before {
			named = append(append(named, leave().Entries...), end().Entries...)
		} else {
			named = append(append(named, end().Entries...), leave().Entries...)
		}
		u.cloud.egraphMu.RLock()
		gone := u.cloud.resultsByID[sharedResultID(a1)] == nil
		u.cloud.egraphMu.RUnlock()
		require.True(t, gone, "a1 went with its last holding")
		require.False(t, u.cloud.PartUploading(a1, uploadJoinPart), "the end uncounted the upload")
		require.Contains(t, named, a2, "leaving before the end %v", before)
	}
}

// An end with no start changes nothing: a count never goes below none.
func TestEndPartUploadsEndsNoUploadNeverStarted(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	b := u.number(t, u.bk)
	_, err := u.cloud.EndPartUploads([]PartUpload{{Number: b, Address: uploadJoinPart}})
	require.NoError(t, err)
	startUpload(t, u.cloud, b, uploadJoinPart)
	require.True(t, u.cloud.PartUploading(b, uploadJoinPart), "the end before the start didn't cancel it")
}

// A part's answer says when an upload of it is in progress for the entry or
// any entry of its class, so a reader that asks whether the part is needed
// reads no class of its own: the lowest-numbered entry with the upload
// answers, the entry itself included, for that part alone, and a join brings
// the upload to the other side as the uploads' end takes it from all. A batch
// answers as one read does.
func TestAvailablePartAnswersAnUploadInProgressInItsClass(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	a1, a2, b := u.number(t, u.a1), u.number(t, u.a2), u.number(t, u.bk)
	now := time.Now().Unix()
	other := PersistedPartAddress{Part: "other"}
	answers := func() map[uint64]PartAvailability {
		got := map[uint64]PartAvailability{}
		var queries []PartQuery
		for _, n := range []uint64{a1, a2, b} {
			av, err := u.cloud.AvailablePart(n, uploadJoinPart, now)
			require.NoError(t, err)
			got[n] = av
			queries = append(queries, PartQuery{Number: n, Address: uploadJoinPart})
			av, err = u.cloud.AvailablePart(n, other, now)
			require.NoError(t, err)
			require.Equal(t, PartAvailability{}, av, "an upload of fs answers for fs alone")
		}
		for i, a := range u.cloud.AvailableParts(queries, now) {
			require.NoError(t, a.Err)
			require.Equal(t, got[queries[i].Number], a.PartAvailability, "a batch answers as one read does")
		}
		return got
	}
	none := PartAvailability{}
	uploading := func(donor uint64) PartAvailability { return PartAvailability{State: PartUploading, Donor: donor} }
	require.Equal(t, map[uint64]PartAvailability{a1: none, a2: none, b: none}, answers(), "nothing holds the part")

	startUpload(t, u.cloud, a2, uploadJoinPart)
	require.Equal(t, map[uint64]PartAvailability{a1: uploading(a2), a2: uploading(a2), b: none}, answers(), "a2's upload answers for its class, itself included, and not for b's")

	startUpload(t, u.cloud, b, uploadJoinPart)
	require.Equal(t, map[uint64]PartAvailability{a1: uploading(a2), a2: uploading(a2), b: uploading(b)}, answers(), "b's upload answers for its own class")

	u.join(t)
	low := min(a2, b)
	require.Equal(t, map[uint64]PartAvailability{a1: uploading(low), a2: uploading(low), b: uploading(low)}, answers(), "the join brings each side's upload to the other, and the lowest-numbered entry with one answers")

	_, err := u.cloud.EndPartUploads([]PartUpload{{Number: a2, Address: uploadJoinPart}, {Number: b, Address: uploadJoinPart}})
	require.NoError(t, err)
	require.Equal(t, map[uint64]PartAvailability{a1: none, a2: none, b: none}, answers(), "the uploads' end takes them from every answer")
}

// An entry with an upload in progress leaving its class takes the upload
// from its class mates' answers, though its count stays until its reader
// ends it: only the class's entries answer.
func TestALeavingTakesAnUploadFromItsClassMatesAnswers(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	a1, a2 := u.number(t, u.a1), u.number(t, u.a2)
	now := time.Now().Unix()
	startUpload(t, u.cloud, a1, uploadJoinPart)
	av, err := u.cloud.AvailablePart(a2, uploadJoinPart, now)
	require.NoError(t, err)
	require.Equal(t, PartAvailability{State: PartUploading, Donor: a1}, av)
	_, found, err := u.cloud.CollectRemoteHolding(u.ctx, u.a1)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, u.cloud.PartUploading(a1, uploadJoinPart), "the count stays until its reader ends it")
	av, err = u.cloud.AvailablePart(a2, uploadJoinPart, now)
	require.NoError(t, err)
	require.Equal(t, PartAvailability{}, av, "a1 left a2's class")
}

// A part stored, or marked absent by a stored record, in the class answers
// before an upload of it in progress: a reader needs neither, whatever is
// uploading, and names the part as it is.
func TestAStoredOrAbsentPartAnswersBeforeAnUpload(t *testing.T) {
	t.Parallel()
	t.Run("stored", func(t *testing.T) {
		t.Parallel()
		h := newHolderTestClass(t)
		mount := storedPruneTestPart("mount:/stored", map[string]int64{"holder-r-stored": 10})
		startUpload(t, h.cloud, h.m, mount.Address)
		stored, _, err := h.cloud.SetStoredPart(h.ctx, h.r, mount, 0)
		require.NoError(t, err)
		require.True(t, stored)
		require.True(t, h.cloud.PartUploading(h.m, mount.Address), "M's upload is still counted")
		av, err := h.cloud.AvailablePart(h.m, mount.Address, h.now)
		require.NoError(t, err)
		require.Equal(t, PartStored, av.State)
		require.Equal(t, h.r, av.Donor, "R stores it")
	})
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		bctx, b, bsrv := transferTestCache(t)
		bsrv.InstallObject(NewClass(bsrv, ClassOpts[*inlineCaptureTestValue]{}))
		absentRes := persistedListTestResult(t, bctx, b, bsrv, "uploading-absent", &inlineCaptureTestValue{})
		_, numbers := changeTestMerge(t, ctx, cloud, "cache-b", exportTestBundle(t, bctx, b, absentRes))
		absent := numbers[uint64(absentRes.cacheSharedResult().id)]
		require.NotZero(t, absent)
		cloud.egraphMu.RLock()
		declared := cloud.resultsByID[sharedResultID(absent)].declaredPartsLocked()
		cloud.egraphMu.RUnlock()
		var key string
		for _, part := range declared {
			if part.state == "absent" {
				key = part.key
				break
			}
		}
		require.NotEmpty(t, key, "the value marks a part absent")
		var address PersistedPartAddress
		require.NoError(t, json.Unmarshal([]byte(key), &address))
		recipe, field := changeTestRecipe(cloud, absent)
		content := testDigest("uploading-absent-content")
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 1}, RemoteHolding{Recipe: recipe, Request: recipe, Field: field, ContentDigest: content})
		require.NoError(t, err)
		mate := holdingOf("uploading-absent-mate")
		mate.ContentDigest = content
		_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 2}, mate)
		require.NoError(t, err)
		held, ok := cloud.RemoteHoldingInfo(HolderKey{"cache-c", 2})
		require.True(t, ok)
		startUpload(t, cloud, held.Number, address)
		av, err := cloud.AvailablePart(held.Number, address, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartAbsent, av.State)
		require.Equal(t, absent, av.Donor, "the absent value's record marks it")
		starts, _ := cloud.StartPartUploads([]PartStartQuery{{PartQuery: PartQuery{Number: held.Number, Address: address}, AnswerOnly: true}}, time.Now().Unix())
		require.Equal(t, PartAbsent, starts[0].State, "a start answers as AvailableParts does")
		require.Equal(t, held.Number, starts[0].UploadedBy, "and names the upload in progress all the same")
	})
}
