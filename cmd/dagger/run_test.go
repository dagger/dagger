package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := rootCmd()
	cmd.SetArgs([]string{
		"run",
		"--",
		"env",
	})

	output := bytes.NewBuffer(nil)
	cmd.SetOut(output)

	err := cmd.ExecuteContext(ctx)
	require.NoError(t, err)

	require.Regexp(t, `DAGGER_SESSION_URL=http://localhost:\d+/query\n`, output.String())
	require.Regexp(t, `DAGGER_SESSION_TOKEN=[0-9a-f-]+\n`, output.String())
}
