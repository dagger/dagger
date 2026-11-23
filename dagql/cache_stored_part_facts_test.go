package dagql

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// partFactsTestParts are the parts a record of these tests maps to: one in
// each state a record declares or proves.
var partFactsTestParts = []transferTestPart{
	{Part: "snapshot", State: "pending", Kind: "directory"},
	{Part: "fs", State: "completed", Kind: "directory"},
	{Part: "mount:/a", State: "absent", Kind: "directory"},
	{Part: "metadata", State: "metadata"},
}

var (
	partFactsSnapshot = PersistedPartAddress{Part: "snapshot"}
	partFactsFS       = PersistedPartAddress{Part: "fs"}
	partFactsMount    = PersistedPartAddress{Part: "mount:/a"}
)

// partFactsTestForm is how a test holds the Cloud's copy of its record when
// it asks: whole, as a merge installs it, or compressed by the body worker,
// with its frame or without it.
type partFactsTestForm string

const (
	partFactsWhole        partFactsTestForm = "whole"
	partFactsCompressed   partFactsTestForm = "compressed"
	partFactsWithoutFrame partFactsTestForm = "compressed without its frame"
)

var partFactsForms = []partFactsTestForm{partFactsWhole, partFactsCompressed, partFactsWithoutFrame}

// partFactsTestCloud is a Cloud cache with its body worker stopped, so a test
// decides when its records are compressed.
func partFactsTestCloud(t *testing.T) (context.Context, *Cache) {
	t.Helper()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	storedTestHoldWhole(cloud)
	return ctx, cloud
}

// partFactsTestMerge merges an engine's record of field, whose payload's text
// is text, with parts, into cloud from the engine named from, and returns the
// Cloud's entry for it.
func partFactsTestMerge(t *testing.T, ctx context.Context, cloud *Cache, from CacheID, field, text string, parts []transferTestPart) *sharedResult {
	t.Helper()
	ectx, engine, srv := transferTestCache(t)
	out := persistedListTestResult(t, ectx, engine, srv, field, &transferTestValue{Text: text, Parts: parts})
	reply, err := cloud.MergeValues(ctx, from, exportTestBundle(t, ectx, engine, out))
	require.NoError(t, err)
	require.True(t, reply.Committed)
	entry, _ := cloudEntryOfRecipe(t, cloud, engine, out)
	require.NotNil(t, entry)
	return entry
}

// partFactsTestHold puts every stored record of cloud in form.
func partFactsTestHold(t *testing.T, cloud *Cache, form partFactsTestForm) {
	t.Helper()
	switch form {
	case partFactsCompressed:
		storedTestCompress(cloud)
	case partFactsWithoutFrame:
		storedTestCompressWithoutFrames(cloud)
	}
}

// partFactsTestRequireForm checks that entry's record is held in form.
func partFactsTestRequireForm(t *testing.T, cloud *Cache, entry *sharedResult, form partFactsTestForm) {
	t.Helper()
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	switch form {
	case partFactsWhole:
		require.Nil(t, entry.stored, "the record is whole")
	case partFactsCompressed:
		require.True(t, entry.stored.holdsFrame(), "the record is compressed with its frame")
	case partFactsWithoutFrame:
		require.NotNil(t, entry.stored, "the record is compressed")
		require.False(t, entry.stored.holdsFrame(), "the body doesn't hold the frame")
		require.NotNil(t, entry.loadResultCall(), "the entry holds its frame decoded")
	}
}

// partFactsTestInvariant checks the Cloud's invariant on its stored records'
// parts: an entry with a stored record has its parts, and they are the ones
// decoding the record with its frame gives; an entry without one has none. A
// record whose entry kept no parts would read as declaring none, and a save
// would write it so, with nothing to say so. It decodes every stored record,
// so a test that counts decodes checks it outside the count.
func partFactsTestInvariant(t *testing.T, cloud *Cache) {
	t.Helper()
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	for id, res := range cloud.resultsByID {
		if res.persistedEnvelope == nil {
			require.Nil(t, res.partFacts, "entry %d has no stored record, and no parts", id)
			continue
		}
		require.NotNil(t, res.partFacts, "entry %d keeps its stored record's parts", id)
		want := recordPartFacts(storedTestRecordLocked(t, res))
		require.Equal(t, partFactsTestKeys(want), partFactsTestKeys(res.partFacts), "entry %d's parts are its record's", id)
	}
}

// partFactsTestKeys lists parts as the invariant compares them: each declared
// part's key, state and kind, and each complete part's key, sorted.
func partFactsTestKeys(f *partFacts) []string {
	var keys []string
	for _, part := range f.declaredParts() {
		keys = append(keys, "declared "+part.key+" "+part.state+" "+part.kind)
	}
	for _, key := range f.completeParts().list() {
		keys = append(keys, "complete "+key)
	}
	slices.Sort(keys)
	return keys
}

