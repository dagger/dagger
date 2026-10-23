package core

import (
	"context"
	"crypto/rand"

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

			// This change leaves a subpath read without a filter as it was: a
			// view into the root, through which linked/link still reaches the
			// root's file. linked/marker.txt makes each root's linked differ,
			// so the check doesn't depend on how equal views are keyed.
			t.Run("link out of subpath", func(ctx context.Context, t *testctx.T) {
				for _, tc := range []struct {
					ws   *core.Workspace
					want string
				}{{baseWS, "a"}, {sameWS, "b"}} {
					contents, err := tc.ws.Directory("/linked").File("link").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, tc.want, contents)
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
