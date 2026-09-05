package core

// Coverage for Workspace.checkpoint: freezing a live client checkout into a
// portable, host-independent workspace.

import (
	"context"
	"encoding/json"
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

type checkpointState struct {
	ID       string
	Portable bool
	File     struct{ Contents string }
	Git      struct {
		Head        struct{ Commit string }
		Uncommitted struct{ ModifiedPaths []string }
	}
}

func readCheckpoint(ctx context.Context, t *testctx.T, c *dagger.Client, query string, variables map[string]any) checkpointState {
	t.Helper()
	var got struct{ State checkpointState }
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: query, Variables: variables}, &dagger.Response{Data: &got}))
	return got.State
}

func (WorkspaceSuite) TestWorkspaceCheckpointFreezesLocalCheckout(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workdir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	git("init", "-b", "main")
	git("config", "user.name", "Checkpoint")
	git("config", "user.email", "checkpoint@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("base"), 0o644))
	git("add", ".")
	git("commit", "-m", "base")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("dirty"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	var captured struct {
		CurrentWorkspace struct{ Checkpoint checkpointState }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `{
		currentWorkspace { checkpoint {
			id portable file(path: "tracked.txt") { contents }
			git { head { commit } uncommitted { modifiedPaths } }
		} }
	}`}, &dagger.Response{Data: &captured}))
	frozen := captured.CurrentWorkspace.Checkpoint
	require.NotEmpty(t, frozen.ID)
	require.False(t, frozen.Portable)
	require.Equal(t, "dirty", frozen.File.Contents)
	require.Equal(t, []string{"tracked.txt"}, frozen.Git.Uncommitted.ModifiedPaths)

	require.NoError(t, os.WriteFile(filepath.Join(workdir, "tracked.txt"), []byte("later"), 0o644))
	git("add", ".")
	git("commit", "-m", "later")
	again := readCheckpoint(ctx, t, c, `query($id: ID!) {
		state: node(id: $id) { ... on Workspace {
			id portable file(path: "tracked.txt") { contents } git { head { commit } }
		} }
	}`, map[string]any{"id": frozen.ID})
	require.Equal(t, frozen.File.Contents, again.File.Contents)
	require.Equal(t, frozen.Git.Head.Commit, again.Git.Head.Commit)
	require.False(t, again.Portable)

	// Every capture reads fresh host state, including its approval candidates.
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "loose.txt"), []byte("untracked bytes"), 0o644))
	var rejected any
	err := c.Do(ctx, &dagger.Request{Query: `{
		currentWorkspace { checkpoint { id } }
	}`}, &dagger.Response{Data: &rejected})
	require.Error(t, err)
	require.Contains(t, err.Error(), "loose.txt")
	require.NotContains(t, err.Error(), "untracked bytes")

	mount := c.Directory().WithNewFile("readme.txt", "mounted")
	mountID, err := mount.ID(ctx)
	require.NoError(t, err)
	var mounted struct {
		CurrentWorkspace struct {
			WithMountedDirectory struct {
				Checkpoint struct {
					Portable  bool
					File      struct{ Contents string }
					Untracked struct{ Contents string }
				}
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($mount: ID!) {
			currentWorkspace { withMountedDirectory(path: "/deps", source: $mount) {
				checkpoint(include: ["loose.txt"]) {
					portable
					file(path: "/deps/readme.txt") { contents }
					untracked: file(path: "/loose.txt") { contents }
				}
			} }
		}`,
		Variables: map[string]any{"mount": mountID},
	}, &dagger.Response{Data: &mounted}))
	require.False(t, mounted.CurrentWorkspace.WithMountedDirectory.Checkpoint.Portable)
	require.Equal(t, "mounted", mounted.CurrentWorkspace.WithMountedDirectory.Checkpoint.File.Contents)
	require.Equal(t, "untracked bytes", mounted.CurrentWorkspace.WithMountedDirectory.Checkpoint.Untracked.Contents)

	// A filesystem remote can serve Git to this client, but cannot be used
	// by an engine restoring the recipe in another environment.
	remotePath := filepath.Join(t.TempDir(), "origin.git")
	git("clone", "--bare", workdir, remotePath)
	git("remote", "add", "origin", remotePath)
	var localRemote struct {
		CurrentWorkspace struct{ Checkpoint checkpointState }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `{
		currentWorkspace { checkpoint(include: ["loose.txt"]) {
			id portable file(path: "tracked.txt") { contents }
		} }
	}`}, &dagger.Response{Data: &localRemote}))
	require.False(t, localRemote.CurrentWorkspace.Checkpoint.Portable)
	require.Equal(t, "later", localRemote.CurrentWorkspace.Checkpoint.File.Contents)
}

func (WorkspaceSuite) TestWorkspaceCheckpointPortableCapture(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := checkpointCheckoutBase(ctx, t, c)
	out, err := base.With(daggerQuery(`{
		currentWorkspace { checkpoint {
			id portable file(path: "tracked.txt") { contents }
			git { head { commit } uncommitted { modifiedPaths } }
		} }
	}`)).Stdout(ctx)
	require.NoError(t, err)
	var got struct {
		CurrentWorkspace struct{ Checkpoint checkpointState }
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	frozen := got.CurrentWorkspace.Checkpoint
	require.NotEmpty(t, frozen.ID)
	require.True(t, frozen.Portable)
	require.Equal(t, "base\ndirty\n", frozen.File.Contents)
	require.Equal(t, []string{"tracked.txt"}, frozen.Git.Uncommitted.ModifiedPaths)

	// The producing nested client exits before the recipe is loaded by the
	// outer client. No original /work checkout or host route is available.
	recipe, err := base.With(daggerShell(`llm | with-workspace --workspace $(current-workspace | with-new-file overlay.txt "engine edit" | checkpoint) | portable-id`)).Stdout(ctx)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(strings.TrimSpace(recipe)))
	dag, err := id.ToProto()
	require.NoError(t, err)
	for _, vertex := range dag.GetRecipe().CallsByDigest {
		require.NotEqual(t, "checkpoint", vertex.Field)
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
	require.Equal(t, frozen.Git.Head.Commit, head)
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

func (WorkspaceSuite) TestWorkspaceCheckpointLoadsFrozenModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	root := c.Directory().
		WithNewFile("dagger.toml", "[modules.probe]\nsource = \"modules/probe\"\n").
		WithNewFile("modules/probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0","sdk":"go"}`).
		WithNewFile("modules/probe/main.go", `package main
type Probe struct{}
// +check
func (*Probe) Frozen() error { return nil }
`)
	ws := root.AsWorkspace()
	id, err := ws.ID(ctx)
	require.NoError(t, err)
	var got struct {
		Node struct {
			Checkpoint struct {
				Portable bool
				Checks   struct{ List []struct{ Name string } }
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($id: ID!) { node(id: $id) { ... on Workspace {
			checkpoint { portable checks(noGenerate: true) { list { name } } }
		} } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &got}))
	require.True(t, got.Node.Checkpoint.Portable)
	require.Len(t, got.Node.Checkpoint.Checks.List, 1)
	require.Equal(t, "probe:frozen", got.Node.Checkpoint.Checks.List[0].Name)
}

func (WorkspaceSuite) TestWorkspaceCheckpointPinsGitOverlayRecipe(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "original"))
	branch := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main")
	// Prime the equivalent commit tree through a mutable ref before checkpoint
	// requests the SHA-pinned tree. Cache sharing must not unpin its recipe.
	_, err := branch.TargetCommit().Tree(dagger.GitCommitTreeOpts{DiscardGitDir: true}).Sync(ctx)
	require.NoError(t, err)
	source := branch.AsWorkspace().
		WithNewFile("base.txt", "overlay")
	frozenID, err := source.Checkpoint().ID(ctx)
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
		require.NotEqual(t, "checkpoint", vertex.Field)
	}
}

