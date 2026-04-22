package core

// Workspace alignment: mostly aligned; coverage targets post-workspace private dependency access, though setup still relies on historical module helpers.
// Scope: SSH agent handling and private repository access for module loading and non-Dagger language dependencies.
// Intent: Keep authenticated/private dependency behavior separate from the remaining runtime, SDK, and client/session coverage in the historical umbrella suite.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestSSHAgentConnection(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("ConcurrentSetupAndCleanup", func(ctx context.Context, t *testctx.T) {
			var wg sync.WaitGroup
			for range 100 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, cleanup := setupPrivateRepoSSHAgent(t)
					time.Sleep(10 * time.Millisecond) // Simulate some work
					cleanup()
				}()
			}
			wg.Wait()
		})
	})
}

func (ModuleSuite) TestSSHAuthSockPathHandling(ctx context.Context, t *testctx.T) {
	tc := getVCSTestCase(t, "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")

	t.Run("SSH auth with home expansion and symlink", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithEnvVariable("HOME", "/home/dagger").
			WithExec([]string{"ln", "-s", "/sock/unix-socket", "/home/dagger/.ssh-sock"}).
			WithEnvVariable("SSH_AUTH_SOCK", "~/.ssh-sock")

		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithExec([]string{"sh", "-c", "cd", "/work/some/subdir"}).
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})

	t.Run("SSH auth from different relative paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/work/subdir"})

		out, err := ctr.
			WithWorkdir("/sock").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		out, err = ctr.
			WithWorkdir("/work/subdir").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		out, err = ctr.
			WithWorkdir("/").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})
}

func (ModuleSuite) TestPrivateDeps(ctx context.Context, t *testctx.T) {
	t.Run("golang", func(ctx context.Context, t *testctx.T) {
		privateDepCode := `package main

import (
	"github.com/dagger/dagger-test-modules/privatedeps/pkg/cooldep"
)

type Foo struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Foo) HowCoolIsDagger() string {
	return cooldep.HowCoolIsThat
}
`

		daggerjson := `{
  "name": "foo",
  "engineVersion": "v0.16.2",
  "sdk": {
    "source": "go",
    "config": {
      "goprivate": "github.com/dagger/dagger-test-modules"
    }
  }
}`

		c := connect(ctx, t)
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		socket := c.Host().UnixSocket(sockPath)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithExec([]string{"apk", "add", "git", "openssh", "openssl"}).
			WithUnixSocket("/sock/unix-socket", socket).
			WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket").
			WithWorkdir("/work").
			WithNewFile("/root/.gitconfig", `
[url "ssh://git@github.com/"]
	insteadOf = https://github.com/
`).
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			WithNewFile("main.go", privateDepCode).
			WithNewFile("dagger.json", daggerjson)

		howCoolIsDagger, err := modGen.
			With(daggerExec("call", "how-cool-is-dagger")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ubercool", howCoolIsDagger)
	})
}
