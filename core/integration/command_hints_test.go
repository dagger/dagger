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

// Init creates the workspace config, then prints executable next steps.
// Repeated initialization preserves the config.
func (CommandHintsSuite) TestInitHint(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	out, err := hostDaggerExecRaw(ctx, t, workdir, "init", "--auto-apply")
	require.NoError(t, err, "%s", string(out))
	require.Contains(t, string(out), "Workspace configuration initialized at ./dagger.toml")
	require.Contains(t, string(out), "#!/bin/sh")
	require.Contains(t, string(out), "dagger module recommend")
	require.Contains(t, string(out), "dagger cloud checks on")

	data, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
	require.NoError(t, err)
	require.Empty(t, data)

	out, err = hostDaggerExecRaw(ctx, t, workdir, "init", "--auto-apply")
	require.NoError(t, err, "%s", string(out))
	require.Contains(t, string(out), "Workspace configuration found at ./dagger.toml")
}
