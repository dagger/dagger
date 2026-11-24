package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
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
			out.Values[i] = TransferredValue{Ordinal: row.Ordinal, SenderNumber: row.SenderNumber, SenderReplacements: row.SenderReplacements, Record: PersistedRecord{ResultID: uint64(row.Ordinal)}, ExpiresAtUnix: row.ExpiresAtUnix, Held: recipe}
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

// A held row stands for the sender's own holding of the row, which the
// Cloud learned when the row arrived in full or when the Cloud's merge into
// the sender landed it. A held row from an engine the Cloud holds no holding
// of the row for is stale like one whose value is gone: nothing commits, and
// the sender's retry carries the row in full, which gives the Cloud the
// holding.
func TestCloudRefusesAHeldRowItsSenderDoesntHold(t *testing.T) {
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
	require.False(t, reply.Committed, "the sender holds no holding of the held row")
	require.Len(t, reply.Stale, 1)

	reply, err = cloud.MergeValues(cctx, "engine", childOnly)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	reply, err = cloud.MergeValues(cctx, "engine", held)
	require.NoError(t, err)
	require.True(t, reply.Committed, "once the sender's own copy arrived, its held row holds")
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
	stored, err := cloud.SetStoredPart(cctx, numbers[outOrdinal], part, 0)
	require.NoError(t, err)
	require.True(t, stored)
	_, deps = cloudEntryOfRecipe(t, cloud, engine, out)
	require.Equal(t, []sharedResultID{sharedResultID(svcNumber)}, deps, "the stored part makes its entry depend on the entry it names")
}
