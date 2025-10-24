package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"dagger.io/dagger"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/testctx"
)

type GitSuite struct{}

func TestGit(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GitSuite{})
}

// verify directory is a git checkout with specified clean/dirty status
func requireDirIsGitCheckout(ctx context.Context, t *testctx.T, checkout *dagger.Directory, clean bool, c *dagger.Client) {
	out, err := c.Container().From(alpineImage).
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

// verify string is a valid commit SHA
func requireIsCommitSHA(ctx context.Context, t *testctx.T, actual string) {
	actual = strings.TrimSpace(actual)
	require.True(t, gitutil.IsCommitSHA(actual), actual+"should be a valid git commit SHA")
}

// verify git ref has expected commit hash
func requireGitRefCommitEqual(ctx context.Context, t *testctx.T, expectedCommit string, ref *dagger.GitRef) {
	commit, err := ref.Commit(ctx)
	require.NoError(t, err)
	if expectedCommit != "" {
		require.Equal(t, expectedCommit, commit)
	}
}

// verify all git refs have the same commit hash
func requireGitRefCommitsEqual(ctx context.Context, t *testctx.T, refs ...*dagger.GitRef) {
	var first string
	for i, ref := range refs {
		if i == 0 {
			commit, err := ref.Commit(ctx)
			require.NoError(t, err)
			first = commit
			continue
		}
		requireGitRefCommitEqual(ctx, t, first, ref)
	}
}

// verify git ref has expected name
func requireGitRefNameEqual(ctx context.Context, t *testctx.T, expected string, ref *dagger.GitRef) {
	name, err := ref.Ref(ctx)
	require.NoError(t, err)
	require.Equal(t, expected, name)
}

// verify git ref name matches pattern
func requireGitRefNameRegexp(ctx context.Context, t *testctx.T, pattern string, ref *dagger.GitRef) {
	name, err := ref.Ref(ctx)
	require.NoError(t, err)
	require.Regexp(t, pattern, name)
}

// verify file contains expected string
func requireFileContains(ctx context.Context, t *testctx.T, expected string, file *dagger.File) {
	contents, err := file.Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, contents, expected)
}

// verify file contents pass validation function
func requireFileIsValid(ctx context.Context, t *testctx.T, validate func(context.Context, *testctx.T, string), file *dagger.File) {
	contents, err := file.Contents(ctx)
	require.NoError(t, err)
	validate(ctx, t, contents)
}

// verify git ref is a tag with expected name pattern and commit
func requireGitRefIsTag(ctx context.Context, t *testctx.T, c *dagger.Client, namePattern, expectedCommit string, ref *dagger.GitRef) {
	requireGitRefCommitEqual(ctx, t, expectedCommit, ref)
	requireGitRefNameRegexp(ctx, t, namePattern, ref)
	tree := ref.Tree()
	requireDirIsGitCheckout(ctx, t, tree, true, c)
	head := tree.File(".git/HEAD")
	switch expectedCommit {
	case "":
		requireFileIsValid(ctx, t, requireIsCommitSHA, head)
	default:
		requireFileContains(ctx, t, expectedCommit, head)
	}
}

// verify git ref is a commit with expected hash
func requireGitRefIsCommit(ctx context.Context, t *testctx.T, expectedCommit string, ref *dagger.GitRef, c *dagger.Client) {
	requireGitRefCommitEqual(ctx, t, expectedCommit, ref)
	requireGitRefNameEqual(ctx, t, expectedCommit, ref)
	tree := ref.Tree()
	requireDirIsGitCheckout(ctx, t, tree, true, c)
	head := tree.File(".git/HEAD")
	switch expectedCommit {
	case "":
		requireFileIsValid(ctx, t, requireIsCommitSHA, head)
	default:
		requireFileContains(ctx, t, expectedCommit, head)
	}
}

// verify git ref is a branch with expected name and commit
func requireGitRefIsBranch(ctx context.Context, t *testctx.T, expectedName, expectedCommit string, ref *dagger.GitRef, c *dagger.Client) {
	requireGitRefCommitEqual(ctx, t, expectedCommit, ref)
	requireGitRefNameEqual(ctx, t, expectedName, ref)
	tree := ref.Tree()
	requireDirIsGitCheckout(ctx, t, tree, true, c)
	requireFileContains(ctx, t, "ref: "+expectedName, tree.File(".git/HEAD"))
}

// verify directory is a sample checkout with clean status and contains "Dagger" in README.md
func requireSampleGitRootDir(ctx context.Context, t *testctx.T, c *dagger.Client, dir *dagger.Directory) {
	requireDirIsGitCheckout(ctx, t, dir, true, c)
	requireFileContains(ctx, t, "Dagger", dir.File("README.md"))
}

func requireSampleGitSubDir(ctx context.Context, t *testctx.T, _ *dagger.Client, dir *dagger.Directory) {
	requireFileContains(ctx, t, "package main", dir.File("main.go"))
}

func requireSampleGitFile(ctx context.Context, t *testctx.T, _ *dagger.Client, file *dagger.File) {
	requireFileContains(ctx, t, "package main", file)
}

