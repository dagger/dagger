package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestPendingOffersRestart is the native restart and forward row (design §5 as
// amended by B1). A exports one executed Container with two selected outputs,
// its root filesystem and its written mount. B installs only the filesystem:
// that offer is redundant once B owns the snapshot and is retired, while the
// mount's offer stays pending. Both facts survive a real restart, the engine
// asks for nothing at boot, and the pending sibling still installs afterwards.
// B then forwards the same selected chains to a third engine, which reads both
// without running anything.
func (RemoteCacheTransferSuite) TestPendingOffersRestart(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	a := newFixtureEngine(ctx, t, outer, "pending-a", true)
	b := newFixtureEngine(ctx, t, outer, "pending-b", true)
	c := newFixtureEngine(ctx, t, outer, "pending-c", true)
	nonce := identity.NewID()
	fsAddress := dagql.PersistedPartAddress{Part: "fs"}
	mountAddress := dagql.PersistedPartAddress{Part: "mount:/work"}
	// The part is addressed by its target; the owner role is positional.
	const mountRole = "mount_dir:0"

	executed := a.client.Container().From(alpineImage).
		WithMountedDirectory("/work", a.client.Directory().WithNewFile("seed.txt", "seed "+nonce)).
		WithExec([]string{"sh", "-ec", "printf 'rootfs " + nonce + "' > /payload; printf 'mount " + nonce + "' > /work/out.txt"})
	_, err := executed.Sync(ctx)
	require.NoError(t, err)
	aID, err := executed.ID(ctx)
	require.NoError(t, err)
	exportBoth := func(from *fixtureEngine, bundle, handle string) fixtureExportSelectedResult {
		var exported fixtureExportSelectedResult
		require.NoError(t, from.fixture("exportSelected", from.control("export-"+bundle, map[string]any{"bundle": bundle, "outputs": []map[string]any{
			{"handle": handle, "address": fsAddress},
			{"handle": handle, "address": mountAddress},
		}}), []string{handle}, &exported))
		require.Len(t, exported.Outputs, 2)
		return exported
	}
	exportBoth(a, "pending.json", string(aID))
	a.copyFixtureTo(b, "pending.json")

	var imported []transferFixtureMapping
	require.NoError(t, b.fixture("import", "pending.json", nil, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Container", imported[0].Type.NamedType)
	rHandle, rID := imported[0].Handle, imported[0].ResultID
	row := func(e *fixtureEngine, handle string) (dagql.TransferFixtureRow, fixtureControlsReport) {
		var report fixtureControlsReport
		require.NoError(t, e.fixture("report", "", []string{handle}, &report))
		require.Len(t, report.Rows, 1)
		var all fixtureControlsReport
		require.NoError(t, e.fixture("report", "", nil, &all))
		return report.Rows[0], all
	}
	offered := func(r dagql.TransferFixtureRow) []dagql.PartKey {
		var parts []dagql.PartKey
		for _, offer := range r.Offers {
			parts = append(parts, offer.Address.Part)
		}
		return parts
	}
	roles := func(r dagql.TransferFixtureRow) []string {
		var out []string
		for _, link := range r.SnapshotLinks {
			out = append(out, link.Role)
		}
		return out
	}

	pending, _ := row(b, rHandle)
	require.True(t, pending.Imported)
	require.ElementsMatch(t, []dagql.PartKey{"fs", "mount:/work"}, offered(pending), "both selected outputs arrive as pending offers")
	require.Empty(t, pending.SnapshotLinks, "an import owns nothing yet")

	// Only the filesystem is demanded.
	payload, err := dagger.Ref[*dagger.Container](b.client, dagger.ID(rHandle)).File("/payload").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "rootfs "+nonce, payload)
	installed, all := row(b, rHandle)
	require.Equal(t, []dagql.PartKey{"mount:/work"}, offered(installed), "the installed offer is redundant once its owner is attached; its sibling is kept raw")
	require.Equal(t, []string{"fs"}, roles(installed))
	require.Len(t, partEventsOf(all.transferFixtureReport, rID, dagql.PartEventInstalledChain), 1)
	require.Empty(t, partEventsOf(all.transferFixtureReport, rID, dagql.PartEventLazyEnter))
	fsRef := installed.SnapshotLinks[0].RefKey

	b.restart()
	restored, all := row(b, rHandle)
	require.Equal(t, dagql.CachePersistenceResetNone, all.Persistence.PersistenceResetReason)
	require.Empty(t, all.Persistence.LocalCacheResetReason)
	require.True(t, restored.Imported)
	require.Equal(t, []dagql.PartKey{"mount:/work"}, offered(restored), "the retired offer stays retired and the pending sibling stays pending across the restart")
	require.Equal(t, []string{"fs"}, roles(restored))
	require.Equal(t, fsRef, restored.SnapshotLinks[0].RefKey)
	require.Empty(t, all.Parts, "boot demands nothing")
	for _, o := range all.Reached {
		require.NotEqual(t, dagql.FixtureRenewalEnqueued, o.Point, "boot asks for no renewal")
		require.NotEqual(t, dagql.FixtureChainReaderOpen, o.Point, "boot opens no offered content")
	}
	require.Zero(t, all.Transport.FixtureHosts)

	// The owned filesystem reads without content; the sibling then installs
	// from its offer for the first time, after the restart.
	payload, err = dagger.Ref[*dagger.Container](b.client, dagger.ID(rHandle)).File("/payload").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "rootfs "+nonce, payload)
	_, all = row(b, rHandle)
	require.Empty(t, partEventsOf(all.transferFixtureReport, rID, dagql.PartEventProviderRead), "an owned part reads no offered content")
	out, err := dagger.Ref[*dagger.Container](b.client, dagger.ID(rHandle)).Directory("/work").File("out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "mount "+nonce, out)
	both, all := row(b, rHandle)
	require.Empty(t, offered(both))
	require.ElementsMatch(t, []string{"fs", mountRole}, roles(both))
	chains := partEventsOf(all.transferFixtureReport, rID, dagql.PartEventInstalledChain)
	require.Len(t, chains, 1)
	require.Equal(t, mountAddress.Part, all.Parts[chains[0]].Address.Part)
	require.Empty(t, partEventsOf(all.transferFixtureReport, rID, dagql.PartEventLazyEnter), "the exec never runs on B")

	// Forward: B exports the row it imported, with the same two selected
	// chains, and C reads both.
	forwarded := exportBoth(b, "forward.json", rHandle)
	for _, output := range forwarded.Outputs {
		require.NotEmpty(t, output.Layers, "%s forwards a chain", output.Address.Part)
	}
	b.copyFixtureTo(c, "forward.json")
	var third []transferFixtureMapping
	require.NoError(t, c.fixture("import", "forward.json", nil, &third))
	require.NotEmpty(t, third)
	require.Equal(t, "Container", third[0].Type.NamedType)
	cHandle, cID := third[0].Handle, third[0].ResultID
	payload, err = dagger.Ref[*dagger.Container](c.client, dagger.ID(cHandle)).File("/payload").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "rootfs "+nonce, payload)
	out, err = dagger.Ref[*dagger.Container](c.client, dagger.ID(cHandle)).Directory("/work").File("out.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "mount "+nonce, out)
	final, all := row(c, cHandle)
	require.True(t, final.Imported)
	require.Empty(t, offered(final))
	require.ElementsMatch(t, []string{"fs", mountRole}, roles(final))
	require.Len(t, partEventsOf(all.transferFixtureReport, cID, dagql.PartEventInstalledChain), 2)
	require.Empty(t, partEventsOf(all.transferFixtureReport, cID, dagql.PartEventLazyEnter), "the exec never runs on C")
}
