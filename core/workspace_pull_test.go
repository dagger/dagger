package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

type pullFixture struct {
	dir string
	t   testing.TB
}

func newPullFixture(t testing.TB) pullFixture {
	t.Helper()
	f := pullFixture{dir: t.TempDir(), t: t}
	f.git("init", "-b", "main")
	f.commit("base.txt", "base\n", "base")
	f.git("branch", "source")
	return f
}
func (f pullFixture) git(args ...string) string {
	f.t.Helper()
	out, err := runWorkspacePullGit(f.t.Context(), f.dir, []string{"GIT_AUTHOR_DATE=2026-09-05T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-05T12:00:00Z"}, args...)
	require.NoError(f.t, err)
	return strings.TrimSpace(out)
}
func (f pullFixture) commit(name, text, message string) string {
	f.t.Helper()
	require.NoError(f.t, os.MkdirAll(filepath.Dir(filepath.Join(f.dir, name)), 0o755))
	require.NoError(f.t, os.WriteFile(filepath.Join(f.dir, name), []byte(text), 0o644))
	f.git("add", ".")
	f.git("commit", "-m", message)
	return f.git("rev-parse", "HEAD")
}
func (f pullFixture) fold(dirty []string, commits ...string) []WorkspacePullPick {
	f.t.Helper()
	picks, err := foldWorkspacePull(f.t.Context(), f.dir, f.git("rev-parse", "source"), dirty, WorkspacePullOpts{MaxCommits: 100, Commits: commits})
	require.NoError(f.t, err)
	return picks
}

func TestWorkspaceExportBaseStorageReady(t *testing.T) {
	for _, scenario := range []string{"complete", "packed", "missing commit", "missing tree", "missing blob", "shallow", "alternate", "promisor", "partial config", "replacement", "graft"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPullFixture(t)
			base := f.git("rev-parse", "HEAD")
			head := f.commit("next.txt", "next", "next")
			// Unrelated private objects must not influence readiness or be copied.
			require.NoError(t, os.WriteFile(filepath.Join(f.dir, "private"), []byte("not captured"), 0o600))
			f.git("hash-object", "-w", "private")
			writeGit := func(name, contents string) {
				require.NoError(t, os.WriteFile(filepath.Join(f.dir, ".git", name), []byte(contents), 0o600))
			}
			switch scenario {
			case "packed":
				f.git("gc", "--quiet")
			case "missing commit", "missing tree", "missing blob":
				sha := base
				switch scenario {
				case "missing tree":
					sha = f.git("rev-parse", base+"^{tree}")
				case "missing blob":
					sha = f.git("rev-parse", base+":base.txt")
				}
				require.NoError(t, os.Remove(filepath.Join(f.dir, ".git", "objects", sha[:2], sha[2:])))
			case "shallow":
				writeGit("shallow", head+"\n")
			case "alternate":
				writeGit("objects/info/alternates", "/unavailable\n")
			case "promisor":
				writeGit("objects/pack/pack-test.promisor", "")
			case "partial config":
				f.git("config", "remote.origin.promisor", "true")
			case "replacement":
				f.git("replace", head, base)
			case "graft":
				writeGit("info/grafts", head+"\n")
			}
			before := gitBundleTestSnapshot(t, f.dir)
			err := workspaceExportBaseStorageReady(t.Context(), gitutil.NewGitCLI(gitutil.WithDir(f.dir)), head)
			if scenario == "complete" || scenario == "packed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, before, gitBundleTestSnapshot(t, f.dir), "readiness must not mutate source storage")
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ready, err := (*GitRef)(nil).WorkspaceExportBaseReady(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, ready)
}

func TestWorkspaceExportReusesEmptySnapshot(t *testing.T) {
	f := newPullFixture(t)
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "executable"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink("executable", filepath.Join(f.dir, "link")))
	f.git("rm", "base.txt")
	f.git("add", ".")
	f.git("commit", "-m", "modes, symlink, and deletion")
	source := f.git("rev-parse", "HEAD")
	want, err := workspaceSnapshotCommit(t.Context(), f.dir)
	require.NoError(t, err)
	f.git("switch", "main")
	// Tree reuse must neither reset to source nor stage the current checkout.
	// Real callers own its state and may still be processing a different input.
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "base.txt"), []byte("staged"), 0o644))
	f.git("add", "base.txt")
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "base.txt"), []byte("unstaged"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "untracked"), []byte("keep"), 0o644))
	index, err := os.ReadFile(filepath.Join(f.dir, ".git", "index"))
	require.NoError(t, err)
	got, err := workspaceExportSnapshot(t.Context(), &gitMergeWorkspace{workDir: f.dir}, source, &changesetContent{paths: &ChangesetPaths{}})
	require.NoError(t, err)
	require.Equal(t, want, got, "reuse preserves the exact parentless snapshot object")
	indexAfter, err := os.ReadFile(filepath.Join(f.dir, ".git", "index"))
	require.NoError(t, err)
	require.Equal(t, index, indexAfter, "reuse must not restage or rewrite the index")
	require.Equal(t, base, f.git("rev-parse", "HEAD"))
	contents, err := os.ReadFile(filepath.Join(f.dir, "base.txt"))
	require.NoError(t, err)
	require.Equal(t, "unstaged", string(contents))
	require.FileExists(t, filepath.Join(f.dir, "untracked"))
	require.Contains(t, f.git("ls-tree", got), "100755 blob")
	require.Contains(t, f.git("ls-tree", got), "120000 blob")
}