// verify ref is the sample main branch with expected commit
func requireSampleGitBranch(ctx context.Context, t *testctx.T, c *dagger.Client, ref *dagger.GitRef) {
	requireSampleGitRootDir(ctx, t, c, ref.Tree())
	requireGitRefIsBranch(ctx, t, `refs/heads/main`, "", ref, c)
}

// verify ref is the sample v0.9.5 tag
func requireSampleGitTag(ctx context.Context, t *testctx.T, c *dagger.Client, ref *dagger.GitRef) {
	requireSampleGitRootDir(ctx, t, c, ref.Tree())
	requireGitRefIsTag(ctx, t, c, `^refs/tags/v0.9.5$`, "9ea5ea7c848fef2a2c47cce0716d5fcb8d6bedeb", ref)
}

// verify ref is the sample v0.6.1 annotated tag
func requireGitRefIsSampleAnnotatedTag(ctx context.Context, t *testctx.T, c *dagger.Client, ref *dagger.GitRef) {
	requireSampleGitRootDir(ctx, t, c, ref.Tree())
	requireGitRefIsTag(ctx, t, c, `^refs/tags/v0.6.1$`, "6ed6264f1c4efbf84d310a104b57ef1bc57d57b0", ref)
}

// verify ref is the sample commit
func requireSampleGitCommit(ctx context.Context, t *testctx.T, c *dagger.Client, ref *dagger.GitRef) {
	requireSampleGitRootDir(ctx, t, c, ref.Tree())
	requireGitRefIsCommit(ctx, t, "c80ac2c13df7d573a069938e01ca13f7a81f0345", ref, c)
}

// verify ref is the sample hidden commit (from pull request)
func requireSampleGitHiddenCommit(ctx context.Context, t *testctx.T, c *dagger.Client, ref *dagger.GitRef) {
	requireSampleGitRootDir(ctx, t, c, ref.Tree())
	requireGitRefIsCommit(ctx, t, "318970484f692d7a76cfa533c5d47458631c9654", ref, c)
}

func requireStrictCommit(ctx context.Context, t *testctx.T, repo *dagger.GitRepository, refStr string) {
	ref := repo.Commit(refStr)
	_, err := ref.Commit(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "invalid commit SHA")
}

func requireStrictTag(ctx context.Context, t *testctx.T, repo *dagger.GitRepository, refStr string) {
	ref := repo.Tag(refStr)
	_, err := ref.Commit(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "repository does not contain")
	requireErrOut(t, err, "refs/tags/")
	requireErrOut(t, err, refStr)
}

func requireStrictBranch(ctx context.Context, t *testctx.T, repo *dagger.GitRepository, refStr string) {
	ref := repo.Branch(refStr)
	_, err := ref.Commit(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "repository does not contain")
	requireErrOut(t, err, "refs/heads/")
	requireErrOut(t, err, refStr)
}

// verify repository has expected branches, tags, and commits
func requireSampleGitRepo(ctx context.Context, t *testctx.T, c *dagger.Client, repo *dagger.GitRepository) {
	// 1. TEST BRANCH REFS
	mainBranches := []*dagger.GitRef{repo.Head(), repo.Branch("main"), repo.Branch("refs/heads/main")}
	for _, branch := range mainBranches {
		requireSampleGitBranch(ctx, t, c, branch)
	}
	requireGitRefCommitsEqual(ctx, t, mainBranches...)

	// 2. TEST COMMIT REFS
	// sample commit
	requireSampleGitCommit(ctx, t, c, repo.Commit("c80ac2c13df7d573a069938e01ca13f7a81f0345"))
	// sample hidden commit
	// $ git ls-remote https://github.com/dagger/dagger.git | grep pull/8735
	// 318970484f692d7a76cfa533c5d47458631c9654	refs/pull/8735/head
	requireSampleGitHiddenCommit(ctx, t, c, repo.Commit("318970484f692d7a76cfa533c5d47458631c9654"))

	// 3. TEST TAG REFS
	// listing tags
	tags, err := repo.Tags(ctx)
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
	// latest tag
	latestTag := repo.LatestVersion()
	requireSampleGitRootDir(ctx, t, c, latestTag.Tree())
	requireGitRefIsTag(ctx, t, c, `^refs/tags/v\d\.+\d+\.\d+$`, "", latestTag)
	// sample tag
	requireSampleGitTag(ctx, t, c, repo.Tag("v0.9.5"))
	// sample annotated tag
	requireGitRefIsSampleAnnotatedTag(ctx, t, c, repo.Tag("v0.6.1"))

	// attempting to use lax refs should fail (see TestLegacyGitLaxRefs)
	requireStrictCommit(ctx, t, repo, "main")
	requireStrictTag(ctx, t, repo, "main")
	requireStrictTag(ctx, t, repo, "refs/heads/main")
	requireStrictBranch(ctx, t, repo, "v0.9.5")
	requireStrictBranch(ctx, t, repo, "refs/tags/v0.9.5")
}

