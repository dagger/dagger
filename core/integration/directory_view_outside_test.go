package core

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// writeTree writes files (contents) and links (targets) under dir.
func writeTree(t *testctx.T, dir string, files, links map[string]string) {
	t.Helper()
	for name, contents := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
	}
	for name, target := range links {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.Symlink(target, p))
	}
}

// TestSubdirectoryViewResolution covers how a subdirectory resolves what it
// is given beyond plain reads: selections through links, names, Dockerfiles,
// copies and timestamps all stay within it.
func (DirectorySuite) TestSubdirectoryViewResolution(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	host := func(dir string) *core.Directory {
		return core.NewQuery(c).Host().Directory(dir)
	}

	t.Run("outside loop", func(ctx context.Context, t *testctx.T) {
		// Only one root has a link loop outside app/; resolving app/abs
		// within app/ must not consult it.
		nonce := rand.Text()
		for _, outside := range []map[string]string{nil, {"main.txt": "main.txt"}} {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{"app/main.txt": nonce}, map[string]string{"app/abs": "/main.txt"})
			writeTree(t, dir, nil, outside)
			contents, err := host(dir).Directory("app").File("abs").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, nonce, contents)
		}
	})

	t.Run("names", func(ctx context.Context, t *testctx.T) {
		// sub/alias -> app (relative, within sub/) keeps its name;
		// sub/abs -> /app (absolute) is resolved and named for its target.
		// app/ and lib/ have equal files, so only the names can tell them apart.
		nonce := rand.Text()
		for _, target := range []string{"app", "lib"} {
			dir := t.TempDir()
			writeTree(t, dir,
				map[string]string{"sub/" + target + "/main.txt": nonce},
				map[string]string{"sub/alias": target, "sub/abs": "/" + target})
			sub := host(dir).Directory("sub")

			alias := sub.Directory("alias")
			name, err := alias.Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "alias/", name)
			stat, err := alias.Stat(ctx, ".")
			require.NoError(t, err)
			statName, err := stat.Name(ctx)
			require.NoError(t, err)
			require.Equal(t, "alias", statName, "a directory's stat name is its own name")

			name, err = sub.Directory("abs").Name(ctx)
			require.NoError(t, err)
			require.Equal(t, target+"/", name)
		}
	})

	t.Run("stat parent", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"app/main.txt": "x", "other.txt": "y"}, nil)
		stat, err := host(dir).Directory("app").Stat(ctx, "..", core.DirectoryStatOpts{DoNotFollowSymlinks: true})
		require.NoError(t, err)
		name, err := stat.Name(ctx)
		require.NoError(t, err)
		require.Equal(t, "app", name, `".." stops at the directory`)
	})

	t.Run("dockerfile outside", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		writeTree(t, dir,
			map[string]string{"app/main.txt": "x", "Dockerfile": "FROM scratch\nCOPY main.txt /main.txt\n"},
			map[string]string{"app/Dockerfile.link": "../Dockerfile"})
		app := host(dir).Directory("app")
		for _, dockerfile := range []string{"../Dockerfile", "Dockerfile.link"} {
			_, err := app.DockerBuild(core.DirectoryDockerBuildOpts{Dockerfile: dockerfile}).Sync(ctx)
			require.Error(t, err, "%s is outside the build context", dockerfile)
		}
	})

	t.Run("dockerfile copy", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		writeTree(t, dir,
			map[string]string{
				"app/file1.txt":       "one",
				"other.txt":           "outside",
				"app/Dockerfile":      "FROM scratch\nCOPY file?.txt /out/\n",
				"app/Dockerfile.link": "FROM scratch\nCOPY link /out\n",
			},
			map[string]string{"app/link": "../other.txt"})
		app := host(dir).Directory("app")

		contents, err := app.DockerBuild().File("/out/file1.txt").Contents(ctx)
		require.NoError(t, err, "COPY keeps ? wildcards")
		require.Equal(t, "one", contents)

		contents, err = app.DockerBuild(core.DirectoryDockerBuildOpts{Dockerfile: "Dockerfile.link"}).File("/out").Contents(ctx)
		require.Error(t, err, "COPY of a link out of the context must not copy the outside file, got %q", contents)
	})

	t.Run("destination through a link", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outside"), 0o755))
		writeTree(t, dir, map[string]string{"app/main.txt": "x"}, map[string]string{"app/child": "../outside"})
		src := core.NewQuery(c).Directory().WithNewFile("f", "v")
		out := host(dir).Directory("app").WithDirectory("child/x", src)

		contents, err := out.File("child/x/f").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v", contents)
		stat, err := out.Stat(ctx, "child", core.DirectoryStatOpts{DoNotFollowSymlinks: true})
		require.NoError(t, err)
		fileType, err := stat.FileType(ctx)
		require.NoError(t, err)
		require.Equal(t, core.FileTypeDirectory, fileType, "a link out of the directory is replaced, not written through")
	})

	t.Run("replace a link", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"app/target": "old"}, map[string]string{"app/link": "target"})
		replacement := core.NewQuery(c).Directory().WithNewFile("r", "new").File("r")
		for _, d := range []*core.Directory{host(filepath.Join(dir, "app")), host(dir).Directory("app")} {
			out := d.WithFile("link", replacement)
			stat, err := out.Stat(ctx, "link", core.DirectoryStatOpts{DoNotFollowSymlinks: true})
			require.NoError(t, err)
			fileType, err := stat.FileType(ctx)
			require.NoError(t, err)
			require.Equal(t, core.FileTypeRegular, fileType, "the link is replaced")
			contents, err := out.File("target").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "old", contents, "the link's target is untouched")
		}
	})

	t.Run("timestamps", func(ctx context.Context, t *testctx.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"app/main.txt": "x"}, map[string]string{"app/dangling": "../missing"})
		_, err := host(dir).Directory("app").WithTimestamps(0).Sync(ctx)
		require.NoError(t, err, "setting timestamps must not follow a link out of the directory")
	})
}

