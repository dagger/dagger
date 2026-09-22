package core

import (
	"context"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// This uses the real engine executor and two independent persistent stores.
// Only FS chains cross the boundary; execMeta must run the saved exec privately.
func (RemoteCacheTransferSuite) TestPartMixedExecOutputs(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	start := func(volume *dagger.CacheVolume) *dagger.Client {
		ctr := devEngineContainerWithStateKey(outer, "b4-mixed-state-"+identity.NewID(), func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
		upstream := devEngineContainerAsService(ctr)
		tunnel, err := outer.Host().Tunnel(upstream).Start(ctx)
		require.NoError(t, err)
		endpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		client, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithWorkdir(t.TempDir()), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, stopNestedEngine(ctx, &client, &upstream, &tunnel))
		})
		return client
	}
	aVolume := outer.CacheVolume("b4-mixed-a-" + identity.NewID())
	bVolume := outer.CacheVolume("b4-mixed-b-" + identity.NewID())
	a, b := start(aVolume), start(bVolume)
	parent := a.Container().From(alpineImage)
	executed := parent.WithExec([]string{"sh", "-ec", "printf 'downloaded filesystem' > /payload; printf 'private exec metadata'"})
	stdout, err := executed.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "private exec metadata", stdout)
	aID, err := executed.ID(ctx)
	require.NoError(t, err)
	parentID, err := parent.ID(ctx)
	require.NoError(t, err)
	var exported []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, a, "mixed.json", []string{string(aID)}, []string{string(aID), string(parentID)}, &exported))
	_, err = outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/mixed.json /destination/bundles/; cp -a /source/blobs /destination/"}).Sync(ctx)
	require.NoError(t, err)
	var imported []transferFixtureMapping
	require.NoError(t, transferFixture(ctx, b, "import", "mixed.json", []string{}, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Container", imported[0].Type.NamedType)
	handle, rowID := imported[0].Handle, imported[0].ResultID
	loaded := dagger.Ref[*dagger.Container](b, dagger.ID(handle))
	readReport := func() transferFixtureReport {
		var report transferFixtureReport
		require.NoError(t, transferFixture(ctx, b, "report", "", []string{handle}, &report))
		require.Len(t, report.Rows, 1)
		return report
	}
	rootEvents := func(report transferFixtureReport, kind string) []dagql.TransferFixturePartEvent {
		var events []dagql.TransferFixturePartEvent
		for _, event := range report.Parts {
			if event.ResultID == rowID && event.Kind == kind {
				events = append(events, event)
			}
		}
		return events
	}
	fsSnapshot := func(report transferFixtureReport) string {
		for _, link := range report.Rows[0].SnapshotLinks {
			if link.Role == "fs" {
				return link.RefKey
			}
		}
		t.Fatal("missing installed filesystem role")
		return ""
	}
	metadata, err := loaded.EnvVariables(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, metadata)
	before := readReport()
	require.Empty(t, rootEvents(before, "provider-read"), "known metadata must not demand a layer")
	require.Empty(t, rootEvents(before, "lazy-enter"), "known metadata must not enter an operation")
	contents, err := loaded.File("/payload").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "downloaded filesystem", contents)
	fsOnly := readReport()
	require.Len(t, rootEvents(fsOnly, "installed-chain"), 1)
	require.Positive(t, len(rootEvents(fsOnly, "provider-read")))
	require.Empty(t, rootEvents(fsOnly, "lazy-enter"))
	for _, link := range fsOnly.Rows[0].SnapshotLinks {
		require.NotEqual(t, "meta", link.Role, "FS-only demand cannot complete execMeta")
	}
	originalFS := fsSnapshot(fsOnly)
	started := time.Now()
	stdout, err = loaded.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "private exec metadata", stdout)
	latency := time.Since(started)
	after := readReport()
	require.Equal(t, originalFS, fsSnapshot(after), "first installed filesystem wins")
	require.Len(t, rootEvents(after, "lazy-enter"), 1)
	require.Len(t, rootEvents(after, "installed-lazy"), 1)
	require.Equal(t, dagql.PartKey("execMeta"), rootEvents(after, "installed-lazy")[0].Address.Part)
	released := rootEvents(after, "lazy-ref-released")
	require.Len(t, released, 1, "the actual redundant private FS ref must be released once")
	require.NotEmpty(t, released[0].SnapshotID)
	require.NotEqual(t, originalFS, released[0].SnapshotID)
	require.Empty(t, rootEvents(after, "lazy-ref-release-error"))
	var releaseSeen, syncSeen, settlementSeen bool
	for _, event := range after.Parts[len(fsOnly.Parts):] {
		if event.ResultID != rowID {
			continue
		}
		switch event.Kind {
		case "lazy-ref-released":
			releaseSeen = true
		case "owner-sync":
			require.True(t, releaseSeen, "redundant ref release precedes owner sync")
			syncSeen = true
		case "settled":
			require.True(t, syncSeen)
			settlementSeen = true
		}
	}
	require.True(t, settlementSeen)
	// Neither repeating metadata nor reading the downloaded bytes may rerun exec.
	stdout, err = loaded.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "private exec metadata", stdout)
	contents, err = loaded.File("/payload").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "downloaded filesystem", contents)
	require.Len(t, rootEvents(readReport(), "lazy-enter"), 1)
	t.Logf("mixed private exec latency=%s originalFS=%s redundantFS=%s rootEvents=%v", latency, originalFS, released[0].SnapshotID, rootEvents(after, "lazy-enter"))
}