func TestWorkspaceExportEmptySnapshotClassification(t *testing.T) {
	for name, paths := range map[string]*ChangesetPaths{
		"empty":                  {},
		"empty directory":        {Added: []string{"empty/"}},
		"modified directory":     {Modified: []string{"directory/"}},
		"mode or symlink change": {Modified: []string{"file"}},
		"removed":                {Removed: []string{"file"}},
		"all removed":            {AllRemoved: []string{"file"}},
		"rename":                 {Renamed: map[string]string{"new": "old"}},
		"git metadata":           {Added: []string{".git/HEAD"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, name == "empty", workspaceExportContentEmpty(&changesetContent{paths: paths}))
		})
	}
}

func TestWorkspaceExportNonemptySnapshot(t *testing.T) {
	for _, emptyDir := range []bool{false, true} {
		t.Run(strconv.FormatBool(emptyDir), func(t *testing.T) {
			f := newPullFixture(t)
			base := f.git("rev-parse", "HEAD")
			ws := &gitMergeWorkspace{root: f.dir, dir: "/", workDir: f.dir}
			// First use the fast path, then a delta requiring full validation.
			_, err := workspaceExportSnapshot(t.Context(), ws, base, &changesetContent{paths: &ChangesetPaths{}})
			require.NoError(t, err)
			paths := &ChangesetPaths{Removed: []string{"base.txt"}, AllRemoved: []string{"base.txt"}}
			if emptyDir {
				paths.Added = []string{"empty/"}
			}
			sha, err := workspaceExportSnapshot(t.Context(), ws, base, &changesetContent{paths: paths})
			if emptyDir {
				require.ErrorContains(t, err, "cannot export empty directories")
				return
			}
			require.NoError(t, err)
			require.Empty(t, f.git("ls-tree", sha))
			require.NoFileExists(t, filepath.Join(f.dir, "base.txt"))
		})
	}
}

func TestWorkspaceExportTreeTransportParents(t *testing.T) {
	f := newPullFixture(t)
	base := f.git("rev-parse", "HEAD")
	before, err := workspaceSnapshotTreeCommit(t.Context(), f.dir, base+"^{tree}", base)
	require.NoError(t, err)
	target := f.commit("next.txt", "next", "source change")
	tree := f.git("rev-parse", target+"^{tree}")
	f.git("reset", "--hard", base)
	require.NoError(t, workspaceExportTransport(t.Context(), f.dir, target, before, tree))
	require.Equal(t, target+" "+before, f.git("show", "-s", "--format=%P", "HEAD"))
	require.Equal(t, base, f.git("show", "-s", "--format=%P", "HEAD^2"))
	require.Equal(t, tree, f.git("rev-parse", "HEAD^{tree}"))
	require.FileExists(t, filepath.Join(f.dir, "next.txt"))
	require.Empty(t, f.git("status", "--porcelain"))
}

