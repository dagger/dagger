package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engineHeldTestBundle is bundle with the row of each of held sent as a held
// row: its recipe and sending facts, with no record and no dependencies.
func engineHeldTestBundle(t *testing.T, c *Cache, bundle ValueBundle, held ...AnyResult) ValueBundle {
	t.Helper()
	out := bundle
	out.Values = append([]TransferredValue(nil), bundle.Values...)
	for _, res := range held {
		shared := res.cacheSharedResult()
		recipe, err := shared.loadResultCall().deriveRecipeDigest(c)
		require.NoError(t, err)
		found := false
		for i, row := range out.Values {
			if row.SenderNumber != uint64(shared.id) {
				continue
			}
			out.Values[i] = TransferredValue{Ordinal: row.Ordinal, SenderNumber: row.SenderNumber, SenderReplacements: row.SenderReplacements, Record: PersistedRecord{ResultID: row.SenderNumber}, ExpiresAtUnix: row.ExpiresAtUnix, Held: recipe}
			found = true
		}
		require.True(t, found, "the bundle carries entry %d", shared.id)
	}
	return out
}

// cloudEntryOfRecipe is the Cloud's current entry for res's recipe, and the
// numbers of its dependencies.
func cloudEntryOfRecipe(t *testing.T, cloud *Cache, engine *Cache, res AnyResult) (*sharedResult, []sharedResultID) {
	t.Helper()
	recipe, err := res.cacheSharedResult().loadResultCall().deriveRecipeDigest(engine)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	entry := cloud.currentEntryForRecipeLocked(recipe)
	if entry == nil {
		return nil, nil
	}
	var deps []sharedResultID
	for dep := range entry.deps {
		deps = append(deps, dep)
	}
	return entry, deps
}

// The Cloud merges an engine's bundle that sends a row the Cloud already
// holds as a held row: the rows above it land as they do with the row in
// full, depending on the Cloud's entry for the held row's recipe.
func TestCloudMergesAnEnginesHeldRows(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "held-child", String("child"))
	root := persistedListTestResult(t, ctx, engine, srv, "held-root", String("root"))
	transferTestDependency(engine, ctx, root, child)
	childOnly := exportTestBundle(t, ctx, engine, child)
	full := exportTestBundle(t, ctx, engine, root)
	require.Len(t, full.Values, 2)
	held := engineHeldTestBundle(t, engine, full, child)

	merge := func(t *testing.T, cloud *Cache, cctx context.Context, bundle ValueBundle) MergeReply {
		t.Helper()
		reply, err := cloud.MergeValues(cctx, "engine", bundle)
		require.NoError(t, err)
		return reply
	}
	inFullCtx, inFull := storedPartTestCache(t, WithBlobStore())
	require.True(t, merge(t, inFull, inFullCtx, childOnly).Committed)
	require.True(t, merge(t, inFull, inFullCtx, full).Committed)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	require.True(t, merge(t, cloud, cctx, childOnly).Committed)
	reply := merge(t, cloud, cctx, held)
	require.True(t, reply.Committed)
	require.Empty(t, reply.Stale)

	childEntry, _ := cloudEntryOfRecipe(t, cloud, engine, child)
	require.NotNil(t, childEntry)
	rootEntry, rootDeps := cloudEntryOfRecipe(t, cloud, engine, root)
	require.NotNil(t, rootEntry, "the row above the held row lands")
	require.Equal(t, []sharedResultID{childEntry.id}, rootDeps, "on the Cloud's entry for the held row's recipe")
	cloud.egraphMu.RLock()
	entries := len(cloud.resultsByID)
	cloud.egraphMu.RUnlock()
	inFull.egraphMu.RLock()
	inFullEntries := len(inFull.resultsByID)
	inFull.egraphMu.RUnlock()
	require.Equal(t, inFullEntries, entries, "as merging the row in full does")
}

// A held row the Cloud has no live entry for refuses the engine's whole
// bundle as stale, naming the row, and the merge commits nothing.
func TestCloudRefusesAnEnginesStaleHeldRow(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "stale-child", String("child"))
	root := persistedListTestResult(t, ctx, engine, srv, "stale-root", String("root"))
	transferTestDependency(engine, ctx, root, child)
	full := exportTestBundle(t, ctx, engine, root)
	held := engineHeldTestBundle(t, engine, full, child)
	var heldOrdinal TransferOrdinal
	for _, row := range held.Values {
		if row.Held != "" {
			heldOrdinal = row.Ordinal
		}
	}

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", held)
	require.NoError(t, err)
	require.False(t, reply.Committed)
	require.Equal(t, []TransferOrdinal{heldOrdinal}, reply.Stale)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Empty(t, cloud.resultsByID, "nothing commits")
}