func (WorkspaceSuite) TestWorkspaceCheckpointPreservesDirectories(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	tree := c.Directory().WithNewFile("last/file", "remove me")
	daemon, url := gitService(ctx, t, c, tree)
	base := c.Git(url, dagger.GitOpts{ExperimentalServiceHost: daemon}).Branch("main").AsWorkspace()
	dirs := c.Directory().WithNewDirectory("empty", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0o700}).WithNewDirectory("gone")
	source := base.WithChanges(dirs.Changes(c.Directory())).
		WithChanges(tree.WithoutFile("last/file").WithNewDirectory("last").Changes(tree))
	frozen := source.Checkpoint()
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
	updated := frozen.WithChanges(replacement.Changes(dirs)).Checkpoint()
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

func (WorkspaceSuite) TestWorkspaceCheckpointRejectsNestedClientCapture(ctx context.Context, t *testctx.T) {
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
 return source.Checkpoint().File("tracked.txt").Contents(ctx)
}
`)
	out, err := base.With(daggerExecFail("--silent", "-m", "modules/probe", "call", "capture")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "workspace checkpoint capture is only available to the workspace's owning client")
}

func (WorkspaceSuite) TestWorkspaceCheckpointHostDirectoryIsSessionOnly(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var got struct {
		Host struct {
			Directory struct {
				AsWorkspace struct{ Checkpoint checkpointState }
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($path: String!) { host { directory(path: $path) {
			asWorkspace { checkpoint { id portable } }
		} } }`,
		Variables: map[string]any{"path": t.TempDir()},
	}, &dagger.Response{Data: &got}))
	require.NotEmpty(t, got.Host.Directory.AsWorkspace.Checkpoint.ID)
	require.False(t, got.Host.Directory.AsWorkspace.Checkpoint.Portable)
}