func TestWorkspaceExportIncrementalPending(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(strconv.FormatBool(committed), func(t *testing.T) {
			f := newPullFixture(t)
			base := f.git("rev-parse", "HEAD")
			f.git("switch", "source")
			require.NoError(t, os.WriteFile(filepath.Join(f.dir, "pending.txt"), []byte("first"), 0o644))
			fromTree, err := workspaceSnapshotCommit(t.Context(), f.dir)
			require.NoError(t, err)
			f.git("reset", "--hard", fromTree)
			f.git("reset", "--hard", base)
			source := base
			if committed {
				source = f.commit("pending.txt", "second", "commit saved pending with another edit")
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(f.dir, "pending.txt"), []byte("second"), 0o644))
			}
			var sourceTree string
			if committed {
				sourceTree, err = workspaceExportSnapshot(t.Context(), &gitMergeWorkspace{workDir: f.dir}, source, &changesetContent{paths: &ChangesetPaths{}})
			} else {
				sourceTree, err = workspaceSnapshotCommit(t.Context(), f.dir)
			}
			require.NoError(t, err)
			f.git("reset", "--hard", sourceTree)
			f.git("switch", "main")
			// A clean captured destination excludes previously saved untracked
			// files; its reused snapshot must not suppress their restoration.
			_, err = workspaceExportSnapshot(t.Context(), &gitMergeWorkspace{workDir: f.dir}, base, &changesetContent{paths: &ChangesetPaths{}})
			require.NoError(t, err)
			require.NoError(t, workspaceExportRestoreSavedUntracked(t.Context(), f.dir, base, base, fromTree))
			before, err := workspaceSnapshotTreeCommit(t.Context(), f.dir, f.git("write-tree"), base)
			require.NoError(t, err)
			f.git("reset", "--hard", base)
			target, tree, err := workspaceExportIntegrate(t.Context(), f.dir, base, before, source, sourceTree, fromTree, WorkspacePullOpts{MaxCommits: 100, FromSHA: base})
			require.NoError(t, err)
			require.Equal(t, source, target)
			require.Equal(t, "second", f.git("show", tree+":pending.txt"))
			if committed {
				require.Equal(t, f.git("rev-parse", target+"^{tree}"), tree, "saved pending is consumed into the commit")
			}
		})
	}
}

func TestWorkspaceExportPreservesCapturedStagedAddition(t *testing.T) {
	f := newPullFixture(t)
	base := f.git("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "pending.txt"), []byte("saved"), 0o644))
	from, err := workspaceSnapshotCommit(t.Context(), f.dir)
	require.NoError(t, err)
	f.git("reset", "--hard", from)
	f.git("reset", "--hard", base)
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "pending.txt"), []byte("staged host edit"), 0o644))
	f.git("add", "pending.txt")
	require.NoError(t, workspaceExportRestoreSavedUntracked(t.Context(), f.dir, base, base, from))
	require.Equal(t, "staged host edit", f.git("show", ":pending.txt"))
}

func TestWorkspaceExportDoesNotReplayPriorCherryPick(t *testing.T) {
	f := newPullFixture(t)
	f.git("switch", "source")
	from := f.commit("base.txt", "agent first", "first")
	f.git("switch", "main")
	f.commit("host.txt", "host", "host divergence")
	f.fold(nil)
	require.NotEqual(t, from, f.git("rev-parse", "HEAD"))
	// The host has deliberately undone a previously saved change. Saving only
	// subsequent work must neither replay its commit nor overwrite the undo.
	base := f.commit("base.txt", "base\n", "host undo")
	f.git("switch", "source")
	source := f.commit("next.txt", "next", "next")
	f.git("switch", "main")
	target, tree, err := workspaceExportIntegrate(t.Context(), f.dir, base, base, source, source, from, WorkspacePullOpts{MaxCommits: 100, FromSHA: from})
	require.NoError(t, err)
	require.Equal(t, "1", f.git("rev-list", "--count", base+".."+target))
	require.Equal(t, "base", f.git("show", tree+":base.txt"))
	require.Equal(t, "host", f.git("show", tree+":host.txt"))
	require.Equal(t, "next", f.git("show", tree+":next.txt"))
	require.Equal(t, "agent first", f.git("show", source+":base.txt"), "source stays unchanged")
}

