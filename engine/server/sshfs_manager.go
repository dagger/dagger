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
	for range probeAttempts {
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
		"-o", "ServerAliveInterval=15", // keepalive
		"-p", parsed.port,
	}
	if os.Getenv("DAGGER_SSHFS_DEBUG") == "1" {
		args = append(args, "-o", "sshfs_debug", "-o", "loglevel=DEBUG3", "-d")
	}

	logrus.WithFields(logrus.Fields{
		"id":        id,
		"endpoint":  sshfsEndpoint,
		"port":      parsed.port,
		"mountPath": mp,
		"args":      args,
	}).Info("sshfs: mounting")

	// CRITICAL: Use background context so sshfs persists beyond the current GraphQL request.
	// The mount must live as long as the engine is running.
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

// parseSSHEndpoint accepts either scp-style user@host[:port][/path] or ssh://user@host[:port]/path.
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

	atIdx := strings.Index(ep, "@")
	if atIdx < 0 {
		return parsedSSHEndpoint{}, fmt.Errorf("missing '@'")
	}
	user := ep[:atIdx]
	hostRest := ep[atIdx+1:]
	if user == "" {
		return parsedSSHEndpoint{}, fmt.Errorf("empty user")
	}
	var hostPart, pathPart string
	slashIdx := strings.Index(hostRest, "/")
	if slashIdx >= 0 {
		hostPart = hostRest[:slashIdx]
		pathPart = hostRest[slashIdx:]
	} else {
		hostPart = hostRest
		pathPart = "/"
	}
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

// isMounted checks /proc/self/mountinfo for the given mount point path.
func isMounted(mountPath string) bool {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	needle := []byte(" " + mountPath + " ")
	return bytes.Contains(data, needle)
}

// RegisterSSHFSVolume writes the key plaintexts to digest-named files under
// the engine root (so repeat calls with the same keys reuse them) and mounts
// the endpoint via sshfs.
func (srv *Server) RegisterSSHFSVolume(ctx context.Context, endpoint string, privateKey, publicKey *core.Secret) (*core.Volume, error) {
	privPlain, err := privateKey.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read sshfs private key plaintext: %w", err)
	}
	pubPlain, err := publicKey.Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read sshfs public key plaintext: %w", err)
	}

	keysDir := filepath.Join(srv.rootDir, "ssh-keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		return nil, fmt.Errorf("create ssh keys dir: %w", err)
	}
	privPath := filepath.Join(keysDir, digest.FromBytes(privPlain).Encoded())
	pubPath := filepath.Join(keysDir, digest.FromBytes(pubPlain).Encoded())
	if err := os.WriteFile(privPath, privPlain, 0o600); err != nil {
		return nil, fmt.Errorf("write ssh private key: %w", err)
	}
	if err := os.WriteFile(pubPath, pubPlain, 0o600); err != nil {
		return nil, fmt.Errorf("write ssh public key: %w", err)
	}

	id, mp, err := srv.sshfsMgr.ensureMounted(ctx, endpoint, privPath, pubPath)
	if err != nil {
		return nil, err
	}
	return &core.Volume{ID: id, MountPath: mp}, nil
}
