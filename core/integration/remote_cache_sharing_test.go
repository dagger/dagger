package core

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// An imported Host directory whose bytes B already has locally ends up owning
// a snapshot of its own, installed once, settled once, with no content read
// and no evaluation of its Lazy operation - and it keeps that ownership
// across a restart.
//
// The install may come from an early sharing pass or from the later ordinary
// demand in this test; both emit installed-ready and neither downloads. The
// assertions therefore never wait for an asynchronous pass, and the report is
// never re-read to let one finish.
func (RemoteCacheTransferSuite) TestSharedHostDirectoryLifetime(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	checkout := func() string {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("shared host notes"), 0644))
		return dir
	}
	type running struct {
		upstream, tunnel *dagger.Service
		client           *dagger.Client
	}
	stop := func(e *running) {
		if e.client != nil {
			require.NoError(t, e.client.Close())
		}
		if e.upstream != nil {
			_, err := e.upstream.Stop(context.WithoutCancel(ctx))
			require.NoError(t, err)
		}
		if e.tunnel != nil {
			_, err := e.tunnel.Stop(context.WithoutCancel(ctx), dagger.ServiceStopOpts{Kill: true})
			require.NoError(t, err)
		}
	}
	start := func(state string, volume *dagger.CacheVolume, workdir string) *running {
		ctr := devEngineContainerWithStateKey(outer, state, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
		e := &running{upstream: devEngineContainerAsService(ctr)}
		tunnel, err := outer.Host().Tunnel(e.upstream).Start(ctx)
		require.NoError(t, err)
		e.tunnel = tunnel
		endpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		e.client, err = dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithWorkdir(workdir), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		return e
	}

	aVolume := outer.CacheVolume("b6-share-a-" + identity.NewID())
	bVolume := outer.CacheVolume("b6-share-b-" + identity.NewID())
	aDir, bDir := checkout(), checkout()
	a := start("b6-share-a-state-"+identity.NewID(), aVolume, aDir)
	defer stop(a)
	bState := "b6-share-b-state-" + identity.NewID()
	b := start(bState, bVolume, bDir)
	defer func() { stop(b) }()

	// A captures its host directory and exports it.
	aDirectory, err := a.client.Host().Directory(".").Sync(ctx)
	require.NoError(t, err)
	aID, err := aDirectory.ID(ctx)
	require.NoError(t, err)
	var exported []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, a.client, "share.json", []string{string(aID)}, []string{string(aID)}, &exported))
	require.NotEmpty(t, exported)

	// B captures the same content locally first: this is the donor.
	bDirectory, err := b.client.Host().Directory(".").Sync(ctx)
	require.NoError(t, err)
	var donorReport transferFixtureReport
	bID, err := bDirectory.ID(ctx)
	require.NoError(t, err)
	require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{string(bID)}, &donorReport))
	require.Len(t, donorReport.Rows, 1)
	donorRow := donorReport.Rows[0]
	require.False(t, donorRow.Imported, "B's own capture is not an imported row")
	require.Len(t, donorRow.SnapshotLinks, 1)
	donorRef := donorRow.SnapshotLinks[0].RefKey
	require.NotEmpty(t, donorRef)

	_, err = outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/share.json /destination/bundles/; cp -a /source/blobs /destination/"}).Sync(ctx)
	require.NoError(t, err)
	var imported []transferFixtureMapping
	require.NoError(t, transferFixture(ctx, b.client, "import", "share.json", []string{}, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Directory", imported[0].Type.NamedType)
	handle, rowID := imported[0].Handle, imported[0].ResultID

	// An ordinary read through the exact imported reference. Whether the
	// early pass or this demand installed the part, the bytes are local.
	entries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(handle)).Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "notes.txt")

	var report transferFixtureReport
	require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &report))
	counts := map[string]int{}
	selectedBeforeInstall := false
	installed := false
	for _, event := range report.Parts {
		if event.ResultID != rowID || event.Address.Part != "snapshot" {
			continue
		}
		counts[event.Kind]++
		if event.Kind == "selected-ready" && !installed {
			selectedBeforeInstall = true
		}
		if event.Kind == "installed-ready" {
			installed = true
		}
	}
	require.Equal(t, 1, counts["installed-ready"], "the imported row takes its snapshot exactly once: %v", counts)
	require.Equal(t, 1, counts["settled"], "its ownership bookkeeping settles once: %v", counts)
	require.Zero(t, counts["installed-chain"], "a local equivalent is used, never a downloaded chain")
	require.Zero(t, counts["provider-read"], "no content is requested for a locally available snapshot")
	require.Zero(t, counts["lazy-enter"], "the imported value's Lazy operation is never evaluated")
	// Whether a demand selected the source first is a route observation, not
	// a requirement: an early sharing pass emits no selected-ready.
	t.Logf("shared host directory row=%d selected-ready-before-install=%t counts=%v", rowID, selectedBeforeInstall, counts)

	var installedRow *dagql.TransferFixtureRow
	for i := range report.Rows {
		if report.Rows[i].ResultID == rowID {
			installedRow = &report.Rows[i]
		}
	}
	require.NotNil(t, installedRow)
	require.True(t, installedRow.Imported, "the receiver keeps its imported origin")
	require.Len(t, installedRow.SnapshotLinks, 1, "the receiver owns its own snapshot link")
	require.Equal(t, donorRef, installedRow.SnapshotLinks[0].RefKey, "it is the donor's exact snapshot, owned independently")

	// Restart B: ownership survives, and reading through the saved handle
	// adds no further part event for that row.
	stop(b)
	b = start(bState, bVolume, bDir)
	var restored transferFixtureReport
	require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{handle}, &restored))
	require.Equal(t, dagql.CachePersistenceResetNone, restored.Persistence.PersistenceResetReason)
	require.Empty(t, restored.Persistence.LocalCacheResetReason)
	require.Len(t, restored.Rows, 1)
	restoredRow := restored.Rows[0]
	require.True(t, restoredRow.Imported, "the restored row is still imported")
	require.Len(t, restoredRow.SnapshotLinks, 1)
	require.Equal(t, donorRef, restoredRow.SnapshotLinks[0].RefKey, "its owner link survived the restart")

	restoredEntries, err := dagger.Ref[*dagger.Directory](b.client, dagger.ID(handle)).Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, restoredEntries, "notes.txt")
	var afterRead transferFixtureReport
	require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &afterRead))
	for _, event := range afterRead.Parts {
		require.NotEqual(t, restoredRow.ResultID, event.ResultID, "a read of a restored owned snapshot needs no part operation: %+v", event)
	}
}
