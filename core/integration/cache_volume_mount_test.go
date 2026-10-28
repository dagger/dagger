package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// The attributes of a "cache volume mount" span, by their wire names.
const (
	cacheVolumeKeyAttr       = "dagger.io/cache_volume.key"
	cacheVolumeNamespaceAttr = "dagger.io/cache_volume.namespace"
	cacheVolumeSharingAttr   = "dagger.io/cache_volume.sharing"
	cacheVolumeTargetAttr    = "dagger.io/cache_volume.target"
	cacheVolumeSnapshotAttr  = "dagger.io/cache_volume.snapshot"
	cacheVolumeDirAttr       = "dagger.io/cache_volume.dir"
	cacheVolumeStateAttr     = "dagger.io/cache_volume.state"
)

// cacheVolumeMountLine is one exported span as the fake Cloud recorded it,
// with its string attributes.
type cacheVolumeMountLine struct {
	Cache string            `json:"cache"`
	Name  string            `json:"name"`
	Attrs map[string]string `json:"attrs"`
}

// mountCacheVolumeIn runs cmd in an exec on engine that mounts the cache
// volume named volume at /cache, and returns its output.
func mountCacheVolumeIn(ctx context.Context, t *testctx.T, c *dagger.Client, cli *core.File, cloud telemetrySplitCloud, engine *core.Service, volume, cmd string) string {
	t.Helper()
	// A new command each time, so no exec is a cache hit that skips the
	// mount.
	script := fmt.Sprintf(`container | from %s | with-mounted-cache /cache %s | with-exec -- sh -c '%s; : %s' | stdout`,
		alpineImage, volume, cmd, rand.Text())
	out, err := telemetrySplitClient(ctx, t, c, cli, engine, cloud).
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithExec([]string{"/bin/dagger", "-c", script}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

// An exec that mounts a cache volume reports the volume and how the engine
// got its backing snapshot, and every report carries the engine cache's
// identity and generation, so a trace can tell a new volume, one a restarted
// engine reopened from its disk, and one the same engine already had.
//
// On one state, the first engine process mounts a new volume and writes a
// sentinel into it, then mounts it again in another session. After a clean
// restart on that state, the second process reads the sentinel back from the
// snapshot the first one made, under the same cache identity at a later
// generation, and mounts a key it never saw as new. An engine on another
// state is another cache, where the first volume's key is new and empty.
func (ClientSuite) TestCacheVolumeMountDiagnostic(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	cli := daggerCliFile(t, c)
	state := "dagger-cache-volume-mount-state-" + rand.Text()
	otherState := "dagger-cache-volume-mount-other-state-" + rand.Text()
	key := "cache-volume-mount-" + rand.Text()
	freshKey := "cache-volume-mount-fresh-" + rand.Text()
	sentinel := rand.Text()

	mount := func(engine *core.Service, volume, cmd string) string {
		return mountCacheVolumeIn(ctx, t, c, cli, cloud, engine, volume, cmd)
	}
	const readOrEmpty = "cat /cache/sentinel 2>/dev/null || echo empty"
	stop := func(engine *core.Service) {
		_, err := engine.Stop(ctx)
		require.NoError(t, err)
	}

	engine := engineEventsEngine(ctx, t, c, cloud, state)
	require.Equal(t, sentinel, mount(engine, key, "echo "+sentinel+" > /cache/sentinel && cat /cache/sentinel"))
	require.Equal(t, sentinel, mount(engine, key, readOrEmpty))
	stop(engine)

	engine = engineEventsEngine(ctx, t, c, cloud, state)
	require.Equal(t, sentinel, mount(engine, key, readOrEmpty), "the volume survives the restart")
	require.Equal(t, "empty", mount(engine, freshKey, readOrEmpty))
	stop(engine)

	engine = engineEventsEngine(ctx, t, c, cloud, otherState)
	require.Equal(t, "empty", mount(engine, key, readOrEmpty), "another state is another disk")
	stop(engine)

	// Every mount span names its engine cache, as <identity>/<generation>.
	var mounts []cacheVolumeMountLine
	byIdentity := map[string]map[int]bool{}
	for _, span := range readTelemetrySplitLines[cacheVolumeMountLine](ctx, t, cloud, "v1/traces.json.spans") {
		if _, ok := span.Attrs[cacheVolumeKeyAttr]; !ok {
			continue
		}
		require.Equal(t, "cache volume mount: "+span.Attrs[cacheVolumeKeyAttr], span.Name)
		id, gen, ok := strings.Cut(span.Cache, "/")
		require.True(t, ok, "mount span %q names its engine cache as <identity>/<generation>: %q", span.Name, span.Cache)
		n, err := strconv.Atoi(gen)
		require.NoError(t, err, "generation of %q", span.Cache)
		if byIdentity[id] == nil {
			byIdentity[id] = map[int]bool{}
		}
		byIdentity[id][n] = true
		mounts = append(mounts, span)
	}
	require.Len(t, mounts, 5, "one mount span per exec")
	require.Len(t, byIdentity, 2, "two states are two caches")

	// The state with two processes is the restarted one; its earlier
	// generation is the first process.
	var restarted, other string
	for id, gens := range byIdentity {
		if len(gens) == 2 {
			restarted = id
		} else {
			other = id
		}
	}
	require.NotEmpty(t, restarted, "one cache was restarted")
	require.NotEmpty(t, other)
	gens := slices.Sorted(maps.Keys(byIdentity[restarted]))
	process := func(span cacheVolumeMountLine) string {
		id, gen, _ := strings.Cut(span.Cache, "/")
		switch {
		case id == other:
			return "other"
		case gen == strconv.Itoa(gens[0]):
			return "first"
		default:
			return "restarted"
		}
	}

	type observed struct{ process, key, state string }
	var got []observed
	snapshot := map[observed]string{}
	for _, span := range mounts {
		require.Equal(t, "/cache", span.Attrs[cacheVolumeTargetAttr])
		require.Equal(t, "mainClient", span.Attrs[cacheVolumeNamespaceAttr])
		require.Equal(t, "SHARED", span.Attrs[cacheVolumeSharingAttr])
		require.NotEmpty(t, span.Attrs[cacheVolumeSnapshotAttr])
		o := observed{process(span), span.Attrs[cacheVolumeKeyAttr], span.Attrs[cacheVolumeStateAttr]}
		got = append(got, o)
		snapshot[o] = span.Attrs[cacheVolumeSnapshotAttr]
	}
	newVolume := observed{"first", key, "new"}
	reused := observed{"first", key, "reused"}
	reopened := observed{"restarted", key, "reopened"}
	fresh := observed{"restarted", freshKey, "new"}
	otherDisk := observed{"other", key, "new"}
	require.ElementsMatch(t, []observed{newVolume, reused, reopened, fresh, otherDisk}, got)

	require.Equal(t, snapshot[newVolume], snapshot[reused], "the same engine reused the snapshot")
	require.Equal(t, snapshot[newVolume], snapshot[reopened], "the restarted engine reopened the same snapshot")
	require.NotEqual(t, snapshot[newVolume], snapshot[fresh], "a new key gets its own snapshot")
}

// With cacheVolumes.dir, a mount reports the volume's ID in the directory in
// place of a snapshot, and how this engine got the volume: new, left in the
// directory by an earlier engine, or already mounted by this one.
//
// The first engine creates a volume in the directory, writes a sentinel into
// it and mounts it again. The second engine, on a state of its own, gets the
// directory, reads the sentinel back from the volume the first one left, and
// mounts a key the directory doesn't have as new.
func (ClientSuite) TestCacheVolumeDirMountDiagnostic(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	cli := daggerCliFile(t, c)
	dirVol := core.NewQuery(c).CacheVolume("dagger-cache-volume-dir-mount-" + rand.Text())
	key := "cache-volume-dir-mount-" + rand.Text()
	freshKey := "cache-volume-dir-mount-fresh-" + rand.Text()
	sentinel := rand.Text()

	mount := func(engine *core.Service, volume, cmd string) string {
		return mountCacheVolumeIn(ctx, t, c, cli, cloud, engine, volume, cmd)
	}
	const readOrEmpty = "cat /cache/sentinel 2>/dev/null || echo empty"
	start := func() *core.Service {
		return engineEventsEngine(ctx, t, c, cloud, "dagger-cache-volume-dir-mount-state-"+rand.Text(), withCacheVolumeDir(ctx, t, dirVol))
	}
	stop := func(engine *core.Service) {
		_, err := engine.Stop(ctx)
		require.NoError(t, err)
	}

	engine := start()
	require.Equal(t, sentinel, mount(engine, key, "echo "+sentinel+" > /cache/sentinel && cat /cache/sentinel"))
	require.Equal(t, sentinel, mount(engine, key, readOrEmpty))
	stop(engine)

	engine = start()
	require.Equal(t, sentinel, mount(engine, key, readOrEmpty), "the next engine gets the volume with the directory")
	require.Equal(t, "empty", mount(engine, freshKey, readOrEmpty))
	stop(engine)

	var mounts []cacheVolumeMountLine
	for _, span := range readTelemetrySplitLines[cacheVolumeMountLine](ctx, t, cloud, "v1/traces.json.spans") {
		if _, ok := span.Attrs[cacheVolumeKeyAttr]; ok {
			mounts = append(mounts, span)
		}
	}
	require.Len(t, mounts, 4, "one mount span per exec")

	// The engines have their own states, so their own caches; the second
	// is the one that mounted the fresh key.
	var second string
	for _, span := range mounts {
		if span.Attrs[cacheVolumeKeyAttr] == freshKey {
			second, _, _ = strings.Cut(span.Cache, "/")
		}
	}
	require.NotEmpty(t, second)

	type observed struct{ engine, key, state string }
	var got []observed
	dirID := map[observed]string{}
	for _, span := range mounts {
		require.Equal(t, "/cache", span.Attrs[cacheVolumeTargetAttr])
		require.Empty(t, span.Attrs[cacheVolumeSnapshotAttr], "a directory-backed volume has no snapshot")
		require.NotEmpty(t, span.Attrs[cacheVolumeDirAttr])
		which := "first"
		if id, _, _ := strings.Cut(span.Cache, "/"); id == second {
			which = "second"
		}
		o := observed{which, span.Attrs[cacheVolumeKeyAttr], span.Attrs[cacheVolumeStateAttr]}
		got = append(got, o)
		dirID[o] = span.Attrs[cacheVolumeDirAttr]
	}
	newVolume := observed{"first", key, "new"}
	reused := observed{"first", key, "reused"}
	reopened := observed{"second", key, "reopened"}
	fresh := observed{"second", freshKey, "new"}
	require.ElementsMatch(t, []observed{newVolume, reused, reopened, fresh}, got)

	require.Equal(t, dirID[newVolume], dirID[reused])
	require.Equal(t, dirID[newVolume], dirID[reopened], "the second engine mounted the volume the first one left")
	require.NotEqual(t, dirID[newVolume], dirID[fresh], "a new key gets its own volume")
}