// A held row from an engine the Cloud holds no holding of the row for, whose
// recipe has a live value there, teaches the Cloud that engine's holding: an
// engine sends a row the Cloud has from another engine as a held row, before
// the engine's telemetry has named it. The commit records the holding on the
// Cloud's entry for the recipe, and the rows above it depend on it there, as
// they would on the row sent in full.
func TestCloudLearnsAnEnginesHoldingFromItsHeldRow(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "unheld-child", String("child"))
	root := persistedListTestResult(t, ctx, engine, srv, "unheld-root", String("root"))
	transferTestDependency(engine, ctx, root, child)
	childOnly := exportTestBundle(t, ctx, engine, child)
	held := engineHeldTestBundle(t, engine, exportTestBundle(t, ctx, engine, root), child)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "other-engine", childOnly)
	require.NoError(t, err)
	require.True(t, reply.Committed, "another engine's copy gives the recipe a live value")

	reply, err = cloud.MergeValues(cctx, "engine", held)
	require.NoError(t, err)
	require.True(t, reply.Committed, "the held row holds on the other engine's copy")
	require.Empty(t, reply.Stale)

	childEntry, _ := cloudEntryOfRecipe(t, cloud, engine, child)
	require.NotNil(t, childEntry)
	childNumber := uint64(child.cacheSharedResult().id)
	rootNumber := uint64(root.cacheSharedResult().id)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	entry, h := cloud.holdingLocked(HolderKey{Cache: "engine", Number: childNumber})
	require.NotNil(t, h, "the held row taught the engine's holding")
	require.Equal(t, childEntry, entry, "on the Cloud's entry for its recipe")
	_, rootHolding := cloud.holdingLocked(HolderKey{Cache: "engine", Number: rootNumber})
	require.NotNil(t, rootHolding)
	require.Contains(t, rootHolding.deps, childNumber, "the row above depends on the held row's holding")
	require.Empty(t, rootHolding.unknownDeps)
}

// heldPartTestBundles are an engine's bundles over root above out: out's own
// record in full, and a content export of root with out's snapshot part
// selected and out sent as a held row, so that the part sits on the held row.
func heldPartTestBundles(t *testing.T, name string) (engine *Cache, outOnly, held ValueBundle, out, root AnyResult) {
	t.Helper()
	ctx, engine, srv := transferTestCache(t)
	out = persistedListTestResult(t, ctx, engine, srv, name+"-out", &transferTestValue{Text: "pending"})
	root = persistedListTestResult(t, ctx, engine, srv, name+"-root", String("root"))
	transferTestDependency(engine, ctx, root, out)
	outOnly = exportTestBundle(t, ctx, engine, out)
	var content ValueBundle
	selection := ValueSelection{Roots: []AnyResult{root}, Outputs: []SelectedValueOutput{{Result: out, Address: PersistedPartAddress{Part: "snapshot"}}}}
	require.NoError(t, engine.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		content = values.Bundle
		return nil
	}))
	require.Len(t, content.Outputs, 1)
	held = engineHeldTestBundle(t, engine, content, out)
	return engine, outOnly, held, out, root
}

// The Cloud checks a part an engine's bundle puts on a held row against its
// own copy of the row: the current entry of the row's recipe, as the commit
// finds it. The part goes only where that copy has the same part: at the
// same address, and, for an upload, with a descriptor of the same kind. Where
// the Cloud never had the row, no longer has its value, or has a copy without
// that part, the held row is stale: nothing commits, and the engine's retry
// carries the row in full.
func TestCloudChecksAPartOnAHeldRowAgainstItsOwnCopy(t *testing.T) {
	t.Parallel()
	for _, tc := range []string{"matches", "uploads", "never had", "no longer has", "differs", "uploads another kind"} {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			engine, outOnly, held, out, root := heldPartTestBundles(t, "part-"+tc)
			cctx, cloud := storedPartTestCache(t, WithBlobStore())
			if tc != "never had" {
				reply, err := cloud.MergeValues(cctx, "engine", outOnly)
				require.NoError(t, err)
				require.True(t, reply.Committed)
			}
			switch tc {
			case "no longer has":
				entry, _ := cloudEntryOfRecipe(t, cloud, engine, out)
				require.NotNil(t, entry)
				cloud.egraphMu.Lock()
				entry.expiresAtUnix = time.Now().Unix() - 60
				cloud.egraphMu.Unlock()
			case "differs":
				held.Outputs[0].Address = PersistedPartAddress{Part: "other"}
			case "uploads", "uploads another kind":
				kind := "directory"
				if tc == "uploads another kind" {
					kind = "file"
				}
				output := &held.Outputs[0]
				output.State = "completed"
				output.Value = &SnapshotValue{Kind: kind, Path: "/"}
				output.Chain = &OfferedChain{Layers: storedBenchPart(1).Chain.Layers}
				output.Owner = &PersistedOfferOwner{}
			}
			reply, err := cloud.MergeValues(cctx, "engine", held)
			require.NoError(t, err)
			rootEntry, _ := cloudEntryOfRecipe(t, cloud, engine, root)
			if tc == "matches" || tc == "uploads" {
				require.True(t, reply.Committed)
				require.Empty(t, reply.Stale)
				require.NotNil(t, rootEntry)
				return
			}
			require.False(t, reply.Committed)
			require.Equal(t, []TransferOrdinal{ordinalOfNumber(t, held, uint64(out.cacheSharedResult().id))}, reply.Stale)
			require.Nil(t, rootEntry, "nothing commits")
		})
	}
}

