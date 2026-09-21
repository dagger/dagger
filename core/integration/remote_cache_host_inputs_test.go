package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestHostInputs is the native Host, views and selected-address row (design
// §5 as amended by B1): whole-chain export of a view, copied byte counts,
// provider opens per selected address, and an unselected sibling that is
// never opened.
func (RemoteCacheTransferSuite) TestHostInputs(ctx context.Context, t *testctx.T) {
	// Folded row, from core's TestValueTransferPartsSelectedChain: a nested
	// view made by a real directory("visible").file("value.txt") exports its
	// whole parent chain, and B, which has no such Host directory, reads
	// "selected bytes" through it.
	t.Run("NestedViewExportsWholeChain", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		a := newFixtureEngine(ctx, t, outer, "view-a", true)
		b := newFixtureEngine(ctx, t, outer, "view-b", true)
		a.hostFile("src/visible/value.txt", "selected bytes")
		a.hostFile("src/hidden/other.txt", "unselected "+identity.NewID())

		view := a.client.Host().Directory("src").Directory("visible").File("value.txt")
		_, err := view.Sync(ctx)
		require.NoError(t, err)
		id, err := view.ID(ctx)
		require.NoError(t, err)
		var exported fixtureExportSelectedResult
		require.NoError(t, a.fixture("exportSelected", a.control("export.json", map[string]any{"bundle": "view.json", "outputs": []map[string]any{{"handle": string(id), "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), []string{string(id)}, &exported))
		require.Len(t, exported.Outputs, 1)
		require.NotEmpty(t, exported.Outputs[0].Layers, "the view's chain was exported")
		var copied int64
		for _, layer := range exported.Outputs[0].Layers {
			require.Positive(t, layer.Size)
			copied += layer.CopiedBytes
		}
		require.Positive(t, copied, "the chain's bytes were copied, not referenced")
		// The File, its visible Directory, the Host directory and the Host
		// row: the whole parent chain is in the closure.
		types := map[string]int{}
		for _, root := range exported.Roots {
			types[root.Type.NamedType]++
		}
		t.Logf("exported closure: %v, copied %d bytes in %d layers", types, copied, len(exported.Outputs[0].Layers))
		require.Positive(t, types["File"])
		require.GreaterOrEqual(t, types["Directory"], 2, "the view's parent Directories travel with it")

		a.copyFixtureTo(b, "view.json")
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "view.json", nil, &imported))
		require.NotEmpty(t, imported)
		require.Equal(t, "File", imported[0].Type.NamedType)
		contents, err := dagger.Ref[*dagger.File](b.client, dagger.ID(imported[0].Handle)).Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected bytes", contents)
		var report fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &report))
		var chains, lazy int
		for _, event := range report.Parts {
			switch event.Kind {
			case dagql.PartEventInstalledChain:
				chains++
			case dagql.PartEventLazyEnter:
				lazy++
			}
		}
		require.Equal(t, 1, chains, "one chain install serves the view")
		require.Zero(t, lazy, "nothing is evaluated on B")
		require.Len(t, report.reachedAt(dagql.FixtureChainReaderOpen), len(exported.Outputs[0].Layers), "one provider open per layer of the selected address")
	})

	// Two Host directories travel in one bundle; only one is selected. B
	// opens content for the selected address only, the sibling is never
	// probed for content, and demanding the sibling is an ordinary
	// unavailable part, not a fallback to anything.
	t.Run("UnselectedSiblingNeverOpened", func(ctx context.Context, t *testctx.T) {
		outer := connect(ctx, t)
		a := newFixtureEngine(ctx, t, outer, "sibling-a", true)
		b := newFixtureEngine(ctx, t, outer, "sibling-b", true)
		a.hostFile("chosen/notes.txt", "chosen "+identity.NewID())
		a.hostFile("sibling/notes.txt", "sibling "+identity.NewID())
		var handles []string
		for _, name := range []string{"chosen", "sibling"} {
			dir, err := a.client.Host().Directory(name).Sync(ctx)
			require.NoError(t, err)
			id, err := dir.ID(ctx)
			require.NoError(t, err)
			handles = append(handles, string(id))
		}
		var exported fixtureExportSelectedResult
		require.NoError(t, a.fixture("exportSelected", a.control("export.json", map[string]any{"bundle": "siblings.json", "outputs": []map[string]any{{"handle": handles[0], "address": dagql.PersistedPartAddress{Part: "snapshot"}}}}), handles, &exported))
		require.Len(t, exported.Outputs, 1, "the unselected sibling caused no export open")
		a.copyFixtureTo(b, "siblings.json")
		var imported []transferFixtureMapping
		require.NoError(t, b.fixture("import", "siblings.json", nil, &imported))
		require.GreaterOrEqual(t, len(imported), 2)
		chosen, sibling := imported[0], imported[1]

		entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(chosen.Handle)).Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "notes.txt")
		var report fixtureControlsReport
		require.NoError(t, b.fixture("report", "", nil, &report))
		require.Len(t, partEventsOf(report.transferFixtureReport, chosen.ResultID, dagql.PartEventInstalledChain), 1)
		require.Empty(t, partKindsOf(report.transferFixtureReport, sibling.ResultID), "the sibling was never touched")
		for _, open := range report.reachedAt(dagql.FixtureChainReaderOpen) {
			require.Equal(t, chosen.ResultID, open.ResultID, "content is opened for the selected address only")
		}

		_, err = dagger.Ref[*dagger.Directory](b.client, dagger.ID(sibling.Handle)).Entries(ctx)
		require.ErrorContains(t, err, "imported filesystem part is unavailable")
		require.NoError(t, b.fixture("report", "", nil, &report))
		for _, kind := range []dagql.TransferFixturePartKind{dagql.PartEventProviderRead, dagql.PartEventInstalledChain, dagql.PartEventLazyEnter} {
			require.Empty(t, partEventsOf(report.transferFixtureReport, sibling.ResultID, kind), "sibling %s", kind)
		}
	})
}
