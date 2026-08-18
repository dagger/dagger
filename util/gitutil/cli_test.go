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

func TestStallTimeouts(t *testing.T) {
	capture := func(cmd **exec.Cmd) Option {
		return WithExec(func(_ context.Context, c *exec.Cmd) error {
			*cmd = c
			return nil
		})
	}
	sshCommand := func(cmd *exec.Cmd) string {
		for _, env := range cmd.Env {
			if after, ok := strings.CutPrefix(env, "GIT_SSH_COMMAND="); ok {
				return after
			}
		}
		return ""
	}

	t.Run("off by default", func(t *testing.T) {
		var cmd *exec.Cmd
		_, err := NewGitCLI(capture(&cmd)).Run(context.Background(), "fetch")
		require.NoError(t, err)
		require.NotContains(t, strings.Join(cmd.Args, " "), "http.lowSpeed")
		require.NotContains(t, sshCommand(cmd), "ServerAliveInterval")
	})

	t.Run("bounds http and ssh stalls", func(t *testing.T) {
		var cmd *exec.Cmd
		_, err := NewGitCLI(capture(&cmd), WithStallTimeouts()).Run(context.Background(), "fetch")
		require.NoError(t, err)
		joined := strings.Join(cmd.Args, " ")
		require.Contains(t, joined, "http.lowSpeedLimit=1000")
		require.Contains(t, joined, "http.lowSpeedTime=60")
		require.Contains(t, sshCommand(cmd), "-o ConnectTimeout=30")
		require.Contains(t, sshCommand(cmd), "-o ServerAliveInterval=30 -o ServerAliveCountMax=3")
	})
}
