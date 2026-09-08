package core

// These tests cover contextual next-step hints.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type CommandHintsSuite struct{}

func TestCommandHints(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CommandHintsSuite{})
}

// TestEmptySetupHint verifies that `dagger setup` on a greenfield workspace
// (nothing to migrate, no config) prints the get-started hint, writes no
// dagger.toml, and that DAGGER_SILENT suppresses the hint (`dagger setup`
// renders no pipeline, so it does not take --silent).
func (CommandHintsSuite) TestEmptySetupHint(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	out, err := hostDaggerExecRaw(ctx, t, workdir, "setup", "--auto-apply")
	require.NoError(t, err, "%s", string(out))

	got := string(out)
	require.Contains(t, got, "nothing to migrate")
	require.Contains(t, got, "dagger module install <module>")
	require.Contains(t, got, "dagger module search <sdk>")
	require.Contains(t, got, "dagger module install <sdk-module>")
	require.Contains(t, got, "dagger module init <sdk>")

	_, statErr := os.Stat(filepath.Join(workdir, "dagger.toml"))
	require.True(t, os.IsNotExist(statErr), "setup should not create dagger.toml on an empty workspace")

	silentCmd := hostDaggerCommandRaw(ctx, t, workdir, "setup", "--auto-apply")
	silentCmd.Env = append(silentCmd.Env, "DAGGER_SILENT=true")
	silentOut, err := silentCmd.CombinedOutput()
	require.NoError(t, err, "%s", string(silentOut))
	require.NotContains(t, string(silentOut), "To get started")
}
