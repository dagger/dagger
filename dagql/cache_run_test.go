package dagql

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runTestExport exports a record of field from an engine cache of its own,
// merges it into cloud as from, and returns the Cloud's number for the
// record and the engine's entry for it: the run's name.
func runTestExport(t *testing.T, ctx context.Context, cloud *Cache, from CacheID, field string) (uint64, RunName) {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, string(from)+" "+field)
	reply, err := cloud.MergeValues(ctx, from, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Values, 1)
	sender := bundle.Values[0]
	return reply.Values[0].Number, RunName{Cache: from, Number: sender.SenderNumber, Replacements: sender.SenderReplacements}
}

// runTestEvaluated applies a lazy row of key at replacements reporting the
// snapshot part complete: an evaluation, or an offer's install.
func runTestEvaluated(t *testing.T, ctx context.Context, cloud *Cache, key HolderKey, replacements uint64, evaluated bool) RemoteChange {
	t.Helper()
	change, found, err := cloud.UpdateRemoteHolding(ctx, key, RemoteHoldingUpdate{
		Parts:        []PersistedPartAddress{testLiveOffer().Address},
		Replacements: replacements,
		Evaluated:    evaluated,
	})
	require.NoError(t, err)
	require.True(t, found)
	return change
}

// runTestRan reports what the Cloud's stored record of number says of its
// run.
func runTestRan(t *testing.T, cloud *Cache, number uint64) EntryRun {
	t.Helper()
	run, ok := cloud.EntryRun(number)
	require.True(t, ok, "entry %d stores a record that names a run", number)
	return run
}

