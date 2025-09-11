package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
)

type sshfsMount struct {
	id        string
	endpoint  string
	mountPath string
	refCount  int
	proc      *os.Process
}

type sshfsManager struct {
	rootDir string
	mu      sync.Mutex
	mounts  map[string]*sshfsMount
}

func newSSHFSManager(rootDir string) *sshfsManager {
	return &sshfsManager{
		rootDir: rootDir,
		mounts:  map[string]*sshfsMount{},
	}
}

type parsedSSHEndpoint struct {
	user string
	host string
	port string
	path string
}

// ensureMounted mounts the endpoint with sshfs using the provided private/public key files (paths)
// and returns the mount id and local mount path.
func (m *sshfsManager) ensureMounted(ctx context.Context, endpoint string, privateKeyPath, publicKeyPath string) (string, string, error) {
	parsed, err := parseSSHEndpoint(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("invalid sshfs endpoint %q: %w", endpoint, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// id is sha256(endpoint + pubKey path)
	h := sha256.Sum256([]byte(endpoint + ":" + publicKeyPath))
	id := hex.EncodeToString(h[:])
	if ex, ok := m.mounts[id]; ok {
		ex.refCount++
		logrus.WithFields(logrus.Fields{
			"id":        id,
			"endpoint":  ex.endpoint,
			"mountPath": ex.mountPath,
			"refCount":  ex.refCount,
		}).Info("sshfs: reusing existing mount")
		return id, ex.mountPath, nil
	}

	// create mount dir
	mp := filepath.Join(m.rootDir, "sshfs", id)
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create mount dir: %w", err)
	}

	sshfsEndpoint := fmt.Sprintf("%s@%s:%s", parsed.user, parsed.host, parsed.path)

	// Optional SSH connectivity probe (fast failure instead of waiting for sshfs to hang)
	probeAttempts := 5
	var probeErr error
	for i := 0; i < probeAttempts; i++ {
		// BatchMode + strict host key off for test/ephemeral usage
		cmd := exec.CommandContext(ctx, "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "BatchMode=yes", "-i", privateKeyPath, "-p", parsed.port, fmt.Sprintf("%s@%s", parsed.user, parsed.host), "true")
		if err := cmd.Run(); err == nil {
			probeErr = nil
			break
		} else {
			probeErr = err
			select {
			case <-ctx.Done():
				return "", "", fmt.Errorf("context canceled during ssh probe: %w", ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if probeErr != nil {
		return "", "", fmt.Errorf("ssh connectivity probe failed (host=%s port=%s): %w", parsed.host, parsed.port, probeErr)
	}

	if st, err := os.Stat("/dev/fuse"); err != nil || (st.Mode()&os.ModeDevice) == 0 {
		return "", "", fmt.Errorf("/dev/fuse not available inside engine container: %w", err)
	}

	args := []string{
		sshfsEndpoint,
		mp,
		"-f", // foreground mode - don't daemonize, stay attached
		"-o", fmt.Sprintf("IdentityFile=%s", privateKeyPath),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "reconnect", // automatically reconnect on connection loss
		"-o", "ServerAliveInterval=15", // send keepalive every 15s
		"-p", parsed.port,
	}
	if os.Getenv("DAGGER_SSHFS_DEBUG") == "1" {
		args = append(args, "-o", "sshfs_debug", "-o", "loglevel=DEBUG3", "-d") // -d for debug mode
	}

	logrus.WithFields(logrus.Fields{
		"id":        id,
		"endpoint":  sshfsEndpoint,
		"port":      parsed.port,
		"mountPath": mp,
		"args":      args,
	}).Info("sshfs: mounting")

	// CRITICAL: Use background context so sshfs persists beyond the current GraphQL request
	// The mount needs to live as long as the engine is running, not just this request
	cmd := exec.CommandContext(context.Background(), "sshfs", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		logrus.WithError(err).WithField("endpoint", sshfsEndpoint).Error("sshfs: start failed")
		return "", "", fmt.Errorf("sshfs start failed (endpoint=%s): %w", sshfsEndpoint, err)
	}

	logrus.WithFields(logrus.Fields{
		"id":  id,
		"pid": cmd.Process.Pid,
	}).Info("sshfs: process started")

	deadline := time.Now().Add(20 * time.Second)
	mounted := false
	for !mounted && time.Now().Before(deadline) {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			logrus.WithError(ctx.Err()).WithField("id", id).Error("sshfs: context canceled during mount wait")
			return "", "", fmt.Errorf("context canceled while waiting for sshfs mount: %w", ctx.Err())
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			_ = cmd.Wait()
			logrus.WithFields(logrus.Fields{
				"id":       id,
				"exitCode": cmd.ProcessState.ExitCode(),
			}).Error("sshfs: process exited early")
			return "", "", fmt.Errorf("sshfs exited early (endpoint=%s)", sshfsEndpoint)
		}
		if isMounted(mp) {
			mounted = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !mounted {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logrus.WithField("id", id).Error("sshfs: mount readiness timeout")
		return "", "", fmt.Errorf("sshfs mount readiness timeout (endpoint=%s)", sshfsEndpoint)
	}

	logrus.WithFields(logrus.Fields{
		"id":        id,
		"pid":       cmd.Process.Pid,
		"mountPath": mp,
	}).Info("sshfs: mount confirmed ready")

	// Double-check the mount is accessible
	if _, err := os.Stat(mp); err != nil {
		logrus.WithError(err).WithField("mountPath", mp).Error("sshfs: mount path not accessible after successful mount")
	}

	go func(pid int) {
		if err := cmd.Wait(); err != nil {
			logrus.WithFields(logrus.Fields{"id": id, "pid": pid, "err": err}).Error("sshfs: process exited with error - MOUNT WILL BE DISCONNECTED")
		} else {
			logrus.WithFields(logrus.Fields{"id": id, "pid": pid}).Error("sshfs: process exited cleanly - MOUNT WILL BE DISCONNECTED")
		}
	}(cmd.Process.Pid)

	mount := &sshfsMount{id: id, endpoint: sshfsEndpoint, mountPath: mp, refCount: 1, proc: cmd.Process}
	m.mounts[id] = mount
	logrus.WithFields(logrus.Fields{
		"id":        id,
		"endpoint":  sshfsEndpoint,
		"mountPath": mp,
	}).Info("sshfs: mounted")
	return id, mp, nil
}

// parseSSHEndpoint accepts either scp-style user@host[:port][/path] or ssh://user@host[:port]/path
// Returns user, host, port (string), path, error.
func parseSSHEndpoint(ep string) (parsedSSHEndpoint, error) {
	if strings.HasPrefix(ep, "ssh://") {
		u, err := url.Parse(ep)
		if err != nil {
			return parsedSSHEndpoint{}, fmt.Errorf("invalid sshfs endpoint %q: %w", ep, err)
		}
		user := u.User.Username()
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "22"
		}
		p := u.Path
		if p == "" {
			p = "/"
		}
		if user == "" || host == "" {
			return parsedSSHEndpoint{}, fmt.Errorf("missing user or host")
		}
		return parsedSSHEndpoint{user: user, host: host, port: port, path: p}, nil
	}
	// scp style: user@host:port/path OR user@host:/path OR user@host/path
	// First split user@rest
	atIdx := strings.Index(ep, "@")
	if atIdx < 0 {
		return parsedSSHEndpoint{}, fmt.Errorf("missing '@'")
	}
	user := ep[:atIdx]
	hostRest := ep[atIdx+1:]
	if user == "" {
		return parsedSSHEndpoint{}, fmt.Errorf("empty user")
	}
	// path part starts at first ':' followed by '/' or first '/'.
	var hostPart, pathPart string
	// try to find '/' that begins path
	slashIdx := strings.Index(hostRest, "/")
	if slashIdx >= 0 {
		hostPart = hostRest[:slashIdx]
		pathPart = hostRest[slashIdx:]
	} else {
		hostPart = hostRest
		pathPart = "/"
	}
	// hostPart may contain :port
	port := "22"
	if colonIdx := strings.Index(hostPart, ":"); colonIdx >= 0 {
		pStr := hostPart[colonIdx+1:]
		hostPart = hostPart[:colonIdx]
		if pStr != "" {
			if _, err := strconv.Atoi(pStr); err == nil {
				port = pStr
			}
		}
	}
	if hostPart == "" {
		return parsedSSHEndpoint{}, fmt.Errorf("empty host")
	}
	return parsedSSHEndpoint{user: user, host: hostPart, port: port, path: pathPart}, nil
}

// dynamicHostCandidates returns possible host substitutions when original is loopback.
// It collects values from env DAGGER_SSHFS_HOST_CANDIDATES (comma-separated) and
// auto-detects default gateway via /proc/net/route (Linux) as a last resort.
// dynamic host resolution removed: engine now trusts the provided host directly.

// isMounted checks /proc/self/mountinfo for the given mount point path.
func isMounted(mountPath string) bool {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	// naive substring match bounded by spaces and slash; acceptable for internal use
	// lines look like: <id> <parent> <major:minor> <root> <mountPoint> <options> ...
	needle := []byte(" " + mountPath + " ")
	return bytes.Contains(data, needle)
}

func (m *sshfsManager) release(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ex, ok := m.mounts[id]
	if !ok {
		return fmt.Errorf("unknown sshfs mount id %s", id)
	}
	ex.refCount--
	if ex.refCount > 0 {
		logrus.WithFields(logrus.Fields{
			"id":       id,
			"refCount": ex.refCount,
		}).Info("sshfs: mount still in use")
		return nil
	}
	// attempt to unmount
	logrus.WithFields(logrus.Fields{
		"id":        id,
		"mountPath": ex.mountPath,
	}).Info("sshfs: unmounting")
	cmd := exec.CommandContext(ctx, "fusermount", "-u", ex.mountPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		logrus.WithError(err).Warn("sshfs: fusermount failed, falling back to umount")
		// try umount fallback
		cmd2 := exec.CommandContext(ctx, "umount", ex.mountPath)
		cmd2.Stdout = os.Stdout
		cmd2.Stderr = os.Stderr
		_ = cmd2.Run() // ignore error; best-effort
	}
	delete(m.mounts, id)
	// ensure background process is gone (best effort)
	if ex.proc != nil {
		// if it's still running after unmount, kill it
		if err := ex.proc.Signal(os.Signal(syscall.Signal(0))); err == nil { // process likely alive
			_ = ex.proc.Kill()
		}
	}
	// remove dir
	_ = os.RemoveAll(ex.mountPath)
	logrus.WithField("id", id).Info("sshfs: unmounted and cleaned up")
	return nil
}

// RegisterSSHFSVolume on the server will mount the sshfs volume and return a Volume object.
func (srv *Server) RegisterSSHFSVolume(ctx context.Context, endpoint string, privateKey digest.Digest, publicKey digest.Digest) (*core.Volume, error) {
	client, err := srv.clientFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}
	// retrieve secrets from client's secret store
	privPlain, err := client.secretStore.GetSecretPlaintext(ctx, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key plaintext: %w", err)
	}
	pubPlain, err := client.secretStore.GetSecretPlaintext(ctx, publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key plaintext: %w", err)
	}

	// write keys to secure temp files under server root
	keysDir := filepath.Join(srv.rootDir, "ssh-keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create keys dir: %w", err)
	}
	privPath := filepath.Join(keysDir, privateKey.String())
	pubPath := filepath.Join(keysDir, publicKey.String())
	if err := os.WriteFile(privPath, privPlain, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write private key: %w", err)
	}
	if err := os.WriteFile(pubPath, pubPlain, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write public key: %w", err)
	}

	if srv.sshfsMgr == nil {
		srv.sshfsMgr = newSSHFSManager(srv.rootDir)
	}

	id, mp, err := srv.sshfsMgr.ensureMounted(ctx, endpoint, privPath, pubPath)
	if err != nil {
		return nil, err
	}

	vol := &core.Volume{ID: id, MountPath: mp}
	return vol, nil
}

// helper to release a registered volume
func (srv *Server) ReleaseSSHFSVolume(ctx context.Context, id string) error {
	if srv.sshfsMgr == nil {
		return fmt.Errorf("sshfs manager not initialized")
	}
	return srv.sshfsMgr.release(ctx, id)
}
