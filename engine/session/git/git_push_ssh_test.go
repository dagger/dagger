package git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Exercise the actual askpass subprocess without building a second CLI binary.
func TestMain(m *testing.M) {
	if socket := os.Getenv(SSHAskpassSocketEnv); socket != "" {
		if RunSSHAskpass(socket, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type pushPassphraseHandler struct {
	passphrase string
	calls      atomic.Int32
	err        error
	started    chan struct{}
}

func (*pushPassphraseHandler) HandlePrompt(context.Context, string, string, any) error {
	return errors.New("passphrases must not use ordinary string prompts")
}

func (h *pushPassphraseHandler) HandleForm(ctx context.Context, form *huh.Form) error {
	h.calls.Add(1)
	if h.started != nil {
		close(h.started)
		<-ctx.Done()
		return ctx.Err()
	}
	if h.err != nil {
		return h.err
	}
	field := form.GetFocusedField()
	field.Focus()
	field.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(h.passphrase)})
	if strings.Contains(field.View(), h.passphrase) {
		return errors.New("passphrase input is not masked")
	}
	return nil
}

func newTestPushSSHAuth(t *testing.T, handler *pushPassphraseHandler, passphrase string) *pushSSHAuth {
	t.Helper()
	for _, binary := range []string{"ssh-agent", "ssh-add"} {
		_, err := exec.LookPath(binary)
		require.NoError(t, err)
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(key, "push test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "push test", []byte(passphrase))
	}
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "identity")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0600))
	m := newPushSSHAuth(t.Context(), handler)
	m.askpassExecutable, err = os.Executable()
	require.NoError(t, err)
	m.identities = func(context.Context, *gitutil.GitURL) ([]string, string, error) { return []string{path}, "", nil }
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	return m
}

func TestPushSSHAuthLazyAndExistingAgent(t *testing.T) {
	m := newPushSSHAuth(t.Context(), nil)
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	m.identities = func(context.Context, *gitutil.GitURL) ([]string, string, error) {
		t.Fatal("must not discover keys at startup or when reusing an agent")
		return nil, "", nil
	}
	require.Empty(t, m.agents)
	t.Setenv("SSH_AUTH_SOCK", "/existing/agent.sock")
	socket, err := m.prepare(t.Context(), "git@example.com:repo")
	require.NoError(t, err)
	require.Equal(t, "/existing/agent.sock", socket)
	require.Empty(t, m.agents)
	_, err = m.prepare(t.Context(), "ssh://git:secret@example.com/repo")
	require.ErrorContains(t, err, "must not contain a password")
	require.NotContains(t, err.Error(), "secret")
}

func TestPushSSHAuthUnencrypted(t *testing.T) {
	h := &pushPassphraseHandler{err: errors.New("unexpected prompt")}
	m := newTestPushSSHAuth(t, h, "")
	socket, err := m.prepare(t.Context(), "git@example.com:repo")
	require.NoError(t, err)
	require.Zero(t, h.calls.Load())
	conn, err := net.Dial("unix", socket)
	require.NoError(t, err)
	defer conn.Close()
	client := agent.NewClient(conn)
	keys, err := client.List()
	require.NoError(t, err)
	require.Len(t, keys, 1)
	sig, err := client.Sign(keys[0], []byte("authorized push"))
	require.NoError(t, err)
	public, err := ssh.ParsePublicKey(keys[0].Blob)
	require.NoError(t, err)
	require.NoError(t, public.Verify([]byte("authorized push"), sig))
	require.NoError(t, m.Close())
	_, err = os.Stat(filepath.Dir(socket))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = m.prepare(t.Context(), "git@example.com:repo")
	require.ErrorIs(t, err, context.Canceled)
}

func TestPushSSHAuthEncrypted(t *testing.T) {
	const passphrase = "not-in-telemetry-or-errors"
	h := &pushPassphraseHandler{passphrase: passphrase}
	m := newTestPushSSHAuth(t, h, passphrase)
	require.Zero(t, h.calls.Load())
	socket, err := m.prepare(t.Context(), "git@example.com:repo")
	require.NoError(t, err)
	require.EqualValues(t, 1, h.calls.Load())
	again, err := m.prepare(t.Context(), "git@example.com:repo")
	require.NoError(t, err)
	require.Equal(t, socket, again)
	require.EqualValues(t, 1, h.calls.Load(), "unlock only once for this destination and session")
}

func TestPushSSHAuthUnlockFailure(t *testing.T) {
	for _, test := range []struct {
		name, passphrase string
		err              error
	}{
		{name: "cancel", err: context.Canceled},
		{name: "wrong", passphrase: "incorrect secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := &pushPassphraseHandler{passphrase: test.passphrase, err: test.err}
			m := newTestPushSSHAuth(t, h, "correct secret")
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			_, err := m.prepare(ctx, "git@example.com:repo")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "correct secret")
			require.Empty(t, m.agents)
			require.LessOrEqual(t, h.calls.Load(), int32(3))
		})
	}
}

func TestPushSSHAuthCanceledWait(t *testing.T) {
	m := newPushSSHAuth(t.Context(), nil)
	t.Cleanup(func() { require.NoError(t, m.Close()) })
	m.lock <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.prepare(ctx, "git@example.com:repo")
	<-m.lock
	require.ErrorIs(t, err, context.Canceled)
}

func TestPushSSHAuthConfiguredIdentities(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := filepath.Join(home, "example.test user key")
	require.NoError(t, os.WriteFile(key, []byte("fixture, not loaded"), 0600))
	keys, socket, err := parsePushSSHIdentities("hostname example.test\nuser user\nport 2222\nidentityfile %d/%h %r key\nidentityfile ~/absent\nidentityfile none\n")
	require.NoError(t, err)
	require.Equal(t, []string{key}, keys)
	require.Empty(t, socket)
	keys, socket, err = parsePushSSHIdentities("identityagent ~/configured-agent.sock\nidentityfile %d/example.test user key\n")
	require.NoError(t, err)
	require.Empty(t, keys)
	require.Equal(t, filepath.Join(home, "configured-agent.sock"), socket)
}

func TestPushSSHAuthConcurrentReuse(t *testing.T) {
	h := &pushPassphraseHandler{passphrase: "fixture passphrase"}
	m := newTestPushSSHAuth(t, h, h.passphrase)
	var wg sync.WaitGroup
	sockets := make([]string, 3)
	errors := make([]error, 3)
	for i := range sockets {
		wg.Go(func() { sockets[i], errors[i] = m.prepare(t.Context(), "git@example.com:repo") })
	}
	wg.Wait()
	for i := range sockets {
		require.NoError(t, errors[i])
		require.Equal(t, sockets[0], sockets[i])
	}
	require.EqualValues(t, 1, h.calls.Load())
}

func TestPushSSHAuthCancelActiveUnlock(t *testing.T) {
	h := &pushPassphraseHandler{started: make(chan struct{})}
	m := newTestPushSSHAuth(t, h, "encrypted fixture")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.prepare(ctx, "git@example.com:repo"); done <- err }()
	select {
	case <-h.started:
	case <-time.After(10 * time.Second):
		t.Fatal("unlock prompt never arrived")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not dismiss unlock")
	}
	require.Empty(t, m.agents)
}

func TestPushSSHAuthNonInteractive(t *testing.T) {
	m := newTestPushSSHAuth(t, nil, "encrypted fixture")
	m.handler = nil
	_, err := m.prepare(t.Context(), "git@example.com:repo")
	require.ErrorContains(t, err, "interactive dagger CLI")
	require.Empty(t, m.agents)
}
