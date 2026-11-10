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

	t.Run("dockerfile copy key", func(ctx context.Context, t *testctx.T) {
		// Both roots have the same other.txt outside app/ and differ only in
		// app/other.txt, which is what a COPY resolving within app/ reads, so
		// each build must copy its own app/other.txt.
		nonce := rand.Text()
		for _, src := range []string{"link", "../other.txt"} {
			for _, inside := range []string{"a", "b"} {
				dir := t.TempDir()
				writeTree(t, dir,
					map[string]string{
						"other.txt":      "outside " + nonce,
						"app/other.txt":  inside + " " + nonce,
						"app/Dockerfile": "FROM scratch\nCOPY " + src + " /out\n",
					},
					map[string]string{"app/link": "../other.txt"})
				contents, err := host(dir).Directory("app").DockerBuild().File("/out").Contents(ctx)
				require.NoError(t, err, src)
				require.Equal(t, inside+" "+nonce, contents, src)
			}
		}
	})

	t.Run("module ignore rules through a link", func(ctx context.Context, t *testctx.T) {
		// The module's source is missing, so loading it checks whether an
		// ignore rule excludes it. Only one root has a rule for it, outside
		// app/, behind app/link; within app/ there is none in either. That
		// root goes first, so its load isn't answered by the other's.
		for _, outside := range []map[string]string{{"rules/.gitignore": "missing\n"}, nil} {
			dir := t.TempDir()
			writeTree(t, dir,
				map[string]string{"app/dagger.json": `{"name":"probe","sdk":"dang","source":"link/missing"}`},
				map[string]string{"app/link": "../rules"})
			writeTree(t, dir, outside, nil)
			subpath, err := host(dir).Directory("app").AsModuleSource().SourceSubpath(ctx)
			require.NoError(t, err)
			require.Equal(t, "link/missing", subpath)
		}
	})

	t.Run("dockerfile bind mount", func(ctx context.Context, t *testctx.T) {
		// Both roots have the same other.txt outside app/ and differ only in
		// app/other.txt, which is what app/link resolves to within app/, so
		// each build must mount its own app/other.txt.
		nonce := rand.Text()
		dockerfile := "FROM " + alpineImage + "\n" +
			"RUN --mount=type=bind,source=link,target=/mnt/in cat /mnt/in > /out\n"
		for _, inside := range []string{"a", "b"} {
			dir := t.TempDir()
			writeTree(t, dir,
				map[string]string{
					"other.txt":      "outside " + nonce,
					"app/other.txt":  inside + " " + nonce,
					"app/Dockerfile": dockerfile,
				},
				map[string]string{"app/link": "../other.txt"})
			contents, err := host(dir).Directory("app").DockerBuild().File("/out").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, inside+" "+nonce, contents)
		}
	})

	t.Run("container path under a mounted subdirectory", func(ctx context.Context, t *testctx.T) {
		// A path below a mounted Directory resolves within that Directory, as
		// it does through the Directory itself, so a link climbing out of it
		// reaches neither the file nor the directory beside it in the parent.
		dir := t.TempDir()
		writeTree(t, dir,
			map[string]string{
				"other.txt":          "outside",
				"outside/secret.txt": "secret",
				"app/other.txt":      "inside",
			},
			map[string]string{"app/link": "../other.txt", "app/dlink": "../outside"})
		ctr := core.NewQuery(c).Container().WithMountedDirectory("/mnt", host(dir).Directory("app"))

		contents, err := ctr.File("/mnt/link").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "inside", contents)

		entries, err := ctr.Directory("/mnt/dlink").Entries(ctx)
		require.Error(t, err, "a link out of a mounted subdirectory must not reach the parent's directory, got %v", entries)
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
// TestHiddenReadsStayInTheView asserts that operations on a subdirectory
// which read files beyond the ones they're given, to resolve an owner's name,
// apply ignore rules or open a Git repository, read them within the
// subdirectory, as they would if it were the whole filesystem. So equal
// subdirectories of different parents give the same results, and share work.
func (DirectorySuite) TestHiddenReadsStayInTheView(ctx context.Context, t *testctx.T) {
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
	passwd := func(prefix, id string) map[string]string {
		return map[string]string{
			prefix + "etc/passwd": "root:x:0:0:root:/root:/bin/sh\napp:x:" + id + ":" + id + "::/:/bin/sh\n",
			prefix + "etc/group":  "root:x:0:\napp:x:" + id + ":\n",
		}
	}

	t.Run("account databases", func(ctx context.Context, t *testctx.T) {
		// The parents define app differently; the subdirectory doesn't
		// define it, so a name resolves in neither, and the two share work.
		nonce := rand.Text()
		dirs := roots(map[string]string{"app/main.txt": nonce}, nil, passwd("", "1000"), passwd("", "2000"))
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")
		require.Equal(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"equal subdirectories must share work whatever their parents' account databases")

		src := core.NewQuery(c).Directory().WithNewFile("f", nonce)
		for _, app := range []*core.Directory{appA, appB} {
			_, err := app.WithDirectory("d", src, core.DirectoryWithDirectoryOpts{Owner: "app"}).Sync(ctx)
			require.Error(t, err, "withDirectory")
			_, err = app.WithFile("g", src.File("f"), core.DirectoryWithFileOpts{Owner: "app"}).Sync(ctx)
			require.Error(t, err, "withFile")
			_, err = app.Chown("main.txt", "app").Sync(ctx)
			require.Error(t, err, "chown")
		}

		// The subdirectory's own account databases name the owner.
		own := roots(mergeFiles(map[string]string{"app/main.txt": nonce}, passwd("app/", "3000")), nil, passwd("", "1000"))
		app := host(own[0]).Directory("app")
		require.Equal(t, "3000", uid(ctx, t, app.WithDirectory("d", src, core.DirectoryWithDirectoryOpts{Owner: "app"}), "d/f"), "withDirectory")
		require.Equal(t, "3000", uid(ctx, t, app.WithFile("g", src.File("f"), core.DirectoryWithFileOpts{Owner: "app"}), "g"), "withFile")
		require.Equal(t, "3000", uid(ctx, t, app.Chown("main.txt", "app"), "main.txt"), "chown")
	})

	t.Run("ignore files", func(ctx context.Context, t *testctx.T) {
		// The parents' ignore files don't apply to the subdirectory; its own
		// do.
		nonce := rand.Text()
		dirs := roots(
			map[string]string{"app/x.log": nonce, "app/y.tmp": nonce, "app/sub/.ignore": "*.tmp\n", "app/sub/z.tmp": nonce}, nil,
			map[string]string{".gitignore": "*.log\n", ".ignore": "*.log\n"},
			map[string]string{".gitignore": "*.tmp\n", ".ignore": "*.tmp\n"},
		)
		appA, appB := host(dirs[0]).Directory("app"), host(dirs[1]).Directory("app")
		require.Equal(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB),
			"equal subdirectories must share work whatever their parents' ignore files")

		for _, app := range []*core.Directory{appA, appB} {
			want := []string{"sub/", "x.log", "y.tmp"}
			require.Equal(t, want, entries(ctx, t, app.Filter(core.DirectoryFilterOpts{Gitignore: true})), "filter")
			require.Equal(t, want, entries(ctx, t, core.NewQuery(c).Directory().
				WithDirectory("/", app, core.DirectoryWithDirectoryOpts{Gitignore: true})), "withDirectory")
			require.Equal(t, []string{"x.log", "y.tmp"}, searched(ctx, t, func() ([]core.SearchResult, error) {
				return app.Search(ctx, nonce, core.DirectorySearchOpts{Literal: true, SkipIgnored: true})
			}), "search applies the subdirectory's own ignore files only")

			ws := app.AsWorkspace()
			require.Equal(t, want, entries(ctx, t, ws.Directory(".", core.WorkspaceDirectoryOpts{Gitignore: true})), "workspace directory")
			require.Equal(t, []string{"x.log", "y.tmp"}, searched(ctx, t, func() ([]core.SearchResult, error) {
				return ws.Search(ctx, nonce, core.WorkspaceSearchOpts{Literal: true, SkipIgnored: true})
			}), "workspace search")
		}
	})

	t.Run("ignore file linked from outside", func(ctx context.Context, t *testctx.T) {
		// app/.gitignore and app/.ignore lead to ../rules, which resolves
		// to app/rules within the subdirectory, so the parents' rules don't
		// apply.
		nonce := rand.Text()
		dirs := roots(
			map[string]string{"app/x.log": nonce, "app/y.tmp": nonce},
			map[string]string{"app/.gitignore": "../rules", "app/.ignore": "../rules"},
			map[string]string{"rules": "*.log\n"},
			map[string]string{"rules": "*.tmp\n"},
		)
		for _, dir := range dirs {
			app := host(dir).Directory("app")
			got := entries(ctx, t, app.Filter(core.DirectoryFilterOpts{Gitignore: true}))
			require.Equal(t, []string{".gitignore", ".ignore", "x.log", "y.tmp"}, got, "filter")
			require.Equal(t, []string{"x.log", "y.tmp"}, searched(ctx, t, func() ([]core.SearchResult, error) {
				return app.Search(ctx, nonce, core.DirectorySearchOpts{Literal: true, SkipIgnored: true})
			}), "search")
		}
	})

	t.Run("other files", func(ctx context.Context, t *testctx.T) {
		nonce := rand.Text()
		dirs := roots(map[string]string{"app/main.txt": nonce}, nil,
			map[string]string{"other.txt": "a"},
			map[string]string{"other.txt": "b"},
		)
		require.Equal(t,
			uncachedViewToken(ctx, t, c, host(dirs[0]).Directory("app")),
			uncachedViewToken(ctx, t, c, host(dirs[1]).Directory("app")),
			"subdirectories with equal files must share work across parents that differ elsewhere")
	})

	t.Run("repositories", func(ctx context.Context, t *testctx.T) {
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
		nonce := rand.Text()

		// A repository above the subdirectory is outside it: Git doesn't
		// find it, and equal subdirectories under different repositories
		// share work.
		var apps []*core.Directory
		for _, msg := range []string{"a", "b"} {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{"app/main.txt": nonce, "other.txt": msg}, nil)
			gitRepo(dir, msg)
			app := host(dir).Directory("app")
			apps = append(apps, app)
			_, err := app.AsGit().Head().CommitSHA(ctx)
			require.Error(t, err, "Git must not find a repository above the subdirectory")
		}
		require.Equal(t, uncachedViewToken(ctx, t, c, apps[0]), uncachedViewToken(ctx, t, c, apps[1]),
			"equal subdirectories must share work whatever repository is above them")

		// A repository at the subdirectory's root is its own.
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"app/main.txt": nonce, "other.txt": "c"}, nil)
		head := gitRepo(filepath.Join(dir, "app"), "c")
		commit, err := host(dir).Directory("app").AsGit().Head().CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, head, commit)

		// Repository metadata that leads out of the subdirectory isn't
		// followed: a .git file or link pointing at a repository beside it
		// finds none.
		for name, links := range map[string]map[string]string{
			"gitdir file": nil,
			"linked .git": {"app/.git": "../meta"},
		} {
			dir := t.TempDir()
			files := map[string]string{"app/main.txt": nonce, "meta-src/f": name}
			if links == nil {
				files["app/.git"] = "gitdir: ../meta\n"
			}
			writeTree(t, dir, files, links)
			gitRepo(filepath.Join(dir, "meta-src"), name)
			require.NoError(t, os.Rename(filepath.Join(dir, "meta-src", ".git"), filepath.Join(dir, "meta")))
			_, err := host(dir).Directory("app").AsGit().Head().CommitSHA(ctx)
			require.Error(t, err, name)
		}

		// Nor is a config include leading out of it.
		dir = t.TempDir()
		writeTree(t, dir, map[string]string{
			"app/main.txt":   nonce,
			"outside.config": "[remote \"origin\"]\n\turl = https://example.com/outside\n",
		}, nil)
		gitRepo(filepath.Join(dir, "app"), "include")
		config, err := os.OpenFile(filepath.Join(dir, "app", ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		_, err = config.WriteString("[include]\n\tpath = ../../outside.config\n")
		require.NoError(t, err)
		require.NoError(t, config.Close())
		remotes, err := host(dir).Directory("app").AsGit().Remotes(ctx)
		if err == nil {
			require.Empty(t, remotes, "a config include leading out of the subdirectory must not add its remotes")
		}
	})

	t.Run("unreadable ignore file", func(ctx context.Context, t *testctx.T) {
		// A parent whose ignore file can't be read doesn't affect the
		// subdirectory.
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
		require.Equal(t, uncachedViewToken(ctx, t, c, appA), uncachedViewToken(ctx, t, c, appB))
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
