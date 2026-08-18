package daggercmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func TestGitSourceArgRef(t *testing.T) {
	// These are valid ModuleSource cloneRef URLs and versions,  taken from
	// core/schema/modulesource_test.go.
	//
	// When producing a path for a Directory or File argument we need to produce a
	// different kind of URL (buildkit convention), which is then passed through
	// to the default CLI flag. The flag checks if it's a git URL by passing it
	// through `parseGitURL`, so we check if that validation will succeed.
	cases := []gitSourceContext{
		{Root: "github.com/shykes/daggerverse", Path: "ci"},
		{Root: "github.com/shykes/daggerverse.git", Path: "ci", Version: "version"},
		{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Path: "depth1/depth2"},
		{Root: "bitbucket.org/test-travail/test", Path: "depth1"},
		{Root: "ssh://git@github.com/shykes/daggerverse"},
		{Root: "github.com:shykes/daggerverse.git", Path: "ci", Version: "version"},
		{Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules", Path: "cool-sdk"},
		{Root: "ssh://git@ssh.dev.azure.com/v3/daggere2e/public/dagger-test-modules", Path: "cool-sdk"},
	}
	for _, c := range cases {
		url := c.ArgRef("")
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			_, err := gitutil.ParseURL(url)
			require.NoError(t, err)
		})
	}
}

func TestCorePseudoModuleUsesDefaultShellWorkdir(t *testing.T) {
	oldModuleURL := moduleURL
	oldModuleNoURL := moduleNoURL
	t.Cleanup(func() {
		moduleURL = oldModuleURL
		moduleNoURL = oldModuleNoURL
	})

	moduleURL = coreModuleRef
	moduleNoURL = false

	handler := newShellCallHandler(nil, &idtui.FrontendMock{})
	require.True(t, handler.noModule)
	require.Equal(t, moduleURLDefault, handler.moduleURL)
}

