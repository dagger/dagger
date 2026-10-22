package core

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestSubdirectoryViewHoldsOnlyItsFiles asserts that a subdirectory resolves
// paths with itself as the root: neither "..", a symlink out of it, an
// absolute symlink, nor a symlink added to it later reaches its parent's files.
// A subdirectory of a content-keyed directory is identified by the content of
// its own files, so if any of these reached the parent, two subdirectories of
// equal content from different parents would be interchangeable while reading
// different files.
func (DirectorySuite) TestSubdirectoryViewHoldsOnlyItsFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// root lays out a parent whose app/ is the same in every root and whose
	// other.txt differs, with links in app/ as given.
	root := func(nonce, other string, links map[string]string) string {
		dir := t.TempDir()
		app := filepath.Join(dir, "app")
		require.NoError(t, os.MkdirAll(filepath.Join(app, "nested"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(app, "main.txt"), []byte(nonce), 0o644))
		require.NoError(t, os.Symlink("../main.txt", filepath.Join(app, "nested", "inner")))
		for name, target := range links {
			require.NoError(t, os.Symlink(target, filepath.Join(app, name)))
		}
		require.NoError(t, os.Symlink("app", filepath.Join(dir, "alias")))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte(other), 0o644))
		return dir
	}
	host := func(dir string) *core.Directory {
		return core.NewQuery(c).Host().Directory(dir)
	}
	// Each read goes through a fresh pair of roots, so a cached result from
	// one case can't stand in for another; the first root's read comes first,
	// so a read taken for it would show in the second.
	for _, tc := range []struct {
		name  string
		links map[string]string
		read  func(*core.Directory) *core.File
	}{
		{"dotdot", nil, func(d *core.Directory) *core.File {
			return d.Directory("app").File("../other.txt")
		}},
		{"link out", map[string]string{"link": "../other.txt"}, func(d *core.Directory) *core.File {
			return d.Directory("app").File("link")
		}},
		{"absolute link", map[string]string{"link": "/other.txt"}, func(d *core.Directory) *core.File {
			return d.Directory("app").File("link")
		}},
		{"link added later", nil, func(d *core.Directory) *core.File {
			return d.Directory("app").WithSymlink("../other.txt", "later").File("later")
		}},
		{"symlinked path", map[string]string{"link": "../other.txt"}, func(d *core.Directory) *core.File {
			return d.Directory("alias").File("link")
		}},
		{"dotdot subdirectory", nil, func(d *core.Directory) *core.File {
			return d.Directory("app").Directory("..").File("other.txt")
		}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			nonce := rand.Text()
			rootA, rootB := root(nonce, "a", tc.links), root(nonce, "b", tc.links)
			for _, r := range []string{rootA, rootB} {
				contents, err := tc.read(host(r)).Contents(ctx)
				require.Error(t, err, "a read in a subdirectory must not reach its parent's files, got %q", contents)
			}
		})
	}

	t.Run("links within it", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		links := map[string]string{"link": "nested/inner", "abs": "/main.txt"}
		rootA, rootB := root(nonce, "a", links), root(nonce, "b", links)
		appA, appB := host(rootA).Directory("app"), host(rootB).Directory("app")
		for _, app := range []*core.Directory{appA, appB} {
			for _, name := range []string{"link", "abs"} {
				contents, err := app.File(name).Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, nonce, contents)
			}
		}
		require.Equal(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"subdirectories with equal files must still reuse work done on either")
	})

	t.Run("name", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		dir := t.TempDir()
		for _, sub := range []string{"app", "lib"} {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, sub, "main.txt"), []byte(nonce), 0o644))
		}
		parent := host(dir)
		for _, sub := range []string{"app", "lib"} {
			name, err := parent.Directory(sub).Name(ctx)
			require.NoError(t, err)
			require.Equal(t, sub+"/", name, "a subdirectory's name must be its own, whatever another of equal content is called")
		}
	})
}

// uncachedViewToken mounts dir into an exec whose output is random, so two
// calls return the same token only when the second was a cache hit.
func uncachedViewToken(ctx context.Context, t *testctx.T, c *dagger.Client, dir *core.Directory) string {
	t.Helper()
	out, err := core.NewQuery(c).Container().
		From(alpineImage).
		WithMountedDirectory("/src", dir).
		WithExec([]string{"sh", "-c", "ls -R /src >/dev/null && cat /proc/sys/kernel/random/uuid"}).
		Stdout(ctx)
	require.NoError(t, err)
	return out
}
