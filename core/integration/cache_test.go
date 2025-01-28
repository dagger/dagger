package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
)

type CacheSuite struct{}

func TestCache(t *testing.T) {
	testctx.Run(testCtx, t, CacheSuite{}, Middleware()...)
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

	fooTmpl := `package main

import (
	"context"
)

type Foo struct{}

func (f *Foo) GetCacheVolumeId(ctx context.Context) (string, error) {
	id, err := dag.CacheVolume("volume-name").ID(ctx)
	return string(id), err
}
`
	barTmpl := `package main

import (
	"context"
)

type Bar struct{}

func (b *Bar) GetCacheVolumeId(ctx context.Context) (string, error) {
	id, err := dag.CacheVolume("volume-name").ID(ctx)
	return string(id), err
}
`
	ctr := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/bar").
		With(daggerExec("init", "--name=bar", "--source=.", "--sdk=go")).
		WithNewFile("main.go", barTmpl).
		WithWorkdir("/work/foo").
		With(daggerExec("init", "--name=foo", "--source=.", "--sdk=go")).
		WithNewFile("main.go", fooTmpl)

	fooID, err := ctr.
		WithWorkdir("/work/foo").
		With(daggerExec("call", "get-cache-volume-id")).
		Stdout(ctx)
	require.NoError(t, err)

	barID, err := ctr.
		WithWorkdir("/work/bar").
		With(daggerExec("call", "get-cache-volume-id")).
		Stdout(ctx)

	require.NoError(t, err)
	require.NotEqual(t, fooID, barID)
}

func (CacheSuite) TestCacheIdSameAcrossSession(ctx context.Context, t *testctx.T) {
	session1 := connect(ctx, t)

	fooTmpl := `package main
	import (
		"context"
	)

	type Foo struct {}
	func (f *Foo) GetCacheVolumeId(ctx context.Context) (string, error) {
		id, err := dag.CacheVolume("volume-name").ID(ctx)
		return string(id), err
	}
	`

	ctr1 := session1.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, session1)).
		WithWorkdir("/work/foo").
		With(daggerExec("init", "--name=foo", "--source=.", "--sdk=go")).
		WithNewFile("main.go", fooTmpl)

	fooID, err := ctr1.
		WithWorkdir("/work/foo").
		With(daggerExec("call", "get-cache-volume-id")).
		Stdout(ctx)
	require.NoError(t, err)

	session2 := connect(ctx, t)
	ctr2 := session2.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, session2)).
		WithWorkdir("/work/foo").
		With(daggerExec("init", "--name=foo", "--source=.", "--sdk=go")).
		WithNewFile("main.go", fooTmpl)

	fooID2, err := ctr2.
		WithWorkdir("/work/foo").
		With(daggerExec("call", "get-cache-volume-id")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, fooID, fooID2)
}

func (CacheSuite) TestCacheVolumePassedAcrossModules(ctx context.Context, t *testctx.T) {
	session := connect(ctx, t)

	fooTmpl := `package main

import (
	"context"
	"dagger/foo/internal/dagger"
	"fmt"
)

type Foo struct{}

func (f *Foo) Populate(ctx context.Context, input string) (*dagger.Container, error) {
	return dag.Container().
		From("alpine").
		WithMountedCache("/tmp-cache-mount", dag.CacheVolume("cache-name")).
		WithExec([]string{"sh", "-c", fmt.Sprintf("echo '%s' > /tmp-cache-mount/input.txt", input)}).
		Sync(ctx)
}

func (f *Foo) Fetch(ctx context.Context) (string, error) {
	cache := dag.CacheVolume("cache-name")
	return dag.Bar().Fetch(ctx, cache)
}
`

	barTmpl := `package main

import (
	"context"
	"dagger/bar/internal/dagger"
)

type Bar struct{}

func (f *Bar) Fetch(ctx context.Context, vol *dagger.CacheVolume) (string, error) {
	return dag.Container().
		From("alpine").
		WithMountedCache("/tmp-cache-mount-bar", vol).
		WithExec([]string{"sh", "-c", "cat /tmp-cache-mount-bar/input.txt"}).
		Stdout(ctx)
}
`

	ctr := session.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, session)).
		WithWorkdir("/work/bar").
		With(daggerExec("init", "--name=bar", "--source=.", "--sdk=go")).
		WithNewFile("main.go", barTmpl).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=foo", "--source=.", "--sdk=go")).
		WithNewFile("main.go", fooTmpl).
		With(daggerExec("use", "./bar"))

	inpContent := "some-foo-bar-content"
	_, err := ctr.
		WithWorkdir("/work").
		With(daggerExec("call", "populate", "--input", inpContent)).
		Stdout(ctx)
	require.NoError(t, err)

	fetchedFromCache, err := ctr.
		WithWorkdir("/work").
		With(daggerExec("call", "fetch")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, inpContent, strings.TrimSpace(fetchedFromCache))
}

func (CacheSuite) TestCacheNotImpactedByChangeInModuleSource(ctx context.Context, t *testctx.T) {
	session := connect(ctx, t)

	fooTmpl := `package main
	import (
		"context"
	)

	type Foo struct {}
	func (f *Foo) UseCacheVolume(ctx context.Context) (string, error) {
		id, err := dag.CacheVolume("cache-name").ID(ctx)
		return string(id), err
	}
	`

	barTmpl := `package main
	import (
		"context"
	)

	type Foo struct {}
	func (f *Foo) UseCacheVolume(ctx context.Context) (string, error) {
		id, err := dag.CacheVolume("cache-name").ID(ctx)
		return string(id), err
	}

	func (f *Foo) PassCacheVolume(ctx context.Context) (string, error) {
		return f.UseCacheVolume(ctx)
	}
	`

	ctr := session.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, session)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=foo", "--source=.", "--sdk=go"))

	fooID, err := ctr.
		WithWorkdir("/work").
		WithNewFile("main.go", fooTmpl).
		With(daggerExec("call", "use-cache-volume")).
		Stdout(ctx)
	require.NoError(t, err)

	fooID2, err := ctr.WithWorkdir("/work").
		WithNewFile("main.go", barTmpl).
		With(daggerExec("call", "use-cache-volume")).
		Stdout(ctx)
	require.NoError(t, err)

	require.Equal(t, fooID, fooID2)
}