// TestOutsideInputsKeyTheirOperation covers the files a few operations read
// outside a subdirectory: the account databases a named owner is resolved
// from, and the ignore files above it. They don't change the subdirectory's
// identity, but they do change the identity of the operations that read them.
func (DirectorySuite) TestOutsideInputsKeyTheirOperation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	host := func(dir string) *core.Directory {
		return core.NewQuery(c).Host().Directory(dir)
	}
	// roots makes one root per outside variant, each with the same app/.
	roots := func(appFiles map[string]string, outside ...map[string]string) []string {
		var dirs []string
		for _, files := range outside {
			dir := t.TempDir()
			writeTree(t, dir, appFiles, nil)
			writeTree(t, dir, files, nil)
			dirs = append(dirs, dir)
		}
		return dirs
	}
	uid := func(ctx context.Context, t *testctx.T, dir *core.Directory, p string) string {
		out, err := core.NewQuery(c).Container().From(alpineImage).
			WithMountedDirectory("/src", dir).
			WithExec([]string{"stat", "-c", "%u", "/src/" + p}).
			Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}

	t.Run("account databases", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		passwd := func(id string) map[string]string {
			return map[string]string{
				"etc/passwd": "root:x:0:0:root:/root:/bin/sh\napp:x:" + id + ":" + id + "::/:/bin/sh\n",
				"etc/group":  "root:x:0:\napp:x:" + id + ":\n",
			}
		}
		dirs := roots(map[string]string{"app/main.txt": nonce}, passwd("1000"), passwd("2000"))
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")

		require.Equal(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"the account databases outside the directory are not part of it")

		src := core.NewQuery(c).Directory().WithNewFile("f", nonce)
		for i, want := range []string{"1000", "2000"} {
			app := []*core.Directory{appA, appB}[i]
			require.Equal(t, want, uid(ctx, t, app.WithDirectory("d", src, core.DirectoryWithDirectoryOpts{Owner: "app"}), "d/f"), "withDirectory")
			require.Equal(t, want, uid(ctx, t, app.WithFile("g", src.File("f"), core.DirectoryWithFileOpts{Owner: "app"}), "g"), "withFile")
			require.Equal(t, want, uid(ctx, t, app.Chown("main.txt", "app"), "main.txt"), "chown")
			chowned := core.NewQuery(c).Directory().WithFile("main.txt", app.File("main.txt").Chown("app"))
			require.Equal(t, want, uid(ctx, t, chowned, "main.txt"), "File.chown")
		}
	})

	t.Run("ignore files", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		dirs := roots(
			map[string]string{"app/x.log": nonce, "app/y.tmp": nonce},
			map[string]string{".gitignore": "app/*.log\n", ".ignore": "app/*.log\n"},
			map[string]string{".gitignore": "app/*.tmp\n", ".ignore": "app/*.tmp\n"},
		)
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")

		require.Equal(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"the ignore files above the directory are not part of it")

		for i, want := range []string{"y.tmp", "x.log"} {
			app := []*core.Directory{appA, appB}[i]
			entries, err := app.Filter(core.DirectoryFilterOpts{Gitignore: true}).Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{want}, entries, "filter")

			entries, err = core.NewQuery(c).Directory().
				WithDirectory("/", app, core.DirectoryWithDirectoryOpts{Gitignore: true}).
				Entries(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{want}, entries, "withDirectory")

			results, err := app.Search(ctx, nonce, core.DirectorySearchOpts{Literal: true, SkipIgnored: true})
			require.NoError(t, err)
			var found []string
			for _, r := range results {
				p, err := r.FilePath(ctx)
				require.NoError(t, err)
				found = append(found, p)
			}
			sort.Strings(found)
			require.Equal(t, []string{want}, found, "search")
		}
	})
}
