package daggercmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleRecommendCommand(t *testing.T) {
	root := testRootCommand()
	for _, name := range []string{"module", "mod"} {
		cmd, args, err := root.Find([]string{name, "recommend"})
		require.NoError(t, err)
		require.Same(t, moduleRecommendCmd, cmd)
		require.Empty(t, args)
		require.NoError(t, cmd.Args(cmd, nil))
		require.Error(t, cmd.Args(cmd, []string{"extra"}))
		require.NoError(t, validateFlagCapabilities(root, []string{name, "recommend", "-W", "/tmp/workspace", "-y"}))
		require.ErrorContains(t, validateFlagCapabilities(root, []string{name, "recommend", "--env", "ci"}), "flag --env is not supported")
		require.ErrorContains(t, validateFlagCapabilities(root, []string{"--env=ci", name, "recommend", "--auto-apply"}), "flag --env is not supported")
	}
	require.True(t, commandShowsFinalProgress(moduleRecommendCmd))
	require.Equal(t, workspaceFlagPolicyLocalOnly, workspaceFlagPolicy(moduleRecommendCmd, nil))
	help := renderHelp(t, moduleRecommendCmd)
	require.Contains(t, help, "--auto-apply")
	require.Contains(t, help, "--workspace")
	require.NotContains(t, help, "--env")
}

func TestSelectRecommendedModulesAutoApply(t *testing.T) {
	previous := autoApply
	autoApply = true
	t.Cleanup(func() { autoApply = previous })
	recs := []recommendation{
		{Module: registryModule{Name: "ruff", Repo: "dagger.io/python/ruff"}, Match: "ruff.toml"},
		{Module: registryModule{Name: "go", Repo: "dagger.io/go"}, Match: "go.mod"},
	}
	selected, err := selectRecommendedModules(t.Context(), recs)
	require.NoError(t, err)
	require.Equal(t, recs, selected)
}

func TestSelectRecommendedModulesNonInteractive(t *testing.T) {
	previousAutoApply, previousStdin := autoApply, os.Stdin
	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	autoApply, os.Stdin = false, stdin
	t.Cleanup(func() {
		autoApply, os.Stdin = previousAutoApply, previousStdin
		require.NoError(t, stdin.Close())
	})
	selected, err := selectRecommendedModules(t.Context(), []recommendation{
		{Module: registryModule{Name: "ruff", Repo: "dagger.io/python/ruff"}, Match: "ruff.toml"},
	})
	require.NoError(t, err)
	require.Empty(t, selected)
}
