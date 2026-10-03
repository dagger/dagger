package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// rebuildOnAnotherEngine builds values on engine A, exports them with no
// blob, imports them into engine B and evaluates each imported row there, so
// B must rebuild every part from its saved operation. check reads the
// rebuilt values through B's client, by name.
func rebuildOnAnotherEngine(ctx context.Context, t *testctx.T, build func(*dagger.Client) map[string]dagger.ID, check func(*dagger.Client, map[string]string)) {
	outer := connect(ctx, t)
	type running struct {
		upstream, tunnel *dagger.Service
		client           *dagger.Client
	}
	start := func(state string, volume *dagger.CacheVolume) *running {
		ctr := devEngineContainerWithStateKey(outer, state, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithMountedCache("/transfer-fixture", volume).WithEnvVariable("_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT", "/transfer-fixture")
		})
		e := &running{upstream: devEngineContainerAsService(ctr)}
		tunnel, err := outer.Host().Tunnel(e.upstream).Start(ctx)
		require.NoError(t, err)
		e.tunnel = tunnel
		endpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
		require.NoError(t, err)
		e.client, err = dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
		require.NoError(t, err)
		return e
	}
	id := identity.NewID()
	aVolume := outer.CacheVolume("rebuild-a-" + id)
	bVolume := outer.CacheVolume("rebuild-b-" + id)
	a := start("rebuild-a-state-"+id, aVolume)
	defer func() { require.NoError(t, discardNestedEngine(ctx, &a.client, &a.upstream, &a.tunnel)) }()
	b := start("rebuild-b-state-"+id, bVolume)
	defer func() { require.NoError(t, discardNestedEngine(ctx, &b.client, &b.upstream, &b.tunnel)) }()

	roots := build(a.client)
	for name, root := range roots {
		var exported []transferFixtureMapping
		require.NoError(t, transferFixtureSelected(ctx, a.client, "rebuild-"+name+".json", []string{string(root)}, []string{}, &exported), name)
		require.NotEmpty(t, exported, name)
	}
	_, err := outer.Container().From(alpineImage).WithMountedCache("/source", aVolume).WithMountedCache("/destination", bVolume).
		WithEnvVariable("COPY", identity.NewID()).WithExec([]string{"sh", "-ec", "mkdir -p /destination/bundles; cp /source/bundles/rebuild-*.json /destination/bundles/"}).Sync(ctx)
	require.NoError(t, err)

	handles := map[string]string{}
	for name := range roots {
		var imported []transferFixtureMapping
		require.NoError(t, transferFixture(ctx, b.client, "import", "rebuild-"+name+".json", []string{}, &imported), name)
		require.NotEmpty(t, imported, name)
		handle := imported[0].Handle
		var evaluated bool
		require.NoError(t, transferFixture(ctx, b.client, "evaluate", "", []string{handle}, &evaluated), name)
		require.True(t, evaluated, name)
		handles[name] = handle
	}
	var report transferFixtureReport
	require.NoError(t, transferFixture(ctx, b.client, "report", "", []string{}, &report))
	for _, event := range report.Parts {
		require.NotEqual(t, "provider-read", event.Kind, "nothing was offered for download: %+v", event)
	}
	check(b.client, handles)
}

// The image files of a container are rebuilt on another engine when their
// blobs are missing.
func (RemoteCacheTransferSuite) TestRebuildContainerImageFiles(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		ctr := c.Container().From(alpineImage).WithNewFile("/data", "rebuilt "+seed)
		tarball, err := ctr.AsTarball().Sync(ctx)
		require.NoError(t, err)
		manifest, err := ctr.Manifest().Sync(ctx)
		require.NoError(t, err)
		tarballID, err := tarball.ID(ctx)
		require.NoError(t, err)
		manifestID, err := manifest.ID(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"tarball": tarballID, "manifest": manifestID}
	}, func(c *dagger.Client, handles map[string]string) {
		tarball := dagger.Ref[*dagger.File](c, dagger.ID(handles["tarball"]))
		contents, err := c.Container().Import(tarball).File("/data").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "rebuilt "+seed, contents)
		manifest, err := dagger.Ref[*dagger.File](c, dagger.ID(handles["manifest"])).Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, manifest, `"layers"`)
	})
}

// A Docker build is rebuilt on another engine when its blob is missing.
func (RemoteCacheTransferSuite) TestRebuildDockerBuild(ctx context.Context, t *testctx.T) {
	seed := identity.NewID()
	rebuildOnAnotherEngine(ctx, t, func(c *dagger.Client) map[string]dagger.ID {
		built, err := c.Directory().
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN echo "+seed+" > /built\n").
			DockerBuild().
			Sync(ctx)
		require.NoError(t, err)
		builtID, err := built.ID(ctx)
		require.NoError(t, err)
		return map[string]dagger.ID{"build": builtID}
	}, func(c *dagger.Client, handles map[string]string) {
		contents, err := dagger.Ref[*dagger.Container](c, dagger.ID(handles["build"])).File("/built").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, seed+"\n", contents)
	})
}
