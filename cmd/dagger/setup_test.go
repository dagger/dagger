package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSetupConfirmUsesProvidedWriters(t *testing.T) {
	oldAutoApply := autoApply
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdin = r
	t.Cleanup(func() {
		autoApply = oldAutoApply
		os.Stdin = oldStdin
		require.NoError(t, r.Close())
		require.NoError(t, w.Close())
	})

	autoApply = false

	var cmdOut, cmdErr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&cmdOut)
	cmd.SetErr(&cmdErr)

	var setupOut, setupErr bytes.Buffer
	require.False(t, confirm(cmd, &setupOut, &setupErr, "Install modules?"))

	require.Contains(t, setupOut.String(), "Install modules? [skipped: non-interactive")
	require.Empty(t, setupErr.String())
	require.Empty(t, cmdOut.String())
	require.Empty(t, cmdErr.String())
}
