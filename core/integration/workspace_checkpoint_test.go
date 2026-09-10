package core

// Coverage for Workspace.sync: freezing a live client checkout into a
// portable, host-independent workspace.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// checkpointCheckoutBase clones a remote-backed repository into /work with the
// CLI installed, then adds unpushed local history and tracked dirt on top: the
// shape capture is for, where the remote supplies the base objects and only the
// local commits and worktree delta have to travel.
func checkpointCheckoutBase(ctx context.Context, t *testctx.T, c *dagger.Client) *dagger.Container {
	t.Helper()
	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("tracked.txt", "base\n"))
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "checkpoint@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Checkpoint"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithServiceBinding("checkpoint-git", gitDaemon).
		WithExec([]string{"git", "clone", repoURL, "/work"}).
		WithWorkdir("/work").
		WithNewFile("/work/local.txt", "local\n").
		WithExec([]string{"git", "add", "local.txt"}).
		WithExec([]string{"git", "commit", "-m", "local commit"}).
		WithNewFile("/work/tracked.txt", "base\ndirty\n")
}

// syncWorkspace captures once and returns the SDK object rooted at the resulting ID.
func syncWorkspace(ctx context.Context, t *testctx.T, ws *dagger.Workspace) *dagger.Workspace {
	t.Helper()
	frozen, err := ws.Sync(ctx)
	require.NoError(t, err)
	return frozen
}

// workspaceRecipeFields inspects the persisted composition for client-bound
// inputs that cannot be restored without the originating checkout.
func workspaceRecipeFields(ctx context.Context, t *testctx.T, c *dagger.Client, workspaceID string) []string {
	t.Helper()
	ws := dagger.Ref[*dagger.Workspace](c, dagger.ID(workspaceID))
	recipe, err := c.LLM().WithWorkspace(ws).PortableID(ctx)
	require.NoError(t, err)
	var id call.ID
	require.NoError(t, id.Decode(string(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	var fields []string
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		fields = append(fields, vertex.Field)
	}
	return fields
}

func (WorkspaceSuite) TestWorkspaceSyncFreezesLocalCheckout(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workdir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	git("init", "-b", "main")
	git("config", "user.name", "Sync")
	git("config", "user.email", "sync@example.com")
	filename := filepath.Join(workdir, "tracked.txt")
	require.NoError(t, os.WriteFile(filename, []byte("base"), 0o644))
	git("add", ".")
	git("commit", "-m", "base")
	require.NoError(t, os.WriteFile(filename, []byte("dirty"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	live := c.CurrentWorkspace()
	// Prime an unsynced read before capture. Later syncs must bypass this cache.
	contents, err := live.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "dirty", contents)
	frozen := syncWorkspace(ctx, t, live)
	frozenID, err := frozen.ID(ctx)
	require.NoError(t, err)
	require.Contains(t, workspaceRecipeFields(ctx, t, c, string(frozenID)), "__gitDir")
	modified, err := frozen.Git().Uncommitted().ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, modified)
	oldHead, err := frozen.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, []byte("later"), 0o644))
	git("add", ".")
	git("commit", "-m", "later")
	again := syncWorkspace(ctx, t, frozen)
	contents, err = again.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "dirty", contents)
	head, err := again.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, oldHead, head)
	fresh := syncWorkspace(ctx, t, live)
	contents, err = fresh.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "later", contents)
	head, err = fresh.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, oldHead, head)

	// Untracked files still require approval, without exposing their contents.
	loose := filepath.Join(workdir, "loose.txt")
	require.NoError(t, os.WriteFile(loose, []byte("untracked bytes"), 0o644))
	_, err = live.Sync(ctx)
	require.ErrorContains(t, err, "loose.txt")
	require.NotContains(t, err.Error(), "untracked bytes")
	// Stage it instead of using the removed per-call include override.
	git("add", "loose.txt")
	mounted := syncWorkspace(ctx, t, live.WithMountedDirectory("/deps", c.Directory().WithNewFile("readme.txt", "mounted")))
	contents, err = mounted.File("/deps/readme.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "mounted", contents)
	contents, err = mounted.File("/loose.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "untracked bytes", contents)

	// Filesystem remotes remain session-local.
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	git("clone", "--bare", workdir, remotePath)
	git("remote", "add", "origin", remotePath)
	localRemote := syncWorkspace(ctx, t, live)
	id, err := localRemote.ID(ctx)
	require.NoError(t, err)
	contents, err = localRemote.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "later", contents)
	require.Contains(t, workspaceRecipeFields(ctx, t, c, string(id)), "__gitDir")
}

