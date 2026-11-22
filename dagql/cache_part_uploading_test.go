package dagql

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A reader marks an upload's start and end while the Cloud's passes read the
// graph: neither must wait for those readers, nor hold up the readers behind
// it. Each returns while the graph is held for reading.
func TestAnUploadsStartAndEndTakeNoWriteHoldOfTheGraph(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	for _, uploading := range []bool{true, false} {
		h.cloud.egraphMu.RLock()
		done := make(chan error, 1)
		go func() {
			if uploading {
				done <- h.cloud.MarkPartUploading(h.m, h.snapshot)
				return
			}
			_, err := h.cloud.EndPartUploads([]PartUpload{{Number: h.m, Address: h.snapshot}})
			done <- err
		}()
		select {
		case err := <-done:
			h.cloud.egraphMu.RUnlock()
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			h.cloud.egraphMu.RUnlock()
			<-done
			t.Fatalf("a mark (uploading %v) waited for a reader of the graph", uploading)
		}
		require.Equal(t, uploading, h.cloud.PartUploading(h.m, h.snapshot))
	}
}

// An upload of a part in progress for one entry changes, for a reader that
// keeps whether a part is needed by such uploads too, every class mate's
// answer: the reader names the class after its start (ClassChange), and its
// end names it (EndPartUploads).
func TestAnUploadsStartAndEndNameItsClassMates(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	require.NoError(t, h.cloud.MarkPartUploading(h.m, h.snapshot))
	require.True(t, h.cloud.PartUploading(h.m, h.snapshot))
	started := h.cloud.ClassChange([]uint64{h.m})
	require.Contains(t, started.Entries, h.m)
	require.Contains(t, started.Entries, h.r)
	ended, err := h.cloud.EndPartUploads([]PartUpload{{Number: h.m, Address: h.snapshot}})
	require.NoError(t, err)
	require.False(t, h.cloud.PartUploading(h.m, h.snapshot))
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
			require.NoError(t, u.cloud.MarkPartUploading(u.number(t, u.bk), uploadJoinPart))
		}
		change := u.join(t)
		if uploading {
			require.Contains(t, change.Entries, u.number(t, u.a2), "a2 now answers from b's upload")
		} else {
			require.NotContains(t, change.Entries, u.number(t, u.a2), "nothing a2 answers changed")
		}
	}
}

// A reader marks an upload, then names the entry's class. A join that lands
// between the two is named twice over: the join sees the mark and names the
// other side, and the naming reads the joined class.
func TestAJoinBetweenAnUploadsMarkAndItsNamingIsNamed(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	b := u.number(t, u.bk)
	require.NoError(t, u.cloud.MarkPartUploading(b, uploadJoinPart))
	joined := u.join(t)
	require.Contains(t, joined.Entries, u.number(t, u.a2), "the join saw b's upload")
	named := u.cloud.ClassChange([]uint64{b})
	for _, key := range []HolderKey{u.a1, u.a2, u.bk} {
		require.Contains(t, named.Entries, u.number(t, key), "the naming reads the joined class")
	}
}

// A join after an upload's naming names the other side: the naming read the
// class before the join, and the join sees the mark.
func TestAJoinAfterAnUploadsNamingIsNamed(t *testing.T) {
	t.Parallel()
	u := newUploadJoinTest(t)
	b := u.number(t, u.bk)
	require.NoError(t, u.cloud.MarkPartUploading(b, uploadJoinPart))
	named := u.cloud.ClassChange([]uint64{b})
	require.Equal(t, []uint64{b}, named.Entries, "b's class is b alone before the join")
	joined := u.join(t)
	require.Contains(t, joined.Entries, u.number(t, u.a2))
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
			require.NoError(t, u.cloud.MarkPartUploading(a1, uploadJoinPart))
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
	require.NoError(t, u.cloud.MarkPartUploading(b, uploadJoinPart))
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
		require.NoError(t, u.cloud.MarkPartUploading(a1, uploadJoinPart))
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
	require.NoError(t, u.cloud.MarkPartUploading(b, uploadJoinPart))
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

	require.NoError(t, u.cloud.MarkPartUploading(a2, uploadJoinPart))
	require.Equal(t, map[uint64]PartAvailability{a1: uploading(a2), a2: uploading(a2), b: none}, answers(), "a2's upload answers for its class, itself included, and not for b's")

	require.NoError(t, u.cloud.MarkPartUploading(a1, uploadJoinPart))
	low := min(a1, a2)
	require.Equal(t, map[uint64]PartAvailability{a1: uploading(low), a2: uploading(low), b: none}, answers(), "the lowest-numbered entry with an upload answers")

	u.join(t)
	require.Equal(t, map[uint64]PartAvailability{a1: uploading(low), a2: uploading(low), b: uploading(low)}, answers(), "the join brings the uploads to b")

	_, err := u.cloud.EndPartUploads([]PartUpload{{Number: a1, Address: uploadJoinPart}, {Number: a2, Address: uploadJoinPart}})
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
	require.NoError(t, u.cloud.MarkPartUploading(a1, uploadJoinPart))
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
		require.NoError(t, h.cloud.MarkPartUploading(h.m, h.snapshot))
		av, err := h.cloud.AvailablePart(h.m, h.snapshot, h.now)
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
		declared, err := cloud.resultsByID[sharedResultID(absent)].declaredPartsLocked()
		cloud.egraphMu.RUnlock()
		require.NoError(t, err)
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
		_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 1}, RemoteHolding{Recipe: recipe, Request: recipe, Field: field, ContentDigest: content})
		require.NoError(t, err)
		mate := holdingOf("uploading-absent-mate")
		mate.ContentDigest = content
		_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 2}, mate)
		require.NoError(t, err)
		held, ok := cloud.RemoteHoldingInfo(HolderKey{"cache-c", 2})
		require.True(t, ok)
		require.NoError(t, cloud.MarkPartUploading(held.Number, address))
		av, err := cloud.AvailablePart(held.Number, address, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartAbsent, av.State)
		require.Equal(t, absent, av.Donor, "the absent value's record marks it")
	})
}
