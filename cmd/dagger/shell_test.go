package main

import (
	"context"
	"testing"

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

func (DaggerCMDSuite) TestShellPromptFileSyncing(ctx context.Context, t *testctx.T) {
	dag, err := dagger.Connect(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { dag.Close() })

	handler := &shellCallHandler{
		dag:      dag,
		llmModel: llmModel,
		mode:     modeShell,
	}

	require.NoError(t, handler.Initialize(ctx))

	// TODO: extraneous assertion, depending on test scope
	require.Equal(t, idtui.UpdatePromptMsg{},
		handler.ReactToInput(ctx, tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{'>'},
		})())

	handler.Handle(ctx, "")
}