// An upload on a held row can name another held row as its owner's
// dependency and as a service it binds. The merge lands both held rows on
// entries, and storing the part on what the reply names
// (SetStoredPart) then makes the part's entry depend on the named one, a
// row it didn't depend on before.
func TestCloudStoresAnUploadOnAHeldRowNamingAHeldRow(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	svc := persistedListTestResult(t, ctx, engine, srv, "named-svc", String("svc"))
	out := persistedListTestResult(t, ctx, engine, srv, "named-out", &transferTestValue{Text: "pending"})
	root := persistedListTestResult(t, ctx, engine, srv, "named-root", String("root"))
	transferTestDependency(engine, ctx, root, out)
	transferTestDependency(engine, ctx, root, svc)
	var content ValueBundle
	selection := ValueSelection{Roots: []AnyResult{root}, Outputs: []SelectedValueOutput{{Result: out, Address: PersistedPartAddress{Part: "snapshot"}}}}
	require.NoError(t, engine.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		content = values.Bundle
		return nil
	}))
	held := engineHeldTestBundle(t, engine, content, out, svc)
	svcOrdinal := uint64(ordinalOfNumber(t, held, uint64(svc.cacheSharedResult().id)))
	outOrdinal := ordinalOfNumber(t, held, uint64(out.cacheSharedResult().id))
	output := &held.Outputs[0]
	output.State = "completed"
	output.Value = &SnapshotValue{Kind: "directory", Path: "/", Services: []TransferredServiceBinding{{ServiceResultID: svcOrdinal, Hostname: "svc"}}}
	output.Chain = &OfferedChain{Layers: storedBenchPart(1).Chain.Layers}
	output.Owner = &PersistedOfferOwner{DependencyIDs: []uint64{svcOrdinal}}

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", exportTestBundle(t, ctx, engine, root))
	require.NoError(t, err)
	require.True(t, reply.Committed)
	outEntry, deps := cloudEntryOfRecipe(t, cloud, engine, out)
	require.NotNil(t, outEntry)
	require.Empty(t, deps)
	reply, err = cloud.MergeValues(cctx, "engine", held)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range reply.Values {
		numbers[value.Ordinal] = value.Number
	}
	svcNumber, ok := numbers[TransferOrdinal(svcOrdinal)]
	require.True(t, ok, "the named held row lands on an entry")
	part := PersistedPartOffer{Address: output.Address, Value: *output.Value, Chain: *output.Chain, Owner: PersistedOfferOwner{DependencyIDs: []uint64{svcNumber}}}
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: svcNumber, Hostname: "svc"}}
	stored, _, err := cloud.SetStoredPart(cctx, numbers[outOrdinal], part, 0)
	require.NoError(t, err)
	require.True(t, stored)
	_, deps = cloudEntryOfRecipe(t, cloud, engine, out)
	require.Equal(t, []sharedResultID{sharedResultID(svcNumber)}, deps, "the stored part makes its entry depend on the entry it names")
}

// receiverTestResult is a result of field called on receiver, so that its
// recipe derives from receiver's.
func receiverTestResult(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string, receiver AnyResult) AnyResult {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(String("").Type()), Receiver: &ResultCallRef{ResultID: uint64(receiver.cacheSharedResult().id)}}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String(field), frame)
	})
	require.NoError(t, err)
	return res
}

