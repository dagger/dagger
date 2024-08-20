package core

import (
	"encoding/base64"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	_ "embed"
)

// Private key used to test the new SSH modules ref format
// It has read-only access to our modules testing private repositories.
// These are all quasi-mirrors of github.com/dagger/dagger-test-modules
// - gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git
// - bitbucket.org/dagger-modules/private-modules-test
// - dev.azure.com/daggere2e/private/_git/dagger-test-modules
//
//go:embed private_key_ro_dagger_modules_test.pem
var base64EncodedPrivateKey string

func setupPrivateRepoSSHAgent(t *testctx.T) (string, func()) {
	decodedPrivateKey, err := base64.StdEncoding.DecodeString(base64EncodedPrivateKey)
	require.NoError(t, err, "Failed to decode base64 private key")

	key, err := ssh.ParseRawPrivateKey(decodedPrivateKey)
	require.NoError(t, err, "Failed to parse private key")

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
	require.NoError(t, err, "Failed to create SSH agent socket")

	var logMu sync.Mutex
	safeLog := func(format string, args ...interface{}) {
		logMu.Lock()
		defer logMu.Unlock()
		t.Logf(format, args...)
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				safeLog("SSH agent l stopped: %v", err)
				return
			}
			go func() {
				defer conn.Close()
				err := agent.ServeAgent(sshAgent, conn)
				if err != nil && err != io.EOF {
					safeLog("SSH agent error: %v", err)
				}
			}()
		}
	}()

	cleanup := func() {
		safeLog("Cleaning up SSH agent: %s", sshAgentPath)
		l.Close()
		os.RemoveAll(tmp)
	}

	return sshAgentPath, cleanup
}