func (GitSuite) TestGitRefs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("remote repo", func(ctx context.Context, t *testctx.T) {
		requireSampleGitRepo(ctx, t, c, c.Git("https://github.com/dagger/dagger"))
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
		requireSampleGitRepo(ctx, t, c, clone().AsGit())
	})

	t.Run("local git", func(ctx context.Context, t *testctx.T) {
		requireSampleGitRepo(ctx, t, c, clone().Directory(".git").AsGit())
	})
	t.Run("local bare", func(ctx context.Context, t *testctx.T) {
		requireSampleGitRepo(ctx, t, c, clone("--bare").AsGit())
	})
	t.Run("local empty", func(ctx context.Context, t *testctx.T) {
		repo := c.Directory().AsGit()
		_, err := repo.Head().Commit(ctx)
		require.ErrorContains(t, err, "not a git repository")
		_, err = repo.Tags(ctx)
		require.ErrorContains(t, err, "not a git repository")
	})
}

func (GitSuite) TestGitURL(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	input := "https://github.com/dagger/dagger.git"
	url, err := c.Git(input).URL(ctx)
	require.NoError(t, err)
	require.Equal(t, input, url)

	input = "github.com/dagger/dagger.git"
	url, err = c.Git(input).URL(ctx)
	require.NoError(t, err)
	require.Equal(t, "https://"+input, url)

	input = "https://github.com/dagger/dagger.git"
	url, err = c.Git(input).Head().Tree().AsGit().URL(ctx)
	require.NoError(t, err)
	require.Equal(t, "", url)
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

func (GitSuite) TestCheckoutOrigin(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	getOrigin := func(ctx context.Context, t *testctx.T, checkout *dagger.Directory) string {
		out, err := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithMountedDirectory(".", checkout).
			WithExec([]string{"git", "remote", "get-url", "origin"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}).
			Stdout(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}

	t.Run("remote", func(ctx context.Context, t *testctx.T) {
		checkout := c.Git("https://github.com/dagger/dagger").Head().Tree()
		require.Equal(t, "https://github.com/dagger/dagger", getOrigin(ctx, t, checkout))
	})

	t.Run("local", func(ctx context.Context, t *testctx.T) {
		clone := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithExec([]string{"git", "clone", "https://github.com/dagger/dagger", ".", "--depth=1"}).
			Directory(".")
		checkout := clone.AsGit().Head().Tree()
		require.Equal(t, "", getOrigin(ctx, t, checkout))
	})
}

func (GitSuite) TestGitDepth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	log := func(ctx context.Context, dir *dagger.Directory) (string, error) {
		res, err := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithMountedDirectory(".", dir).
			WithExec([]string{"git", "log", "--oneline"}).
			Stdout(ctx)
		return strings.TrimSpace(res), err
	}

	// default depth = 1
	dir := c.Git("https://github.com/dagger/dagger").Branch("main").Tree()
	res, err := log(ctx, dir)
	require.NoError(t, err)
	lines := strings.Split(res, "\n")
	require.Len(t, lines, 1)

	// depth = 5
	dir = c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{Depth: 5})
	res, err = log(ctx, dir)
	require.NoError(t, err)
	lines = strings.Split(res, "\n")
	require.Len(t, lines, 5)

	// depth = 1000 (big depth)
	dir = c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{Depth: 1000})
	res, err = log(ctx, dir)
	require.NoError(t, err)
	lines = strings.Split(res, "\n")
	require.Len(t, lines, 1000)

	// depth = 20 (back down)
	dir = c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{Depth: 20})
	res, err = log(ctx, dir)
	require.NoError(t, err)
	lines = strings.Split(res, "\n")
	require.Len(t, lines, 20)

	// depth = -1 (max depth)
	dir = c.Git("https://github.com/dagger/dagger").Branch("main").Tree(dagger.GitRefTreeOpts{Depth: -1})
	res, err = log(ctx, dir)
	require.NoError(t, err)
	lines = strings.Split(res, "\n")
	last := lines[len(lines)-1]
	require.Greater(t, len(lines), 2000)
	require.True(t, strings.HasPrefix(last, "30f75"), last)
	require.Contains(t, last, "Move prototype 69-dagger-archon to top-level")
}

func (GitSuite) TestSSHAuthSock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitSSH := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git", "openssh", "openssl"})

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