// partFactsTestAsk asks every way a reader asks for the parts of the entry
// numbered number, and checks the answers of a record of partFactsTestParts:
// its absent part is absent, its own donor; its pending part and its
// complete one are stored nowhere.
func partFactsTestAsk(t *testing.T, cloud *Cache, number uint64) {
	t.Helper()
	now := time.Now().Unix()
	av, err := cloud.AvailablePart(number, partFactsMount, now)
	require.NoError(t, err)
	require.Equal(t, PartAbsent, av.State)
	require.Equal(t, number, av.Donor)
	for _, address := range []PersistedPartAddress{partFactsSnapshot, partFactsFS} {
		av, err := cloud.AvailablePart(number, address, now)
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, av.State, "%s", address.Part)
	}
	answers := cloud.AvailableParts([]PartQuery{{Number: number, Address: partFactsMount}, {Number: number, Address: partFactsSnapshot}}, now)
	require.NoError(t, answers[0].Err)
	require.NoError(t, answers[1].Err)
	require.Equal(t, PartAbsent, answers[0].State)
	require.Equal(t, PartUnavailable, answers[1].State)
	starts, _ := cloud.StartPartUploads([]PartStartQuery{{PartQuery: PartQuery{Number: number, Address: partFactsMount}, AnswerOnly: true}}, now)
	require.NoError(t, starts[0].Err)
	require.Equal(t, PartAbsent, starts[0].State)
	require.False(t, starts[0].Started)
}

// A Cloud entry's part answers read the parts its stored record declares
// from what the cache kept of the record when it was stored, never by
// decoding the record again: a part's answer, a batch of answers and an
// upload's start, over a record held whole, as a merge installs it, and over
// one compressed, with its frame or without it, decode nothing. On the
// eighteenth whole run's fill, decoding each whole record's envelope again
// for every answer was 92% of passes' CPU under the Cloud cache's lock.
func TestAPartAnswerDecodesNoStoredRecord(t *testing.T) {
	t.Parallel()
	for _, form := range partFactsForms {
		t.Run(string(form), func(t *testing.T) {
			t.Parallel()
			text := "part-facts-answer-" + string(form)
			ctx, cloud := partFactsTestCloud(t)
			entry := partFactsTestMerge(t, ctx, cloud, "engine", "part-facts-answer", text, partFactsTestParts)
			partFactsTestHold(t, cloud, form)
			partFactsTestRequireForm(t, cloud, entry, form)
			partFactsTestInvariant(t, cloud)
			decoded := transferTestMappedCount(text)
			for range 3 {
				partFactsTestAsk(t, cloud, uint64(entry.id))
			}
			require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the record")
		})
	}
}

// The body worker's compressing of a record a merge installed whole leaves
// its entry's parts as they were: the answers before it and after it are the
// same, and neither decodes the record.
func TestTheBodyWorkersInstallKeepsAnEntrysParts(t *testing.T) {
	t.Parallel()
	text := "part-facts-body-install"
	ctx, cloud := partFactsTestCloud(t)
	entry := partFactsTestMerge(t, ctx, cloud, "engine", "part-facts-body-install", text, partFactsTestParts)
	partFactsTestInvariant(t, cloud)
	decoded := transferTestMappedCount(text)
	partFactsTestAsk(t, cloud, uint64(entry.id))
	require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the whole record")

	storedTestCompress(cloud)
	partFactsTestRequireForm(t, cloud, entry, partFactsCompressed)
	partFactsTestInvariant(t, cloud)
	decoded = transferTestMappedCount(text)
	partFactsTestAsk(t, cloud, uint64(entry.id))
	require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the compressed record")
}

// A content digest taught to an entry replaces its frame, and the parts its
// record declares are worked out for the new frame when the frame is
// replaced, not again at each answer after it: whether the record is held
// whole, compressed with its frame, or compressed with its frame decoded on
// the entry, whose body the frame no longer matches.
func TestATaughtContentDigestWorksOutAnEntrysPartsOnce(t *testing.T) {
	t.Parallel()
	for _, form := range partFactsForms {
		t.Run(string(form), func(t *testing.T) {
			t.Parallel()
			text := "part-facts-taught-" + string(form)
			ctx, cloud := partFactsTestCloud(t)
			entry := partFactsTestMerge(t, ctx, cloud, "engine", "part-facts-taught", text, partFactsTestParts)
			partFactsTestHold(t, cloud, form)
			before := entry.loadResultCall()
			cloud.egraphMu.RLock()
			if before == nil {
				before, _ = entry.entryFrameLocked().decode()
			}
			cloud.egraphMu.RUnlock()
			require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: entry}, testDigest(text+"-content")))
			cloud.egraphMu.RLock()
			after, err := entry.entryFrameLocked().decode()
			cloud.egraphMu.RUnlock()
			require.NoError(t, err)
			require.Equal(t, testDigest(text+"-content"), after.ContentDigest(), "the frame was replaced")
			require.NotEqual(t, before.ContentDigest(), after.ContentDigest())
			partFactsTestInvariant(t, cloud)

			decoded := transferTestMappedCount(text)
			for range 3 {
				partFactsTestAsk(t, cloud, uint64(entry.id))
			}
			require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the record")
		})
	}
}