// The Cloud's stored record names the run that made it: the engine entry
// that exported it, at its replacement count. No bundle it sends carries it,
// and an engine names none.
func TestTheCloudsStoredRecordNamesItsRun(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "cache-a", "named")
	got := runTestRan(t, cloud, number)
	require.Equal(t, run, got.Run)
	require.False(t, got.Ran, "a record from an export alone is a recipe until its run runs")

	sent, err := cloud.StoredBundle(ctx, []uint64{number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.NotContains(t, string(mustTransferJSON(t, sent.Bundle.Values)), `"run"`, "the Cloud sends no run")
	bctx, b, _ := transferTestCache(t)
	breply, err := b.MergeValues(bctx, cloudCacheID, sent.Bundle)
	require.NoError(t, err)
	_, ok := b.EntryRun(breply.Values[0].Number)
	require.False(t, ok, "an engine names no run")
}

// A record an engine imported is the run of the Cloud entry its holding of
// it is on, unless the engine ran its copy itself; with no such holding, the
// Cloud names the engine's entry.
func TestAnImportedRecordIsTheRunOfTheEntryItCameFrom(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "cache-a", "imported")
	sent, err := cloud.StoredBundle(ctx, []uint64{number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	bctx, b, bsrv := transferTestCache(t)
	breply, err := b.MergeValues(bctx, cloudCacheID, sent.Bundle)
	require.NoError(t, err)
	bNumber := breply.Imported()[0].ResultID
	imported, err := b.LoadResultByResultID(bctx, "test-session", bsrv, bNumber)
	require.NoError(t, err)

	recipe, ok := cloud.EntryRecipe(number)
	require.True(t, ok)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-b", Number: bNumber}, RemoteHolding{Recipe: recipe})
	require.NoError(t, err)
	held := exportTestBundle(t, bctx, b, imported)
	require.True(t, held.Values[0].Record.Envelope.Imported)
	// A's record has expired, so B's copy is installed in its place.
	cloud.egraphMu.Lock()
	cloud.resultsByID[sharedResultID(number)].expiresAtUnix = time.Now().Unix() - 10
	cloud.egraphMu.Unlock()
	reply, err := cloud.MergeValues(ctx, "cache-b", held)
	require.NoError(t, err)
	require.Equal(t, 1, reply.Rows.Replaced, "B's copy replaces A's expired record")
	require.Equal(t, run, runTestRan(t, cloud, reply.Values[0].Number).Run, "B's copy is A's run")

	// B ran its copy itself, for want of A's content: its copy is B's run.
	bKey := HolderKey{Cache: "cache-b", Number: bNumber}
	runTestEvaluated(t, ctx, cloud, bKey, held.Values[0].SenderReplacements, true)
	cloud.egraphMu.Lock()
	cloud.resultsByID[sharedResultID(reply.Values[0].Number)].expiresAtUnix = time.Now().Unix() - 10
	cloud.egraphMu.Unlock()
	reply, err = cloud.MergeValues(ctx, "cache-b", held)
	require.NoError(t, err)
	require.Equal(t, 1, reply.Rows.Replaced)
	require.Equal(t, EntryRun{Run: RunName{Cache: "cache-b", Number: bNumber, Replacements: held.Values[0].SenderReplacements}, Ran: true}, runTestRan(t, cloud, reply.Values[0].Number), "B evaluated its copy: B's run, which ran")

	// A second Cloud, which holds no holding of B's, as after a restart,
	// doesn't know whose run B's copy is: it names none, not B's own entry,
	// which never ran.
	ctx2, other := storedPartTestCache(t, WithBlobStore())
	unheld, err := other.MergeValues(ctx2, "cache-b", held)
	require.NoError(t, err)
	_, ok = other.EntryRun(unheld.Values[0].Number)
	require.False(t, ok, "a copy whose run isn't known names no run")
}

// runTestServed has the Cloud serve engine cache B the record numbered
// served, as B's own value of another recipe, a class mate: B's holding is
// on the entry of its own call's recipe, and the Cloud's merged reply lands
// the served record on B's number. It returns B's export of that value,
// marked imported.
func runTestServed(t *testing.T, ctx context.Context, cloud *Cache, served uint64) ValueBundle {
	t.Helper()
	bundle, recipe := mergeTestSource(t, "class-mate", "b's copy")
	bundle.Values[0].Record.Envelope.Imported = true
	key := HolderKey{Cache: "cache-b", Number: bundle.Values[0].SenderNumber}
	_, err := cloud.AttachRemoteHolding(ctx, key, RemoteHolding{Recipe: recipe})
	require.NoError(t, err)
	at, ok := cloud.HolderEntry(key)
	require.True(t, ok)
	require.NotEqual(t, served, at, "B's holding is on its own recipe's entry")
	sent, err := cloud.StoredBundle(ctx, []uint64{served}, time.Now().Unix(), nil)
	require.NoError(t, err)
	_, err = cloud.ApplyMergedReply(ctx, "cache-b", sent.Bundle, MergeReply{Values: []MergedValue{{Ordinal: sent.Bundle.Roots[0].Ordinal, Number: key.Number}}})
	require.NoError(t, err)
	return bundle
}

// A record an engine was served from a class mate, which it exports without
// running it, is the run of the record it was served, with what the Cloud
// knows of whether that run ran: not the engine's own, which never ran.
func TestAServedRecordNamesTheRunItWasServedFrom(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "class-source", "a's value")
	runTestEvaluated(t, ctx, cloud, HolderKey{Cache: run.Cache, Number: run.Number}, run.Replacements, true)
	held := runTestServed(t, ctx, cloud, number)
	reply, err := cloud.MergeValues(ctx, "cache-b", held)
	require.NoError(t, err)
	require.Len(t, reply.Values, 1)
	require.NotEqual(t, number, reply.Values[0].Number, "B's copy is its own recipe's record")
	require.Equal(t, EntryRun{Run: run, Ran: true}, runTestRan(t, cloud, reply.Values[0].Number), "B's copy is A's run, which ran")
}

// The served record's run is kept by name: replaced by a later run's before
// the engine exports its copy, the copy still names the run it was served.
func TestAServedRecordNamesItsRunAfterTheServingRecordIsReplaced(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "class-source", "a's value")
	held := runTestServed(t, ctx, cloud, number)
	// A's record expires, and C's run of it is installed in its place.
	cloud.egraphMu.Lock()
	cloud.resultsByID[sharedResultID(number)].expiresAtUnix = time.Now().Unix() - 10
	cloud.egraphMu.Unlock()
	cNumber, cRun := runTestExport(t, ctx, cloud, "cache-c", "class-source")
	_ = cNumber
	require.NotEqual(t, run, cRun)
	reply, err := cloud.MergeValues(ctx, "cache-b", held)
	require.NoError(t, err)
	require.Equal(t, run, runTestRan(t, cloud, reply.Values[0].Number).Run, "B's copy is still A's run")
}

// A copy the Cloud's holding of the engine says nothing of, its entry naming
// no run and nothing served, names no run.
func TestACopyWithNoServedRunNamesNoRun(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	bundle, recipe := mergeTestSource(t, "never-served", "b's copy")
	bundle.Values[0].Record.Envelope.Imported = true
	_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-b", Number: bundle.Values[0].SenderNumber}, RemoteHolding{Recipe: recipe})
	require.NoError(t, err)
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	_, ok := cloud.EntryRun(reply.Values[0].Number)
	require.False(t, ok, "no run is known for it")
}