func (GitSuite) TestGitTags(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	testTags := func(t *testctx.T, repo *dagger.GitRepository) {
		t.Run("all tags", func(ctx context.Context, t *testctx.T) {
			tags, err := repo.Tags(ctx)
			require.NoError(t, err)
			require.Contains(t, tags, "v0.9.3")
			require.Contains(t, tags, "sdk/go/v0.9.3")
		})

		t.Run("tag pattern", func(ctx context.Context, t *testctx.T) {
			tags, err := repo.Tags(ctx, dagger.GitRepositoryTagsOpts{
				Patterns: []string{"v*"},
			})
			require.NoError(t, err)
			require.Contains(t, tags, "v0.9.3")
			require.Contains(t, tags, "sdk/go/v0.9.3")
		})

		t.Run("ref-qualified tag pattern", func(ctx context.Context, t *testctx.T) {
			tags, err := repo.Tags(ctx, dagger.GitRepositoryTagsOpts{
				Patterns: []string{"refs/tags/v*"},
			})
			require.NoError(t, err)
			require.Contains(t, tags, "v0.9.3")
			require.NotContains(t, tags, "sdk/go/v0.9.3")
		})

		t.Run("prefix-qualified tag pattern", func(ctx context.Context, t *testctx.T) {
			tags, err := repo.Tags(ctx, dagger.GitRepositoryTagsOpts{
				Patterns: []string{"sdk/go/v*"},
			})
			require.NoError(t, err)
			require.NotContains(t, tags, "v0.9.3")
			require.Contains(t, tags, "sdk/go/v0.9.3")
		})
	}

	testBranches := func(t *testctx.T, repo *dagger.GitRepository) {
		t.Run("all branches", func(ctx context.Context, t *testctx.T) {
			branches, err := repo.Branches(ctx)
			require.NoError(t, err)
			require.Contains(t, branches, "main")
		})

		t.Run("branches pattern", func(ctx context.Context, t *testctx.T) {
			branches, err := repo.Branches(ctx, dagger.GitRepositoryBranchesOpts{
				Patterns: []string{"ma*"},
			})
			require.NoError(t, err)
			require.Contains(t, branches, "main")
		})
	}

	t.Run("remote", func(ctx context.Context, t *testctx.T) {
		git := c.Git("https://github.com/dagger/dagger.git")
		testTags(t, git)
		testBranches(t, git)
	})
	t.Run("remote (short)", func(ctx context.Context, t *testctx.T) {
		git := c.Git("github.com/dagger/dagger")
		testTags(t, git)
		testBranches(t, git)
	})

	localClone := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/src").
		WithExec([]string{"git", "clone", "https://github.com/dagger/dagger", "."}).
		Directory(".")
	t.Run("local worktree", func(ctx context.Context, t *testctx.T) {
		git := localClone.AsGit()
		testTags(t, git)
		testBranches(t, git)
	})
	t.Run("local git", func(ctx context.Context, t *testctx.T) {
		git := localClone.Directory(".git").AsGit()
		testTags(t, git)
		testBranches(t, git)
	})
}

func (GitSuite) TestGitCheckedTags(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	requireGitTagsExist := func(ctx context.Context, t *testctx.T, git *dagger.GitRef) {
		ctr := c.Container().
			From("alpine").
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithMountedDirectory(".", git.Tree(dagger.GitRefTreeOpts{Depth: -1}))

			// check tag existence
		out, err := ctr.WithExec([]string{"git", "tag", "-l"}).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.Contains(t, lines, "v0.11.8", "v0.11.8 was tagged on main")
		require.NotContains(t, lines, "v0.11.9", "v0.11.9 was tagged off a branch, so shouldn't appear")

		// make sure no dangling tmp refs exist
		// (internal impl detail, but good to check they don't leak out)
		out, err = ctr.WithExec([]string{"git", "ls-remote", "file:///src"}).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "dagger.tmp")
	}

	runCheckedTags := func(t *testctx.T, git *dagger.GitRepository) {
		t.Run("branch", func(ctx context.Context, t *testctx.T) {
			requireGitTagsExist(ctx, t, git.Branch("main"))
		})

		t.Run("tag", func(ctx context.Context, t *testctx.T) {
			requireGitTagsExist(ctx, t, git.Tag("v0.12.0"))
		})

		t.Run("commit", func(ctx context.Context, t *testctx.T) {
			// v0.12.0 => 133917c6f9ce36d8cfdc595d9b7bd2c14cbc2c20
			requireGitTagsExist(ctx, t, git.Commit("133917c6f9ce36d8cfdc595d9b7bd2c14cbc2c20"))
		})

		t.Run("head", func(ctx context.Context, t *testctx.T) {
			requireGitTagsExist(ctx, t, git.Head())
		})
	}

	t.Run("remote", func(ctx context.Context, t *testctx.T) {
		runCheckedTags(t, c.Git("https://github.com/dagger/dagger"))
	})
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		localClone := c.Container().
			From(alpineImage).
			WithExec([]string{"apk", "add", "git"}).
			WithWorkdir("/src").
			WithExec([]string{"git", "clone", "https://github.com/dagger/dagger", "."}).
			Directory(".")
		runCheckedTags(t, localClone.AsGit())
	})
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

	// Test authentication for major Git providers using read-only PATs
	t.Run("GitHub auth", func(ctx context.Context, t *testctx.T) {
		// Base64-encoded read-only PAT for test repo
		pat := "Z2l0aHViX3BhdF8xMUFIUlpENFEwMnVKQm5ESVBNZ0h5X2lHYUVPZTZaR2xOTjB4Y2o2WEdRWjNSalhwdHQ0c2lSMmw0aUJTellKUmFKUFdERlNUVU1hRXlDYXNQCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		_, err = c.Git("https://github.com/grouville/daggerverse-private.git", dagger.GitOpts{
			HTTPAuthToken: c.SetSecret("github_pat", token),
		}).
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

		_, err = c.Git("https://bitbucket.org/dagger-modules/private-modules-test.git", dagger.GitOpts{
			HTTPAuthToken: c.SetSecret("bitbucket_pat", token),
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
	})

	t.Run("GitLab auth", func(ctx context.Context, t *testctx.T) {
		// Base64-encoded read-only PAT for test repo
		pat := "Z2xwYXQtMGF2bWZBbHBxWENwOXpuazZfZ2JmbTg2TVFwMU9tTjRhV3BqQ3cuMDEuMTIxbWF0b2Rx"
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		_, err = c.Git("https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", dagger.GitOpts{
			HTTPAuthToken: c.SetSecret("gitlab_pat", token),
		}).
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
		requireErrOut(t, err, "authentication failed")
	})
}