// One engine's call can be answered with an entry of another recipe: its
// call span reports the call's request digest beside the recipe of what came
// back. Another engine that computed the call itself has a row whose recipe
// is that request digest. Once that engine has merged the row, and the row
// above it, in full, the Cloud holds both, and the engine's next exports send
// them as held rows: each holds, as on any other recipe.
func TestCloudHeldRowsHoldBesideAnotherEnginesRequestDigest(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "asked-child", String("child"))
	mid := receiverTestResult(t, ctx, engine, srv, "asked-mid", child)
	top := receiverTestResult(t, ctx, engine, srv, "asked-top", mid)
	childRecipe, err := child.cacheSharedResult().loadResultCall().deriveRecipeDigest(engine)
	require.NoError(t, err)

	octx, other, osrv := transferTestCache(t)
	answer := persistedListTestResult(t, octx, other, osrv, "asked-answer", String("child"))
	answerRecipe, err := answer.cacheSharedResult().loadResultCall().deriveRecipeDigest(other)
	require.NoError(t, err)
	require.NotEqual(t, childRecipe, answerRecipe)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	merge := func(t *testing.T, from CacheID, bundle ValueBundle) MergeReply {
		t.Helper()
		reply, err := cloud.MergeValues(cctx, from, bundle)
		require.NoError(t, err)
		return reply
	}
	// The other engine's entry, in full, then its call span: a call whose
	// request digest is the child's recipe, answered with that entry.
	require.True(t, merge(t, "other-engine", exportTestBundle(t, octx, other, answer)).Committed)
	_, err = cloud.AttachRemoteHolding(cctx, HolderKey{Cache: "other-engine", Number: uint64(answer.cacheSharedResult().id)}, RemoteHolding{Recipe: answerRecipe, Request: childRecipe, Field: "asked-answer", TypeName: "String"})
	require.NoError(t, err)

	// The engine merges the child and mid in full, then exports rows above
	// each, records only, as a whole fill does: each merged row is sent held.
	known := NewKnownRows()
	export := func(t *testing.T, root AnyResult, held AnyResult) MergeReply {
		t.Helper()
		bundle, rows, _ := recordsOnlyCapture(t, ctx, engine, known, root)
		if held != nil {
			heldRows, _ := heldNumbers(bundle)
			require.True(t, heldRows[knownRowsTestNumber(held)], "the export sends the merged row held")
		}
		reply := merge(t, "engine", bundle)
		if reply.Committed {
			known.Merged(rows)
		}
		return reply
	}
	require.True(t, export(t, mid, nil).Committed)

	reply := export(t, top, mid)
	assert.Empty(t, reply.Stale, "mid, merged in full on the child, holds")
	assert.True(t, reply.Committed)

	reply = export(t, receiverTestResult(t, ctx, engine, srv, "asked-side", child), child)
	assert.Empty(t, reply.Stale, "the child, merged in full, holds")
	assert.True(t, reply.Committed)
}

// A held row accepts nothing on the Cloud that it wouldn't accept once the
// sending engine's telemetry had named the row's holding: the row's recipe
// has a live value that another engine's run gave it, the sending engine
// ran the row's call itself, and its bundle sends the row held. One Cloud
// has the engine's holding of the row already, as telemetry attaches it to
// the entry of the row's recipe; the other doesn't. Both commit the same
// rows, keep the other engine's value on the same entry, and end with the
// engine's holding of the row on it.
func TestCloudHeldRowAcceptsWhatItWouldOnceTelemetryNamedTheHolding(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "named-child", String("child"))
	root := persistedListTestResult(t, ctx, engine, srv, "named-root", String("root"))
	transferTestDependency(engine, ctx, root, child)
	held := engineHeldTestBundle(t, engine, exportTestBundle(t, ctx, engine, root), child)
	childNumber := uint64(child.cacheSharedResult().id)

	// otherRun is another engine's run of the child's call.
	octx, other, osrv := transferTestCache(t)
	otherChild := persistedListTestResult(t, octx, other, osrv, "named-child", String("child"))
	otherRun := exportTestBundle(t, octx, other, otherChild)

	type outcome struct {
		rows   MergeRowCounts
		entry  sharedResultID
		holder sharedResultID
	}
	run := func(t *testing.T, named bool) outcome {
		cctx, cloud := storedPartTestCache(t, WithBlobStore())
		reply, err := cloud.MergeValues(cctx, "other-engine", otherRun)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		before, _ := cloudEntryOfRecipe(t, cloud, engine, child)
		require.NotNil(t, before)
		if named {
			heldTestHold(t, cctx, cloud, "engine", childNumber, uint64(before.id), 0)
		}
		reply, err = cloud.MergeValues(cctx, "engine", held)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		after, _ := cloudEntryOfRecipe(t, cloud, engine, child)
		require.Same(t, before, after, "the entry keeps the other engine's value")
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		_, h := cloud.holdingLocked(HolderKey{Cache: "other-engine", Number: uint64(otherChild.cacheSharedResult().id)})
		require.NotNil(t, h, "the other engine still holds its run")
		return outcome{rows: reply.Rows, entry: after.id, holder: cloud.holderEntriesForTest()[HolderKey{Cache: "engine", Number: childNumber}]}
	}
	named, unnamed := run(t, true), run(t, false)
	require.Equal(t, MergeRowCounts{Held: 1, Created: 1}, named.rows, "the held row stores nothing over the other engine's run")
	require.Equal(t, named.rows, unnamed.rows, "both commit the same rows")
	require.Equal(t, named.entry, named.holder)
	require.Equal(t, unnamed.entry, unnamed.holder, "the engine's holding sits on the entry of the row's recipe either way")
}
