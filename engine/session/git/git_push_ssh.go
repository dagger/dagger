package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/gitutil"
	"golang.org/x/crypto/ssh/agent"
)

// SSHAskpassSocketEnv is private to the ssh-add child and its CLI askpass helper.
// Passphrases never enter engine RPC responses, arguments, files or telemetry.
const SSHAskpassSocketEnv = "_DAGGER_SSH_ASKPASS_SOCKET" //nolint:gosec // Environment variable name, not a credential.

type pushSSHAuth struct {
	ctx     context.Context
	cancel  context.CancelFunc
	lock    chan struct{}
	handler prompt.PromptHandler
	agents  map[string]*pushSSHAgent
	closed  bool
	// Overridden by tests so they never discover the developer's keys.
	identities        func(context.Context, *gitutil.GitURL) ([]string, string, error)
	askpassExecutable string
}

type pushSSHAgent struct {
	dir, socket string
	cmd         *exec.Cmd
	done        chan struct{}
}

func newPushSSHAuth(ctx context.Context, handler prompt.PromptHandler) *pushSSHAuth {
	ctx, cancel := context.WithCancel(ctx)
	m := &pushSSHAuth{ctx: ctx, cancel: cancel, lock: make(chan struct{}, 1), handler: handler,
		agents: make(map[string]*pushSSHAgent), identities: pushSSHIdentities}
	// No process, socket, key discovery or prompting at session startup.
	context.AfterFunc(ctx, func() { _ = m.Close() })
	return m
}

func (m *pushSSHAuth) Close() error {
	m.cancel()
	m.lock <- struct{}{}
	defer func() { <-m.lock }()
	if m.closed {
		return nil
	}
	m.closed = true
	for _, a := range m.agents {
		a.close()
	}
	clear(m.agents)
	return nil
}

func (a *pushSSHAgent) close() {
	_ = a.cmd.Process.Kill()
	<-a.done // Reap before removing the socket directory.
	_ = os.RemoveAll(a.dir)
}

func (m *pushSSHAuth) prepare(ctx context.Context, remote string) (string, error) {
	u, err := gitutil.ParseURL(remote)
	if err != nil || u.Scheme != gitutil.SSHProtocol || u.Host == "" {
		return "", errors.New("invalid SSH push destination")
	}
	if u.User != nil {
		if _, password := u.User.Password(); password {
			return "", errors.New("SSH push destination must not contain a password")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	select {
	case m.lock <- struct{}{}:
		defer func() { <-m.lock }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Never load identities into, replace, or terminate the user's agent.
	if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
		return expandPushSSHPath(socket)
	}
	if a := m.agents[remote]; a != nil {
		select {
		case <-a.done:
			a.close()
			delete(m.agents, remote)
		default:
			return a.socket, nil
		}
	}
	keys, configuredAgent, err := m.identities(ctx, u)
	if err != nil {
		return "", err
	}
	if configuredAgent != "" {
		return configuredAgent, nil
	}
	if len(keys) == 0 {
		return "", errors.New("no SSH identity files found for push; configure an IdentityFile or SSH_AUTH_SOCK")
	}
	a, err := startPushSSHAgent(m.ctx)
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			a.close()
		}
	}()
	for _, key := range keys {
		if err := m.addKey(ctx, a, key); err != nil {
			return "", err
		}
	}
	// Keep separate agents per destination rather than accumulating unrelated
	// host identities in a single automatically forwarded signing capability.
	m.agents[remote] = a
	keep = true
	return a.socket, nil
}

func startPushSSHAgent(ctx context.Context) (*pushSSHAgent, error) {
	dir, err := os.MkdirTemp("", "dagger-push-ssh-") // mode 0700
	if err != nil {
		return nil, err
	}
	a := &pushSSHAgent{dir: dir, socket: filepath.Join(dir, "agent.sock"), done: make(chan struct{})}
	a.cmd = exec.CommandContext(ctx, "ssh-agent", "-D", "-a", a.socket)
	if err := a.cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("start SSH agent for push (OpenSSH is required): %w", err)
	}
	go func() { _ = a.cmd.Wait(); close(a.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := (&net.Dialer{}).DialContext(readyCtx, "unix", a.socket)
		if err == nil {
			conn.Close()
			return a, nil
		}
		select {
		case <-readyCtx.Done():
			a.close()
			return nil, fmt.Errorf("wait for SSH agent: %w", readyCtx.Err())
		case <-a.done:
			a.close()
			return nil, errors.New("SSH agent exited before becoming ready")
		case <-ticker.C:
		}
	}
}