func (GitSuite) TestAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	gitDaemon, repoURL := gitServiceHTTPWithBranch(ctx, t, c, "", c.Directory().WithNewFile("README.md", "Hello, world!"), "main", "", c.SetSecret("target", "foobar"))

	t.Run("no auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "authentication failed")
	})

	t.Run("incorrect auth", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURL, dagger.GitOpts{
			ExperimentalServiceHost: gitDaemon,
			HTTPAuthToken:           c.SetSecret("token-wrong", "wrong"),
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "authentication failed")
	})

	t.Run("token auth", func(ctx context.Context, t *testctx.T) {
		dt, err := c.Git(repoURL, dagger.GitOpts{
			ExperimentalServiceHost: gitDaemon,
			HTTPAuthToken:           c.SetSecret("token", "foobar"),
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, world!", dt)
	})

	t.Run("header auth", func(ctx context.Context, t *testctx.T) {
		dt, err := c.Git(repoURL, dagger.GitOpts{
			ExperimentalServiceHost: gitDaemon,
			HTTPAuthHeader:          c.SetSecret("header", "basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:foobar"))),
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, world!", dt)
	})
}

func (GitSuite) TestAuthUsername(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitDaemonCustom, repoURLCustom := gitServiceHTTPWithBranch(ctx, t, c, "",
		c.Directory().WithNewFile("README.md", "Hello, custom user!"),
		"main",
		"customuser",
		c.SetSecret("custom-pass", "secretpass"))

	t.Run("custom username with token", func(ctx context.Context, t *testctx.T) {
		git := c.Git(repoURLCustom, dagger.GitOpts{
			ExperimentalServiceHost: gitDaemonCustom,
			HTTPAuthToken:           c.SetSecret("custom-token", "secretpass"),
			HTTPAuthUsername:        "customuser",
		})

		dt, err := git.
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, custom user!", dt)
	})

	t.Run("wrong username with correct token", func(ctx context.Context, t *testctx.T) {
		_, err := c.Git(repoURLCustom, dagger.GitOpts{
			ExperimentalServiceHost: gitDaemonCustom,
			HTTPAuthToken:           c.SetSecret("wrong-token", "secretpass"),
			HTTPAuthUsername:        "wronguser",
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "authentication failed")
	})

	gitDaemonDefault, repoURLDefault := gitServiceHTTPWithBranch(ctx, t, c, "",
		c.Directory().WithNewFile("README.md", "Hello, default user!"),
		"main",
		"",
		c.SetSecret("default-pass", "foobar"))

	t.Run("default username (x-access-token)", func(ctx context.Context, t *testctx.T) {
		dt, err := c.Git(repoURLDefault, dagger.GitOpts{
			ExperimentalServiceHost: gitDaemonDefault,
			HTTPAuthToken:           c.SetSecret("default-token", "foobar"),
			// No HTTPAuthUsername specified - should use default
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, default user!", dt)
	})
}

func (GitSuite) TestAuthClient(ctx context.Context, t *testctx.T) {
	username := "git"
	password := "secretpass"

	t.Run("loads username and password from client", func(ctx context.Context, t *testctx.T) {
		hostname := "my-git-repo" + identity.NewID()

		gitConfigPath := path.Join(t.TempDir(), "git-config")
		err := os.WriteFile(gitConfigPath, []byte(makeGitCredentials("http://"+hostname, username, password)), 0600)
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithEnvironmentVariable("GIT_CONFIG_GLOBAL", gitConfigPath))

		gitService, gitServiceURL := gitServiceHTTPWithBranch(ctx, t, c, hostname,
			c.Directory().WithNewFile("README.md", "Hello, user!"),
			"main",
			username,
			c.SetSecret("secret"+identity.NewID(), password),
		)

		dt, err := c.Git(gitServiceURL, dagger.GitOpts{
			ExperimentalServiceHost: gitService,
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, user!", dt)
	})

	t.Run("incorrect username fails", func(ctx context.Context, t *testctx.T) {
		hostname := "my-git-repo" + identity.NewID()

		gitConfigPath := path.Join(t.TempDir(), "git-config")
		err := os.WriteFile(gitConfigPath, []byte(makeGitCredentials("http://"+hostname, username, password)), 0600)
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithEnvironmentVariable("GIT_CONFIG_GLOBAL", gitConfigPath))

		gitService, gitServiceURL := gitServiceHTTPWithBranch(ctx, t, c, hostname,
			c.Directory().WithNewFile("README.md", "Hello, bad username!"),
			"main",
			username+"XXX",
			c.SetSecret("secret"+identity.NewID(), password),
		)

		_, err = c.Git(gitServiceURL, dagger.GitOpts{
			ExperimentalServiceHost: gitService,
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "authentication failed")
	})

	t.Run("incorrect password fails", func(ctx context.Context, t *testctx.T) {
		hostname := "my-git-repo" + identity.NewID()

		gitConfigPath := path.Join(t.TempDir(), "git-config")
		err := os.WriteFile(gitConfigPath, []byte(makeGitCredentials("http://"+hostname, username, password)), 0600)
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithEnvironmentVariable("GIT_CONFIG_GLOBAL", gitConfigPath))

		gitService, gitServiceURL := gitServiceHTTPWithBranch(ctx, t, c, hostname,
			c.Directory().WithNewFile("README.md", "Hello, bad password!"),
			"main",
			username,
			c.SetSecret("secret"+identity.NewID(), password+"XXX"),
		)

		_, err = c.Git(gitServiceURL, dagger.GitOpts{
			ExperimentalServiceHost: gitService,
		}).
			Branch("main").
			Tree().
			File("README.md").
			Contents(ctx)
		requireErrOut(t, err, "git error")
		requireErrOut(t, err, "authentication failed")
	})
}

func (GitSuite) TestSubmoduleAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Cleanup(func() { _ = c.Close() })

	authToken := c.SetSecret("submodule-test-token", "test-token-"+identity.NewID())

	submoduleContent := c.Directory().WithNewFile("submodule.txt", "This is the submodule content")
	parentContent := c.Directory().WithNewFile("parent.txt", "This is the parent content")

	// Create bare parent + submodule repos in /srv
	// Git dance below is necessary: https://github.com/dagger/dagger/pull/10855#discussion_r2264174757
	gitReposCtr := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithExec([]string{"sh", "-lc", "mkdir -p /srv && git init --bare /srv/submodule.git && git init --bare /srv/parent.git"}).
		WithDirectory("/tmp/sub", submoduleContent).
		WithExec([]string{"sh", "-lc", `
set -eux
cd /tmp/sub
git init
git add .
git commit -m 'Initial submodule commit'
git remote add origin file:///srv/submodule.git
git push -u origin main
`}).
		WithDirectory("/tmp/parent", parentContent).
		WithExec([]string{"sh", "-lc", `
set -eux
cd /tmp/parent
git init
git add .
git commit -m 'Initial parent commit'
git -c protocol.file.allow=always submodule add file:///srv/submodule.git sub
git commit -m 'Add submodule (absolute url to fetch)'
git config -f .gitmodules submodule.sub.url ../submodule.git
git add .gitmodules
git commit -m 'Make submodule URL relative'
git remote add origin file:///srv/parent.git
git push -u origin main
# dumb HTTP needs server-info
git --git-dir=/srv/submodule.git update-server-info
git --git-dir=/srv/parent.git    update-server-info
`})

	gitReposDir := gitReposCtr.Directory("/srv")

	t.Run("smart-http", func(ctx context.Context, t *testctx.T) {
		gitSrv, base := gitSmartHTTPServiceDirAuth(ctx, t, c, "", gitReposDir, "", authToken)
		parentURL := base + "/parent.git"

		t.Run("with auth", func(ctx context.Context, t *testctx.T) {
			tree := c.Git(parentURL, dagger.GitOpts{
				ExperimentalServiceHost: gitSrv,
				HTTPAuthToken:           authToken,
			}).Branch("main").Tree()

			txt, err := tree.File("parent.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "This is the parent content", txt)

			sub, err := tree.File("sub/submodule.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "This is the submodule content", sub)
		})

		t.Run("without auth fails", func(ctx context.Context, t *testctx.T) {
			_, err := c.Git(parentURL, dagger.GitOpts{
				ExperimentalServiceHost: gitSrv,
			}).Branch("main").Tree().File("parent.txt").Contents(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "git error")
			requireErrOut(t, err, "authentication failed")
		})
	})

	t.Run("dumb-http", func(ctx context.Context, t *testctx.T) {
		httpSrv, base := httpServiceDirAuth(ctx, t, c, "", gitReposDir, "x-access-token", authToken)
		parentURL := base + "/parent.git"

		t.Run("with auth fallback", func(ctx context.Context, t *testctx.T) {
			tree := c.Git(parentURL, dagger.GitOpts{
				ExperimentalServiceHost: httpSrv,
				HTTPAuthToken:           authToken,
			}).Branch("main").Tree()

			txt, err := tree.File("parent.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "This is the parent content", txt)

			sub, err := tree.File("sub/submodule.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "This is the submodule content", sub)
		})

		t.Run("without auth fails", func(ctx context.Context, t *testctx.T) {
			_, err := c.Git(parentURL, dagger.GitOpts{
				ExperimentalServiceHost: httpSrv,
			}).Branch("main").Tree().File("parent.txt").Contents(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "git error")
			requireErrOut(t, err, "authentication failed")
		})
	})
}

func (GitSuite) TestRemoteUpdates(ctx context.Context, t *testctx.T) {
	// test case for dagger/dagger#9405, where upstream changes between fetches

	c := connect(ctx, t)

	svc, url := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", "Hello "+identity.NewID()))

	svc, err := svc.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := svc.Stop(ctx)
		require.NoError(t, err)
	})

	ctr := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
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

func (GitSuite) TestRemoteUpdatesFrozenTag(ctx context.Context, t *testctx.T) {
	// similar to above, upstream changes between fetches, but we check that
	// the tag gets *resolved* and doesn't get updated

	c := connect(ctx, t)

	svc, url := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", "Hello "+identity.NewID()))

	svc, err := svc.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := svc.Stop(ctx)
		require.NoError(t, err)
	})

	ctr := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/src").
		WithExec([]string{"git", "clone", url, "."}).
		WithExec([]string{"sh", "-c", `touch xyz && git add xyz && git commit -m "xyz" && git tag v1.0 && git push origin main && git push origin v1.0`})
	commit, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	commit = strings.TrimSpace(commit)

	// resolve the commit now (by syncing it), but don't clone it
	ref := c.Git(url).Tag("v1.0")
	result, err := ref.Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, commit, result)

	// modify the upstream
	ctr = ctr.WithExec([]string{"sh", "-c", `touch abc && git add abc && git commit -m "abc" && git tag -d v1.0 && git tag v1.0 && git push origin main && git push -f origin v1.0`})
	_, err = ctr.Sync(ctx)
	require.NoError(t, err)

	// now check that the checkout is for the original commit
	entries, err := ref.Tree().Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, entries, "xyz")
	require.NotContains(t, entries, "abc")

	head, err := ref.Tree().File(".git/HEAD").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, commit, strings.TrimSpace(head))
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

func (GitSuite) TestGitLatestVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/src").
		WithExec([]string{"git", "init"}).
		WithExec([]string{"sh", "-c", `touch xyz && git add xyz && git commit -m "xyz" && git tag v2.0 && touch abc && git add abc && git commit -m "abc" && git tag v1.0`})
	v2commit, err := ctr.WithExec([]string{"git", "rev-parse", "HEAD~"}).Stdout(ctx)
	require.NoError(t, err)
	v2commit = strings.TrimSpace(v2commit)

	git := ctr.Directory(".").AsGit()

	ref, err := git.LatestVersion().Ref(ctx)
	require.NoError(t, err)
	require.Equal(t, "refs/tags/v2.0", ref)
	commit, err := git.LatestVersion().Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, v2commit, commit)
}

