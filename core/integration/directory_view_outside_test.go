package core

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
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

	t.Run("stat name through a self link", func(ctx context.Context, t *testctx.T) {
		// alias -> app in one root and alias -> lib in the other, with equal
		// files: a stat that resolves back to the directory itself reports
		// the directory's own name, not its target's.
		nonce := rand.Text()
		for _, target := range []string{"app", "lib"} {
			dir := t.TempDir()
			writeTree(t, dir,
				map[string]string{target + "/main.txt": nonce},
				map[string]string{"alias": target, target + "/self": ".", target + "/chain": "self"})
			alias := host(dir).Directory("alias")
			for _, p := range []string{"self", "chain", "self/.", "chain/self"} {
				stat, err := alias.Stat(ctx, p)
				require.NoError(t, err)
				name, err := stat.Name(ctx)
				require.NoError(t, err)
				require.Equal(t, "alias", name, p)
			}
		}
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

// TestOutsideInputsKeyTheView covers the files a few operations on a
// subdirectory read outside it: the account databases a named owner is
// resolved from, and the ignore files above it. A subdirectory identified by
// its content carries them in its identity, so subdirectories whose parents
// differ in them don't share work, while parents that differ in anything else
// still do.
func (DirectorySuite) TestOutsideInputsKeyTheView(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	host := func(dir string) *core.Directory {
		return core.NewQuery(c).Host().Directory(dir)
	}
	// roots makes one root per outside variant, each with the same files and
	// links otherwise.
	roots := func(files, links map[string]string, outside ...map[string]string) []string {
		var dirs []string
		for _, extra := range outside {
			dir := t.TempDir()
			writeTree(t, dir, files, links)
			writeTree(t, dir, extra, nil)
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
	entries := func(ctx context.Context, t *testctx.T, dir *core.Directory) []string {
		ents, err := dir.Entries(ctx)
		require.NoError(t, err)
		sort.Strings(ents)
		return ents
	}
	searched := func(ctx context.Context, t *testctx.T, search func() ([]core.SearchResult, error)) []string {
		results, err := search()
		require.NoError(t, err)
		var found []string
		for _, r := range results {
			p, err := r.FilePath(ctx)
			require.NoError(t, err)
			found = append(found, p)
		}
		sort.Strings(found)
		return found
	}

	t.Run("account databases", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		passwd := func(id string) map[string]string {
			return map[string]string{
				"etc/passwd": "root:x:0:0:root:/root:/bin/sh\napp:x:" + id + ":" + id + "::/:/bin/sh\n",
				"etc/group":  "root:x:0:\napp:x:" + id + ":\n",
			}
		}
		dirs := roots(map[string]string{"app/main.txt": nonce}, nil, passwd("1000"), passwd("2000"))
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")
		require.NotEqual(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"subdirectories of parents with different account databases must not share work")

		src := core.NewQuery(c).Directory().WithNewFile("f", nonce)
		for i, want := range []string{"1000", "2000"} {
			app := []*core.Directory{appA, appB}[i]
			require.Equal(t, want, uid(ctx, t, app.WithDirectory("d", src, core.DirectoryWithDirectoryOpts{Owner: "app"}), "d/f"), "withDirectory")
			require.Equal(t, want, uid(ctx, t, app.WithFile("g", src.File("f"), core.DirectoryWithFileOpts{Owner: "app"}), "g"), "withFile")
			require.Equal(t, want, uid(ctx, t, app.Chown("main.txt", "app"), "main.txt"), "chown")
		}
	})

	t.Run("ignore files", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		dirs := roots(
			map[string]string{"app/x.log": nonce, "app/y.tmp": nonce}, nil,
			map[string]string{".gitignore": "*.log\n", ".ignore": "*.log\n"},
			map[string]string{".gitignore": "*.tmp\n", ".ignore": "*.tmp\n"},
		)
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")
		require.NotEqual(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"subdirectories of parents with different ignore files must not share work")

		for i, want := range []string{"y.tmp", "x.log"} {
			app := []*core.Directory{appA, appB}[i]
			require.Equal(t, []string{want}, entries(ctx, t, app.Filter(core.DirectoryFilterOpts{Gitignore: true})), "filter")
			require.Equal(t, []string{want}, entries(ctx, t, core.NewQuery(c).Directory().
				WithDirectory("/", app, core.DirectoryWithDirectoryOpts{Gitignore: true})), "withDirectory")
			require.Equal(t, []string{want}, searched(ctx, t, func() ([]core.SearchResult, error) {
				return app.Search(ctx, nonce, core.DirectorySearchOpts{Literal: true, SkipIgnored: true})
			}), "search")

			ws := app.AsWorkspace()
			require.Equal(t, []string{want}, entries(ctx, t, ws.Directory(".", core.WorkspaceDirectoryOpts{Gitignore: true})), "workspace directory")
			require.Equal(t, []string{want}, searched(ctx, t, func() ([]core.SearchResult, error) {
				return ws.Search(ctx, nonce, core.WorkspaceSearchOpts{Literal: true, SkipIgnored: true})
			}), "workspace search")
		}
	})

	t.Run("ignore files by path", func(ctx context.Context, t *testctx.T) {
		// alias -> app in one root and alias -> lib in the other, with equal
		// files and the same ignore rule for app/: the rule applies to one of
		// them only.
		nonce := rand.Text()
		files := map[string]string{"app/x.log": nonce, "lib/x.log": nonce, "app/y.tmp": nonce, "lib/y.tmp": nonce, ".gitignore": "app/*.log\n"}
		var got [][]string
		for _, target := range []string{"app", "lib"} {
			dir := t.TempDir()
			writeTree(t, dir, files, map[string]string{"alias": target})
			got = append(got, entries(ctx, t, host(dir).Directory("alias").Filter(core.DirectoryFilterOpts{Gitignore: true})))
		}
		require.Equal(t, [][]string{{"y.tmp"}, {"x.log", "y.tmp"}}, got)
	})

	t.Run("ignore file linked from outside", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		dirs := roots(
			map[string]string{"app/x.log": nonce, "app/y.tmp": nonce},
			map[string]string{"app/.gitignore": "../rules"},
			map[string]string{"rules": "*.log\n"},
			map[string]string{"rules": "*.tmp\n"},
		)
		for i, want := range []string{"y.tmp", "x.log"} {
			got := entries(ctx, t, host(dirs[i]).Directory("app").Filter(core.DirectoryFilterOpts{Gitignore: true}))
			require.Equal(t, []string{".gitignore", want}, got)
		}
	})

	t.Run("other files", func(ctx context.Context, t *testctx.T) {
		// Parents with the same account databases and ignore files, which
		// differ elsewhere, still share work on equal subdirectories.
		nonce := rand.Text()
		dirs := roots(
			map[string]string{
				"app/main.txt": nonce,
				"etc/passwd":   "app:x:1000:1000::/:/bin/sh\n",
				".gitignore":   "*.log\n",
			}, nil,
			map[string]string{"other.txt": "a"},
			map[string]string{"other.txt": "b"},
		)
		require.Equal(t,
			uncachedViewToken(ctx, t, c, host(dirs[0]).Directory("app")),
			uncachedViewToken(ctx, t, c, host(dirs[1]).Directory("app")),
			"subdirectories with equal files must still share work across parents that differ elsewhere")
	})

	t.Run("repositories", func(ctx context.Context, t *testctx.T) {
		// Git run on a subdirectory finds a repository above it, or reads one
		// at its root that can point anywhere, so a subdirectory with either is
		// keyed by how it was made, not by its content. One under no
		// repository is still shared.
		nonce := rand.Text()
		gitRepo := func(dir, msg string) string {
			for _, args := range [][]string{
				{"init", "-q"},
				{"-c", "user.email=t@example.com", "-c", "user.name=t", "add", "-A"},
				{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", msg},
			} {
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))
			}
			head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
			require.NoError(t, err)
			return strings.TrimSpace(string(head))
		}

		var apps []*core.Directory
		var heads, commits []string
		for _, msg := range []string{"a", "b"} {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{"app/main.txt": nonce, "other.txt": msg}, nil)
			heads = append(heads, gitRepo(dir, msg))
			app := host(dir).Directory("app")
			apps = append(apps, app)
			commit, err := app.AsGit().Head().Commit(ctx)
			require.NoError(t, err)
			commits = append(commits, commit)
		}
		require.Equal(t, heads, commits, "git on a subdirectory reads its own parent's repository")
		require.NotEqual(t, uncachedViewToken(ctx, t, c, apps[0]), uncachedViewToken(ctx, t, c, apps[1]),
			"subdirectories under different repositories must not share work")

		bare := roots(map[string]string{"app/main.txt": nonce, "objects/.keep": "", "refs/.keep": ""}, nil,
			map[string]string{"HEAD": "ref: refs/heads/a\n"},
			map[string]string{"HEAD": "ref: refs/heads/b\n"},
		)
		require.NotEqual(t,
			uncachedViewToken(ctx, t, c, host(bare[0]).Directory("app")),
			uncachedViewToken(ctx, t, c, host(bare[1]).Directory("app")),
			"subdirectories under different bare repositories must not share work")

		own := roots(map[string]string{"app/main.txt": nonce, "app/.git/HEAD": "ref: refs/heads/main\n"}, nil,
			map[string]string{"other.txt": "a"},
			map[string]string{"other.txt": "b"},
		)
		require.NotEqual(t,
			uncachedViewToken(ctx, t, c, host(own[0]).Directory("app")),
			uncachedViewToken(ctx, t, c, host(own[1]).Directory("app")),
			"a subdirectory with its own .git is keyed by how it was made")

		// Each of these makes git or ripgrep read beyond the view's files: the
		// view itself a bare repository, a repository nested in it pointing
		// out of it, a worktree-style repository above it, and a Jujutsu
		// repository above it, which ripgrep counts as one.
		for name, files := range map[string]map[string]string{
			"bare view":     {"app/HEAD": "ref: refs/heads/main\n", "app/objects/.keep": "", "app/refs/.keep": "", "app/config": "[include]\n\tpath = ../outside\n"},
			"nested":        {"app/repo/.git": "gitdir: ../../meta\n"},
			"worktree-like": {"HEAD": "ref: refs/heads/main\n", "commondir": "../common\n"},
		} {
			dirs := roots(map[string]string{"app/main.txt": nonce}, nil,
				mergeFiles(files, map[string]string{"outside": "a", "meta/HEAD": "a", "common/x": "a"}),
				mergeFiles(files, map[string]string{"outside": "b", "meta/HEAD": "b", "common/x": "b"}),
			)
			require.NotEqual(t,
				uncachedViewToken(ctx, t, c, host(dirs[0]).Directory("app")),
				uncachedViewToken(ctx, t, c, host(dirs[1]).Directory("app")),
				name)
		}
		jj := roots(map[string]string{"app/main.txt": nonce, ".gitignore": "*.log\n"}, nil,
			map[string]string{".jj/repo": "x"},
			map[string]string{"other.txt": "x"},
		)
		require.NotEqual(t,
			uncachedViewToken(ctx, t, c, host(jj[0]).Directory("app")),
			uncachedViewToken(ctx, t, c, host(jj[1]).Directory("app")),
			"a Jujutsu repository above a view changes what search ignores")

		plain := roots(map[string]string{"app/main.txt": nonce}, nil,
			map[string]string{"other.txt": "a"},
			map[string]string{"other.txt": "b"},
		)
		require.Equal(t,
			uncachedViewToken(ctx, t, c, host(plain[0]).Directory("app")),
			uncachedViewToken(ctx, t, c, host(plain[1]).Directory("app")),
			"subdirectories under no repository still share work")
	})

	t.Run("unreadable ignore file", func(ctx context.Context, t *testctx.T) {
		// A parent whose ignore file can't be read still gives a working
		// subdirectory, keyed by how it was made rather than by its content.
		nonce := rand.Text()
		dirs := roots(
			map[string]string{"app/main.txt": nonce},
			map[string]string{".gitignore": ".gitignore"},
			map[string]string{"other.txt": "a"},
			map[string]string{"other.txt": "b"},
		)
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")
		for _, app := range []*core.Directory{appA, appB} {
			contents, err := app.File("main.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, nonce, contents)
		}
		require.NotEqual(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB))
	})
}

func mergeFiles(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
