package core

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/require"

	sdkcore "dagger.io/dagger/core"
	"github.com/dagger/testctx"
)

// A copy that includes part of a host directory is cached by the files that it
// includes, and gives its destination directory the source root's mode, as a
// copy of the whole directory does. The source is a subdirectory: the root of
// a host directory always has the same mode.
func (DirectorySuite) TestWithDirectoryIncludeOfHostDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	include := []string{"deps/lock"}

	for _, tc := range []struct {
		name  string
		mount func(*sdkcore.Container, *sdkcore.Directory) *sdkcore.Container
	}{
		{"directory", func(ctr *sdkcore.Container, src *sdkcore.Directory) *sdkcore.Container {
			return ctr.WithDirectory("/work", sdkcore.NewQuery(c).Directory().
				WithDirectory("app", src, sdkcore.DirectoryWithDirectoryOpts{Include: include}))
		}},
		{"container", func(ctr *sdkcore.Container, src *sdkcore.Directory) *sdkcore.Container {
			return ctr.WithDirectory("/work/app", src, sdkcore.ContainerWithDirectoryOpts{Include: include})
		}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// A host directory is read once per path in a session, so each
			// state of the source is a separate path.
			host := t.TempDir()
			source := func(name, lock, other string) *sdkcore.Directory {
				root := filepath.Join(host, name, "src")
				require.NoError(t, os.MkdirAll(filepath.Join(root, "deps"), 0o755))
				require.NoError(t, os.Chmod(root, 0o770))
				require.NoError(t, os.WriteFile(filepath.Join(root, "deps", "lock"), []byte(lock), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(root, "other"), []byte(other), 0o644))
				return sdkcore.NewQuery(c).Host().Directory(filepath.Join(host, name)).Directory("src")
			}
			step := func(src *sdkcore.Directory) (mode, token string) {
				out, err := tc.mount(sdkcore.NewQuery(c).Container().From(alpineImage), src).
					WithExec([]string{"sh", "-c", "stat -c %a /work/app; echo " + tc.name + "; head -c 102 /dev/urandom | base64 -w0"}).
					Stdout(ctx)
				require.NoError(t, err)
				mode, token, _ = strings.Cut(out, "\n")
				return mode, token
			}

			lock := rand.Text()
			mode, first := step(source("base", lock, rand.Text()))
			require.Equal(t, "770", mode, "the destination directory must have the mode of the source root")
			_, same := step(source("same", lock, rand.Text()))
			require.Equal(t, first, same, "the step must be cached when only other files change")
			_, changed := step(source("changed", rand.Text(), rand.Text()))
			require.NotEqual(t, first, changed, "the step must run again when an included file changes")
		})
	}
}

// Include copies that select the same files with different patterns can still
// copy differently: an explicitly included directory replaces a file at its
// destination, a parent created only for an included file doesn't. Each
// pattern's outcome must not depend on which copy ran first.
func (DirectorySuite) TestWithDirectoryIncludePatternsKeyTheCopy(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	host := t.TempDir()
	setup := func(name string) (dst, src string) {
		dst = filepath.Join(host, name, "dst")
		src = filepath.Join(host, name, "src")
		require.NoError(t, os.MkdirAll(dst, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dst, "dir"), []byte("old-"+name), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(src, "dir"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, "dir", "file"), []byte("new-"+name), 0o644))
		return dst, src
	}
	copied := func(dst, src string, include ...string) bool {
		_, err := sdkcore.NewQuery(c).Host().Directory(dst).
			WithDirectory("/", sdkcore.NewQuery(c).Host().Directory(src), sdkcore.DirectoryWithDirectoryOpts{Include: include}).
			File("dir/file").Contents(ctx)
		return err == nil
	}
	// The contents differ per order, so the two orders share no cache.
	dst, src := setup("explicit-first")
	explicitFirst := copied(dst, src, "dir")
	implicitSecond := copied(dst, src, "dir/file")
	dst, src = setup("implicit-first")
	implicitFirst := copied(dst, src, "dir/file")
	explicitSecond := copied(dst, src, "dir")
	require.Equal(t, explicitFirst, explicitSecond, `include ["dir"] must not depend on order`)
	require.Equal(t, implicitFirst, implicitSecond, `include ["dir/file"] must not depend on order`)
}
