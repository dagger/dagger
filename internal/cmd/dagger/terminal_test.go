package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerminalCommandRequiresTarget(t *testing.T) {
	oldListMode := terminalListMode
	t.Cleanup(func() { terminalListMode = oldListMode })
	terminalListMode = false

	err := runTerminalCommand(terminalCmd, nil)
	require.EqualError(t, err, "terminal target required; use 'dagger terminal -l' to list available targets")
}
