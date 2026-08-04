package core

// This file contains shared workspace fixtures, host-side helpers, and
// container setup for workspace-focused tests. It should not own behavior
// coverage directly.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// gitRepoBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work
func gitRepoBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

// workspaceBase returns a git-backed /work with the CLI installed, but no native
// dagger.toml. A git root enables workspace/lockfile detection; a
// native config opts into native workspace behavior and suppresses legacy
// dagger.json compat inference, so tests should add it explicitly when needed.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return gitRepoBase(t, c)
}

// nativeWorkspaceBase adds the native workspace state created by
// `dagger workspace init`: a dagger.toml inside the git root.
func nativeWorkspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	// The `dagger workspace init` verb was removed in CLI 1.0 (workspace
	// creation is implicit on first install). Seed the workspace config
	// directly so this helper still yields a native workspace with config
	// present, matching what `workspace init` used to write.
	return workspaceBase(t, c).WithNewFile("dagger.toml", "[modules]\n")
}

func workspaceFixture(t testing.TB, c *dagger.Client, fixture string) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).With(withWorkspaceFixture(t, c, ".", "workspaces/"+fixture))
}

// legacyWorkspaceBase creates a native git repo rooted at /work but seeds it
// with a legacy dagger.json project shape. Compat detection and migration tests
// use this to separate "legacy on disk" from "workspace at runtime".
func legacyWorkspaceBase(t testing.TB, c *dagger.Client, config string, ops ...dagger.WithContainerFunc) *dagger.Container {
	t.Helper()

	ctr := workspaceBase(t, c).
		WithNewFile("dagger.json", config)
	for _, op := range ops {
		ctr = ctr.With(op)
	}

	return ctr.
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

// TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules locks in
// the user-visible behavior behind the SingleQuery optimization. A single raw
// GraphQL query that names one workspace module should only load that module;
// unrelated workspace modules must not be loaded eagerly just because they are
// present in the workspace config.
func (WorkspaceSuite) TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "single-query-broken")

	t.Run("query naming only the healthy module succeeds", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{ good { ping } }`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"good":{"ping":"healthy module loaded"}}`, out)
	})

	t.Run("full schema query still loads every workspace module", func(ctx context.Context, t *testctx.T) {
		fullSchema := base.WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{
			Stdin:                         `{ __schema { queryType { name } } }`,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})

		errOut, err := fullSchema.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, errOut, "bad")
	})
}

