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