func (WorkspaceSuite) TestWorkspaceSyncWithoutGitBaseline(ctx context.Context, t *testctx.T) {
	for _, name := range []string{"non-repository", "unborn-repository", "removed-repository"} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			workdir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), nil, 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "host.txt"), []byte("host"), 0o644))
			if name != "non-repository" {
				cmd := exec.CommandContext(ctx, "git", "init", "-b", "main")
				cmd.Dir = workdir
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", out)
			}
			c := connect(ctx, t, dagger.WithWorkdir(workdir))
			liveID, err := c.CurrentWorkspace().ID(ctx)
			require.NoError(t, err)
			if name == "removed-repository" {
				require.NoError(t, os.RemoveAll(filepath.Join(workdir, ".git")))
			}
			for _, overlay := range []bool{false, true} {
				ws := dagger.Ref[*dagger.Workspace](c, liveID).WithConfigEnvironment("dev").
					WithMountedDirectory("/deps", c.Directory().WithNewFile("dep.txt", "dependency"))
				if overlay {
					ws = ws.WithNewFile("overlay.txt", "overlay")
				}
				originalID, err := ws.ID(ctx)
				require.NoError(t, err)
				ws = dagger.Ref[*dagger.Workspace](c, originalID)
				synced := syncWorkspace(ctx, t, ws)
				id, err := synced.ID(ctx)
				require.NoError(t, err)
				require.Equal(t, originalID, id, "fallback must preserve the exact workspace")
				files := map[string]string{"/deps/dep.txt": "dependency"}
				if name != "non-repository" {
					files["host.txt"] = "host"
				}
				for path, want := range files {
					got, err := synced.File(path).Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, want, got)
				}
				if overlay {
					got, err := synced.File("overlay.txt").Contents(ctx)
					require.NoError(t, err)
					require.Equal(t, "overlay", got)
				}
				// Rootless workspaces intentionally have no host export target.
				if overlay && name != "non-repository" {
					require.NoError(t, synced.Export(ctx), "fallback retains direct overlay export")
					contents, err := os.ReadFile(filepath.Join(workdir, "overlay.txt"))
					require.NoError(t, err)
					require.Equal(t, "overlay", string(contents))
				}
			}
		})
	}
}

