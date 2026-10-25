package daggercmd

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestCommandProgressDefault(t *testing.T) {
	oldOrg := cloudOrgFlag
	t.Cleanup(func() { cloudOrgFlag = oldOrg })
	cloudOrgFlag = "unchanged"
	// The session command keeps streaming plain progress for its SDK
	// consumers, however it's spelled: bare, with global flags before the
	// subcommand, or through the `api` group.
	require.Equal(t, "plain", commandProgressDefault([]string{"session"}))
	require.Equal(t, "plain", commandProgressDefault([]string{"--org", "acme", "session"}))
	require.Equal(t, "plain", commandProgressDefault([]string{"api", "session"}))

	// Unannotated commands (and unresolvable command lines) fall through to
	// the regular defaults.
	require.Empty(t, commandProgressDefault([]string{"call"}))
	require.Empty(t, commandProgressDefault(nil))
	require.Equal(t, "unchanged", cloudOrgFlag, "looking up a command must not apply its flags")
}

func TestCloudOrgFlagHidden(t *testing.T) {
	flag := rootCmd.PersistentFlags().Lookup("org")
	require.NotNil(t, flag)
	require.True(t, flag.Hidden)
}

func TestIsObviouslyRemoteWorkspaceRef(t *testing.T) {
	require.True(t, isObviouslyRemoteWorkspaceRef("github.com/acme/mono"))
	require.True(t, isObviouslyRemoteWorkspaceRef("git@github.com:acme/mono"))
	require.True(t, isObviouslyRemoteWorkspaceRef("https://github.com/acme/mono"))

	require.False(t, isObviouslyRemoteWorkspaceRef(""))
	require.False(t, isObviouslyRemoteWorkspaceRef("./services/api"))
	require.False(t, isObviouslyRemoteWorkspaceRef("../services/api"))
	require.False(t, isObviouslyRemoteWorkspaceRef("/srv/services/api"))

	// A dot below the first path segment names a directory, not a host, and a
	// lone dotted token stays local however it reads.
	require.False(t, isObviouslyRemoteWorkspaceRef("services/api.v2"))
	require.False(t, isObviouslyRemoteWorkspaceRef("common/.dagger/mymod"))
	require.False(t, isObviouslyRemoteWorkspaceRef("my.dir"))
}

func TestCanOpenShellOnError(t *testing.T) {
	// Only the pretty TUI can run the shell, and it needs a terminal to read
	// keys from.
	require.True(t, canOpenShellOnError("tty", true))

	require.False(t, canOpenShellOnError("tty", false))
	// The report frontend is what an AI agent gets.
	require.False(t, canOpenShellOnError("report", true))
	require.False(t, canOpenShellOnError("plain", true))
	require.False(t, canOpenShellOnError("dots", true))
	require.False(t, canOpenShellOnError("logs", true))
}

func TestWrapCmdDescriptionBoldsNameOnly(t *testing.T) {
	oldProfile := helpProfile
	helpProfile = func() termenv.Profile { return termenv.ANSI }
	t.Cleanup(func() { helpProfile = oldProfile })

	line := wrapCmdDescription("init", "Initialize a workspace", 11)
	require.Equal(t, "  \x1b[1minit\x1b[0m          Initialize a workspace", line)
}

func TestWrapCmdDescriptionPlainWithNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	line := wrapCmdDescription("init", "Initialize a workspace", 11)
	require.Equal(t, "  init          Initialize a workspace", line)
}

func TestWrapCmdDescriptionPlainInPipe(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "")
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { r.Close(); w.Close() })
	oldStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	line := wrapCmdDescription("init", "Initialize a workspace", 11)
	require.Equal(t, "  init          Initialize a workspace", line)
}

func TestWrapCmdDescriptionAlignsWrappedDescription(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("word ", getViewWidth()/5+5))
	short := ansi.Strip(wrapCmdDescription("module, mod", "Install modules", 11))
	lines := strings.Split(ansi.Strip(wrapCmdDescription("init", long, 11)), "\n")
	require.Greater(t, len(lines), 1)

	column := strings.Index(short, "Install")
	require.Equal(t, column, strings.Index(lines[0], "word"))
	for _, line := range lines[1:] {
		require.Equal(t, column, len(line)-len(strings.TrimLeft(line, " ")))
	}
}
