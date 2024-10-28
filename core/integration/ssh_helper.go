package core

import (
	"context"
	"encoding/base64"
	"errors"
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

// Private key used to test the new SSH modules ref format
// This key is just for GitHub, to transition between the fork and the main dagger-test-modules repo
//
//go:embed private_key_ro_dagger_modules_test_github.pem
var base64EncodedPrivateKeyGitHub string

func setupPrivateRepoSSHAgent(t *testctx.T) (string, func()) {
	decodedPrivateKey, err := base64.StdEncoding.DecodeString(base64EncodedPrivateKey)
	require.NoError(t, err, "Failed to decode base64 private key")

	decodedPrivateKeyGitHub, err := base64.StdEncoding.DecodeString(base64EncodedPrivateKeyGitHub)
	require.NoError(t, err, "Failed to decode base64 private key")

	key, err := ssh.ParseRawPrivateKey(decodedPrivateKey)
	require.NoError(t, err, "Failed to parse private key")

	keyGitHub, err := ssh.ParseRawPrivateKey(decodedPrivateKeyGitHub)
	require.NoError(t, err, "Failed to parse private key")

	sshAgent := agent.NewKeyring()
	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: key,
	})
	require.NoError(t, err)

	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: keyGitHub,
	})
	require.NoError(t, err)

	tmp, err := os.MkdirTemp("", "ssh-agent")
	require.NoError(t, err)

	sshAgentPath := filepath.Join(tmp, "ssh-agent.sock")
	t.Logf("Attempting to create SSH agent socket at: %s", sshAgentPath)

	l, err := net.Listen("unix", sshAgentPath)
	require.NoError(t, err, "Failed to create SSH agent socket")

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				conn, err := l.Accept()
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						t.Logf("SSH agent listener stopped: %v", err)
					}
					return
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer conn.Close()
					err := agent.ServeAgent(sshAgent, conn)
					if err != nil && !errors.Is(err, io.EOF) {
						t.Logf("SSH agent error: %v", err)
					}
				}()
			}
		}
	}()

	cleanup := func() {
		cancel()
		l.Close()
		wg.Wait()
		os.RemoveAll(tmp)
		t.Logf("Cleaned up SSH agent: %s", sshAgentPath)
	}

	return sshAgentPath, cleanup
}
