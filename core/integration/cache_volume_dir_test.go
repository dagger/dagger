package core

// These tests cover keeping cache volumes in a separate directory
// (cacheVolumes.dir): handing the directory from one engine to the next,
// parity with snapshot-backed volumes, single ownership, and pruning.
//
// See also:
// - cache_test.go: cache volume behavior on the default engine.
// - engine/cachevolumedir: the directory store's own tests.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/testutil"
)

type CacheVolumeDirSuite struct{}

func TestCacheVolumeDir(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CacheVolumeDirSuite{})
}

const cacheVolumeDirPath = "/var/lib/dagger-cache-volumes"

// withCacheVolumeDir gives a nested engine a cache volume directory backed by
// dirVol, as our infra backs it with a Namespace cache volume. It adds the
// setting to whatever engine config the image has.
func withCacheVolumeDir(ctx context.Context, t *testctx.T, dirVol *core.CacheVolume) func(*core.Container) *core.Container {
	return func(ctr *core.Container) *core.Container {
		t.Helper()
		cfg := map[string]any{}
		entries, err := ctr.Directory("/etc/dagger").Entries(ctx)
		require.NoError(t, err)
		if slices.Contains(entries, "engine.json") {
			existing, err := ctr.File("/etc/dagger/engine.json").Contents(ctx)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal([]byte(existing), &cfg))
		}
		cfg["cacheVolumes"] = map[string]any{"dir": cacheVolumeDirPath}
		js, err := json.Marshal(cfg)
		require.NoError(t, err)
		return ctr.
			WithMountedCache(cacheVolumeDirPath, dirVol).
			WithNewFile("/etc/dagger/engine.json", string(js))
	}
}

type cacheVolumeDirEngine struct {
	upstream *core.Service
	tunnel   *core.Service
	client   *dagger.Client
}

// startCacheVolumeDirEngine starts a nested dev engine with its own fresh
// state, and connects a client to it.
func startCacheVolumeDirEngine(ctx context.Context, t *testctx.T, c *dagger.Client, opts ...func(*core.Container) *core.Container) *cacheVolumeDirEngine {
	t.Helper()
	upstream := devEngineContainerAsService(devEngineContainer(c, opts...))
	tunnel, err := core.NewQuery(c).Host().Tunnel(upstream).Start(ctx)
	require.NoError(t, err)
	endpoint, err := tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	client, err := dagger.Connect(ctx,
		dagger.WithRunnerHost(endpoint),
		dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	return &cacheVolumeDirEngine{upstream: upstream, tunnel: tunnel, client: client}
}

// stop stops the engine gracefully, so that it gives up the directory.
func (e *cacheVolumeDirEngine) stop(ctx context.Context, t *testctx.T) {
	t.Helper()
	require.NoError(t, e.client.Close())
	_, err := e.upstream.Stop(ctx)
	require.NoError(t, err)
	_, err = e.tunnel.Stop(ctx, core.ServiceStopOpts{Kill: true})
	require.NoError(t, err)
}

func (e *cacheVolumeDirEngine) shell(ctx context.Context, vol *core.CacheVolume, script string) (string, error) {
	q := core.NewQuery(e.client)
	return q.Container().From(alpineImage).
		WithMountedCache("/cache", vol).
		WithExec([]string{"sh", "-c", script}).
		Stdout(ctx)
}

// inspectDir runs script over the directory's backing volume, with no engine
// using it.
func inspectDir(ctx context.Context, t *testctx.T, c *dagger.Client, dirVol *core.CacheVolume, script string) string {
	t.Helper()
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedCache("/dir", dirVol).
		WithEnvVariable("CACHEBUST", rand.Text()).
		WithExec([]string{"sh", "-c", script}).
		Stdout(ctx)
	require.NoError(t, err)
	return out
}

func (CacheVolumeDirSuite) TestHandOff(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirVol := core.NewQuery(c).CacheVolume("cvdir-" + rand.Text())

	a := startCacheVolumeDirEngine(ctx, t, c, withCacheVolumeDir(ctx, t, dirVol))
	qa := core.NewQuery(a.client)
	_, err := a.shell(ctx, qa.CacheVolume("tool-cache"), "echo from-a > /cache/f")
	require.NoError(t, err)
	withSource := qa.CacheVolume("with-source", core.CacheVolumeOpts{
		Source: qa.Directory().WithNewFile("seed", "seeded"),
	})
	_, err = a.shell(ctx, withSource, "echo from-a > /cache/written")
	require.NoError(t, err)
	a.stop(ctx, t)

	// The next engine has fresh state and gets the directory.
	b := startCacheVolumeDirEngine(ctx, t, c, withCacheVolumeDir(ctx, t, dirVol))
	qb := core.NewQuery(b.client)
	out, err := b.shell(ctx, qb.CacheVolume("tool-cache"), "cat /cache/f")
	require.NoError(t, err)
	require.Equal(t, "from-a\n", out)

	// Load unrelated results first, so the source gets different engine-local
	// handles than it had in A: the volume is still found by its recipe.
	for i := range 5 {
		_, err := qb.Directory().WithNewFile("unrelated", strings.Repeat("x", i+1)).Sync(ctx)
		require.NoError(t, err)
	}
	withSource = qb.CacheVolume("with-source", core.CacheVolumeOpts{
		Source: qb.Directory().WithNewFile("seed", "seeded"),
	})
	out, err = b.shell(ctx, withSource, "cat /cache/seed /cache/written")
	require.NoError(t, err)
	require.Equal(t, "seededfrom-a\n", out)
	b.stop(ctx, t)

	// Control: with the option off, a fresh engine doesn't see A's writes.
	off := startCacheVolumeDirEngine(ctx, t, c)
	out, err = off.shell(ctx, core.NewQuery(off.client).CacheVolume("tool-cache"), "cat /cache/f 2>/dev/null || echo missing")
	require.NoError(t, err)
	require.Equal(t, "missing\n", out)
	off.stop(ctx, t)

	// On disk: plain directories named by identity, each with metadata.
	out = inspectDir(ctx, t, c, dirVol, `ls /dir/v1/volumes | wc -l; cat /dir/v1/volumes/*/fs/f 2>/dev/null; for v in /dir/v1/volumes/*; do test -f $v/meta.json || echo "no meta in $v"; done`)
	require.Equal(t, "2\nfrom-a\n", out)
}

func (CacheVolumeDirSuite) TestSourceAndOwnerParity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// The volume's root, its files' modes and owners are the same whether the
	// volume is a snapshot or a directory.
	const script = `cd /cache && stat -c '%n %u:%g %a' . sub sub/file && cat sub/file`
	run := func(e *cacheVolumeDirEngine) string {
		q := core.NewQuery(e.client)
		src := q.Directory().
			WithNewFile("top/sub/file", "seed", core.DirectoryWithNewFileOpts{Permissions: 0o640}).
			Directory("top")
		vol := q.CacheVolume("parity", core.CacheVolumeOpts{Source: src, Owner: "1000:1000"})
		out, err := e.shell(ctx, vol, script)
		require.NoError(t, err)
		return out
	}

	dirEngine := startCacheVolumeDirEngine(ctx, t, c, withCacheVolumeDir(ctx, t, core.NewQuery(c).CacheVolume("cvdir-"+rand.Text())))
	inDir := run(dirEngine)
	dirEngine.stop(ctx, t)

	snapshotEngine := startCacheVolumeDirEngine(ctx, t, c)
	inSnapshot := run(snapshotEngine)
	snapshotEngine.stop(ctx, t)

	require.Equal(t, inSnapshot, inDir)
	require.Contains(t, inDir, ". 1000:1000")
	require.Contains(t, inDir, "seed")
}

