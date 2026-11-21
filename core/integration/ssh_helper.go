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
	"time"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	_ "embed"
)

// Private key used to test SSH module refs.
// It has read-only access to our modules testing private repositories.
// These are all quasi-mirrors of github.com/dagger/dagger-test-modules
// - gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git
// - bitbucket.org/dagger-modules/private-modules-test
// - dev.azure.com/daggere2e/private/_git/dagger-test-modules
//
//go:embed private_key_ro_dagger_modules_test.pem
var base64EncodedPrivateKey string

// Private key used to test SSH module refs.
// This key is just for GitHub, to transition between the fork and the main dagger-test-modules repo
//
//go:embed private_key_ro_dagger_modules_test_github.pem
var base64EncodedPrivateKeyGitHub string

func setupPrivateRepoSSHAgent(t *testctx.T) (string, func()) {
	t.Helper()

	decodedPrivateKey, err := base64.StdEncoding.DecodeString(base64EncodedPrivateKey)
	require.NoError(t, err, "Failed to decode base64 private key")

	decodedPrivateKeyGitHub, err := base64.StdEncoding.DecodeString(base64EncodedPrivateKeyGitHub)
	require.NoError(t, err, "Failed to decode base64 private key")

	key, err := ssh.ParseRawPrivateKey(decodedPrivateKey)
	require.NoError(t, err, "Failed to parse private key")

	keyGitHub, err := ssh.ParseRawPrivateKey(decodedPrivateKeyGitHub)
	require.NoError(t, err, "Failed to parse private key")

	sshAgent := agent.NewKeyring().(agent.ExtendedAgent)
	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: key,
	})
	require.NoError(t, err)

	err = sshAgent.Add(agent.AddedKey{
		PrivateKey: keyGitHub,
	})
	require.NoError(t, err)
	logAgentKey(t, "private repos", key)
	logAgentKey(t, "GitHub", keyGitHub)

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
					// ServeAgent only returns once the connection fails.
					err := agent.ServeAgent(loggingAgent{sshAgent, t}, conn)
					if !errors.Is(err, io.EOF) {
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

func logAgentKey(t *testctx.T, name string, key any) {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	t.Logf("SSH agent: %s key %s", name, ssh.FingerprintSHA256(signer.PublicKey()))
}

// loggingAgent logs the requests that matter to a failed SSH fetch, with the
// time, to line them up with the engine's log of the fetch. They show how far
// an authentication got, not why it stopped: ssh lists the keys, then asks for
// a signature only once the server accepts an offered key. A listing with no
// signature is a server that refused every key, or a forwarding break after
// the listing; a signature can still be lost on its way back to ssh. The
// engine also lists the keys itself, to derive the socket's handle.
type loggingAgent struct {
	agent.ExtendedAgent
	t *testctx.T
}

func (a loggingAgent) List() ([]*agent.Key, error) {
	keys, err := a.ExtendedAgent.List()
	a.t.Logf("SSH agent: %s listed %d keys (err: %v)", agentLogTime(), len(keys), err)
	return keys, err
}

func (a loggingAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	sig, err := a.ExtendedAgent.Sign(key, data)
	a.t.Logf("SSH agent: %s signed with %s (err: %v)", agentLogTime(), ssh.FingerprintSHA256(key), err)
	return sig, err
}

func (a loggingAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	sig, err := a.ExtendedAgent.SignWithFlags(key, data, flags)
	a.t.Logf("SSH agent: %s signed with %s (err: %v)", agentLogTime(), ssh.FingerprintSHA256(key), err)
	return sig, err
}

func agentLogTime() string {
	return time.Now().UTC().Format("15:04:05.000")
}
