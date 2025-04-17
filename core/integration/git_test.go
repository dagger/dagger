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
	"github.com/dagger/testctx"
)

type GitSuite struct{}

func TestGit(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GitSuite{})
}

func (GitSuite) TestGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	checkClean := func(ctx context.Context, t *testctx.T, checkout *dagger.Directory, clean bool) {
		out, err := c.Container().From("alpine").
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithMountedDirectory(".", checkout).
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		if clean {
			require.Empty(t, out)
		} else {
			require.NotEmpty(t, out)
		}
	}

	testGitCheckout := func(ctx context.Context, t *testctx.T, git *dagger.GitRepository) {
		// head
		byHead := git.Head()
		mainCommit, err := byHead.Commit(ctx)
		require.NoError(t, err)
		mainRef, err := byHead.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "refs/heads/main", mainRef)
		checkClean(ctx, t, byHead.Tree(), true)
		readme, err := byHead.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err := byHead.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "ref: refs/heads/main")

		// main
		byBranch := git.Branch("main")
		commit, err := byBranch.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, mainCommit, commit)
		ref, err := byBranch.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "refs/heads/main", ref)
		checkClean(ctx, t, byBranch.Tree(), true)
		readme, err = byBranch.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err = byBranch.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "ref: refs/heads/main")

		// refs/heads/main
		byHeadMain := git.Ref("refs/heads/main")
		commit, err = byHeadMain.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, mainCommit, commit)
		ref, err = byHeadMain.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "refs/heads/main", ref)
		checkClean(ctx, t, byHeadMain.Tree(), true)
		readme, err = byHeadMain.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err = byHeadMain.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "ref: refs/heads/main")

		// v0.9.5
		byTag := git.Tag("v0.9.5")
		commit, err = byTag.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, "9ea5ea7c848fef2a2c47cce0716d5fcb8d6bedeb", commit)
		ref, err = byTag.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v0.9.5", ref)
		checkClean(ctx, t, byTag.Tree(), true)
		readme, err = byTag.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err = byTag.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "9ea5ea7c848fef2a2c47cce0716d5fcb8d6bedeb")

		// v0.6.1 (annotated tag)
		byAnnotatedTag := git.Tag("v0.6.1")
		commit, err = byAnnotatedTag.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, "6ed6264f1c4efbf84d310a104b57ef1bc57d57b0", commit)
		ref, err = byAnnotatedTag.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "refs/tags/v0.6.1", ref)
		checkClean(ctx, t, byAnnotatedTag.Tree(), true)
		readme, err = byAnnotatedTag.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err = byAnnotatedTag.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "6ed6264f1c4efbf84d310a104b57ef1bc57d57b0")

		// c80ac2c13df7d573a069938e01ca13f7a81f0345
		byCommit := git.Commit("c80ac2c13df7d573a069938e01ca13f7a81f0345")
		commit, err = byCommit.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, "c80ac2c13df7d573a069938e01ca13f7a81f0345", commit)
		ref, err = byCommit.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "c80ac2c13df7d573a069938e01ca13f7a81f0345", ref)
		checkClean(ctx, t, byCommit.Tree(), true)
		readme, err = byCommit.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err = byCommit.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "c80ac2c13df7d573a069938e01ca13f7a81f0345")

		// $ git ls-remote https://github.com/dagger/dagger.git | grep pull/8735
		// 318970484f692d7a76cfa533c5d47458631c9654	refs/pull/8735/head
		byHiddenCommit := git.Tag("318970484f692d7a76cfa533c5d47458631c9654")
		commit, err = byHiddenCommit.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, "318970484f692d7a76cfa533c5d47458631c9654", commit)
		ref, err = byHiddenCommit.Ref(ctx)
		require.NoError(t, err)
		require.Equal(t, "318970484f692d7a76cfa533c5d47458631c9654", ref)
		checkClean(ctx, t, byHiddenCommit.Tree(), true)
		readme, err = byHiddenCommit.Tree().File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readme, "Dagger")
		head, err = byHiddenCommit.Tree().File(".git/HEAD").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, head, "318970484f692d7a76cfa533c5d47458631c9654")
	}

	testGitTags := func(ctx context.Context, t *testctx.T, git *dagger.GitRepository) {
		tags, err := git.Tags(ctx)
		require.NoError(t, err)
		require.Subset(t, tags, []string{
			// tags
			"v0.14.0", "v0.15.0",
			// annotated tags
			"v0.6.1",
		})
		require.NotSubset(t, tags, []string{
			// annotated tags
			"v0.6.1^{}",
		})
	}

	t.Run("remote", func(ctx context.Context, t *testctx.T) {
		git := c.Git("https://github.com/dagger/dagger")
		testGitCheckout(ctx, t, git)
		testGitTags(ctx, t, git)
	})

	clone := func(opts ...string) *dagger.Directory {
		return c.Container().
			From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithDirectory("/src", c.Directory()).
			WithWorkdir("/src").
			WithExec(append([]string{"git", "clone", "https://github.com/dagger/dagger", "."}, opts...)).
			WithExec([]string{"git", "fetch", "origin", "318970484f692d7a76cfa533c5d47458631c9654"}).
			Directory(".")
	}
	t.Run("local worktree", func(ctx context.Context, t *testctx.T) {
		git := clone().AsGit()
		testGitCheckout(ctx, t, git)
		testGitTags(ctx, t, git)
	})
	t.Run("local git", func(ctx context.Context, t *testctx.T) {
		git := clone().Directory(".git").AsGit()
		testGitCheckout(ctx, t, git)
		testGitTags(ctx, t, git)
	})
	t.Run("local bare", func(ctx context.Context, t *testctx.T) {
		git := clone("--bare").AsGit()
		testGitCheckout(ctx, t, git)
		testGitTags(ctx, t, git)
	})
	t.Run("local empty", func(ctx context.Context, t *testctx.T) {
		git := c.Directory().AsGit()
		_, err := git.Head().Commit(ctx)
		require.ErrorContains(t, err, "not a git repository")
		_, err = git.Tags(ctx)
		require.ErrorContains(t, err, "not a git repository")
	})
}

