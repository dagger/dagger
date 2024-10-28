package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/testctx"
)

type GitCredentialSuite struct{}

func TestGitCredential(t *testing.T) {
	testctx.Run(testCtx, t, GitCredentialSuite{}, Middleware()...)
}

// Integration tests of the real feature
// Needs some more testing (without nested exec, as we only isolate the main client)
func (GitCredentialSuite) TestGitCredentialErrors(ctx context.Context, t *testctx.T) {
	// c := connect(ctx, t)

	// 	t.Run("INVALID_REQUEST", func(ctx context.Context, t *testctx.T) {
	// 		container := c.Container().
	// 			From("golang:1.16").
	// 			WithExec([]string{"apt-get", "update"}).
	// 			WithExec([]string{"apt-get", "install", "-y", "git"}).
	// 			WithEnvVariable("HOME", "/root").
	// 			WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig")

	// 		out, err := container.
	// 			WithExec([]string{"git", "credential", "fill"}).
	// 			Stdout(ctx)

	// 		require.NoError(t, err)
	// 		require.Contains(t, out, "Host and protocol are required")
	// 	})

	// 	t.Run("NO_GIT", func(ctx context.Context, t *testctx.T) {
	// 		container := c.Container().
	// 			From("golang:1.16").
	// 			WithExec([]string{"apt-get", "update"}).
	// 			WithExec([]string{"apt-get", "install", "-y", "git"}).
	// 			WithExec([]string{"mv", "/usr/bin/git", "/usr/bin/git_temp"})

	// 		out, err := container.
	// 			WithExec([]string{"/usr/bin/git", "credential", "fill"}).
	// 			Stdout(ctx)

	// 		require.NoError(t, err)
	// 		require.Contains(t, out, "Git is not installed or not in PATH")
	// 	})

	// 	t.Run("NO_CREDENTIAL_HELPER", func(ctx context.Context, t *testctx.T) {
	// 		container := c.Container().
	// 			From("golang:1.16").
	// 			WithExec([]string{"apt-get", "update"}).
	// 			WithExec([]string{"apt-get", "install", "-y", "git"}).
	// 			WithEnvVariable("HOME", "/root").
	// 			WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig").
	// 			WithExec([]string{"git", "config", "--global", "--unset", "credential.helper"})

	// 		out, err := container.
	// 			WithExec([]string{"git", "credential", "fill"}).
	// 			Stdout(ctx)

	// 		require.NoError(t, err)
	// 		require.Contains(t, out, "Failed to retrieve credentials: exit status 128")
	// 	})

	// 	t.Run("TIMEOUT", func(ctx context.Context, t *testctx.T) {
	// 		container := c.Container().
	// 			From("golang:1.16").
	// 			WithExec([]string{"apt-get", "update"}).
	// 			WithExec([]string{"apt-get", "install", "-y", "git"}).
	// 			WithEnvVariable("HOME", "/root").
	// 			WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig").
	// 			WithNewFile("/tmp/slow_helper.sh", `#!/bin/sh
	// sleep 31
	// `).
	// 			WithExec([]string{"chmod", "+x", "/tmp/slow_helper.sh"}).
	// 			WithExec([]string{"git", "config", "--global", "credential.helper", "/tmp/slow_helper.sh"})

	// 		out, err := container.
	// 			WithExec([]string{"git", "credential", "fill"}).
	// 			Stdout(ctx)

	// 		require.NoError(t, err)
	// 		require.Contains(t, out, "Git credential command timed out")
	// 	})

	// 	t.Run("CREDENTIAL_RETRIEVAL_FAILED", func(ctx context.Context, t *testctx.T) {
	// 		container := c.Container().
	// 			From("golang:1.16").
	// 			WithExec([]string{"apt-get", "update"}).
	// 			WithExec([]string{"apt-get", "install", "-y", "git"}).
	// 			WithEnvVariable("HOME", "/root").
	// 			WithEnvVariable("GIT_CONFIG_GLOBAL", "/root/.gitconfig").
	// 			WithNewFile("/tmp/git-input", "protocol=https\nhost=nonexistent.com\n\n")

	// 		out, err := container.
	// 			WithExec([]string{"sh", "-c", "cat /tmp/git-input | git credential fill"}).
	// 			Stdout(ctx)

	//		require.NoError(t, err)
	//		require.Contains(t, out, "Failed to retrieve credentials: exit status 128")
	//	})
}