func (m *pushSSHAuth) addKey(ctx context.Context, a *pushSSHAgent, key string) error {
	askCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	exe := m.askpassExecutable
	if exe == "" || m.handler == nil {
		// Non-CLI attachables must never re-execute their own binary as askpass.
		// Unencrypted keys still work; encrypted keys fail without a prompt.
		exe = filepath.Join(a.dir, "interactive-unlock-unavailable")
	}
	listener, err := net.Listen("unix", filepath.Join(a.dir, "askpass.sock"))
	if err != nil {
		return err
	}
	defer listener.Close()
	var promptErr error
	var promptMu sync.Mutex
	attempts := 0
	// ssh-add invokes askpass synchronously; this server is private to one key
	// load, and Shutdown below joins its handler before promptErr is read.
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return askCtx }}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promptMu.Lock()
		defer promptMu.Unlock()
		if promptErr != nil {
			http.Error(w, "key unlocking unavailable", http.StatusForbidden)
			return
		}
		if attempts >= 3 {
			promptErr = errors.New("SSH key unlocking failed after three attempts")
			http.Error(w, "key unlocking failed", http.StatusForbidden)
			return
		}
		attempts++
		if m.handler == nil {
			promptErr = errors.New("SSH key needs a passphrase; an interactive client is required")
			http.Error(w, "key unlocking unavailable", http.StatusForbidden)
			return
		}
		var passphrase string
		label := strconv.QuoteToASCII(key)
		form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("SSH key passphrase").
			Description("Unlock " + label + " for this push session").EchoMode(huh.EchoModePassword).Value(&passphrase)))
		if err := m.handler.HandleForm(r.Context(), form); err != nil {
			promptErr = errors.New("SSH key unlocking canceled or unavailable")
			http.Error(w, "key unlocking canceled", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, passphrase)
	})
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	cmd := exec.CommandContext(askCtx, "ssh-add", "--", key)
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+a.socket, "SSH_ASKPASS="+exe,
		"SSH_ASKPASS_REQUIRE=force", "DISPLAY=dagger", SSHAskpassSocketEnv+"="+listener.Addr().String())
	// Never attach stdin/tty or send ssh-add output into telemetry. A failed
	// unlock must not echo a passphrase or uncontrolled helper output.
	err = cmd.Run()
	cancelRequest()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	promptMu.Lock()
	defer promptMu.Unlock()
	if promptErr != nil {
		return promptErr
	}
	if err != nil {
		if m.handler == nil || m.askpassExecutable == "" {
			return errors.New("could not load SSH identity; an encrypted key requires an interactive dagger CLI")
		}
		return errors.New("could not load SSH identity for push (key unsupported, unreadable, or incorrect passphrase)")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", a.socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	keys, err := agent.NewClient(conn).List()
	if err != nil || len(keys) == 0 {
		return errors.New("SSH agent has no identities after loading the push key")
	}
	return nil
}

// RunSSHAskpass is the CLI's early, telemetry-free helper entrypoint. Only the
// ssh-add child receives the private socket location in its environment.
func RunSSHAskpass(socket string, output io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://askpass/", nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("SSH key unlocking failed")
	}
	_, err = io.Copy(output, io.LimitReader(res.Body, 64<<10))
	return err
}

func expandPushSSHPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	return filepath.Abs(path)
}

func pushSSHIdentities(ctx context.Context, remote *gitutil.GitURL) ([]string, string, error) {
	u := &url.URL{Host: remote.Host}
	host := u.Hostname()
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, "\r\n\x00") {
		return nil, "", errors.New("invalid SSH push host")
	}
	args := []string{"-G", "-oCanonicalizeHostname=no"}
	if remote.User != nil {
		args = append(args, "-l", remote.User.Username())
	}
	if port := u.Port(); port != "" {
		args = append(args, "-p", port)
	}
	args = append(args, "--", host)
	configCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(configCtx, "ssh", args...).Output()
	if err != nil {
		return nil, "", errors.New("cannot discover SSH identities for push (OpenSSH is required)")
	}
	return parsePushSSHIdentities(string(out))
}

func parsePushSSHIdentities(config string) ([]string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	localUser, err := user.Current()
	if err != nil {
		return nil, "", err
	}
	localHost, err := os.Hostname()
	if err != nil {
		return nil, "", err
	}
	values := map[string]string{}
	for _, line := range strings.Split(config, "\n") {
		name, value, _ := strings.Cut(line, " ")
		values[name] = value
	}
	expand := func(value string) (string, error) {
		tokens := map[byte]string{'%': "%", 'd': home, 'u': localUser.Username, 'i': localUser.Uid,
			'l': localHost, 'h': values["hostname"], 'r': values["user"], 'p': values["port"]}
		var out strings.Builder
		for i := 0; i < len(value); i++ {
			if value[i] != '%' {
				out.WriteByte(value[i])
				continue
			}
			i++
			if i == len(value) {
				return "", errors.New("incomplete SSH identity path token")
			}
			replacement, ok := tokens[value[i]]
			if !ok {
				return "", errors.New("unsupported SSH identity path token")
			}
			out.WriteString(replacement)
		}
		return expandPushSSHPath(out.String())
	}
	var keys []string
	for _, line := range strings.Split(config, "\n") {
		name, value, ok := strings.Cut(line, " ")
		if !ok || value == "none" {
			continue
		}
		switch name {
		case "identityagent":
			// OpenSSH may spell the environment reference literally in -G output.
			if value == "SSH_AUTH_SOCK" || strings.HasPrefix(value, "$") {
				value = os.Getenv(strings.TrimPrefix(value, "$"))
			}
			if value != "" {
				path, err := expand(value)
				return nil, path, err
			}
		case "identityfile":
			path, err := expand(value)
			if err != nil {
				return nil, "", err
			}
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				keys = append(keys, path)
			}
		}
	}
	return keys, "", nil
}