// A copy of a record the Cloud sent, installed in place of the record it was
// sent from, keeps what the Cloud knew of that record's run: the run ran,
// though its holding has since moved on to a later value.
func TestAnImportedCopyKeepsWhatTheCloudKnewOfItsRun(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "cache-a", "kept-ran")
	aKey := HolderKey{Cache: run.Cache, Number: run.Number}
	runTestEvaluated(t, ctx, cloud, aKey, run.Replacements, true)
	_, found, err := cloud.UpdateRemoteHolding(ctx, aKey, RemoteHoldingUpdate{Replacements: run.Replacements + 1})
	require.NoError(t, err)
	require.True(t, found)

	sent, err := cloud.StoredBundle(ctx, []uint64{number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	bctx, b, bsrv := transferTestCache(t)
	breply, err := b.MergeValues(bctx, cloudCacheID, sent.Bundle)
	require.NoError(t, err)
	bNumber := breply.Imported()[0].ResultID
	imported, err := b.LoadResultByResultID(bctx, "test-session", bsrv, bNumber)
	require.NoError(t, err)
	recipe, ok := cloud.EntryRecipe(number)
	require.True(t, ok)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-b", Number: bNumber}, RemoteHolding{Recipe: recipe})
	require.NoError(t, err)
	held := exportTestBundle(t, bctx, b, imported)
	cloud.egraphMu.Lock()
	cloud.resultsByID[sharedResultID(number)].expiresAtUnix = time.Now().Unix() - 10
	cloud.egraphMu.Unlock()
	reply, err := cloud.MergeValues(ctx, "cache-b", held)
	require.NoError(t, err)
	require.Equal(t, 1, reply.Rows.Replaced, "B's copy replaces A's expired record")
	require.Equal(t, EntryRun{Run: run, Ran: true}, runTestRan(t, cloud, reply.Values[0].Number), "B's copy is A's run, which ran")
	require.Equal(t, 1, cloud.StoredRuns().RanByEvaluation, "learned as A's run was")
}

// A stored record's run ran once the run's own holding reports a part
// complete, from an executed call or an evaluation, at the run's replacement
// count, whether before the merge or after, or the record proves such a part
// complete itself. The write that learns it names the entry. A record that
// proves only its metadata, an executed call of a lazy result, which reports its parts
// pending, an install from a Cloud offer, another engine's evaluation, and an
// evaluation of a later value say nothing of the run.
func TestTheCloudLearnsThatARecordsRunRan(t *testing.T) {
	t.Parallel()
	t.Run("an evaluation after the merge", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "evaluated")
		key := HolderKey{Cache: run.Cache, Number: run.Number}
		change := runTestEvaluated(t, ctx, cloud, key, run.Replacements, true)
		require.True(t, runTestRan(t, cloud, number).Ran)
		require.Contains(t, change.Entries, number, "the write names the entry, for readers that keep their answers")
		require.True(t, change.Saved, "a save writes what the record learned")
		require.Equal(t, 1, cloud.StoredRuns().RanByEvaluation)
	})
	t.Run("an evaluation of the run after a later value's report", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "late")
		key := HolderKey{Cache: run.Cache, Number: run.Number}
		_, found, err := cloud.UpdateRemoteHolding(ctx, key, RemoteHoldingUpdate{Replacements: run.Replacements + 1})
		require.NoError(t, err)
		require.True(t, found)
		runTestEvaluated(t, ctx, cloud, key, run.Replacements, true)
		require.True(t, runTestRan(t, cloud, number).Ran, "the delayed report is of the stored run")
	})
	t.Run("an executed call after the merge", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "executed")
		recipe, ok := cloud.EntryRecipe(number)
		require.True(t, ok)
		change, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: run.Cache, Number: run.Number}, RemoteHolding{
			Recipe:       recipe,
			Parts:        []PersistedPartAddress{testLiveOffer().Address},
			Replacements: run.Replacements,
			Executed:     true,
		})
		require.NoError(t, err)
		require.True(t, runTestRan(t, cloud, number).Ran)
		require.Contains(t, change.Entries, number)
		require.True(t, change.Saved)
		require.Equal(t, 1, cloud.StoredRuns().RanByCall)
	})
	t.Run("an executed call from before the last drop", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "before-drop")
		recipe, ok := cloud.EntryRecipe(number)
		require.True(t, ok)
		key := HolderKey{Cache: run.Cache, Number: run.Number}
		_, err := cloud.AttachRemoteHolding(ctx, key, RemoteHolding{
			Recipe:         recipe,
			Parts:          []PersistedPartAddress{testLiveOffer().Address},
			Replacements:   run.Replacements,
			Executed:       true,
			BeforeLastDrop: true,
		})
		require.NoError(t, err)
		require.True(t, runTestRan(t, cloud, number).Ran, "the run ran, whenever its row came")
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		_, h := cloud.holdingLocked(key)
		require.False(t, h.executed, "a computation from before the drop doesn't bring a dropped value back")
	})
	t.Run("an executed call of a lazy result", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "lazy")
		recipe, ok := cloud.EntryRecipe(number)
		require.True(t, ok)
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: run.Cache, Number: run.Number}, RemoteHolding{Recipe: recipe, Replacements: run.Replacements, Executed: true})
		require.NoError(t, err)
		require.False(t, runTestRan(t, cloud, number).Ran, "the call returned the step before it ran")
		require.Equal(t, 1, cloud.StoredRuns().NotRan)
	})
	t.Run("an evaluation before the merge", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		bundle, recipe := mergeTestSource(t, "before", "before")
		sender := bundle.Values[0]
		key := HolderKey{Cache: "cache-a", Number: sender.SenderNumber}
		_, err := cloud.AttachRemoteHolding(ctx, key, RemoteHolding{Recipe: recipe, Replacements: sender.SenderReplacements})
		require.NoError(t, err)
		runTestEvaluated(t, ctx, cloud, key, sender.SenderReplacements, true)
		reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
		require.NoError(t, err)
		require.True(t, runTestRan(t, cloud, reply.Values[0].Number).Ran)
		require.Equal(t, 1, cloud.StoredRuns().RanAtMerge)
	})
	t.Run("an offer's install", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "installed")
		change := runTestEvaluated(t, ctx, cloud, HolderKey{Cache: run.Cache, Number: run.Number}, run.Replacements, false)
		require.False(t, runTestRan(t, cloud, number).Ran, "the run's engine installed the part: it never evaluated the step")
		require.False(t, change.Saved, "nothing a save writes changed")
		counts := cloud.StoredRuns()
		require.Equal(t, 1, counts.NotRan)
		recipe, ok := cloud.EntryRecipe(number)
		require.True(t, ok)
		require.Equal(t, []NotRanRecord{{Number: number, Recipe: recipe, Run: run}}, counts.NotRanRecords, "a reader finds the run among those that didn't")
	})
	t.Run("a record that proves a part complete", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		ectx, e, esrv := transferTestCache(t)
		r := persistedListTestResult(t, ectx, e, esrv, "proves", &transferTestValue{Text: "proves", Parts: []transferTestPart{{Part: "snapshot", State: "completed", Kind: "directory"}}})
		reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, ectx, e, r))
		require.NoError(t, err)
		require.Len(t, reply.Values, 1)
		require.True(t, runTestRan(t, cloud, reply.Values[0].Number).Ran, "its run ran before any telemetry says so")
		counts := cloud.StoredRuns()
		require.Equal(t, 1, counts.RanByProof)
		require.Empty(t, counts.NotRanRecords)
	})
	t.Run("a record that proves only its metadata", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		ectx, e, esrv := transferTestCache(t)
		r := persistedListTestResult(t, ectx, e, esrv, "metadata", &transferTestValue{Text: "metadata", Parts: []transferTestPart{{Part: "metadata", State: "metadata"}, {Part: "snapshot", State: "pending", Kind: "directory"}}})
		reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, ectx, e, r))
		require.NoError(t, err)
		require.Len(t, reply.Values, 1)
		require.False(t, runTestRan(t, cloud, reply.Values[0].Number).Ran, "its step's content is still pending")
	})
	t.Run("another engine's evaluation", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, _ := runTestExport(t, ctx, cloud, "cache-a", "other")
		recipe, ok := cloud.EntryRecipe(number)
		require.True(t, ok)
		other := HolderKey{Cache: "cache-b", Number: 7}
		_, err := cloud.AttachRemoteHolding(ctx, other, RemoteHolding{Recipe: recipe, Executed: true})
		require.NoError(t, err)
		runTestEvaluated(t, ctx, cloud, other, 0, true)
		require.False(t, runTestRan(t, cloud, number).Ran, "B's run of the entry isn't A's")
	})
	t.Run("an evaluation of a later value", func(t *testing.T) {
		t.Parallel()
		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		number, run := runTestExport(t, ctx, cloud, "cache-a", "later")
		runTestEvaluated(t, ctx, cloud, HolderKey{Cache: run.Cache, Number: run.Number}, run.Replacements+1, true)
		require.False(t, runTestRan(t, cloud, number).Ran, "the engine ran a value that replaced the run's")
	})
}