func TestWorkspaceExportPendingDeleteAndConflicts(t *testing.T) {
	for _, change := range []string{"delete", "rename", "conflict", "rewrite"} {
		t.Run(change, func(t *testing.T) {
			f := newPullFixture(t)
			base := f.git("rev-parse", "HEAD")
			require.NoError(t, os.WriteFile(filepath.Join(f.dir, "base.txt"), []byte("saved pending"), 0o644))
			from, err := workspaceSnapshotCommit(t.Context(), f.dir)
			require.NoError(t, err)
			f.git("reset", "--hard", from)
			if change == "rename" {
				f.git("mv", "base.txt", "renamed.txt")
			} else {
				f.git("rm", "base.txt")
			}
			sourceTree, err := workspaceSnapshotCommit(t.Context(), f.dir)
			require.NoError(t, err)
			f.git("reset", "--hard", sourceTree)
			f.git("reset", "--hard", from)
			before := from
			if change == "conflict" {
				require.NoError(t, os.WriteFile(filepath.Join(f.dir, "base.txt"), []byte("outside"), 0o644))
				before, err = workspaceSnapshotCommit(t.Context(), f.dir)
				require.NoError(t, err)
			}
			f.git("reset", "--hard", base)
			fromSHA := base
			if change == "rewrite" {
				fromSHA = from
			}
			_, tree, err := workspaceExportIntegrate(t.Context(), f.dir, base, before, base, sourceTree, from, WorkspacePullOpts{MaxCommits: 100, FromSHA: fromSHA})
			if change == "conflict" || change == "rewrite" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if change == "delete" {
				require.Empty(t, f.git("ls-tree", "--name-only", tree))
			} else {
				require.Equal(t, "saved pending", f.git("show", tree+":renamed.txt"))
			}
		})
	}
}

func TestWorkspacePullFastForwardAndSelection(t *testing.T) {
	f := newPullFixture(t)
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	a := f.commit("a.txt", "a", "a")
	b := f.commit("b.txt", "b", "b")
	f.git("switch", "main")
	picks := f.fold(nil, a, base)
	require.Equal(t, []string{base, a}, []string{picks[0].SHA, picks[1].SHA})
	require.Equal(t, WorkspaceCommitPicked, picks[0].Status)
	require.Equal(t, a, f.git("rev-parse", "HEAD"))
	picks = f.fold(nil)
	require.Len(t, picks, 1)
	require.Equal(t, b, f.git("rev-parse", "HEAD"))
	require.Empty(t, f.fold(nil))
	// Selecting only the later independent commit must not include a.
	f.git("reset", "--hard", base)
	picks = f.fold(nil, b)
	require.Equal(t, WorkspaceCommitPickable, picks[0].Status)
	require.NotEqual(t, b, f.git("rev-parse", "HEAD"))
	_, err := os.Stat(filepath.Join(f.dir, "a.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, f.git("log", "-1", "--format=%B", b), f.git("log", "-1", "--format=%B"))
}

func TestWorkspacePullCherryPickPreservesMessage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		status  WorkspaceCommitPickStatus
	}{
		{"plain", "source\n\nMessage body.\n\nSigned-off-by: Source <source@example.com>\n\n", WorkspaceCommitRedundant},
		{"existing marker", "source\n\n(cherry picked from commit " + strings.Repeat("a", 40) + ")\n", WorkspaceCommitPicked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.message
			f := newPullFixture(t)
			f.commit("local.txt", "local", "local")
			base := f.git("rev-parse", "HEAD")
			f.git("switch", "source")
			f.commit("source.txt", "source", "source")
			f.git("commit", "--amend", "--cleanup=verbatim", "-m", message)
			sha := f.git("rev-parse", "HEAD")
			f.git("switch", "main")
			picks := f.fold(nil)
			require.Equal(t, WorkspaceCommitPickable, picks[0].Status)
			first := f.git("rev-parse", "HEAD")
			require.NotEqual(t, sha, first)
			require.Equal(t, base, f.git("rev-parse", "HEAD^"))
			got, err := runWorkspacePullGit(t.Context(), f.dir, nil, "show", "-s", "--format=%B", "HEAD")
			require.NoError(t, err)
			require.Equal(t, message+"\n", got, "preserve the message, including trailing newlines and existing markers")
			picks = f.fold(nil)
			require.Equal(t, tc.status, picks[0].Status)
			require.Equal(t, first, f.git("rev-parse", "HEAD"))
			// Recomputing from the same inputs must not read the wall clock.
			f.git("reset", "--hard", base)
			f.fold(nil)
			require.Equal(t, first, f.git("rev-parse", "HEAD"))
		})
	}
}

