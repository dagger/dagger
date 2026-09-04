package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandProgressDefault(t *testing.T) {
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