// TestAgentWorkspaceBaseline exercises a checkpoint-backed conversation end to
// end: its Git state can be previewed, its portable synchronization checkpoint
// survives save/restore, and explicit synchronization advances that checkpoint.
// It also locks in compatibility with metadata written before the baseline field.
func (DaggerCMDSuite) TestAgentWorkspaceBaseline(ctx context.Context, t *testctx.T) {
	workdir := filepath.Join(t.TempDir(), "work")
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1",
		"https://github.com/dagger/dagger-test-modules.git", workdir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	dag, err := dagger.Connect(ctx, dagger.WithWorkdir(workdir))
	require.NoError(t, err)
	t.Cleanup(func() { dag.Close() })

	// Trace restore starts from an intentionally unbound LLM. It keeps that
	// composition but has no baseline until an attached snapshot supplies one,
	// and previewing it must not issue an unbound workspace query.
	unboundSession := &LLMSession{dag: dag, plumbingCtx: ctx}
	unbound := unboundSession.newAgent("unbound")
	unboundSession.agents = []*sessionAgent{unbound}
	unboundSession.target = unbound
	require.NoError(t, unbound.setInitialLLM(dag.LLM(dagger.LLMOpts{Model: "openai/gpt-4o"})))
	require.Nil(t, unbound.lastSynced(nil))
	unboundSession.frontend = &idtui.FrontendMock{
		SetSidebarContentFunc: func(idtui.SidebarSection) {},
	}
	require.NoError(t, unbound.updateChangesPreview(unbound.llm))

	baseline, err := checkpointWorkspace(ctx, dag)
	require.NoError(t, err)
	starting := dag.LLM(dagger.LLMOpts{Model: "openai/gpt-4o"}).
		WithWorkspace(baseline).
		WithSystemPrompt("keep the original agent composition").
		WithTools(baseline)

	session := &LLMSession{dag: dag, plumbingCtx: ctx}
	agent := session.newAgent("baseline-test")
	session.agents = []*sessionAgent{agent}
	session.target = agent
	require.NoError(t, agent.setInitialLLM(starting))
	baselineID, err := baseline.ID(ctx)
	require.NoError(t, err)
	storedBaselineID, err := agent.lastSynced(nil).ID(ctx)
	require.NoError(t, err)
	require.Equal(t, baselineID, storedBaselineID,
		"the composed checkpoint, not NewLLMSession's temporary workspace, is the initial baseline")

	edited := starting.WithWorkspace(baseline.WithNewFile("agent.txt", "from agent\n"))
	require.NoError(t, agent.updateLLM(edited))

	var changesSection idtui.SidebarSection
	session.frontend = &idtui.FrontendMock{
		SetSidebarContentFunc: func(section idtui.SidebarSection) {
			if section.Title == "Changes" {
				changesSection = section
			}
		},
	}
	require.NoError(t, agent.updateChangesPreview(edited))
	require.Contains(t, changesSection.Body(80), "agent.txt")

	t.Run("separates Git state from staged commits", func(ctx context.Context, t *testctx.T) {
		workspace := baseline.
			WithNewFile("committed.txt", "committed\n").
			WithCommit("add committed file", "2026-08-18T00:00:00Z").
			WithNewFile("pending.txt", "pending\n")

		changes, err := idtui.PreviewWorkspaceChanges(ctx, dag, workspace)
		require.NoError(t, err)
		require.Len(t, changes.Uncommitted, 1)
		require.Equal(t, "pending.txt", changes.Uncommitted[0].Path)
		require.Len(t, changes.StagedCommits, 1)
		require.Len(t, changes.StagedCommits[0].Entries, 1)
		require.Equal(t, "committed.txt", changes.StagedCommits[0].Entries[0].Path)
	})

	t.Run("keeps a later edit to a staged path uncommitted", func(ctx context.Context, t *testctx.T) {
		workspace := baseline.
			WithNewFile("overlap.txt", "staged\n").
			WithCommit("add overlap file", "2026-08-18T00:00:00Z").
			WithNewFile("overlap.txt", "pending\n")

		changes, err := idtui.PreviewWorkspaceChanges(ctx, dag, workspace)
		require.NoError(t, err)
		require.Len(t, changes.Uncommitted, 1)
		require.Equal(t, "overlap.txt", changes.Uncommitted[0].Path)
		require.Len(t, changes.StagedCommits, 1)
		require.Len(t, changes.StagedCommits[0].Entries, 1)
		require.Equal(t, "overlap.txt", changes.StagedCommits[0].Entries[0].Path)
	})

	t.Run("includes unmanaged pending edits", func(ctx context.Context, t *testctx.T) {
		ignoredWorkdir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(ignoredWorkdir, ".gitignore"), []byte("*.previewignored\n"), 0o644))
		git := func(args ...string) {
			t.Helper()
			cmd := exec.CommandContext(ctx, "git", append([]string{"-C", ignoredWorkdir}, args...)...)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
		}
		git("init", "-q", "-b", "main")
		git("add", ".gitignore")
		git("-c", "user.name=Preview Test", "-c", "user.email=preview@localhost", "commit", "-qm", "initial")

		ignoredDag, err := dagger.Connect(ctx, dagger.WithWorkdir(ignoredWorkdir))
		require.NoError(t, err)
		t.Cleanup(func() { ignoredDag.Close() })
		workspace := ignoredDag.CurrentWorkspace().
			WithNewFile("artifact.previewignored", "pending but ignored\n")

		gitVisible, err := workspace.Git().Uncommitted().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, gitVisible, "ignored edit must not appear in git.uncommitted")
		unmanaged, err := workspace.Git().Unmanaged().AddedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"artifact.previewignored"}, unmanaged)

		changes, err := idtui.PreviewWorkspaceChanges(ctx, ignoredDag, workspace)
		require.NoError(t, err)
		require.Len(t, changes.Uncommitted, 1)
		require.Equal(t, "artifact.previewignored", changes.Uncommitted[0].Path)
	})
	session.frontend = nil

	// Auto-save persists a second portable recipe for the baseline. Reload it
	// through a different client to prove it is not an engine-local Workspace ID.
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	sessionID, err := agent.AutoSaveSession(ctx, "baseline persistence", "")
	require.NoError(t, err)
	sessionDir, err := getSessionDir()
	require.NoError(t, err)
	sessionFile := filepath.Join(sessionDir, sessionID+".json")
	data, err := os.ReadFile(sessionFile)
	require.NoError(t, err)
	var metadata sessionMetadata
	require.NoError(t, json.Unmarshal(data, &metadata))
	require.NotEmpty(t, metadata.WorkspaceBaselineID)

	dag2, err := dagger.Connect(ctx, dagger.WithWorkdir(workdir))
	require.NoError(t, err)
	t.Cleanup(func() { dag2.Close() })
	session2 := &LLMSession{dag: dag2, plumbingCtx: ctx}
	restored := session2.newAgent("restored")
	session2.agents = []*sessionAgent{restored}
	session2.target = restored
	require.NoError(t, restored.LoadSession(ctx, ctx, sessionID))
	added, err := restored.llm.Workspace().Changes(dagger.WorkspaceChangesOpts{
		From: restored.lastSynced(nil),
	}).AddedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"agent.txt"}, added,
		"restored pending changes must still be measured from the saved checkpoint")

	// Old metadata has no baseline. Resume conservatively compares the restored
	// workspace with itself rather than guessing currentWorkspace and risking an
	// unlike-host-root failure.
	metadata.WorkspaceBaselineID = ""
	data, err = json.MarshalIndent(metadata, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sessionFile, data, 0o600))
	legacy := session2.newAgent("legacy")
	session2.agents = append(session2.agents, legacy)
	session2.target = legacy
	require.NoError(t, legacy.LoadSession(ctx, ctx, sessionID))
	empty, err := legacy.llm.Workspace().Changes(dagger.WorkspaceChangesOpts{
		From: legacy.lastSynced(nil),
	}).IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)

	// Export and reset each install their fresh checkpoint as the new baseline.
	// .clear reuses that latest checkpoint while retaining the starting toolset.
	beforeExport := agent.lastSynced(nil)
	require.NoError(t, agent.ExportChanges(ctx))
	afterExport := agent.lastSynced(nil)
	require.NotSame(t, beforeExport, afterExport)
	contents, err := os.ReadFile(filepath.Join(workdir, "agent.txt"))
	require.NoError(t, err)
	require.Equal(t, "from agent\n", string(contents))

	wantTools, err := starting.Tools(ctx)
	require.NoError(t, err)
	agent.Clear()
	gotTools, err := agent.llm.Tools(ctx)
	require.NoError(t, err)
	require.Equal(t, wantTools, gotTools, ".clear must preserve the original tool composition")
	workspaceContents, err := agent.llm.Workspace().File("agent.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "from agent\n", workspaceContents, ".clear must use the latest synchronized checkpoint")

	dirty := agent.llm.Workspace().WithNewFile("discard.txt", "discard me\n")
	require.NoError(t, agent.updateLLM(agent.llm.WithWorkspace(dirty)))
	beforeReset := agent.lastSynced(nil)
	require.NoError(t, agent.ResetWorkspace(ctx))
	require.NotSame(t, beforeReset, agent.lastSynced(nil))
	entries, err := agent.llm.Workspace().Directory(".").Entries(ctx)
	require.NoError(t, err)
	require.NotContains(t, entries, "discard.txt")
}

