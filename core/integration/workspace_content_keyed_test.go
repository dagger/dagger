package core

import (
	"context"
	"crypto/rand"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestFilteredReadsAreContentKeyed asserts that a filtered read from a
// source-backed workspace is identified by the files it selects, not by the
// workspace root it came from. Two roots that differ only outside the
// selection must give reads that downstream work treats as the same input, the
// way host-backed reads already are; a change inside the selection must not.
// This is what lets CI reuse work for unchanged parts of a repository from one
// commit to the next.
func (WorkspaceSuite) TestFilteredReadsAreContentKeyed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	reads := []struct {
		name string
		read func(*core.Workspace) *core.Directory
	}{
		{"include", func(ws *core.Workspace) *core.Directory {
			return ws.Directory("/", core.WorkspaceDirectoryOpts{Include: []string{"app/**"}})
		}},
		{"subpath include", func(ws *core.Workspace) *core.Directory {
			return ws.Directory("/app", core.WorkspaceDirectoryOpts{Include: []string{"**/*.txt"}})
		}},
		{"exclude", func(ws *core.Workspace) *core.Directory {
			return ws.Directory("/", core.WorkspaceDirectoryOpts{Exclude: []string{"other.txt", "linked/marker.txt", ".git"}})
		}},
	}

	backends := []struct {
		name string
		load func(*core.Directory) *core.Workspace
		// lazySubpath is whether a subpath read is left unevaluated until
		// used. A Git root carries a content digest, so a subpath read of it
		// is evaluated, and fails if the subpath is missing, when it is made.
		lazySubpath bool
	}{
		{"directory", func(src *core.Directory) *core.Workspace {
			return src.AsWorkspace()
		}, true},
		{"git", func(src *core.Directory) *core.Workspace {
			gitDaemon, repoURL := gitService(ctx, t, c, src)
			return core.NewQuery(c).Git(repoURL, core.GitOpts{ExperimentalServiceHost: gitDaemon}).Head().AsWorkspace()
		}, false},
	}

	for _, backend := range backends {
		t.Run(backend.name, func(ctx context.Context, t *testctx.T) {
			// Unique per run and backend, so neither earlier runs nor the other
			// backend's reads can satisfy the test.
			nonce := rand.Text()
			source := func(app, other string) *core.Directory {
				return core.NewQuery(c).Directory().
					WithNewFile("app/main.txt", nonce+app).
					WithNewFile("app/nested/leaf.txt", "leaf").
					WithSymlink("../main.txt", "app/nested/inner").
					WithSymlink("../other.txt", "linked/link").
					WithNewFile("linked/marker.txt", nonce+other).
					WithNewFile("other.txt", other)
			}
			baseWS := backend.load(source("", "a"))
			sameWS := backend.load(source("", "b"))
			changedWS := backend.load(source("changed", "a"))

			for _, read := range reads {
				t.Run(read.name, func(ctx context.Context, t *testctx.T) {
					first := uncachedDirectoryToken(ctx, t, c, read.read(baseWS))
					require.Equal(t, first, uncachedDirectoryToken(ctx, t, c, read.read(sameWS)),
						"a read whose selected files are unchanged must reuse work done on it")
					require.NotEqual(t, first, uncachedDirectoryToken(ctx, t, c, read.read(changedWS)),
						"a read whose selected files changed must not reuse work done on it")
				})
			}

			// A subpath read is its own root, so linked/link, which climbs
			// above it, doesn't reach the root's file.
			t.Run("link out of subpath", func(ctx context.Context, t *testctx.T) {
				for _, ws := range []*core.Workspace{baseWS, sameWS} {
					contents, err := ws.Directory("/linked").File("link").Contents(ctx)
					require.Error(t, err, "a link out of a subpath read must not reach the root's file, got %q", contents)
				}
			})

			// A filtered read of a subpath the root doesn't have fails only
			// where it is used, so a caller can still handle it: exists reports
			// that the path is missing.
			t.Run("missing subpath", func(ctx context.Context, t *testctx.T) {
				if !backend.lazySubpath {
					t.Skip("this root evaluates subpath reads when they are made")
				}
				read := baseWS.Directory("/missing", core.WorkspaceDirectoryOpts{Include: []string{"**/*.txt"}})
				exists, err := read.Exists(ctx, "main.txt")
				require.NoError(t, err)
				require.False(t, exists)
			})

			t.Run("subpath name", func(ctx context.Context, t *testctx.T) {
				name, err := baseWS.Directory("/app").Name(ctx)
				require.NoError(t, err)
				require.Equal(t, "app/", name)
			})
		})
	}
}

