package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
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
			Head         result
			Ref          result
			Commit       result
			Branch       result
			Tag          result
			HiddenCommit result
		}
	}{}

	err := testutil.Query(t,
		`{
			git(url: "github.com/dagger/dagger") {
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
				hiddenCommit: commit(id: "318970484f692d7a76cfa533c5d47458631c9654") {
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

	// $ git ls-remote https://github.com/dagger/dagger.git | grep pull/8735
	// 318970484f692d7a76cfa533c5d47458631c9654	refs/pull/8735/head
	require.Equal(t, res.Git.HiddenCommit.Commit, "318970484f692d7a76cfa533c5d47458631c9654")
	require.Contains(t, res.Git.HiddenCommit.Tree.File.Contents, "Dagger")
}

func (GitSuite) TestDiscardGitDir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git dir is present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree()
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ent, ".git")
	})

	t.Run("git dir is not present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, ent, ".git")
	})
}

func (GitSuite) TestKeepGitDir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git dir is present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree()
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ent, ".git")
	})

	t.Run("git dir is not present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, ent, ".git")
	})
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
		WithDefaultArgs([]string{"sh", "/root/start.sh"}).
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
		Tree(dagger.GitRefTreeOpts{
			DiscardGitDir: true,
		}).
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)
}

func (GitSuite) TestGitTagsSSH(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	repoURL := "git@gitlab.com:dagger-modules/private/test/more/dagger-test-modules-private.git"

	// Test fetching tags with SSH authentication
	t.Run("with SSH auth", func(ctx context.Context, t *testctx.T) {
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		tags, err := c.Git(repoURL, dagger.GitOpts{
			SSHAuthSocket: c.Host().UnixSocket(sockPath),
		}).Tags(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"cool-sdk/v0.1", "v0.1.1"}, tags)
	})

	t.Run("without SSH auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL).Tags(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "Permission denied (publickey)")
	})
}

func (GitSuite) TestAuthProviders(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Helper to decode base64-encoded PATs and trim whitespace
	decodeAndTrimPAT := func(encoded string) (string, error) {
		decodedPAT, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("failed to decode PAT: %w", err)
		}
		return strings.TrimSpace(string(decodedPAT)), nil
	}

	// Test authentication for major Git providers using read-only PATs
	t.Run("GitHub auth", func(ctx context.Context, t *testctx.T) {
		// Base64-encoded read-only PAT for test repo
		pat := "Z2l0aHViX3BhdF8xMUFIUlpENFEwMnVKQm5ESVBNZ0h5X2lHYUVPZTZaR2xOTjB4Y2o2WEdRWjNSalhwdHQ0c2lSMmw0aUJTellKUmFKUFdERlNUVU1hRXlDYXNQCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		_, err = c.Git("https://github.com/grouville/daggerverse-private.git").
			WithAuthToken(c.SetSecret("github_pat", token)).
			Branch("main").
			Tree().
			File("LICENSE").
			Contents(ctx)
		require.NoError(t, err)
	})

	t.Run("BitBucket auth", func(ctx context.Context, t *testctx.T) {
		// Base64-encoded read-only PAT for test repo
		pat := "QVRDVFQzeEZmR04wTHhxdWRtNVpjNFFIOE0xc3V0WWxHS2dfcjVTdVJxN0gwOVRrT0ZuUUViUDN4OURodldFQ3V1N1dzaTU5NkdBR2pIWTlhbVMzTEo5VE9OaFVFYlotUW5ZXzFmNnN3alRYRXJhUEJrcnI1NlpMLTdCeG4xMjdPYXpJRlFOMUF3VndLaWJDeW8wMm50U0JtYVA5MlRyUkMtUFN5a2sxQk4weXg1LUhjVXRqNmNVPTIwOEY2RThFCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		_, err = c.Git("https://bitbucket.org/dagger-modules/private-modules-test.git").
			WithAuthToken(c.SetSecret("bitbucket_pat", token)).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
	})

	t.Run("GitLab auth", func(ctx context.Context, t *testctx.T) {
		// Base64-encoded read-only PAT for test repo
		pat := "Z2xwYXQtQXlHQU4zR0xOeEhfM3VSckNzck0K"
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		_, err = c.Git("https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git").
			WithAuthToken(c.SetSecret("gitlab_pat", token)).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
	})

	// TODO: Implement Azure DevOps auth when PAT expiration is configurable
	// t.Run("Azure auth", func(ctx context.Context, t *testctx.T) {
	// 	_, err = c.Git("https://dev.azure.com/daggere2e/private/_git/dagger-test-modules").
	// 		Branch("main").
	// 		Tree().
	// 		File("README.md").
	// 		Contents(ctx)
	// 	require.NoError(t, err)
	// })

	t.Run("authentication error", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git("https://github.com/grouville/daggerverse-private.git").
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "Authentication failed for 'https://github.com/grouville/daggerverse-private.git/'")
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
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "failed to fetch remote")
	})

	t.Run("incorrect auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			WithAuthToken(c.SetSecret("token-wrong", "wrong")).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "failed to fetch remote")
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
			WithDefaultArgs([]string{"sleep"}).
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

	t.Run("all tags (short url)", func(ctx context.Context, t *testctx.T) {
		tags, err := c.Git("github.com/dagger/dagger").Tags(ctx)
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