// A later merge of a record of the same recipe changes the entry's parts
// only where it changes its record. One that keeps the entry's value, which
// hasn't expired, leaves its parts as its record declares them. One that
// replaces the expired value in place, as the Cloud always does, brings the
// new record's parts: here a part the old record declared absent is pending
// in the new one, and a part pending in the old one it proves complete.
// Neither record is decoded to answer.
func TestALaterMergeOfTheSameRecipeBringsItsRecordsParts(t *testing.T) {
	t.Parallel()
	newer := []transferTestPart{
		{Part: "snapshot", State: "completed", Kind: "directory"},
		{Part: "fs", State: "completed", Kind: "directory"},
		{Part: "mount:/a", State: "pending", Kind: "directory"},
		{Part: "metadata", State: "metadata"},
	}
	// has reports whether the entry's record has a part in the state, as a
	// merge checks a held row's output against it.
	has := func(cloud *Cache, entry *sharedResult, address PersistedPartAddress, state string) bool {
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		return cloud.entryHasPartsLocked(entry, []TransferredOutput{{Address: address, State: state}})
	}
	// partState is the state the entry's record has a part in: a held row's
	// pending part holds against a completed one too, so has doesn't say.
	partState := func(cloud *Cache, entry *sharedResult, address PersistedPartAddress) string {
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		parts, ok := cloud.recordPartsLocked(entry)
		require.True(t, ok)
		key, err := partAddressKey(address)
		require.NoError(t, err)
		part, ok := parts.at(key, address)
		require.True(t, ok)
		return part.State
	}

	t.Run("kept", func(t *testing.T) {
		t.Parallel()
		text, later := "part-facts-kept", "part-facts-kept-later"
		ctx, cloud := partFactsTestCloud(t)
		entry := partFactsTestMerge(t, ctx, cloud, "engine-a", "part-facts-kept", text, partFactsTestParts)
		kept := partFactsTestMerge(t, ctx, cloud, "engine-b", "part-facts-kept", later, newer)
		require.Same(t, entry, kept, "the later record lands on the same entry")
		partFactsTestInvariant(t, cloud)
		decoded, decodedLater := transferTestMappedCount(text), transferTestMappedCount(later)
		partFactsTestAsk(t, cloud, uint64(entry.id))
		require.True(t, has(cloud, entry, partFactsSnapshot, "pending"), "the kept record's snapshot is still pending")
		require.True(t, has(cloud, entry, partFactsMount, "absent"), "the kept record's mount is still absent")
		require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the kept record")
		require.Equal(t, decodedLater, transferTestMappedCount(later), "nothing decodes the later record")
	})

	t.Run("replaced in place", func(t *testing.T) {
		t.Parallel()
		text, later := "part-facts-replaced", "part-facts-replaced-later"
		ctx, cloud := partFactsTestCloud(t)
		entry := partFactsTestMerge(t, ctx, cloud, "engine-a", "part-facts-replaced", text, partFactsTestParts)
		mergeTestExpire(cloud, entry)
		replaced := partFactsTestMerge(t, ctx, cloud, "engine-b", "part-facts-replaced", later, newer)
		require.Same(t, entry, replaced, "the expired value is replaced in place")
		cloud.egraphMu.RLock()
		replacements := entry.replacements
		cloud.egraphMu.RUnlock()
		require.EqualValues(t, 1, replacements)
		partFactsTestInvariant(t, cloud)

		decoded, decodedLater := transferTestMappedCount(text), transferTestMappedCount(later)
		av, err := cloud.AvailablePart(uint64(entry.id), partFactsMount, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, av.State, "the new record's mount is pending, not absent")
		require.False(t, has(cloud, entry, partFactsMount, "absent"), "the old record's absent mount went with it")
		require.Equal(t, "completed", partState(cloud, entry, partFactsSnapshot), "the new record proves its snapshot complete")
		require.True(t, has(cloud, entry, partFactsSnapshot, "completed"))
		require.Equal(t, decoded, transferTestMappedCount(text))
		require.Equal(t, decodedLater, transferTestMappedCount(later), "nothing decodes the new record to answer")
	})
}