func TestWorkspacePullRedundantAndDirty(t *testing.T) {
	f := newPullFixture(t)
	f.commit("same.txt", "same", "local equivalent")
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	a := f.commit("same.txt", "same", "source equivalent")
	b := f.commit("dirty/file.txt", "incoming", "dirty")
	f.git("switch", "main")
	picks := f.fold([]string{"dirty"})
	require.Equal(t, a, picks[0].SHA)
	require.Equal(t, WorkspaceCommitRedundant, picks[0].Status)
	require.Equal(t, b, picks[1].SHA)
	require.Equal(t, WorkspaceCommitConflict, picks[1].Status)
	require.Equal(t, WorkspaceCommitPickReasonDirty, picks[1].Reason)
	require.Equal(t, []string{"dirty/file.txt"}, picks[1].ConflictPaths)
	require.Equal(t, base, f.git("rev-parse", "HEAD"))
}

func TestWorkspacePullConflictsContinueFold(t *testing.T) {
	f := newPullFixture(t)
	f.commit("base.txt", "local\n", "local")
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	a := f.commit("base.txt", "source\n", "conflict")
	b := f.commit("independent.txt", "independent", "independent")
	c := f.commit("base.txt", "dependent\n", "dependent")
	f.git("switch", "main")
	picks := f.fold(nil)
	require.Equal(t, []string{a, b, c}, []string{picks[0].SHA, picks[1].SHA, picks[2].SHA})
	require.Equal(t, WorkspaceCommitConflict, picks[0].Status)
	require.Equal(t, WorkspaceCommitPickReasonContent, picks[0].Reason)
	require.Equal(t, []string{"base.txt"}, picks[0].ConflictPaths)
	require.Equal(t, WorkspaceCommitPickable, picks[1].Status)
	require.Equal(t, WorkspaceCommitConflict, picks[2].Status)
	require.Equal(t, base, f.git("rev-parse", "HEAD^"))
	require.Equal(t, "local", f.git("show", "HEAD:base.txt"))
}

func TestWorkspacePullMerges(t *testing.T) {
	f := newPullFixture(t)
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	f.commit("a", "a", "a")
	f.git("switch", "-c", "side", base)
	f.commit("b", "b", "b")
	f.git("switch", "source")
	f.git("merge", "--no-ff", "side", "-m", "merge")
	merged := f.git("rev-parse", "HEAD")
	f.git("switch", "main")
	picks := f.fold(nil)
	require.Len(t, picks, 3)
	require.Equal(t, merged, f.git("rev-parse", "HEAD"))
	f.git("reset", "--hard", base)
	f.commit("local", "local", "local")
	_, err := foldWorkspacePull(t.Context(), f.dir, merged, nil, WorkspacePullOpts{MaxCommits: 100})
	require.ErrorContains(t, err, "without a mainline")
}

func TestWorkspacePullLimits(t *testing.T) {
	f := newPullFixture(t)
	f.git("switch", "source")
	a := f.commit("a", "a", "a")
	f.commit("b", "b", "b")
	f.git("switch", "main")
	base := f.git("rev-parse", "HEAD")
	_, err := foldWorkspacePull(t.Context(), f.dir, f.git("rev-parse", "source"), nil, WorkspacePullOpts{MaxCommits: 1})
	require.ErrorContains(t, err, "exceeds maxCommits")
	require.Equal(t, base, f.git("rev-parse", "HEAD"))
	for _, opts := range []WorkspacePullOpts{{MaxCommits: 0}, {MaxCommits: 1001}, {MaxCommits: 1, Commits: []string{"--all"}}, {MaxCommits: 2, Commits: []string{a, a}}} {
		require.Error(t, opts.Validate())
	}
	_, err = foldWorkspacePull(t.Context(), f.dir, f.git("rev-parse", "source"), nil, WorkspacePullOpts{MaxCommits: 100, Commits: []string{strings.Repeat("a", 40)}})
	require.ErrorContains(t, err, "not within the source")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = foldWorkspacePull(ctx, f.dir, a, nil, WorkspacePullOpts{MaxCommits: 100})
	require.Error(t, err)
	// Receiver-side duplicate detection must fail on overflow too.
	f.commit("local-a", "a", "local a")
	f.commit("local-b", "b", "local b")
	_, err = foldWorkspacePull(t.Context(), f.dir, a, nil, WorkspacePullOpts{MaxCommits: 1})
	require.ErrorContains(t, err, "exceeds maxCommits")
}