func (GitSuite) TestGitCommonAncestor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/src").
		WithExec([]string{"git", "init"}).
		With(gitUserConfig).
		WithExec([]string{"sh", "-c", `echo "A" > file.txt && git add file.txt && git commit -m "A"`}).
		WithExec([]string{"git", "checkout", "-b", "branch1"}).
		WithExec([]string{"sh", "-c", `echo "B" >> file.txt && git add file.txt && git commit -m "B"`}).
		WithExec([]string{"git", "checkout", "master"}).
		WithExec([]string{"git", "checkout", "-b", "branch2"}).
		WithExec([]string{"sh", "-c", `echo "C" >> file.txt && git add file.txt && git commit -m "C"`}).
		WithExec([]string{"git", "checkout", "master"})
	git := ctr.Directory(".").AsGit()

	base, err := ctr.WithExec([]string{"git", "rev-parse", "master"}).Stdout(ctx)
	require.NoError(t, err)
	base = strings.TrimSpace(base)

	// test the common ancestor between two branches
	mergeBase := git.Branch("branch1").CommonAncestor(git.Branch("branch2"))
	commit, err := mergeBase.Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, base, commit)
	ref, err := mergeBase.Ref(ctx)
	require.NoError(t, err)
	require.Equal(t, base, ref)

	ctr = ctr.
		WithExec([]string{"git", "checkout", "-b", "branch3"}).
		WithExec([]string{"sh", "-c", `echo "D" >> file.txt && git add file.txt && git commit -m "D"`})
	git2 := ctr.Directory(".").AsGit()

	// test the common ancestor between two branches from different refs
	mergeBase = git.Branch("branch1").CommonAncestor(git2.Branch("branch3"))
	commit, err = mergeBase.Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, base, commit)
	ref, err = mergeBase.Ref(ctx)
	require.NoError(t, err)
	require.Equal(t, base, ref)
}

