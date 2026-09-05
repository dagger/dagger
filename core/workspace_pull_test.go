package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type pullFixture struct {
	dir string
	t   *testing.T
}

func newPullFixture(t *testing.T) pullFixture {
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
	require.Contains(t, f.git("log", "-1", "--format=%B"), "(cherry picked from commit "+b+")")
}

func TestWorkspacePullCherryPickAndOrigins(t *testing.T) {
	f := newPullFixture(t)
	f.commit("local.txt", "local", "local")
	base := f.git("rev-parse", "HEAD")
	f.git("switch", "source")
	sha := f.commit("source.txt", "source", "source")
	f.git("switch", "main")
	picks := f.fold(nil)
	require.Equal(t, WorkspaceCommitPickable, picks[0].Status)
	first := f.git("rev-parse", "HEAD")
	require.NotEqual(t, sha, first)
	require.Equal(t, base, f.git("rev-parse", "HEAD^"))
	picks = f.fold(nil)
	require.Equal(t, WorkspaceCommitPicked, picks[0].Status)
	require.Equal(t, first, f.git("rev-parse", "HEAD"))
	// Recomputing from the same inputs must not read the wall clock.
	f.git("reset", "--hard", base)
	f.fold(nil)
	require.Equal(t, first, f.git("rev-parse", "HEAD"))
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
	f.fold(nil)
	first := f.git("rev-parse", "HEAD")
	// Another branch carries a distinct cherry-pick of the same origin.
	f.git("switch", "-c", "relay", base)
	f.commit("relay", "relay", "relay")
	f.git("cherry-pick", "-x", original)
	relay := f.git("rev-parse", "HEAD")
	f.git("branch", "-f", "source", relay)
	f.git("switch", "main")
	picks := f.fold(nil, relay)
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
