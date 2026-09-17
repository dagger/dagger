package daggercmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/util/patchpreview"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceChangesRendering(t *testing.T) {
	require.True(t, (workspaceChangesPreview{}).empty())
	p := workspaceChangesPreview{
		Files:    []patchpreview.Entry{{Path: "pending.txt", Kind: "ADDED", Added: 1}},
		Incoming: []changesCommit{{SHA: strings.Repeat("b", 40), MessageHeadline: "checkout commit"}},
	}
	for i := range changesHistoryLimit + 1 {
		p.Outgoing = append(p.Outgoing, changesCommit{SHA: strings.Repeat("a", 40), MessageHeadline: fmt.Sprintf("commit %02d", i)})
	}
	text := ansi.Strip(p.render(80))
	require.Contains(t, text, "Uncommitted changes\npending.txt")
	require.Contains(t, text, "Commits to save (20+)")
	require.Contains(t, text, "commit 19")
	require.NotContains(t, text, "commit 20")
	require.Contains(t, text, "more commits not shown")
	require.Contains(t, text, "Checkpoint-only commits (1)\nbbbbbbb checkout commit")
	p.Files, p.Incoming = nil, nil
	p.Outgoing = []changesCommit{{SHA: "abcdefghi", MessageHeadline: "subject\x1b[2J\n\rwith a long suffix"}}
	require.False(t, p.empty(), "commit-only changes must keep the panel visible")
	text = p.render(24)
	require.NotContains(t, text, "\x1b")
	require.NotContains(t, text, "\r")
	require.Contains(t, text, "…")
	require.NotContains(t, text, "Uncommitted")
	for _, line := range strings.Split(text, "\n")[1:] {
		require.LessOrEqual(t, ansi.StringWidth(line), 24)
	}
	require.False(t, (workspaceChangesPreview{HistoryError: "unavailable"}).empty())
}

func (DaggerCMDSuite) TestAgentWorkspaceWithoutGitBaseline(ctx context.Context, t *testctx.T) {
	for _, unborn := range []bool{false, true} {
		name := "non-repository"
		if unborn {
			name = "unborn-repository"
		}
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			checkout := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(checkout, "dagger.toml"), nil, 0o644))
			if unborn {
				cmd := exec.CommandContext(ctx, "git", "init", "-b", "main")
				cmd.Dir = checkout
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", out)
			}
			dag, err := dagger.Connect(ctx, dagger.WithWorkdir(checkout))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, dag.Close()) })
			baseline, err := snapshotWorkspace(ctx, dag)
			require.NoError(t, err)
			var changes idtui.SidebarSection
			s, err := NewLLMSession(ctx, dag, "", nil, &idtui.FrontendMock{
				SetSidebarContentFunc: func(section idtui.SidebarSection) { changes = section },
				SetStatusLineFunc:     func(idtui.StatusLineData) {},
			}, dag.LLM(dagger.LLMOpts{Model: "openai/gpt-4o"}).WithWorkspace(baseline))
			require.NoError(t, err)
			waitRefresh := func() {
				t.Helper()
				require.Eventually(t, func() bool {
					a := s.Target()
					a.refreshL.Lock()
					defer a.refreshL.Unlock()
					return !a.refreshInFlight
				}, 10*time.Second, time.Millisecond)
			}
			waitRefresh()
			require.Empty(t, changes.Body(80))
			s.Target().llm = s.Target().llm.WithWorkspace(baseline.WithNewFile("agent.txt", "agent edit"))
			require.NoError(t, s.Target().updateChangesPreview(s.Target().llm))
			require.Contains(t, changes.Body(80), "agent.txt")
			require.NoError(t, s.Target().ResetWorkspace(ctx))
			waitRefresh()
			require.Empty(t, changes.Body(80), "reload discards pending edits without Git capture")
		})
	}
}