// uncachedDirectoryToken mounts dir into an exec whose output is random, so two
// calls return the same token only when the second was a cache hit.
func uncachedDirectoryToken(ctx context.Context, t *testctx.T, c *dagger.Client, dir *core.Directory) string {
	t.Helper()
	out, err := core.NewQuery(c).Container().
		From(alpineImage).
		WithMountedDirectory("/src", dir).
		WithExec([]string{"sh", "-c", "ls -R /src >/dev/null && cat /proc/sys/kernel/random/uuid"}).
		Stdout(ctx)
	require.NoError(t, err)
	return out
}

// TestGitWorkspaceModulesAreContentKeyed asserts that a module loaded from a
// Git workspace, and a dependency it loads by relative path, are identified by
// their own files rather than by the commit they came from. A commit that
// changes nothing in a module must reuse the work done on it, as it does for a
// local checkout; a commit that changes the module's files must not, even a
// committed file an ignore rule matches. Otherwise every new commit of a
// repository would rebuild every module in it.
func (WorkspaceSuite) TestGitWorkspaceModulesAreContentKeyed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Unique per run, so earlier runs' work can't satisfy the test.
	nonce := rand.Text()
	tokenFn := func(typeName string) string {
		return `
# ` + nonce + `
type ` + typeName + ` {
  pub token: String! @cache(policy: FunctionCachePolicy.Never) {
    Dagger.container.from("` + alpineImage + `")
      .withMountedDirectory("/src", currentModule.source)
      .withExec(["sh", "-c", "ls -R /src >/dev/null && cat /proc/sys/kernel/random/uuid"])
      .stdout
  }
`
	}
	repo := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", `[modules.probe]
source = ".dagger/modules/probe"
`).
		WithNewFile("README.md", "first").
		WithNewFile(".dagger/modules/probe/dagger.json", `{"name":"probe","sdk":{"source":"dang"},"dependencies":[{"name":"dep","source":"../dep"}]}`).
		WithNewFile(".dagger/modules/probe/main.dang", tokenFn("Probe")+`
  pub depToken: String! @cache(policy: FunctionCachePolicy.Never) {
    dep.token
  }
}
`).
		WithNewFile(".dagger/modules/dep/dagger.json", `{"name":"dep","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/dep/main.dang", tokenFn("Dep")+"}\n").
		// A Git tree holds committed files even where an ignore rule matches
		// them, so the dependency's change below is to such a file.
		WithNewFile(".dagger/modules/dep/.gitignore", "extra.txt\n")

	// Three commits: the base, one outside both modules, one in the dependency.
	history := core.NewQuery(c).Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/repo", repo).
		WithWorkdir("/repo").
		WithExec([]string{"sh", "-c", `set -e
git config --global user.email "root@localhost"
git config --global user.name "Test User"
git init -q -b main
git add -A && git commit -qm base && git tag base
echo second > README.md
git commit -qam outside && git tag outside
echo changed > .dagger/modules/dep/extra.txt
git add -f .dagger/modules/dep/extra.txt && git commit -qm dependency && git tag dependency
`}).
		Directory("/repo")
	remote := strings.TrimSuffix(workspaceSelectionRemoteRef(ctx, t, c, history), "@main")

	tokens := func(tag string) (probe, dep string) {
		t.Helper()
		call := func(fn string) string {
			out, err := core.NewQuery(c).Container().From(alpineImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/empty").
				With(workspaceSelectionDaggerCall("-W", remote+"@"+tag, "probe", fn)).
				Stdout(ctx)
			require.NoError(t, err)
			return strings.TrimSpace(out)
		}
		return call("token"), call("dep-token")
	}

	baseProbe, baseDep := tokens("base")
	outsideProbe, outsideDep := tokens("outside")
	require.Equal(t, baseProbe, outsideProbe, "a commit outside the module must reuse work done on it")
	require.Equal(t, baseDep, outsideDep, "a commit outside the dependency must reuse work done on it")

	changedProbe, changedDep := tokens("dependency")
	require.Equal(t, baseProbe, changedProbe, "a commit in another module must reuse work done on this one")
	require.NotEqual(t, baseDep, changedDep, "a commit in the dependency must not reuse work done on it")
}
