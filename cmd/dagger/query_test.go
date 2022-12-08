package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpdir := t.TempDir()
	tmpfile := tmpdir + "/test.graphql"
	err := os.WriteFile(tmpfile, []byte(`query Test{defaultPlatform}`), 0600)
	require.NoError(t, err)

	cmd := rootCmd()
	cmd.SetArgs([]string{
		"query",
		"--doc", tmpfile,
		"Test",
	})

	output := bytes.NewBuffer(nil)
	cmd.SetOut(output)

	err = cmd.ExecuteContext(ctx)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf(`{
    "defaultPlatform": "%s/%s"
}
`, runtime.GOOS, runtime.GOARCH), output.String())
}
