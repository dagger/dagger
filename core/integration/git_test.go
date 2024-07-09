package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

type GitSuite struct{}

func TestGit(t *testing.T) {
	testctx.Run(testCtx, t, GitSuite{}, Middleware()...)
}

func (GitSuite) TestGit(ctx context.Context, t *testctx.T) {
	type result struct {
		Commit string
		Tree   struct {
			File struct {
				Contents string
			}
		}
	}

	res := struct {
		Git struct {
			Head   result
			Ref    result
			Commit result
			Branch result
			Tag    result
		}
	}{}

	err := testutil.Query(t,
		`{
			git(url: "github.com/dagger/dagger", keepGitDir: true) {
				head {
					commit
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
				ref(name: "refs/heads/main") {
					commit
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
				commit(id: "c80ac2c13df7d573a069938e01ca13f7a81f0345") {
					commit
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
				branch(name: "main") {
					commit
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
				tag(name: "v0.9.5") {
					commit
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)

	// head
	require.NotEmpty(t, res.Git.Head.Commit)
	require.Contains(t, res.Git.Head.Tree.File.Contents, "Dagger")
	mainCommit := res.Git.Head.Commit

	// refs/heads/main
	require.Equal(t, mainCommit, res.Git.Ref.Commit)
	require.Contains(t, res.Git.Ref.Tree.File.Contents, "Dagger")

	// c80ac2c13df7d573a069938e01ca13f7a81f0345
	require.Equal(t, res.Git.Commit.Commit, "c80ac2c13df7d573a069938e01ca13f7a81f0345")
	require.Contains(t, res.Git.Commit.Tree.File.Contents, "Dagger")

	// main
	require.NotEmpty(t, mainCommit, res.Git.Branch.Commit)
	require.Contains(t, res.Git.Branch.Tree.File.Contents, "Dagger")

	// v0.9.5
	require.Equal(t, res.Git.Tag.Commit, "9ea5ea7c848fef2a2c47cce0716d5fcb8d6bedeb")
	require.Contains(t, res.Git.Tag.Tree.File.Contents, "Dagger")
}

func (GitSuite) TestSSHAuthSock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitSSH := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git", "openssh"})

	hostKeyGen := gitSSH.
		WithExec([]string{
			"ssh-keygen", "-t", "rsa", "-b", "4096", "-f", "/root/.ssh/host_key", "-N", "",
		}).
		WithExec([]string{
			"ssh-keygen", "-t", "rsa", "-b", "4096", "-f", "/root/.ssh/id_rsa", "-N", "",
		}).
		WithExec([]string{
			"cp", "/root/.ssh/id_rsa.pub", "/root/.ssh/authorized_keys",
		})

	hostPubKey, err := hostKeyGen.File("/root/.ssh/host_key.pub").Contents(ctx)
	require.NoError(t, err)

	userPrivateKey, err := hostKeyGen.File("/root/.ssh/id_rsa").Contents(ctx)
	require.NoError(t, err)

	setupScript := c.Directory().
		WithNewFile("setup.sh", `#!/bin/sh

set -e -u -x

cd /root
mkdir repo

cd repo
git init
git branch -m main
echo test >> README.md
git add README.md
git config --global user.email "root@localhost"
git config --global user.name "Test User"
git commit -m "init"

chmod 0600 ~/.ssh/host_key
$(which sshd) -h ~/.ssh/host_key -p 2222

sleep infinity
`).
		File("setup.sh")

	key, err := ssh.ParseRawPrivateKey([]byte(userPrivateKey))
	require.NoError(t, err)

	sshAgent := agent.NewKeyring()
	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: key,
	})
	require.NoError(t, err)

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "agent.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer l.Close()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				break
			}

			t.Log("agent serving")

			err = agent.ServeAgent(sshAgent, c)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Logf("serve agent: %s", err)
				panic(err)
			}
		}
	}()

	sshPort := 2222
	sshSvc := hostKeyGen.
		WithMountedFile("/root/start.sh", setupScript).
		WithExposedPort(sshPort).
		WithExec([]string{"sh", "/root/start.sh"}).
		AsService()

	sshHost, err := sshSvc.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("ssh://root@%s:%d/root/repo", sshHost, sshPort)
	entries, err := c.Git(repoURL, dagger.GitOpts{
		ExperimentalServiceHost: sshSvc,
		SSHKnownHosts:           fmt.Sprintf("[%s]:%d %s", sshHost, sshPort, strings.TrimSpace(hostPubKey)),
		SSHAuthSocket:           c.Host().UnixSocket(sock),
	}).
		Branch("main").
		Tree().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)
}

func (GitSuite) TestGitTagsWithAndWithoutSSHAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// private key with only read rights to our private modules test repo
	userPrivateKey := `-----BEGIN OPENSSH PRIVATE KEY-----
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
-----END OPENSSH PRIVATE KEY-----
`

	key, err := ssh.ParseRawPrivateKey([]byte(userPrivateKey))
	require.NoError(t, err)

	sshAgent := agent.NewKeyring()
	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: key,
	})
	require.NoError(t, err)

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "agent.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	// Ensure the listener is closed when the test is done
	t.Cleanup(func() {
		l.Close()
	})

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				break
			}

			t.Log("agent serving")

			err = agent.ServeAgent(sshAgent, c)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Logf("serve agent: %s", err)
				panic(err)
			}
		}
	}()

	conn, err := net.Dial("unix", sock)
	require.NoError(t, err)
	defer conn.Close()

	// ensure test environment does not contain this env var, as client might inherit it
	os.Unsetenv("SSH_AUTH_SOCK")

	repoURL := "git@gitlab.com:dagger-modules/private/test/more/dagger-test-modules-private.git"

	// Test fetching tags with SSH authentication
	t.Run("with SSH auth", func(ctx context.Context, t *testctx.T) {
		tags, err := c.Git(repoURL, dagger.GitOpts{
			SSHAuthSocket: c.Host().UnixSocket(sock),
		}).Tags(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"cool-sdk/v0.1", "v0.1.1"}, tags)
	})

	// Test fetching tags without SSH authentication
	t.Run("without SSH auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL).Tags(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Permission denied (publickey)")
	})
}

func (GitSuite) TestAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	gitDaemon, repoURL := gitServiceHTTPWithBranch(ctx, t, c, c.Directory().WithNewFile("README.md", "Hello, world!"), "main", c.SetSecret("target", "foobar"))

	t.Run("no auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.ErrorContains(t, err, "git error")
		require.ErrorContains(t, err, "failed to fetch remote")
	})

	t.Run("incorrect auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			WithAuthToken(c.SetSecret("token-wrong", "wrong")).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.ErrorContains(t, err, "git error")
		require.ErrorContains(t, err, "failed to fetch remote")
	})

	t.Run("token auth", func(ctx context.Context, t *testctx.T) {
		dt, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			WithAuthToken(c.SetSecret("token", "foobar")).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, world!", dt)
	})

	t.Run("header auth", func(ctx context.Context, t *testctx.T) {
		dt, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			WithAuthHeader(c.SetSecret("header", "basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:foobar")))).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, world!", dt)
	})
}

func (GitSuite) TestKeepGitDir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git dir is present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree()
		ent, _ := dir.Entries(ctx)
		require.Contains(t, ent, ".git")
	})

	t.Run("git dir is not present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree()
		ent, _ := dir.Entries(ctx)
		require.NotContains(t, ent, ".git")
	})
}

func (GitSuite) TestServiceStableDigest(ctx context.Context, t *testctx.T) {
	content := identity.NewID()
	hostname := func(c *dagger.Client) string {
		svc, url := gitService(ctx, t, c,
			c.Directory().WithNewFile("content", content))

		hn, err := c.Container().
			From(alpineImage).
			WithMountedDirectory("/repo", c.Git(url, dagger.GitOpts{
				ExperimentalServiceHost: svc,
			}).Branch("main").Tree()).
			AsService().
			Hostname(ctx)
		require.NoError(t, err)
		return hn
	}

	c1 := connect(ctx, t)
	c2 := connect(ctx, t)
	require.Equal(t, hostname(c1), hostname(c2))
}

func (GitSuite) TestGitTags(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("all tags", func(ctx context.Context, t *testctx.T) {
		tags, err := c.Git("https://github.com/dagger/dagger").Tags(ctx)
		require.NoError(t, err)
		require.Contains(t, tags, "v0.9.3")
		require.Contains(t, tags, "sdk/go/v0.9.3")
	})

	t.Run("tag pattern", func(ctx context.Context, t *testctx.T) {
		tags, err := c.Git("https://github.com/dagger/dagger").Tags(ctx, dagger.GitRepositoryTagsOpts{
			Patterns: []string{"v*"},
		})
		require.NoError(t, err)
		require.Contains(t, tags, "v0.9.3")
		require.Contains(t, tags, "sdk/go/v0.9.3")
	})

	t.Run("ref-qualified tag pattern", func(ctx context.Context, t *testctx.T) {
		tags, err := c.Git("https://github.com/dagger/dagger").Tags(ctx, dagger.GitRepositoryTagsOpts{
			Patterns: []string{"refs/tags/v*"},
		})
		require.NoError(t, err)
		require.Contains(t, tags, "v0.9.3")
		require.NotContains(t, tags, "sdk/go/v0.9.3")
	})

	t.Run("prefix-qualified tag pattern", func(ctx context.Context, t *testctx.T) {
		tags, err := c.Git("https://github.com/dagger/dagger").Tags(ctx, dagger.GitRepositoryTagsOpts{
			Patterns: []string{"sdk/go/v*"},
		})
		require.NoError(t, err)
		require.NotContains(t, tags, "v0.9.3")
		require.Contains(t, tags, "sdk/go/v0.9.3")
	})
}
