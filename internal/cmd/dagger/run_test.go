package daggercmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunClientParamsVersion(t *testing.T) {
	oldVersion := runVersion
	t.Cleanup(func() { runVersion = oldVersion })

	for _, args := range [][]string{{"api", "with-session"}, {"api", "exec"}, {"run"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd, _, err := rootCmd.Find(args)
			require.NoError(t, err)
			flag := cmd.Flags().Lookup("version")
			require.NotNil(t, flag)
			require.True(t, flag.Hidden)

			runVersion = ""
			require.Empty(t, runClientParams("secret").Version)

			require.NoError(t, cmd.ParseFlags([]string{"--version", "v1.2.3", "go", "run", "."}))
			params := runClientParams("secret")
			require.Equal(t, "secret", params.SecretToken)
			require.Equal(t, "v1.2.3", params.Version)
		})
	}
}