// A Cloud cache's restore takes back each stored record's parts with the
// record: after a save and a restore its answers are the same, and none
// decodes the record.
func TestARestoreKeepsAnEntrysParts(t *testing.T) {
	t.Parallel()
	text := "part-facts-restore"
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	storedTestHoldWhole(cloud)
	entry := partFactsTestMerge(t, ctx, cloud, "engine", "part-facts-restore", text, partFactsTestParts)
	number := uint64(entry.id)
	partFactsTestAsk(t, cloud, number)
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	partFactsTestInvariant(t, cloud)
	decoded := transferTestMappedCount(text)
	partFactsTestAsk(t, cloud, number)
	require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the restored record")
}

// The Cloud checks the parts an engine puts on a held row against its copy's
// kept parts, and a write that joins its copy's class asks whether the copy
// holds a part from them too: neither decodes the copy, though it is still
// held whole, as a merge installed it.
func TestAWholeCopysPartsAreReadWithoutDecodingIt(t *testing.T) {
	t.Parallel()
	text := "part-facts-whole-copy"
	ctx, cloud := partFactsTestCloud(t)
	entry := partFactsTestMerge(t, ctx, cloud, "engine", "part-facts-whole-copy", text, partFactsTestParts)
	partFactsTestRequireForm(t, cloud, entry, partFactsWhole)
	partFactsTestInvariant(t, cloud)
	decoded := transferTestMappedCount(text)

	cloud.egraphMu.RLock()
	require.True(t, cloud.entryHasPartsLocked(entry, []TransferredOutput{{Address: partFactsMount, State: "absent"}}))
	require.False(t, cloud.entryHasPartsLocked(entry, []TransferredOutput{{Address: partFactsMount, State: "pending"}}))
	cloud.egraphMu.RUnlock()

	recipe, field := changeTestRecipe(cloud, uint64(entry.id))
	content := testDigest(text + "-content")
	_, err := cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 1}, RemoteHolding{Recipe: recipe, Request: recipe, Field: field, TypeName: "TransferTestValue", ContentDigest: content})
	require.NoError(t, err)
	mate := holdingOf("part-facts-whole-copy-mate")
	mate.ContentDigest = content
	change, err := cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 2}, mate)
	require.NoError(t, err)
	held, ok := cloud.RemoteHoldingInfo(HolderKey{"cache-c", 2})
	require.True(t, ok)
	require.Contains(t, change.Entries, held.Number, "the mate now answers from the copy's absent part")
	require.NotContains(t, change.Entries, uint64(entry.id), "the copy's answers don't change: the mate holds nothing")
	require.Equal(t, decoded, transferTestMappedCount(text), "nothing decodes the whole copy")
}

// A record a merge stores into an entry the Cloud knew only through an
// engine's holding, which had no value, keeps its parts as a new entry's
// does, and so does every entry beside it: the holding's entry had none, and
// has them once the record is stored.
func TestARecordStoredIntoAHoldingsEntryKeepsItsParts(t *testing.T) {
	t.Parallel()
	text := "part-facts-holding"
	ctx, cloud := partFactsTestCloud(t)
	ectx, engine, srv := transferTestCache(t)
	out := persistedListTestResult(t, ectx, engine, srv, "part-facts-holding", &transferTestValue{Text: text, Parts: partFactsTestParts})
	recipe, err := out.cacheSharedResult().loadResultCall().deriveRecipeDigest(engine)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-c", 1}, RemoteHolding{Recipe: recipe, Request: recipe, Field: "part-facts-holding", TypeName: "TransferTestValue"})
	require.NoError(t, err)
	held, ok := cloud.RemoteHoldingInfo(HolderKey{"cache-c", 1})
	require.True(t, ok)
	cloud.egraphMu.RLock()
	holding := cloud.resultsByID[sharedResultID(held.Number)]
	require.NotNil(t, holding)
	require.Nil(t, holding.persistedEnvelope, "the holding's entry has no record yet")
	cloud.egraphMu.RUnlock()
	partFactsTestInvariant(t, cloud)

	reply, err := cloud.MergeValues(ctx, "engine", exportTestBundle(t, ectx, engine, out))
	require.NoError(t, err)
	require.True(t, reply.Committed)
	entry, _ := cloudEntryOfRecipe(t, cloud, engine, out)
	require.Same(t, holding, entry, "the record is stored into the holding's entry")
	partFactsTestInvariant(t, cloud)
	decoded := transferTestMappedCount(text)
	partFactsTestAsk(t, cloud, uint64(entry.id))
	require.Equal(t, decoded, transferTestMappedCount(text), "no answer decodes the record")
}