func (DaggerCMDSuite) TestLLMFileSyncing(ctx context.Context, t *testctx.T) {
	if _, err := os.Stat("/dagger.env"); os.IsNotExist(err) {
		t.Skip(".env not configured")
	}

	testModDir := filepath.Join("testdata", "cmd-test")

	// run out of the module test dir
	t.Chdir(testModDir)

	// use .env file configured through module
	cp := exec.Command("cp", "/dagger.env", ".env")
	cp.Stdout = os.Stdout
	cp.Stderr = os.Stderr
	err := cp.Run()
	require.NoError(t, err)

	// connect (from test module dir, workdir)
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { dag.Close() })

	sidebarContent := map[string]idtui.SidebarSection{}
	handler := newShellCallHandler(dag, &idtui.FrontendMock{
		SetSidebarContentFunc: func(sec idtui.SidebarSection) {
			sidebarContent[sec.Title] = sec
		},
		SetStatusLineFunc: func(idtui.StatusLineData) {},
	})

	require.NoError(t, handler.Initialize(ctx))

	// runReact calls ReactToInput and runs async work synchronously (for testing).
	runReact := func(ev uv.KeyPressEvent) {
		work := handler.ReactToInput(ctx, ev, "", true)
		if work != nil {
			work()
		}
	}

	// set prompt to our test agent and switch to prompt mode
	handler.Handle(ctx, "agent=$(agent)")
	runReact(uv.KeyPressEvent{Text: ">", Code: '>'})

	// make a change
	handler.Handle(ctx, "Write 'apple' to fruit.txt.")

	sec, has := sidebarContent["Changes"]
	require.True(t, has, "Should have shown a Changes section in the sidebar.")
	require.Contains(t, sec.Body(80), "fruit.txt")

	// sync it down
	runReact(uv.KeyPressEvent{Code: 's', Mod: uv.ModCtrl})
	contents, err := os.ReadFile("fruit.txt")
	require.NoError(t, err)
	require.Contains(t, string(contents), "apple")

	// make our own changes
	require.NoError(t, os.WriteFile("fruit.txt", []byte("potato"), 0644))
	// make it unambiguously newer so we can detect the change
	future := time.Now().Add(time.Minute)
	require.NoError(t, os.Chtimes("fruit.txt", future, future))

	// sync them up
	runReact(uv.KeyPressEvent{Code: 'u', Mod: uv.ModCtrl})

	// check agent sees it
	handler.Handle(ctx, "What do you see in fruit.txt?")
	sess, err := handler.llm(ctx)
	require.NoError(t, err)
	reply, err := sess.Target().llm.LastReply(ctx)
	require.NoError(t, err)
	require.Contains(t, reply, "potato")

	handler.Handle(ctx, "Now write 'banana' to fruit.txt.")

	// NB: we had to set mtime to the future, but for this test we want to ensure
	// the file is considered even if it's stale, so chtimes it back to the past
	past := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes("fruit.txt", past, past))

	// blow away their changes — upload local changes to agent
	runReact(uv.KeyPressEvent{Code: 'u', Mod: uv.ModCtrl})

	// check agent sees it
	handler.Handle(ctx, "What do you see in fruit.txt now?")
	reply, err = sess.Target().llm.LastReply(ctx)
	require.NoError(t, err)
	require.Contains(t, reply, "potato")
}