func (WorkspaceSuite) TestWorkspaceCheckpointReplayableValuePassesThrough(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	var got struct {
		Directory struct {
			AsWorkspace struct {
				WithNewFile struct {
					Original string `json:"original"`
					Frozen   struct {
						ID string `json:"id"`
					} `json:"frozen"`
				} `json:"withNewFile"`
			} `json:"asWorkspace"`
		} `json:"directory"`
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `{
  directory {
    asWorkspace {
      withNewFile(path: "overlay.txt", contents: "portable") {
        original: id
        frozen: checkpoint { id }
      }
    }
  }
}`}, &dagger.Response{Data: &got}))
	original := got.Directory.AsWorkspace.WithNewFile.Original
	require.NotEmpty(t, original)
	require.Equal(t, original, got.Directory.AsWorkspace.WithNewFile.Frozen.ID)
}

// A rootless local workspace is context-only: even though it carries the
// caller's host path for identity, reads resolve against an empty in-engine
// tree. Checkpointing must preserve that boundary, retain functional edits made
// to the in-engine tree, and return a recipe that a second checkpoint accepts
// as replayable.
func (WorkspaceSuite) TestWorkspaceCheckpointFreezesRootlessEffectiveTree(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "host-only.txt"), []byte("must not be captured"), 0o644))
	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	type frozen struct {
		ID         string `json:"id"`
		Cwd        string `json:"cwd"`
		ConfigFile string `json:"configFile"`
		Directory  struct {
			Entries []string `json:"entries"`
		} `json:"directory"`
		File struct {
			Contents string `json:"contents"`
		} `json:"file"`
		Replay struct {
			ID        string `json:"id"`
			Directory struct {
				Entries []string `json:"entries"`
			} `json:"directory"`
			File struct {
				Contents string `json:"contents"`
			} `json:"file"`
		} `json:"replay"`
	}
	var got struct {
		CurrentWorkspace struct {
			Pristine frozen `json:"pristine"`
			Edited   struct {
				Checkpoint frozen `json:"checkpoint"`
			} `json:"edited"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: `{
  currentWorkspace {
    pristine: checkpoint {
      id
      cwd
      configFile
      directory(path: "/") { entries }
      replay: checkpoint {
        id
        directory(path: "/") { entries }
      }
    }
    edited: withNewFile(path: "overlay.txt", contents: "in engine") {
      checkpoint {
        id
        cwd
        configFile
        directory(path: "/") { entries }
        file(path: "overlay.txt") { contents }
        replay: checkpoint {
          id
          directory(path: "/") { entries }
          file(path: "overlay.txt") { contents }
        }
      }
    }
  }
}`}, &dagger.Response{Data: &got}))

	pristine := got.CurrentWorkspace.Pristine
	require.NotEmpty(t, pristine.ID)
	require.Equal(t, "/", pristine.Cwd)
	require.Empty(t, pristine.ConfigFile)
	require.Empty(t, pristine.Directory.Entries, "a rootless checkpoint must not capture its host path")
	require.Equal(t, pristine.ID, pristine.Replay.ID, "the normalized checkpoint recipe must be replayable")
	require.Empty(t, pristine.Replay.Directory.Entries)

	edited := got.CurrentWorkspace.Edited.Checkpoint
	require.NotEmpty(t, edited.ID)
	require.Equal(t, "/", edited.Cwd)
	require.Empty(t, edited.ConfigFile)
	require.Equal(t, []string{"overlay.txt"}, edited.Directory.Entries)
	require.Equal(t, "in engine", edited.File.Contents)
	require.Equal(t, edited.ID, edited.Replay.ID, "the normalized overlay recipe must be replayable")
	require.Equal(t, []string{"overlay.txt"}, edited.Replay.Directory.Entries)
	require.Equal(t, "in engine", edited.Replay.File.Contents)
}