func (GitSuite) TestGitSchemeless(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	checkAccess := func(ctx context.Context, repo *dagger.GitRepository) error {
		_, err := repo.
			Branch("main").
			Tree().
			File("LICENSE").
			Contents(ctx)
		return err
	}

	t.Run("public https", func(ctx context.Context, t *testctx.T) {
		repo := c.Git("github.com/dagger/dagger")
		require.NoError(t, checkAccess(ctx, repo))

		url, err := repo.URL(ctx)
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", url)
	})

	t.Run("private https", func(ctx context.Context, t *testctx.T) {
		pat := "Z2l0aHViX3BhdF8xMUFIUlpENFEwMnVKQm5ESVBNZ0h5X2lHYUVPZTZaR2xOTjB4Y2o2WEdRWjNSalhwdHQ0c2lSMmw0aUJTellKUmFKUFdERlNUVU1hRXlDYXNQCg=="
		token, err := decodeAndTrimPAT(pat)
		require.NoError(t, err)

		repo := c.Git("github.com/grouville/daggerverse-private.git", dagger.GitOpts{
			HTTPAuthToken: c.SetSecret("github_pat", token),
		})
		require.NoError(t, checkAccess(ctx, repo))

		url, err := repo.URL(ctx)
		require.NoError(t, err)
		require.Equal(t, "https://github.com/grouville/daggerverse-private.git", url)
	})

	t.Run("private ssh", func(ctx context.Context, t *testctx.T) {
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		repo := c.Git("gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", dagger.GitOpts{
			SSHAuthSocket: c.Host().UnixSocket(sockPath),
		})
		require.NoError(t, checkAccess(ctx, repo))

		url, err := repo.URL(ctx)
		require.NoError(t, err)
		require.Equal(t, "ssh://git@gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", url)
	})

	t.Run("private no auth fails", func(ctx context.Context, t *testctx.T) {
		repo := c.Git("github.com/grouville/daggerverse-private.git")
		err := checkAccess(ctx, repo)
		require.Error(t, err)
		requireErrOut(t, err, "failed to determine Git URL protocol")

		_, err = repo.URL(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "failed to determine Git URL protocol")
	})
}

