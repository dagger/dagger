package core

// These tests cover the contextual next-step hints the CLI prints on the
// success path of the authoring commands: `dagger setup` (empty workspace),
// `dagger sdk install` (per SDK capability), and `dagger api client init`.

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
// dagger.toml, and that --silent suppresses the hint.
func (CommandHintsSuite) TestEmptySetupHint(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	out, err := hostDaggerExecRaw(ctx, t, workdir, "setup", "--auto-apply")
	require.NoError(t, err, "%s", string(out))

	got := string(out)
	require.Contains(t, got, "nothing to migrate")
	require.Contains(t, got, "dagger install <module>")
	require.Contains(t, got, "dagger sdk install <sdk>")
	require.Contains(t, got, "dagger module init <sdk> <name>")

	_, statErr := os.Stat(filepath.Join(workdir, "dagger.toml"))
	require.True(t, os.IsNotExist(statErr), "setup should not create dagger.toml on an empty workspace")

	silentOut, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "setup", "--auto-apply")
	require.NoError(t, err, "%s", string(silentOut))
	require.NotContains(t, string(silentOut), "To get started")
}

// TestSDKInstallAndClientInitHints verifies that `dagger sdk install go` prints
// a hint for each capability the go SDK has (it authors both modules and
// clients), and that `dagger api client init` points the user at
// `dagger generate` once the client is scaffolded.
func (CommandHintsSuite) TestSDKInstallAndClientInitHints(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	installOut, err := hostDaggerExecRaw(ctx, t, workdir, "sdk", "install", "go")
	require.NoError(t, err, "%s", string(installOut))

	got := string(installOut)
	require.Contains(t, got, "This SDK can")
	require.Contains(t, got, "dagger module init go")
	require.Contains(t, got, "dagger api client init go")

	_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--auto-apply", "module", "init", "go", "myapp")
	require.NoError(t, err)

	clientOut, err := hostDaggerExecRaw(ctx, t, workdir, "--auto-apply", "api", "client", "init", "go", "./myclient", ".dagger/modules/myapp")
	require.NoError(t, err, "%s", string(clientOut))
	require.Contains(t, string(clientOut), "dagger generate")
}

// TestUninstalledSDKInitHint verifies that `dagger module init <sdk> <name>`
// for a registry-known but uninstalled SDK, in a workspace with no dagger.toml,
// fails with a hint to install the SDK rather than a generic unknown-command
// error. Names the registry doesn't know keep the generic cobra error.
func (CommandHintsSuite) TestUninstalledSDKInitHint(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	out, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", "go", "myapp")
	require.Error(t, err, "%s", string(out))
	require.Contains(t, string(out), "dagger sdk install go")
	require.NotContains(t, string(out), `unknown command "go"`)

	unknownOut, err := hostDaggerExecRaw(ctx, t, workdir, "module", "init", "definitely-not-an-sdk", "myapp")
	require.Error(t, err, "%s", string(unknownOut))
	require.Contains(t, string(unknownOut), `unknown command "definitely-not-an-sdk"`)
}