func (CacheVolumeDirSuite) TestSecondEngineFallsBack(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirVol := core.NewQuery(c).CacheVolume("cvdir-" + rand.Text())

	a := startCacheVolumeDirEngine(ctx, t, c, withCacheVolumeDir(ctx, t, dirVol))
	// B is pointed at the directory A owns: it refuses the directory and
	// keeps cache volumes as snapshots, but works.
	b := startCacheVolumeDirEngine(ctx, t, c, withCacheVolumeDir(ctx, t, dirVol))
	_, err := b.shell(ctx, core.NewQuery(b.client).CacheVolume("k"), "echo from-b > /cache/f")
	require.NoError(t, err)
	out, err := b.shell(ctx, core.NewQuery(b.client).CacheVolume("k"), "cat /cache/f")
	require.NoError(t, err)
	require.Equal(t, "from-b\n", out)

	_, err = a.shell(ctx, core.NewQuery(a.client).CacheVolume("k"), "echo from-a > /cache/f")
	require.NoError(t, err)
	b.stop(ctx, t)
	a.stop(ctx, t)

	out = inspectDir(ctx, t, c, dirVol, `cat /dir/v1/volumes/*/fs/f; grep -rl from-b /dir || echo "no from-b"`)
	require.Equal(t, "from-a\nno from-b\n", out)
}

func (CacheVolumeDirSuite) TestListAndPrune(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirVol := core.NewQuery(c).CacheVolume("cvdir-" + rand.Text())

	e := startCacheVolumeDirEngine(ctx, t, c, withCacheVolumeDir(ctx, t, dirVol))
	q := core.NewQuery(e.client)
	_, err := e.shell(ctx, q.CacheVolume("listed"), "head -c 1048576 /dev/zero > /cache/f")
	require.NoError(t, err)

	cacheVolumeEntries := func() []string {
		t.Helper()
		entries, err := q.Engine().LocalCache().EntrySet().Entries(ctx)
		require.NoError(t, err)
		var found []string
		for _, entry := range entries {
			desc, err := entry.Description(ctx)
			require.NoError(t, err)
			recordType, err := entry.RecordType(ctx)
			require.NoError(t, err)
			if recordType == "exec.cachemount" && strings.Contains(desc, `"listed"`) {
				found = append(found, desc)
			}
		}
		return found
	}
	require.Len(t, cacheVolumeEntries(), 1, "the directory's volumes are listed with the local cache")

	require.NoError(t, q.Engine().LocalCache().Prune(ctx))
	require.Empty(t, cacheVolumeEntries(), "a full prune empties the directory")
	e.stop(ctx, t)

	out := inspectDir(ctx, t, c, dirVol, `ls /dir/v1/volumes | wc -l`)
	require.Equal(t, "0\n", out)
}