// From a holding's side, the Cloud says whose run its entry's stored record
// is: the holding's own, at its replacement count, or another's; whether that
// run ran; and whether the run's own holding is still held at the run's
// count. A holding that moves on to a later value no longer holds the run.
func TestHoldingRunsSayWhoseRunAHoldingsRecordIs(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "cache-a", "holding-runs")
	a := HolderKey{Cache: run.Cache, Number: run.Number}
	recipe, ok := cloud.EntryRecipe(number)
	require.True(t, ok)
	b := HolderKey{Cache: "cache-b", Number: 7}
	_, err := cloud.AttachRemoteHolding(ctx, b, RemoteHolding{Recipe: recipe})
	require.NoError(t, err)
	unknown := HolderKey{Cache: "cache-c", Number: 9}

	require.Equal(t, []HoldingRun{
		{Stored: true, Run: run, Own: true, RunHeld: true},
		{Stored: true, Run: run, RunHeld: true},
		{},
	}, cloud.HoldingRuns([]HolderKey{a, b, unknown}))

	runTestEvaluated(t, ctx, cloud, a, run.Replacements, true)
	require.Equal(t, []HoldingRun{{Stored: true, Run: run, Ran: true, RunHeld: true}}, cloud.HoldingRuns([]HolderKey{b}))

	_, found, err := cloud.UpdateRemoteHolding(ctx, a, RemoteHoldingUpdate{Replacements: run.Replacements + 1})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []HoldingRun{
		{Stored: true, Run: run, Ran: true},
		{Stored: true, Run: run, Ran: true},
	}, cloud.HoldingRuns([]HolderKey{a, b}), "A's engine moved on to a later value")
}

