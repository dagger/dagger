package gitutil

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotBackedRepoConfig(t *testing.T) {
	capture := func(args *[]string) Option {
		return WithExec(func(_ context.Context, cmd *exec.Cmd) error {
			*args = cmd.Args
			return nil
		})
	}

	t.Run("off by default", func(t *testing.T) {
		var args []string
		_, err := NewGitCLI(capture(&args)).Run(context.Background(), "status")
		require.NoError(t, err)
		require.NotContains(t, strings.Join(args, " "), "core.checkStat")
	})

	t.Run("relaxes the stat fields snapshots invalidate", func(t *testing.T) {
		var args []string
		_, err := NewGitCLI(capture(&args), WithSnapshotBackedRepo()).Run(context.Background(), "status")
		require.NoError(t, err)
		joined := strings.Join(args, " ")
		require.Contains(t, joined, "core.checkStat=minimal")
		require.Contains(t, joined, "core.trustctime=false")
	})
}
