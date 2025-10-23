package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	tea "github.com/charmbracelet/bubbletea"
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
	})

	require.NoError(t, handler.Initialize(ctx))

	// set prompt to our test agent and switch to prompt mode
	handler.Handle(ctx, "agent=$(agent)")
	handler.ReactToInput(ctx, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'>'},
	})()

	// make a change
	handler.Handle(ctx, "Write 'apple' to fruit.txt.")

	sec, has := sidebarContent["Changes"]
	require.True(t, has, "Should have shown a Changes section in the sidebar.")
	require.Contains(t, sec.Body(80), "fruit.txt")

	// sync it down
	handler.ReactToInput(ctx, tea.KeyMsg{
		Type: tea.KeyCtrlS,
	})()
	contents, err := os.ReadFile("fruit.txt")
	require.NoError(t, err)
	require.Contains(t, string(contents), "apple")

	// make our own changes
	require.NoError(t, os.WriteFile("fruit.txt", []byte("potato"), 0644))
	// make it unambiguously newer so we can detect the change
	future := time.Now().Add(time.Minute)
	require.NoError(t, os.Chtimes("fruit.txt", future, future))

	// sync them up
	handler.ReactToInput(ctx, tea.KeyMsg{
		Type: tea.KeyCtrlU,
	})()

	// check agent sees it
	handler.Handle(ctx, "What do you see in fruit.txt?")
	sess, err := handler.llm(ctx)
	require.NoError(t, err)
	reply, err := sess.llm.LastReply(ctx)
	require.NoError(t, err)
	require.Contains(t, reply, "potato")

	handler.Handle(ctx, "Now write 'banana' to fruit.txt.")

	// NB: we had to set mtime to the future, but for this test we want to ensure
	// the file is considered even if it's stale, so chtimes it back to the past
	past := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes("fruit.txt", past, past))

	// blow away their changes
	handler.ReactToInput(ctx, tea.KeyMsg{
		Type: tea.KeyCtrlU,
	})()

	// check agent sees it
	handler.Handle(ctx, "What do you see in fruit.txt now?")
	reply, err = sess.llm.LastReply(ctx)
	require.NoError(t, err)
	require.Contains(t, reply, "potato")
}
