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

// TestWorkspaceWorktreeTreeNeutralized locks in that a linked worktree's
// dangling .git POINTER FILE is neutralized in the workspace *tree* while its
// git-ness is preserved. Workspace.directory(".") must NOT list the .git pointer
// (the engine drops the root .git regular file from the synced workspace rootfs),
// yet Workspace.git.head.commit must still resolve to the per-worktree HEAD,
// because git-ness is reconstructed canonically from a pack of the client's own
// checkout rather than read from the raw on-disk .git layout.
func (WorkspaceSuite) TestWorkspaceWorktreeTreeNeutralized(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithExec([]string{"git", "worktree", "add", "-b", "feature", "/linked"}).
		WithWorkdir("/linked").
		WithNewFile("/linked/feature.txt", "feature work").
		WithExec([]string{"git", "add", "feature.txt"}).
		WithExec([]string{"git", "commit", "-m", "feature commit"})

	wtHead, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	wtHead = strings.TrimSpace(wtHead)

	out, err := ctr.With(daggerQuery(`{
  currentWorkspace {
    directory(path: ".") { entries }
    git { head { commit } }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			Directory struct {
				Entries []string `json:"entries"`
			} `json:"directory"`
			Git struct {
				Head struct {
					Commit string `json:"commit"`
				} `json:"head"`
			} `json:"git"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	entries := got.CurrentWorkspace.Directory.Entries
	t.Logf("worktree workspace directory entries: %v", entries)
	// The worktree's tracked content is present in the workspace tree...
	require.Contains(t, entries, "tracked.txt")
	require.Contains(t, entries, "feature.txt")
	// ...but the dangling .git pointer file has been dropped.
	require.NotContains(t, entries, ".git")
	require.NotContains(t, entries, ".git/")
	// Git-ness is preserved: HEAD resolves to the per-worktree commit.
	require.Equal(t, wtHead, got.CurrentWorkspace.Git.Head.Commit)
}

// worktreeParityProbe is the decoded shape for the plain-clone vs worktree
// parity query: the workspace tree's own listing plus git-anchored HEAD and
// uncommitted state.
type worktreeParityProbe struct {
	CurrentWorkspace struct {
		Directory struct {
			Entries []string `json:"entries"`
		} `json:"directory"`
		Git struct {
			Head struct {
				Commit string `json:"commit"`
			} `json:"head"`
			Uncommitted uncommittedChanges `json:"uncommitted"`
		} `json:"git"`
	} `json:"currentWorkspace"`
}

// TestWorkspaceWorktreePlainParity locks in that a linked worktree checkout and
// a plain clone at the SAME commit report identical Workspace.git state — the
// worktree's neutralized .git pointer must not perturb head/uncommitted
// reporting, including the uncommitted diffStats. It also pins the tree-level
// asymmetry between the two checkout shapes (worktree .git pointer dropped;
// plain clone .git directory left as-is).
func (WorkspaceSuite) TestWorkspaceWorktreePlainParity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithNewFile("keep.txt", "k1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		// A linked worktree at the current HEAD (explicit branch name).
		WithExec([]string{"git", "worktree", "add", "-b", "wt", "/worktree"}).
		// A plain clone at the same HEAD.
		WithExec([]string{"git", "clone", "/work", "/clone"})

	head, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	head = strings.TrimSpace(head)

	query := `{
  currentWorkspace {
    directory(path: ".") { entries }
    git {
      head { commit }
      uncommitted {
        isEmpty
        diffStats { path kind addedLines removedLines }
      }
    }
  }
}`

	// Apply IDENTICAL uncommitted edits in a checkout, then read its workspace.
	probe := func(dir string) worktreeParityProbe {
		out, err := base.
			WithNewFile(dir+"/tracked.txt", "v2"). // modify a tracked file
			WithNewFile(dir+"/added.txt", "new").  // add an untracked file
			WithWorkdir(dir).
			With(daggerQuery(query)).
			Stdout(ctx)
		require.NoError(t, err)
		var p worktreeParityProbe
		require.NoError(t, json.Unmarshal([]byte(out), &p))
		return p
	}

	worktree := probe("/worktree")
	clone := probe("/clone")

	t.Logf("worktree directory entries: %v", worktree.CurrentWorkspace.Directory.Entries)
	t.Logf("plain clone directory entries: %v", clone.CurrentWorkspace.Directory.Entries)

	// Same commit for both checkout shapes.
	require.Equal(t, head, worktree.CurrentWorkspace.Git.Head.Commit)
	require.Equal(t, head, clone.CurrentWorkspace.Git.Head.Commit)

	// Identical uncommitted changeset: the worktree's neutralized .git pointer
	// does not perturb git-anchored reporting relative to a plain clone.
	require.Equal(t,
		clone.CurrentWorkspace.Git.Uncommitted.IsEmpty,
		worktree.CurrentWorkspace.Git.Uncommitted.IsEmpty)
	require.ElementsMatch(t,
		clone.CurrentWorkspace.Git.Uncommitted.DiffStats,
		worktree.CurrentWorkspace.Git.Uncommitted.DiffStats)

	// And the changeset is what we actually edited (not vacuously empty).
	require.False(t, worktree.CurrentWorkspace.Git.Uncommitted.IsEmpty)
	_, ok := worktree.CurrentWorkspace.Git.Uncommitted.find("tracked.txt")
	require.True(t, ok, "expected tracked.txt in worktree diffStats")
	_, ok = worktree.CurrentWorkspace.Git.Uncommitted.find("added.txt")
	require.True(t, ok, "expected added.txt in worktree diffStats")

	// Tree-level asymmetry: the worktree's .git pointer FILE is dropped from
	// the workspace tree, while a plain clone's .git DIRECTORY is left as-is.
	hasGit := func(entries []string) bool {
		for _, e := range entries {
			if e == ".git" || e == ".git/" {
				return true
			}
		}
		return false
	}
	require.False(t, hasGit(worktree.CurrentWorkspace.Directory.Entries),
		"worktree .git pointer file should be dropped from the workspace tree")
	require.True(t, hasGit(clone.CurrentWorkspace.Directory.Entries),
		"plain clone .git directory should still be listed in the workspace tree")
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

// workspaceDiffStat is the shape of one Changeset.diffStats entry.
type workspaceDiffStat struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	AddedLines   int    `json:"addedLines"`
	RemovedLines int    `json:"removedLines"`
}

// uncommittedChanges is the shape of a Workspace.git.uncommitted changeset,
// read as a diff rather than as path lists.
type uncommittedChanges struct {
	IsEmpty   bool                `json:"isEmpty"`
	DiffStats []workspaceDiffStat `json:"diffStats"`
}

// find returns the diff stat for path, if any.
func (u uncommittedChanges) find(path string) (workspaceDiffStat, bool) { //nolint:unparam // shared helper; the stat is used by other suites
	for _, stat := range u.DiffStats {
		if stat.Path == path {
			return stat, true
		}
	}
	return workspaceDiffStat{}, false
}

// paths returns the diff stat paths, dropping bare-directory entries. The
// changeset sometimes carries a synthetic entry for a directory that only
// exists inside a staged commit (a separate defect); these tests are about the
// files, so directory entries are filtered out rather than asserted on.
func (u uncommittedChanges) paths() []string {
	var out []string
	for _, stat := range u.DiffStats {
		if strings.HasSuffix(stat.Path, "/") {
			continue
		}
		out = append(out, stat.Path)
	}
	return out
}