// TestWorkspaceGit exercises the happy path for the Workspace.git API from a
// real Dagger query. It checks that the reported HEAD commit matches the local
// repository, that a clean repository reports an empty uncommitted changeset,
// and that dirty state is detected for the whole repository even when the
// current workspace is a nested directory.
func (WorkspaceSuite) TestWorkspaceGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithNewFile("toolchains/gitinfo/.keep", "").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithNewFile("tracked.txt", "v2").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "head"})

	headCommit, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	headCommit = strings.TrimSpace(headCommit)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    git {
      head { commit }
      uncommitted { isEmpty }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			Git struct {
				Head struct {
					Commit string `json:"commit"`
				} `json:"head"`
				Uncommitted struct {
					IsEmpty bool `json:"isEmpty"`
				} `json:"uncommitted"`
			} `json:"git"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, headCommit, got.CurrentWorkspace.Git.Head.Commit)
	require.True(t, got.CurrentWorkspace.Git.Uncommitted.IsEmpty)

	out, err = base.
		WithNewFile("dirty.txt", "dirty").
		With(daggerQuery(`{currentWorkspace{git{uncommitted{isEmpty}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"git":{"uncommitted":{"isEmpty":false}}}}`, out)

	out, err = base.
		WithWorkdir("/work/toolchains/gitinfo").
		WithNewFile("/work/root-dirty.txt", "dirty").
		With(daggerQuery(`{currentWorkspace{cwd git{uncommitted{isEmpty}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"cwd":"/toolchains/gitinfo","git":{"uncommitted":{"isEmpty":false}}}}`, out)
}

// TestWorkspaceGitWorktree verifies Workspace.git works from a linked git
// worktree checkout, whose .git is a pointer file into the main checkout's
// .git/worktrees/<name>. The engine never interprets that raw layout: the
// client's own git packs the checkout's repository and the engine reconstructs
// a standalone .git from the pack, so Workspace.git reports the per-worktree
// HEAD and detects uncommitted changes just like a plain clone.
func (WorkspaceSuite) TestWorkspaceGitWorktree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithExec([]string{"git", "worktree", "add", "-b", "feature", "/linked"}).
		WithWorkdir("/linked").
		// Diverge from the main checkout to prove the per-worktree HEAD is
		// honored, not the main checkout's.
		WithNewFile("/linked/feature.txt", "feature work").
		WithExec([]string{"git", "add", "feature.txt"}).
		WithExec([]string{"git", "commit", "-m", "feature commit"})

	wtHead, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	wtHead = strings.TrimSpace(wtHead)
	mainHead, err := ctr.WithExec([]string{"git", "-C", "/work", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	require.NotEqual(t, wtHead, strings.TrimSpace(mainHead))

	out, err := ctr.With(daggerQuery(`{
  currentWorkspace {
    git {
      head { commit }
      uncommitted { isEmpty }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			Git struct {
				Head struct {
					Commit string `json:"commit"`
				} `json:"head"`
				Uncommitted struct {
					IsEmpty bool `json:"isEmpty"`
				} `json:"uncommitted"`
			} `json:"git"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, wtHead, got.CurrentWorkspace.Git.Head.Commit)
	require.True(t, got.CurrentWorkspace.Git.Uncommitted.IsEmpty)

	out, err = ctr.
		WithNewFile("/linked/dirty.txt", "dirty").
		With(daggerQuery(`{currentWorkspace{git{uncommitted{isEmpty}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"git":{"uncommitted":{"isEmpty":false}}}}`, out)
}

// TestEntrypointWithFieldHidden verifies that the synthetic `with` field
// installed on Query for entrypoint constructors with arguments is hidden
// from user-facing CLI listings (`dagger functions`, `dagger call --help`)
// while remaining callable and introspectable.
func (WorkspaceSuite) TestEntrypointWithFieldHidden(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "workspace-entrypoint")

	t.Run("dagger functions omits with", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		// The blueprint's real functions should appear.
		require.Contains(t, out, "greet")
		// The synthetic `with` field must not leak into user listings.
		require.NotRegexp(t, `(?m)^with\b`, out)
	})

	t.Run("dagger call routes constructor args through with", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=world", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world!", strings.TrimSpace(out))
	})

	t.Run("with remains in graphql introspection", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{ __type(name: "Query") { fields { name } } }`)).Stdout(ctx)
		require.NoError(t, err)
		// `with` is callable via raw GraphQL; only user-facing CLI
		// listings filter it.
		require.Contains(t, out, `"name": "with"`)
	})
}

// workspaceRemovalProbe is the decoded shape of the removal-pruning probe
// query: the workspace tree's own view (glob) plus git-anchored status.
type workspaceRemovalProbe struct {
	Glob []string `json:"glob"`
	Git  struct {
		Uncommitted struct {
			IsEmpty   bool `json:"isEmpty"`
			DiffStats []struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			} `json:"diffStats"`
		} `json:"uncommitted"`
	} `json:"git"`
}

// decodeWorkspaceRemovalProbe pulls the probe payload out of a
// `currentWorkspace { ...edits... { glob git{...} } }` response, following the
// given chain of edit field names.
func decodeWorkspaceRemovalProbe(t *testctx.T, out string, chain ...string) workspaceRemovalProbe {
	t.Helper()
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	cur := raw["currentWorkspace"].(map[string]any)
	for _, field := range chain {
		cur = cur[field].(map[string]any)
	}
	leaf, err := json.Marshal(cur)
	require.NoError(t, err)
	var probe workspaceRemovalProbe
	require.NoError(t, json.Unmarshal(leaf, &probe))
	return probe
}

// globHas reports whether a Workspace.glob result contains the given path,
// ignoring the trailing slash directories are reported with.
func globHas(matches []string, want string) bool {
	want = strings.TrimSuffix(want, "/")
	for _, m := range matches {
		if strings.TrimSuffix(m, "/") == want {
			return true
		}
	}
	return false
}

// diffStatHas reports whether a diffStats result mentions the given path,
// ignoring the trailing slash directory entries are reported with.
func diffStatHas(probe workspaceRemovalProbe, want string) bool {
	want = strings.TrimSuffix(want, "/")
	for _, s := range probe.Git.Uncommitted.DiffStats {
		if strings.TrimSuffix(s.Path, "/") == want {
			return true
		}
	}
	return false
}

// TestWorkspaceRemovalPrunesEmptiedDirectories locks in `git rm`-like workspace
// removal semantics: removing the last entry in a directory must take the
// now-empty directory with it, walking up as far as the removal empties (but
// never past the workspace root).
//
// Git cannot represent an empty directory, so a leftover one shows up in
// git-anchored status as a phantom `ADDED qa/ +0 -0` entry — and can even make
// Changeset.isEmpty (true) disagree with Changeset.diffStats (one bare dir
// entry). Directories that are *not* emptied by the removal must be left alone.
func (WorkspaceSuite) TestWorkspaceRemovalPrunesEmptiedDirectories(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})

	t.Run("removing the last file prunes the emptied directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "qa/probe.txt", contents: "probe\n") {
      withoutFile(path: "qa/probe.txt") {
        glob(pattern: "qa*")
        git { uncommitted { isEmpty diffStats { path kind } } }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)
		probe := decodeWorkspaceRemovalProbe(t, out, "withNewFile", "withoutFile")

		require.False(t, globHas(probe.Glob, "qa"), "empty %q left behind in workspace tree: %v", "qa/", probe.Glob)
		require.False(t, diffStatHas(probe, "qa"), "phantom %q entry in git status: %v", "qa/", probe.Git.Uncommitted.DiffStats)
		// isEmpty and diffStats must agree: nothing changed at all.
		require.True(t, probe.Git.Uncommitted.IsEmpty)
		require.Empty(t, probe.Git.Uncommitted.DiffStats)
	})

	t.Run("removing one of two files prunes nothing", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "qa/keep.txt", contents: "keep\n") {
      withNewFile(path: "qa/probe.txt", contents: "probe\n") {
        withoutFile(path: "qa/probe.txt") {
          glob(pattern: "qa/*")
          git { uncommitted { isEmpty diffStats { path kind } } }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)
		probe := decodeWorkspaceRemovalProbe(t, out, "withNewFile", "withNewFile", "withoutFile")

		require.True(t, globHas(probe.Glob, "qa/keep.txt"), "surviving sibling was pruned: %v", probe.Glob)
		require.False(t, globHas(probe.Glob, "qa/probe.txt"), "removed file still present: %v", probe.Glob)
		require.False(t, probe.Git.Uncommitted.IsEmpty)
		require.True(t, diffStatHas(probe, "qa/keep.txt"))
		require.False(t, diffStatHas(probe, "qa"), "non-emptied directory reported: %v", probe.Git.Uncommitted.DiffStats)
	})

	t.Run("pruning stops at the first ancestor that still has entries", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "a/keep.txt", contents: "keep\n") {
      withNewFile(path: "a/b/c/probe.txt", contents: "probe\n") {
        withoutFile(path: "a/b/c/probe.txt") {
          glob(pattern: "a/**")
          git { uncommitted { isEmpty diffStats { path kind } } }
        }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)
		probe := decodeWorkspaceRemovalProbe(t, out, "withNewFile", "withNewFile", "withoutFile")

		// a/b and a/b/c were emptied by the removal and go away; a still has
		// keep.txt, so it survives.
		require.False(t, globHas(probe.Glob, "a/b"), "emptied ancestor left behind: %v", probe.Glob)
		require.False(t, globHas(probe.Glob, "a/b/c"), "emptied ancestor left behind: %v", probe.Glob)
		require.True(t, globHas(probe.Glob, "a/keep.txt"), "non-emptied ancestor was pruned: %v", probe.Glob)
		require.False(t, diffStatHas(probe, "a/b"), "phantom dir entry in git status: %v", probe.Git.Uncommitted.DiffStats)
		require.True(t, diffStatHas(probe, "a/keep.txt"))
	})

	t.Run("a directory that was already empty is left alone", func(ctx context.Context, t *testctx.T) {
		// Workspace.withNewDirectory requires a source Directory ID, which a
		// single raw GraphQL document cannot produce, so the pre-existing empty
		// directory is created on the host instead. Either way it is not the
		// product of a removal, so pruning must not touch it.
		out, err := base.
			WithExec([]string{"mkdir", "-p", "/work/already-empty"}).
			With(daggerQuery(`{
  currentWorkspace {
    withNewFile(path: "qa/probe.txt", contents: "probe\n") {
      withoutFile(path: "qa/probe.txt") {
        glob(pattern: "already-empty*")
        git { uncommitted { isEmpty diffStats { path kind } } }
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)
		probe := decodeWorkspaceRemovalProbe(t, out, "withNewFile", "withoutFile")

		require.True(t, globHas(probe.Glob, "already-empty"), "pre-existing empty directory was pruned: %v", probe.Glob)
	})
}