func (DaggerCMDSuite) TestAgentWorkspaceChanges(ctx context.Context, t *testctx.T) {
	checkout := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "UI Test")
	git("config", "user.email", "ui@localhost")
	git("config", "commit.gpgSign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("base\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte("ignored.txt\n"), 0o644))
	git("add", ".")
	git("commit", "-m", "base")
	// Pre-existing dirt is part of the baseline, not a change by the agent.
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("initial dirt\n"), 0o644))
	dag, err := dagger.Connect(ctx, dagger.WithWorkdir(checkout))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })
	baseline, err := snapshotWorkspace(ctx, dag)
	require.NoError(t, err)
	start := dag.LLM(dagger.LLMOpts{Model: "openai/gpt-4o"}).WithWorkspace(baseline)
	var changes idtui.SidebarSection
	// Startup and preview must work entirely from the checkpoint even when
	// the host Git repository is unavailable. The old throwaway LLM and live
	// history reads both require it.
	gitDir := filepath.Join(checkout, ".git")
	parkedGitDir := filepath.Join(t.TempDir(), "checkout.git")
	require.NoError(t, os.Rename(gitDir, parkedGitDir))
	t.Cleanup(func() {
		if _, err := os.Stat(parkedGitDir); err == nil {
			require.NoError(t, os.Rename(parkedGitDir, gitDir))
		}
	})
	s, err := NewLLMSession(ctx, dag, "", nil,
		&idtui.FrontendMock{
			SetSidebarContentFunc: func(section idtui.SidebarSection) { changes = section },
			SetStatusLineFunc:     func(idtui.StatusLineData) {},
		}, start)
	require.NoError(t, err)
	// Session updates now refresh the UI asynchronously. Drain the refresh
	// before inspecting the panel or mutating this test's conversation value.
	waitRefresh := func(session *LLMSession) {
		t.Helper()
		require.Eventually(t, func() bool {
			a := session.Target()
			a.refreshL.Lock()
			defer a.refreshL.Unlock()
			return !a.refreshInFlight
		}, 10*time.Second, time.Millisecond)
	}
	waitRefresh(s)
	require.Empty(t, changes.Body(80), "initial dirt must not appear as an agent change")
	unbound, err := NewLLMSession(ctx, dag, "", nil, s.frontend, dag.LLM(dagger.LLMOpts{Model: "openai/gpt-4o"}))
	require.NoError(t, err, "a conversational LLM need not have a workspace")
	require.Nil(t, unbound.Target().lastSynced())
	waitRefresh(unbound)
	const date = "2026-09-05T12:00:00Z"
	id, err := baseline.WithNewFile("saved.txt", "committed\n").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "agent commit", date)
	}).ID(ctx)
	require.NoError(t, err)
	committed := dagger.Ref[*dagger.Workspace](dag, id)
	s.Target().llm = start.WithWorkspace(committed)
	require.NoError(t, s.Target().updateChangesPreview(s.Target().llm))
	require.Contains(t, changes.Body(80), "Commits to save (1)")
	require.Contains(t, changes.Body(80), "agent commit")
	require.NotContains(t, changes.Body(80), "saved.txt")
	require.NotContains(t, changes.Body(80), "base.txt")
	require.NotContains(t, changes.Body(80), "History unavailable")
	require.NotContains(t, changes.Body(80), "Uncommitted")
	require.Len(t, changes.KeyMap, 2)

	// File stats describe only the edits above HEAD, not the total diff from
	// the checkpoint. Even undoing a committed addition is a pending deletion.
	for _, tc := range []struct {
		name string
		ws   *dagger.Workspace
		want patchpreview.Entry
	}{
		{"addition", committed.WithNewFile("pending.txt", "new\n"), patchpreview.Entry{Path: "pending.txt", Kind: "ADDED", Added: 1}},
		{"modification", committed.WithNewFile("saved.txt", "pending\n"), patchpreview.Entry{Path: "saved.txt", Kind: "MODIFIED", Added: 1, Removed: 1}},
		{"deletion", committed.WithoutFile("saved.txt"), patchpreview.Entry{Path: "saved.txt", Kind: "REMOVED", Removed: 1}},
		// Not an uncommitted change in git's eyes, but still an edit the
		// user will save, so it stays visible after the agent commits.
		{"ignored addition", committed.WithNewFile("ignored.txt", "new\n"), patchpreview.Entry{Path: "ignored.txt", Kind: "ADDED", Added: 1}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			preview, err := previewWorkspaceChanges(ctx, dag, tc.ws, baseline)
			require.NoError(t, err)
			require.Equal(t, []patchpreview.Entry{tc.want}, preview.Files)
			require.Len(t, preview.Outgoing, 1)
			text := ansi.Strip(preview.render(80))
			require.Less(t, strings.Index(text, "Uncommitted changes"), strings.Index(text, "Commits to save"))
		})
	}

	_, err = os.Stat(filepath.Join(checkout, "saved.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	sha, err := committed.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NoError(t, os.Rename(parkedGitDir, gitDir))
	// The agent commit includes the captured initial dirt. Save consumes that
	// matching host edit into the commit without requiring an explicit reload.
	require.NoError(t, s.Target().ExportChanges(ctx))
	waitRefresh(s)
	require.Equal(t, sha, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	require.Empty(t, changes.Body(80), "saving commit-only changes clears the panel")
	s.Target().reset()
	waitRefresh(s)
	clearedSHA, err := s.Target().llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, clearedSHA, ".clear retains the saved checkpoint")

	// A later edit to the same path remains separate from its committed version.
	s.Target().llm = s.Target().llm.WithWorkspace(s.Target().llm.Workspace().WithNewFile("saved.txt", "pending\n"))
	require.NoError(t, s.Target().updateChangesPreview(s.Target().llm))
	require.Contains(t, changes.Body(80), "Uncommitted changes")
	require.NotContains(t, changes.Body(80), "Commits to save")
	require.NoError(t, s.Target().ExportChanges(ctx))
	waitRefresh(s)
	contents, err := os.ReadFile(filepath.Join(checkout, "saved.txt"))
	require.NoError(t, err)
	require.Equal(t, "pending\n", string(contents))
	require.Equal(t, sha, git("rev-parse", "HEAD"))
	require.NoError(t, s.Target().updateChangesPreview(s.Target().llm))
	require.Empty(t, changes.Body(80), "saved pending edits become the new baseline")

	// Host-only commits don't change the preview baseline. Saving still checks
	// the live checkout and cherry-picks nonconflicting divergent agent work.
	git("add", "saved.txt")
	git("commit", "-m", "checkout commit")
	hostSHA := git("rev-parse", "HEAD")
	require.NoError(t, s.Target().updateChangesPreview(s.Target().llm))
	require.Empty(t, changes.Body(80), "host commits must not change the sidebar")
	s.Target().llm = s.Target().llm.WithWorkspace(s.Target().llm.Workspace().WithNewFile("agent.txt", "agent\n").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "divergent agent", date)
	}))
	require.NoError(t, s.Target().updateChangesPreview(s.Target().llm))
	require.NotContains(t, changes.Body(80), "Checkpoint-only commits")
	require.NotContains(t, changes.Body(80), "checkout commit")
	require.Contains(t, changes.Body(80), "divergent agent")
	before, err := s.Target().llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NoError(t, s.Target().ExportChanges(ctx))
	waitRefresh(s)
	require.NotEqual(t, hostSHA, git("rev-parse", "HEAD"))
	require.Contains(t, git("log", "--format=%H"), hostSHA)
	after, err := s.Target().llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "saving must not replace the agent's history with host cherry-picks")
	savedHostSHA := git("rev-parse", "HEAD")
	require.NotEqual(t, after, savedHostSHA, "divergent agent commit is cherry-picked on the host only")
	require.Empty(t, changes.Body(80), "saving clears the preview without reloading the agent")
	require.NoError(t, s.Target().ExportChanges(ctx))
	waitRefresh(s)
	require.Equal(t, savedHostSHA, git("rev-parse", "HEAD"), "saving twice must not duplicate cherry-picked commits")

	// A second save from the original agent history only contributes new work.
	s.Target().llm = s.Target().llm.WithWorkspace(s.Target().llm.Workspace().WithNewFile("later.txt", "later\n").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "later agent", date)
	}))
	unsyncedLLM := s.Target().llm
	unsyncedID, err := unsyncedLLM.Workspace().ID(ctx)
	require.NoError(t, err)
	rt := newFakeRuntime()
	rt.snapshot, err = unsyncedLLM.ID(ctx)
	require.NoError(t, err)
	s.Target().bindRuntime(rt, "save-test", "", true)
	require.NoError(t, s.Target().ExportChanges(ctx))
	waitRefresh(s)
	require.Same(t, rt, s.Target().runtime(), "saving must retain the runtime")
	require.Zero(t, rt.reseedCount(), "saving must not reseed the runtime")
	_, _, stops := rt.counts()
	require.Zero(t, stops, "saving must not stop the runtime")
	s.Target().detachAgent()
	require.Same(t, unsyncedLLM, s.Target().llm, "saving must not replace the LLM")
	savedID, err := s.Target().lastSynced().ID(ctx)
	require.NoError(t, err)
	require.Equal(t, unsyncedID, savedID, "checkpoint records the exported source, not the destination")
	require.Equal(t, "1", git("rev-list", "--count", savedHostSHA+"..HEAD"))
	require.Contains(t, git("log", "--format=%H"), hostSHA)
	savedHostSHA = git("rev-parse", "HEAD")

	require.NoError(t, s.Target().ResetWorkspace(ctx))
	waitRefresh(s)
	resetSHA, err := s.Target().llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, savedHostSHA, resetSHA, "only reload adopts host history")
	require.Empty(t, changes.Body(80))

	// A real content conflict preserves the checkout, agent workspace, and
	// checkpoint baseline, even though nonconflicting divergence is accepted.
	baselineID, err := s.Target().lastSynced().ID(ctx)
	require.NoError(t, err)
	conflictingID, err := s.Target().llm.Workspace().WithNewFile("agent.txt", "another agent edit\n").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "conflicting agent", date)
	}).ID(ctx)
	require.NoError(t, err)
	s.Target().llm = s.Target().llm.WithWorkspace(dagger.Ref[*dagger.Workspace](dag, conflictingID))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "agent.txt"), []byte("user edit\n"), 0o644))
	git("commit", "-am", "conflicting user")
	hostSHA = git("rev-parse", "HEAD")
	require.Error(t, s.Target().ExportChanges(ctx))
	require.Equal(t, hostSHA, git("rev-parse", "HEAD"))
	remainingID, err := s.Target().llm.Workspace().ID(ctx)
	require.NoError(t, err)
	require.Equal(t, conflictingID, remainingID)
	remainingBaselineID, err := s.Target().lastSynced().ID(ctx)
	require.NoError(t, err)
	require.Equal(t, baselineID, remainingBaselineID)
}