// What a stored record says of its run is saved with it, whole or
// compressed, and comes back over a restart; a prune of the record takes it
// with the value.
func TestARecordsRunIsSavedAndGoesWithItsValue(t *testing.T) {
	t.Parallel()
	for _, compress := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole", true: "compressed"}[compress], func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "cloud.db")
			ctx, cloud := storedPersistenceTestCloud(t, path)
			ran, run := runTestExport(t, ctx, cloud, "cache-a", "saved-ran")
			runTestEvaluated(t, ctx, cloud, HolderKey{Cache: run.Cache, Number: run.Number}, run.Replacements, true)
			recipe, recipeRun := runTestExport(t, ctx, cloud, "cache-b", "saved-recipe")
			if compress {
				storedTestCompress(cloud)
			}
			require.NoError(t, cloud.Close(ctx))

			ctx, cloud = storedPersistenceTestCloud(t, path)
			defer func() { require.NoError(t, cloud.Close(ctx)) }()
			require.Equal(t, EntryRun{Run: run, Ran: true}, runTestRan(t, cloud, ran))
			require.Equal(t, EntryRun{Run: recipeRun}, runTestRan(t, cloud, recipe))
			counts := cloud.StoredRuns()
			require.Equal(t, 1, counts.RanByEvaluation)
			require.Equal(t, 1, counts.NotRan)
		})
	}
}

// A prune that drops a stored record's value drops its run with it.
func TestAPruneDropsARecordsRun(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	number, run := runTestExport(t, ctx, cloud, "cache-a", "pruned")
	runTestEvaluated(t, ctx, cloud, HolderKey{Cache: run.Cache, Number: run.Number}, run.Replacements, true)
	require.Equal(t, EntryRun{Run: run, Ran: true}, runTestRan(t, cloud, number), "before the prune")
	cloud.egraphMu.Lock()
	res := cloud.resultsByID[sharedResultID(number)]
	_, err := cloud.dropValueLocked(ctx, res)
	cloud.egraphMu.Unlock()
	require.NoError(t, err)
	_, ok := cloud.EntryRun(number)
	require.False(t, ok, "the run went with the value")
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Nil(t, res.run)
	require.Equal(t, runNotRan, res.runRan)
	require.False(t, slices.Contains([]runRanBy{runRanAtMerge, runRanByCall, runRanByEvaluation}, res.runRan))
}
