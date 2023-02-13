package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestGit(t *testing.T) {
	t.Parallel()

	res := struct {
		Git struct {
			Branch struct {
				Tree struct {
					File struct {
						Contents string
					}
				}
			}
			Commit struct {
				Tree struct {
					File struct {
						Contents string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			git(url: "github.com/dagger/dagger", keepGitDir: true) {
				branch(name: "main") {
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
				commit(id: "c80ac2c13df7d573a069938e01ca13f7a81f0345") {
					tree {
						file(path: "README.md") {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Git.Branch.Tree.File.Contents, "Dagger")
	require.Contains(t, res.Git.Commit.Tree.File.Contents, "Dagger")
}

func TestGitSSHAuthSock(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	gitSSH := c.Container().
		From("alpine:3.16.2").
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
		WithExec([]string{"sh", "/root/start.sh"})

	sshHost, err := sshSvc.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("ssh://root@%s:%d/root/repo", sshHost, sshPort)
	entries, err := c.Git(repoURL, dagger.GitOpts{ServiceHost: sshSvc}).
		Branch("main").
		Tree(dagger.GitRefTreeOpts{
			SSHKnownHosts: fmt.Sprintf("[%s]:%d %s", sshHost, sshPort, strings.TrimSpace(hostPubKey)),
			SSHAuthSocket: c.Host().UnixSocket(sock),
		}).
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)
}

func TestGitKeepGitDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := dagger.Connect(ctx)
	defer client.Close()

	t.Run("git dir is present", func(t *testing.T) {
		dir := client.Git("https://github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Branch("main").Tree()
		ent, _ := dir.Entries(ctx)
		require.Contains(t, ent, ".git")
	})

	t.Run("git dir is not present", func(t *testing.T) {
		dir := client.Git("https://github.com/dagger/dagger").Branch("main").Tree()
		ent, _ := dir.Entries(ctx)
		require.NotContains(t, ent, ".git")
	})
}
