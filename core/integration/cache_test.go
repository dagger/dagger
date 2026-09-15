package core

// These tests cover Dagger cache volumes and API-level cache keys used to reuse
// filesystem state across container executions.
//
// See also:
// - localcache_test.go: engine-side local cache GC and retention.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
)

type CacheSuite struct{}

func TestCache(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CacheSuite{})
}

func (CacheSuite) TestVolume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	volID1, err := c.CacheVolume("ab").ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, volID1)

	volID2, err := c.CacheVolume("ab").ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, volID2)

	volID3, err := c.CacheVolume("ac").ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, volID3)

	require.Equal(t, volID1, volID2)
	require.NotEqual(t, volID1, volID3)
}

func (CacheSuite) TestVolumeWithSubmount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("file mount", func(ctx context.Context, t *testctx.T) {
		subfile := c.Directory().WithNewFile("foo", "bar").File("foo")
		ctr := c.Container().From(alpineImage).
			WithMountedCache("/cache", c.CacheVolume(identity.NewID())).
			WithMountedFile("/cache/subfile", subfile)

		out, err := ctr.WithExec([]string{"cat", "/cache/subfile"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", strings.TrimSpace(out))

		contents, err := ctr.File("/cache/subfile").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", strings.TrimSpace(contents))
	})

	t.Run("dir mount", func(ctx context.Context, t *testctx.T) {
		subdir := c.Directory().WithNewFile("foo", "bar").WithNewFile("baz", "qux")
		ctr := c.Container().From(alpineImage).
			WithMountedCache("/cache", c.CacheVolume(identity.NewID())).
			WithMountedDirectory("/cache/subdir", subdir)

		for fileName, expectedContents := range map[string]string{
			"foo": "bar",
			"baz": "qux",
		} {
			subpath := filepath.Join("/cache/subdir", fileName)
			out, err := ctr.WithExec([]string{"cat", subpath}).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedContents, strings.TrimSpace(out))

			contents, err := ctr.File(subpath).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedContents, strings.TrimSpace(contents))

			dir := ctr.Directory("/cache/subdir")
			contents, err = dir.File(fileName).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, expectedContents, strings.TrimSpace(contents))
		}
	})
}

func (CacheSuite) TestLockedCacheVolumeSerializesWriters(ctx context.Context, t *testctx.T) {
	const writers = 3

	cacheKey := "locked-cache-" + identity.NewID()
	clients := make([]*dagger.Client, writers)
	for i := range clients {
		clients[i] = connect(ctx, t)
	}

	start := make(chan struct{})
	var eg errgroup.Group
	for i := range writers {
		i := i
		eg.Go(func() error {
			<-start
			_, err := clients[i].
				Container().
				From(alpineImage).
				WithEnvVariable("RUN_ID", fmt.Sprint(i)).
				WithEnvVariable("CACHEBUSTER", identity.NewID()).
				WithMountedCache("/cache", clients[i].CacheVolume(cacheKey, dagger.CacheVolumeOpts{
					Sharing: dagger.CacheSharingModeLocked,
				})).
				WithExec([]string{
					"sh",
					"-euxc",
					`mkdir /cache/in-use
echo "$RUN_ID" >> /cache/order
sleep 1
rmdir /cache/in-use`,
				}).
				Sync(ctx)
			return err
		})
	}

	close(start)
	require.NoError(t, eg.Wait())

	out, err := clients[0].
		Container().
		From(alpineImage).
		WithMountedCache("/cache", clients[0].CacheVolume(cacheKey, dagger.CacheVolumeOpts{
			Sharing: dagger.CacheSharingModeLocked,
		})).
		WithExec([]string{"cat", "/cache/order"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Len(t, strings.Fields(strings.TrimSpace(out)), writers)
}

func (CacheSuite) TestLocalImportCacheReuse(ctx context.Context, t *testctx.T) {
	hostDirPath := t.TempDir()
	err := os.WriteFile(filepath.Join(hostDirPath, "foo"), []byte("bar"), 0o644)
	require.NoError(t, err)

	runExec := func(c *dagger.Client) string {
		out, err := c.Container().From(alpineImage).
			WithDirectory("/fromhost", c.Host().Directory(hostDirPath)).
			WithExec([]string{"stat", "/fromhost/foo"}).
			WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	c1 := connect(ctx, t)
	out1 := runExec(c1)

	c2 := connect(ctx, t)
	out2 := runExec(c2)

	require.Equal(t, out1, out2)
}

func (CacheSuite) TestCacheIsNamespaced(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		With(withModuleFixture(t, c, "foo", "go/cache-volume-id-foo")).
		With(withModuleFixture(t, c, "bar", "go/cache-volume-id-bar"))

	fooID, err := ctr.
		WithWorkdir("/work/foo").
		With(daggerCallAt(".", "get-cache-volume-id")).
		Stdout(ctx)
	require.NoError(t, err)

	barID, err := ctr.
		WithWorkdir("/work/bar").
		With(daggerCallAt(".", "get-cache-volume-id")).
		Stdout(ctx)

	require.NoError(t, err)
	require.NotEqual(t, fooID, barID)
}

func (CacheSuite) TestCacheIdSameAcrossSession(ctx context.Context, t *testctx.T) {
	session1 := connect(ctx, t)

	ctr1 := moduleFixture(t, session1, "go/cache-volume-id-foo")

	fooID, err := ctr1.
		With(daggerCallAt(".", "get-cache-volume-id")).
		Stdout(ctx)
	require.NoError(t, err)

	session2 := connect(ctx, t)
	ctr2 := moduleFixture(t, session2, "go/cache-volume-id-foo")

	fooID2, err := ctr2.
		With(daggerCallAt(".", "get-cache-volume-id")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, fooID, fooID2)
}

func (CacheSuite) TestCacheVolumePassedAcrossModules(ctx context.Context, t *testctx.T) {
	session := connect(ctx, t)

	ctr := moduleFixture(t, session, "go/cache-volume-passed")

	inpContent := "some-foo-bar-content"
	_, err := ctr.
		With(daggerCallAt(".", "populate", "--input", inpContent)).
		Stdout(ctx)
	require.NoError(t, err)

	fetchedFromCache, err := ctr.
		With(daggerCallAt(".", "fetch")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, inpContent, strings.TrimSpace(fetchedFromCache))
}

func (CacheSuite) TestCacheNotImpactedByChangeInModuleSource(ctx context.Context, t *testctx.T) {
	session := connect(ctx, t)

	ctr := goGitBase(t, session)

	fooID, err := ctr.
		With(withModuleFixture(t, session, ".", "go/cache-source-stable-a")).
		With(daggerCallAt(".", "use-cache-volume")).
		Stdout(ctx)
	require.NoError(t, err)

	fooID2, err := ctr.
		With(withModuleFixture(t, session, ".", "go/cache-source-stable-b")).
		With(daggerCallAt(".", "use-cache-volume")).
		Stdout(ctx)
	require.NoError(t, err)

	require.Equal(t, fooID, fooID2)
}
