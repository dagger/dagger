package core

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A directory File.extract made on one engine is an ordinary result another
// engine downloads: reading it there installs the downloaded snapshot and
// never runs the extraction.
func (RemoteCacheTransferSuite) TestExtractedDirectory(ctx context.Context, t *testctx.T) {
	outer := connect(ctx, t)
	start := func(volume *core.CacheVolume) *dagger.Client {
		ctr := devEngineContainerWithStateKey(outer, "extract-state-"+rand.Text(), func(ctr *core.Container) *core.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		ctr = engineWithConfig(ctx, t, engineConfigWithEnabled(true), engineConfigWithGC("1000000000000000", "0", "1000000000000000", "0"))(ctr)
		upstream := devEngineContainerAsService(ctr)
		tunnel, err := core.NewQuery(outer).Host().Tunnel(upstream).Start(ctx)
		require.NoError(t, err)
		endpoint, err := tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		client, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithWorkdir(t.TempDir()), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, stopNestedEngine(ctx, &client, &upstream, &tunnel))
		})
		return client
	}
	aVolume := core.NewQuery(outer).CacheVolume("extract-a-" + rand.Text())
	bVolume := core.NewQuery(outer).CacheVolume("extract-b-" + rand.Text())
	a, b := start(aVolume), start(bVolume)

	payload := "extracted on A " + rand.Text()
	archiveDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "a.zip"), extractTestZip(t, extractTestEntry{name: "dir/f", body: []byte(payload)}), 0o644))
	extracted, err := core.NewQuery(a).Host().Directory(archiveDir).File("a.zip").Extract().Sync(ctx)
	require.NoError(t, err)
	aID, err := extracted.ID(ctx)
	require.NoError(t, err)
	var exported []transferFixtureMapping
	require.NoError(t, transferFixtureSelected(ctx, a, "extract.json", []string{string(aID)}, []string{string(aID)}, &exported))
	require.NotEmpty(t, exported)
	_, err = core.NewQuery(outer).Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", rand.Text()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/extract.json /destination/bundles/; cp -a /source/blobs /destination/"}).Sync(ctx)
	require.NoError(t, err)

	var imported []transferFixtureMapping
	require.NoError(t, transferFixture(ctx, b, "import", "extract.json", []string{}, &imported))
	require.NotEmpty(t, imported)
	require.Equal(t, "Directory", imported[0].Type.NamedType)
	handle, rowID := imported[0].Handle, imported[0].ResultID
	contents, err := core.Ref[*core.Directory](core.NewQuery(b), core.ID(handle)).File("dir/f").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, payload, contents)

	var report transferFixtureReport
	require.NoError(t, transferFixture(ctx, b, "report", "", []string{}, &report))
	counts := map[string]int{}
	for _, event := range report.Parts {
		if event.ResultID == rowID && event.Address.Part == "snapshot" {
			counts[event.Kind]++
		}
	}
	require.Equal(t, 1, counts["installed-chain"], "B installs the downloaded snapshot once: %v", counts)
	require.Positive(t, counts["provider-read"], "B reads the snapshot's content from A's export: %v", counts)
	require.Zero(t, counts["lazy-enter"], "B never runs the extraction: %v", counts)
}