func (GitSuite) TestDiscardGitDir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git dir is present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree()
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ent, ".git/")
	})

	t.Run("git dir is not present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, ent, ".git/")
	})
}

func (GitSuite) TestKeepGitDir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("git dir is present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree()
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ent, ".git/")
	})

	t.Run("git dir is not present", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		ent, err := dir.Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, ent, ".git/")
	})
}

func (GitSuite) TestGitDepth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	log := func(ctx context.Context, dir *dagger.Directory) (string, error) {
		res, err := c.Container().From("alpine").
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithMountedDirectory(".", dir).
			WithExec([]string{"git", "log", "--oneline"}).
			Stdout(ctx)
		return strings.TrimSpace(res), err
	}

	t.Run("default depth", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree()
		res, err := log(ctx, dir)
		require.NoError(t, err)
		lines := strings.Split(res, "\n")
		require.Len(t, lines, 1)
	})

	t.Run("depth 5", func(ctx context.Context, t *testctx.T) {
		dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{Depth: 5})
		res, err := log(ctx, dir)
		require.NoError(t, err)
		lines := strings.Split(res, "\n")
		require.Len(t, lines, 5)
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
		requireErrOut(t, err, "SSH URLs are not supported without an SSH socket")
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
		_, err := c.Git("https://bitbucket.org/dagger-modules/private-modules-test.git").
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "Authentication failed for 'https://bitbucket.org/dagger-modules/private-modules-test.git/'")
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
		requireErrOut(t, err, "Authentication failed")
	})

	t.Run("incorrect auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			WithAuthToken(c.SetSecret("token-wrong", "wrong")).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "Authentication failed")
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

func (GitSuite) TestRemoteUpdates(ctx context.Context, t *testctx.T) {
	// test case for dagger/dagger#9405, where upstream changes between fetches

	c := connect(ctx, t)

	svc, url := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", "Hello, world!"))

	svc, err := svc.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := svc.Stop(ctx)
		require.NoError(t, err)
	})

	ctr := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.name", "tester"}).
		WithExec([]string{"git", "config", "--global", "user.email", "test@dagger.io"}).
		WithWorkdir("/src").
		WithExec([]string{"git", "clone", url, "."}).
		WithExec([]string{"sh", "-c", `touch xyz && git add xyz && git commit -m "xyz" && git tag v1.0 && git push origin main && git push origin v1.0`})
	commit, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	commit = strings.TrimSpace(commit)

	ref := c.Git(url).Commit(commit)
	entries, err := ref.Tree().Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "xyz")

	ctr = ctr.
		WithExec([]string{"sh", "-c", `touch abc && git add abc && git commit -m "abc" && git tag -d v1.0 && git tag v1.0 && git push origin main && git push -f origin v1.0`})
	commit, err = ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	commit = strings.TrimSpace(commit)

	// in the original case, this failed, because we failed to update our tags
	ref = c.Git(url).Commit(commit)
	entries, err = ref.Tree().Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "abc")
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
