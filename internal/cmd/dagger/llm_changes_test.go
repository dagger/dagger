package daggercmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
		Uncommitted: []patchpreview.Entry{{Path: "pending.txt", Kind: "ADDED", Added: 1}},
		Incoming:    []changesCommit{{SHA: strings.Repeat("b", 40), MessageHeadline: "checkout commit"}},
	}
	for i := range changesHistoryLimit + 1 {
		p.Outgoing = append(p.Outgoing, changesCommit{SHA: strings.Repeat("a", 40), MessageHeadline: fmt.Sprintf("commit %02d", i)})
	}
	text := ansi.Strip(p.render(80))
	require.Contains(t, text, "Uncommitted\npending.txt")
	require.Contains(t, text, "Commits to save (20+)")
	require.Contains(t, text, "commit 19")
	require.NotContains(t, text, "commit 20")
	require.Contains(t, text, "more commits not shown")
	require.Contains(t, text, "Checkout-only commits (1)\nbbbbbbb checkout commit")
	p.Uncommitted, p.Incoming = nil, nil
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
	git("add", ".")
	git("commit", "-m", "base")
	dag, err := dagger.Connect(ctx, dagger.WithWorkdir(checkout))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dag.Close()) })
	baseline, err := checkpointWorkspace(ctx, dag)
	require.NoError(t, err)
	start := dag.LLM(dagger.LLMOpts{Model: "openai/gpt-4o"}).WithWorkspace(baseline)
	var changes idtui.SidebarSection
	s := &LLMSession{
		dag: dag, llm: start, initialLLM: start, workspaceBaseline: baseline,
		plumbingCtx: ctx, autoCompactL: new(sync.Mutex),
		frontend: &idtui.FrontendMock{
			SetSidebarContentFunc: func(section idtui.SidebarSection) { changes = section },
			SetStatusLineFunc:     func(idtui.StatusLineData) {},
		},
	}
	const date = "2026-09-05T12:00:00Z"
	id, err := baseline.WithNewFile("saved.txt", "committed\n").WithCommit("agent commit", date).ID(ctx)
	require.NoError(t, err)
	committed := dagger.Ref[*dagger.Workspace](dag, id)
	s.llm = start.WithWorkspace(committed)
	require.NoError(t, s.updateChangesPreview(s.llm))
	require.Contains(t, changes.Body(80), "Commits to save (1)")
	require.Contains(t, changes.Body(80), "agent commit")
	require.NotContains(t, changes.Body(80), "Uncommitted")
	require.Len(t, changes.KeyMap, 2)
	_, err = os.Stat(filepath.Join(checkout, "saved.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	sha, err := committed.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NoError(t, s.ExportChanges(ctx))
	require.Equal(t, sha, git("rev-parse", "HEAD"))
	require.Empty(t, changes.Body(80), "saving commit-only changes clears the panel")
	s.reset()
	clearedSHA, err := s.llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, sha, clearedSHA, ".clear retains the saved checkpoint")

	// A later edit to the same path remains separate from its committed version.
	s.llm = s.llm.WithWorkspace(s.llm.Workspace().WithNewFile("saved.txt", "pending\n"))
	require.NoError(t, s.updateChangesPreview(s.llm))
	require.Contains(t, changes.Body(80), "Uncommitted")
	require.NotContains(t, changes.Body(80), "Commits to save")
	require.NoError(t, s.ExportChanges(ctx))
	contents, err := os.ReadFile(filepath.Join(checkout, "saved.txt"))
	require.NoError(t, err)
	require.Equal(t, "pending\n", string(contents))
	require.Equal(t, sha, git("rev-parse", "HEAD"))

	// Moved checkout history is shown in the opposite direction. Saving a
	// divergent agent commit hands off without replacing either side's history.
	git("add", "saved.txt")
	git("commit", "-m", "checkout commit")
	hostSHA := git("rev-parse", "HEAD")
	s.llm = s.llm.WithWorkspace(s.llm.Workspace().WithNewFile("agent.txt", "agent\n").WithCommit("divergent agent", date))
	require.NoError(t, s.updateChangesPreview(s.llm))
	require.Contains(t, changes.Body(80), "Checkout-only commits (1)")
	require.Contains(t, changes.Body(80), "checkout commit")
	require.Contains(t, changes.Body(80), "divergent agent")
	before, err := s.llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.ErrorContains(t, s.ExportChanges(ctx), "refs/dagger/checkpoints/")
	require.Equal(t, hostSHA, git("rev-parse", "HEAD"))
	after, err := s.llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, s.ResetWorkspace(ctx))
	resetSHA, err := s.llm.Workspace().Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, hostSHA, resetSHA)
	require.Empty(t, changes.Body(80))
}