func (DaggerCMDSuite) TestAgentToolsetFollowsFocusAndSnapshot(ctx context.Context, t *testctx.T) {
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })
	directoryLLM := dag.LLM().WithTools(dag.Directory())
	containerLLM := dag.LLM().WithTools(dag.Container())
	directoryTools, err := directoryLLM.Tools(ctx)
	require.NoError(t, err)
	containerTools, err := containerLLM.Tools(ctx)
	require.NoError(t, err)
	require.NotEqual(t, directoryTools, containerTools)
	s := &LLMSession{dag: dag}
	first := &sessionAgent{session: s, llm: directoryLLM}
	second := &sessionAgent{session: s, llm: containerLLM}
	s.SetTarget(first)
	got, err := s.tools(ctx)
	require.NoError(t, err)
	require.Equal(t, directoryTools, got)
	s.SetTarget(second)
	got, err = s.tools(ctx)
	require.NoError(t, err)
	require.Equal(t, containerTools, got)
	// A running agent's committed snapshot takes precedence over its turn seed.
	id, err := directoryLLM.ID(ctx)
	require.NoError(t, err)
	second.agent = &fakeRuntime{snapshot: id}
	got, err = s.tools(ctx)
	require.NoError(t, err)
	require.Equal(t, directoryTools, got)
}
