package core

// These tests cover the contextual next-step hints and SDK command layout.

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
	require.Contains(t, got, "dagger search --sdk")
	require.Contains(t, got, "dagger install <sdk-module>")
	require.Contains(t, got, "dagger sdk <sdk> module init <name>")

	_, statErr := os.Stat(filepath.Join(workdir, "dagger.toml"))
	require.True(t, os.IsNotExist(statErr), "setup should not create dagger.toml on an empty workspace")

	silentOut, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "setup", "--auto-apply")
	require.NoError(t, err, "%s", string(silentOut))
	require.NotContains(t, string(silentOut), "To get started")
}

// TestSDKInstallAndDynamicCommands verifies that generic install auto-detects
// an SDK, infers its concise command name, and exposes both capabilities under
// `dagger sdk <SDK>`.
func (CommandHintsSuite) TestSDKInstallAndDynamicCommands(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	installOut, err := hostDaggerExecRaw(ctx, t, workdir, "install", "github.com/dagger/go-sdk", "--auto-apply")
	require.NoError(t, err, "%s", string(installOut))

	config, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
	require.NoError(t, err)
	require.Contains(t, string(config), "[sdks.go]")
	require.Contains(t, string(config), `module = "go-sdk"`)

	helpOut, err := hostDaggerExecRaw(ctx, t, workdir, "sdk", "go", "--help")
	require.NoError(t, err, "%s", string(helpOut))
	require.Contains(t, string(helpOut), "Develop Dagger modules using the go SDK")
	require.Contains(t, string(helpOut), "Generate API clients using the go SDK")

	_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--auto-apply", "sdk", "go", "module", "init", "myapp")
	require.NoError(t, err)

	clientOut, err := hostDaggerExecRaw(ctx, t, workdir, "--auto-apply", "sdk", "go", "client", "init", "./myclient", ".dagger/modules/myapp")
	require.NoError(t, err, "%s", string(clientOut))
	require.NotContains(t, string(clientOut), "dagger generate",
		"client init generates the bindings, so it must not send the user to `dagger generate`")

	infoOut, err := hostDaggerExecRaw(ctx, t, workdir, "sdk", "go", "info")
	require.NoError(t, err, "%s", string(infoOut))
	require.Contains(t, string(infoOut), "sdk-name: go")
	require.Contains(t, string(infoOut), "module-name: go-sdk")
	require.Contains(t, string(infoOut), "module-source: github.com/dagger/go-sdk")
	require.Contains(t, string(infoOut), "claimed-modules: 1")
}