func (WorkspaceSuite) TestWorkspaceSyncPortableCapture(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c)

	initialRecipe, err := base.With(daggerShell(`llm | with-workspace --workspace $(node $(current-workspace | sync)) | portable-id`)).Stdout(ctx)
	require.NoError(t, err)
	initial := dagger.Ref[*dagger.LLM](c, dagger.ID(strings.TrimSpace(initialRecipe))).Workspace()
	initialHead, err := initial.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	initialContents, err := initial.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base\ndirty\n", initialContents)
	modified, err := initial.Git().Uncommitted().ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, modified)

	// The producing nested client exits before the recipe is loaded by the
	// outer client. No original /work checkout or host route is available.
	recipe, err := base.With(daggerShell(`llm | with-workspace --workspace $(node $(current-workspace | with-new-file overlay.txt "engine edit" | sync)) | portable-id`)).Stdout(ctx)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(strings.TrimSpace(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		require.NotEqual(t, "sync", vertex.Field)
		require.NotEqual(t, "currentWorkspace", vertex.Field)
		require.NotEqual(t, "__gitDir", vertex.Field)
	}
	// The prerequisite hint remains usable when the remote branch advances.
	_, err = base.WithNewFile("tracked.txt", "remote advanced\n").
		WithExec([]string{"git", "add", "tracked.txt"}).
		WithExec([]string{"git", "commit", "-m", "advance remote"}).
		WithExec([]string{"git", "push", "origin", "HEAD:main"}).Sync(ctx)
	require.NoError(t, err)
	restored := dagger.Ref[*dagger.LLM](c, dagger.ID(strings.TrimSpace(recipe))).Workspace()
	contents, err := restored.File("tracked.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "base\ndirty\n", contents)
	head, err := restored.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, initialHead, head)
	local, err := restored.File("local.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "local\n", local)
	overlay, err := restored.File("overlay.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "engine edit", overlay)
	log, err := restored.Git().Head().Log(ctx)
	require.NoError(t, err)
	for _, commit := range log {
		message, err := commit.Message(ctx)
		require.NoError(t, err)
		require.NotContains(t, message, "Dagger workspace snapshot")
	}
}

func (WorkspaceSuite) TestWorkspaceSyncLoadsFrozenModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	root := c.Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"modules/probe\"\n").
		WithNewFile("modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("modules/probe/main.go", `package main
type Probe struct{}
// +check
func (*Probe) Frozen() error { return nil }
`)

	ws := syncWorkspace(ctx, t, root.AsWorkspace())
	id, err := ws.ID(ctx)
	require.NoError(t, err)
	var got struct {
		Node struct {
			Checks struct{ List []struct{ Name string } }
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($id: ID!) { node(id: $id) { ... on Workspace {
   checks(noGenerate: true) { list { name } }
  } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &got}))
	require.Len(t, got.Node.Checks.List, 1)
	require.Equal(t, "probe:frozen", got.Node.Checks.List[0].Name)
}

func (WorkspaceSuite) TestWorkspaceSyncPinsGitOverlayRecipe(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "original"))
	branch := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main")
	// Prime the equivalent commit tree through a mutable ref before checkpoint
	// requests the SHA-pinned tree. Cache sharing must not unpin its recipe.
	_, err := branch.TargetCommit().Tree(dagger.GitCommitTreeOpts{DiscardGitDir: true}).Sync(ctx)
	require.NoError(t, err)
	source := branch.AsWorkspace().
		WithNewFile("base.txt", "overlay")
	frozenID, err := syncWorkspace(ctx, t, source).ID(ctx)
	require.NoError(t, err)
	frozen := dagger.Ref[*dagger.Workspace](c, frozenID)
	contents, err := frozen.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "overlay", contents)
	recipe, err := c.LLM().WithWorkspace(frozen).PortableID(ctx)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(string(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		require.NotEqual(t, "branch", vertex.Field, "both the base and the overlay must be pinned: %s", id.Display())
		require.NotEqual(t, "sync", vertex.Field)
	}
}

func (WorkspaceSuite) TestWorkspaceSyncPreservesDirectories(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	tree := c.Directory().WithNewFile("last/file", "remove me")
	daemon, url := gitService(ctx, t, c, tree)
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	dirs := c.Directory().WithNewDirectory("empty", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o700}).WithNewDirectory("gone")
	source := base.WithChanges(dirs.Changes(c.Directory())).
		WithChanges(tree.WithoutFile("last/file").WithNewDirectory("last").Changes(tree))
	frozen := syncWorkspace(ctx, t, source)
	for _, name := range []string{"empty", "gone", "last"} {
		entries, err := frozen.Directory(name).Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, entries)
	}
	stat, err := frozen.Directory("/").Stat(ctx, "empty")
	require.NoError(t, err)
	permissions, err := stat.Permissions(ctx)
	require.NoError(t, err)
	require.Equal(t, 0o700, permissions)
	// Directory-only removals and a directory-to-file replacement survive too.
	replacement := c.Directory().WithNewFile("empty", "replacement")
	updated := syncWorkspace(ctx, t, frozen.WithChanges(replacement.Changes(dirs)))
	exists, err := updated.Directory("/").Exists(ctx, "gone")
	require.NoError(t, err)
	require.False(t, exists)
	text, err := updated.File("empty").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "replacement", text)
	recipe, err := c.LLM().WithWorkspace(updated).PortableID(ctx)
	require.NoError(t, err)
	var id call.ID
	require.NoError(t, id.Decode(string(recipe)))
	require.NotContains(t, id.Display(), "branch(name:")
}

func (WorkspaceSuite) TestWorkspaceSyncRejectsNestedClientCapture(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c).
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"modules/probe\"\n").
		WithNewFile("modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("modules/probe/main.go", `package main
import (
 "context"
 "dagger/probe/internal/dagger"
)
type Probe struct{}
func (*Probe) Capture(ctx context.Context, source *dagger.Workspace) (string, error) {
 frozen, err := source.Sync(ctx)
 if err != nil { return "", err }
 return frozen.File("tracked.txt").Contents(ctx)
}
`)
	out, err := base.With(daggerExecFail("--silent", "-m", "modules/probe", "call", "capture")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "workspace sync capture is only available to the workspace's owning client")
}

func (WorkspaceSuite) TestWorkspaceSyncHostDirectoryIsSessionOnly(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syncWorkspace(ctx, t, c.Host().Directory(t.TempDir()).AsWorkspace())
	id, err := ws.ID(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Contains(t, workspaceRecipeFields(ctx, t, c, string(id)), "host")
}

func (WorkspaceSuite) TestWorkspaceSyncReplayableValuePassesThrough(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := c.Directory().AsWorkspace().WithNewFile("overlay.txt", "portable")
	original, err := ws.ID(ctx)
	require.NoError(t, err)
	frozen := syncWorkspace(ctx, t, ws)
	id, err := frozen.ID(ctx)
	require.NoError(t, err)
	require.Equal(t, original, id)
}

func (WorkspaceSuite) TestWorkspaceSyncPreservesRootlessEffectiveTree(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "host-only.txt"), []byte("must not be captured"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	for _, edited := range []bool{false, true} {
		ws := c.CurrentWorkspace()
		if edited {
			ws = ws.WithNewFile("overlay.txt", "in engine")
		}
		originalID, err := ws.ID(ctx)
		require.NoError(t, err)
		ws = dagger.Ref[*dagger.Workspace](c, originalID)
		originalCwd, err := ws.Cwd(ctx)
		require.NoError(t, err)
		frozen := syncWorkspace(ctx, t, ws)
		id, err := frozen.ID(ctx)
		require.NoError(t, err)
		require.Equal(t, originalID, id)
		cwd, err := frozen.Cwd(ctx)
		require.NoError(t, err)
		require.Equal(t, originalCwd, cwd)
		config, err := frozen.ConfigFile(ctx)
		require.NoError(t, err)
		require.Empty(t, config)
		replay := syncWorkspace(ctx, t, frozen)
		replayID, err := replay.ID(ctx)
		require.NoError(t, err)
		require.Equal(t, id, replayID)
		for _, value := range []*dagger.Workspace{frozen, replay} {
			entries, err := value.Directory("/").Entries(ctx)
			require.NoError(t, err)
			if edited {
				require.Equal(t, []string{"overlay.txt"}, entries)
				contents, err := value.File("overlay.txt").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "in engine", contents)
			} else {
				require.Empty(t, entries, "rootless sync must not capture its host path")
			}
		}
	}
}

// Ordinary edits can accumulate and export without capturing unrelated
// untracked files or requiring approval to freeze the checkout.
func (WorkspaceSuite) TestWorkspaceOverlayWithoutSync(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	loose := filepath.Join(workdir, "loose.txt")
	require.NoError(t, os.WriteFile(loose, []byte("local only"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	ws := c.CurrentWorkspace().WithNewFile("first.txt", "first").WithNewFile("second.txt", "second")
	require.NoError(t, ws.Export(ctx))
	for name, want := range map[string]string{"loose.txt": "local only", "first.txt": "first", "second.txt": "second"} {
		contents, err := os.ReadFile(filepath.Join(workdir, name))
		require.NoError(t, err)
		require.Equal(t, want, string(contents))
	}
}