// Helper to decode base64-encoded PATs and trim whitespace
func decodeAndTrimPAT(encoded string) (string, error) {
	decodedPAT, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode PAT: %w", err)
	}
	return strings.TrimSpace(string(decodedPAT)), nil
}

// Ensure IsRemotePublic correctly detects repo visibility (see dagger/dagger#11112)
func (GitSuite) TestIsRemotePublic(ctx context.Context, t *testctx.T) {
	vc := append([]vcsTestCase{
		{
			name:                "Azure DevOps private",
			expectedBaseHTMLURL: "dev.azure.com/daggere2e/private/_git/dagger-test-modules.git",
			isPrivateRepo:       true,
		},
	}, vcsTestCases...)

	for _, v := range vc {
		t.Run(v.name, func(ctx context.Context, t *testctx.T) {
			remoteURL := v.expectedBaseHTMLURL
			if !strings.Contains(remoteURL, "://") {
				remoteURL = "https://" + remoteURL
			}

			remote, err := gitutil.ParseURL(remoteURL)
			require.NoError(t, err)

			isRemotePublic, err := schema.IsRemotePublic(ctx, remote)
			require.NoError(t, err)

			require.Equalf(t, !v.isPrivateRepo, isRemotePublic, "Expected public=%v for repo %q", !v.isPrivateRepo, v.name)
		})
	}
}

func (GitSuite) TestGitUncommittedRemote(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	git := c.Git("https://github.com/dagger/dagger")
	changes := git.Uncommitted()
	empty, err := changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)
}

func (GitSuite) TestGitUncommittedLocal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/src").
		WithExec([]string{"git", "init"}).
		WithExec([]string{"sh", "-c", `echo "Initial content" > mod.txt && cp mod.txt rem.txt && git add mod.txt rem.txt && git commit -m "Initial commit"`})

	// No changes yet
	git := ctr.Directory(".").AsGit()
	changes := git.Uncommitted()
	empty, err := changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)

	// No changes if we just select the git directory
	git = ctr.Directory(".git").AsGit()
	changes = git.Uncommitted()
	empty, err = changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)

	// Make some changes
	ctr = ctr.WithExec([]string{"sh", "-c", `echo "Modified content" >> mod.txt && echo "New file content" > new.txt && rm rem.txt`})
	git = ctr.Directory(".").AsGit()
	changes = git.Uncommitted()
	empty, err = changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty)

	added, err := changes.AddedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"new.txt"}, added)

	modified, err := changes.ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"mod.txt"}, modified)

	removed, err := changes.RemovedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"rem.txt"}, removed)

	// Still no changes if we just select the .git directory
	git = ctr.Directory(".git").AsGit()
	changes = git.Uncommitted()
	empty, err = changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)

	// Stage all changes (we should still see changes)
	ctr = ctr.WithExec([]string{"git", "add", "."})
	git = ctr.Directory(".").AsGit()
	changes = git.Uncommitted()
	empty, err = changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty)

	added, err = changes.AddedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"new.txt"}, added)

	modified, err = changes.ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"mod.txt"}, modified)

	removed, err = changes.RemovedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"rem.txt"}, removed)

	// Again, no changes if we just select the .git directory
	git = ctr.Directory(".git").AsGit()
	changes = git.Uncommitted()
	empty, err = changes.IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)
}

func gitUserConfig(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithExec([]string{"git", "config", "--global", "user.email", "test@dagger.io"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Test User"}).
		WithExec([]string{"git", "config", "--global", "init.defaultBranch", "main"})
}
