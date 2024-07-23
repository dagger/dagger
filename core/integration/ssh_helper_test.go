package core

import (
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func setupPrivateRepoSSHAgent(t *testctx.T) (string, func()) {
	key, err := ssh.ParseRawPrivateKey([]byte(globalPrivateKeyReadOnly))
	require.NoError(t, err)

	sshAgent := agent.NewKeyring()
	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: key,
	})
	require.NoError(t, err)

	tmp, err := os.MkdirTemp("", "ssh-agent")
	require.NoError(t, err)

	sshAgentPath := filepath.Join(tmp, "ssh-agent.sock")
	t.Logf("Attempting to create SSH agent socket at: %s", sshAgentPath)

	l, err := net.Listen("unix", sshAgentPath)
	if err != nil {
		t.Fatalf("Failed to create SSH agent socket: %v", err)
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				t.Logf("SSH agent l stopped: %v", err)
				return
			}
			go func() {
				defer conn.Close()
				err := agent.ServeAgent(sshAgent, conn)
				if err != nil && err != io.EOF {
					t.Logf("SSH agent error: %v", err)
				}
			}()
		}
	}()

	cleanup := func() {
		t.Logf("Cleaning up SSH agent: %s", sshAgentPath)
		l.Close()
		os.RemoveAll(tmp)
	}

	return sshAgentPath, cleanup
}