func TestWorkspacePullOutputLimit(t *testing.T) {
	var out workspacePullOutput
	_, err := io.Copy(&out, io.LimitReader(strings.NewReader(strings.Repeat("a", workspacePullOutputLimit+1)), workspacePullOutputLimit))
	require.NoError(t, err)
	require.Len(t, out.String(), workspacePullOutputLimit)
	_, err = out.Write([]byte("b"))
	require.ErrorContains(t, err, "output limit")
	require.True(t, out.exceeded)
	require.Len(t, out.String(), workspacePullOutputLimit)
}

func TestWorkspacePullDirtyDirectories(t *testing.T) {
	dirty := pullDirtyPaths(&ChangesetPaths{
		Added:      []string{"empty/", "parent/", "parent/child", "nested/", "nested/empty/"},
		AllRemoved: []string{"removed/", "removed/file"},
	})
	require.Equal(t, []string{"empty", "nested/empty", "parent/child", "removed/file"}, dirty)
	require.Equal(t, []string{"empty", "nested"}, pullOverlappingPaths([]string{"empty", "nested", "parent/unrelated"}, dirty))
}

func TestWorkspacePullTransitiveOrigins(t *testing.T) {
	f := newPullFixture(t)
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	original := f.commit("shared", "shared", "original")
	f.git("switch", "main")
	f.commit("local", "local", "local")
	f.git("cherry-pick", "-x", original)
	first := f.git("rev-parse", "HEAD")
	// Explicit -x provenance still identifies a directly repeated pull.
	picks := f.fold(nil)
	require.Equal(t, WorkspaceCommitPicked, picks[0].Status)
	require.Equal(t, first, f.git("rev-parse", "HEAD"))
	// Another branch carries a distinct cherry-pick of the same origin.
	f.git("switch", "-c", "relay", base)
	f.commit("relay", "relay", "relay")
	f.git("cherry-pick", "-x", original)
	relay := f.git("rev-parse", "HEAD")
	f.git("branch", "-f", "source", relay)
	f.git("switch", "main")
	picks = f.fold(nil, relay)
	require.Equal(t, WorkspaceCommitPicked, picks[0].Status)
	require.Equal(t, first, f.git("rev-parse", "HEAD"))
}

func TestWorkspacePullFileKinds(t *testing.T) {
	f := newPullFixture(t)
	f.commit("local", "local", "local")
	f.git("switch", "source")
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "binary"), []byte{0, 1, 2, 255}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, "executable"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink("binary", filepath.Join(f.dir, "link")))
	f.git("mv", "base.txt", "renamed.txt")
	f.git("add", ".")
	f.git("commit", "-m", "file kinds")
	f.git("switch", "main")
	picks := f.fold(nil)
	require.Equal(t, WorkspaceCommitPickable, picks[0].Status)
	require.Contains(t, f.git("ls-tree", "HEAD", "executable"), "100755")
	require.Contains(t, f.git("ls-tree", "HEAD", "link"), "120000")
	require.Equal(t, "binary", f.git("show", "HEAD:link"))
	data, err := os.ReadFile(filepath.Join(f.dir, "binary"))
	require.NoError(t, err)
	require.Equal(t, []byte{0, 1, 2, 255}, data)
	require.Equal(t, "base", f.git("show", "HEAD:renamed.txt"))
	_, err = os.Stat(filepath.Join(f.dir, "base.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
