package daggercmd

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sessionwire"
)

const (
	exposeStateStarting = "starting"
	exposeStateReady    = "ready"

	exposeControlStatus = "status"
	exposeControlStop   = "stop"

	exposeCommitByte = byte(1)

	// Darwin's sockaddr_un.sun_path is the smallest POSIX target in scope:
	// 104 bytes including the terminating NUL.
	maxExposeSocketPathLen = 103
)

var serveExposeConfig string

type exposePortMapping struct {
	Service   string                      `json:"service"`
	ServiceID string                      `json:"service_id"`
	Frontend  *int                        `json:"frontend"`
	Backend   int                         `json:"backend"`
	Protocol  sessionwire.NetworkProtocol `json:"protocol"`
}

type exposeRequest struct {
	Mappings []exposePortMapping `json:"mappings"`
}

func (request exposeRequest) canonical() exposeRequest {
	canonical := exposeRequest{Mappings: append([]exposePortMapping(nil), request.Mappings...)}
	sort.Slice(canonical.Mappings, func(i, j int) bool {
		left, right := canonical.Mappings[i], canonical.Mappings[j]
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		if left.ServiceID != right.ServiceID {
			return left.ServiceID < right.ServiceID
		}
		if left.Backend != right.Backend {
			return left.Backend < right.Backend
		}
		if left.Protocol != right.Protocol {
			return left.Protocol < right.Protocol
		}
		if left.Frontend == nil {
			return right.Frontend != nil
		}
		if right.Frontend == nil {
			return false
		}
		return *left.Frontend < *right.Frontend
	})
	return canonical
}

func (request exposeRequest) equal(other exposeRequest) bool {
	left, err := json.Marshal(request.canonical())
	if err != nil {
		return false
	}
	right, err := json.Marshal(other.canonical())
	return err == nil && string(left) == string(right)
}

type exposedPort struct {
	Service  string                      `json:"service"`
	Frontend int                         `json:"frontend"`
	Backend  int                         `json:"backend"`
	Protocol sessionwire.NetworkProtocol `json:"protocol"`
}

func (port exposedPort) URL() string {
	// Match ordinary up's friendly HTTP rendering for conventional web ports;
	// retain an explicit tcp scheme for arbitrary stream services.
	scheme := "tcp"
	switch port.Backend {
	case 80, 3000, 8000, 8080:
		scheme = "http"
	case 443, 8443:
		scheme = "https"
	}
	return fmt.Sprintf("%s://localhost:%d", scheme, port.Frontend)
}

type exposeStatus struct {
	State   string        `json:"state"`
	Request exposeRequest `json:"request"`
	Ports   []exposedPort `json:"ports,omitempty"`
}

type exposeRecord struct {
	PID     int           `json:"pid,omitempty"`
	State   string        `json:"state"`
	Request exposeRequest `json:"request"`
}

type exposeControlRequest struct {
	Action string `json:"action"`
}

type exposeControlResponse struct {
	Status *exposeStatus `json:"status,omitempty"`
	Error  string        `json:"error,omitempty"`
}

type exposeChildStatus struct {
	Ports []exposedPort `json:"ports,omitempty"`
	Error string        `json:"error,omitempty"`
}

type exposeServerConfig struct {
	SessionID string        `json:"session_id"`
	StateDir  string        `json:"state_dir"`
	Request   exposeRequest `json:"request"`
}

func (config exposeServerConfig) encode() (string, error) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode expose server config: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeExposeServerConfig(encoded string) (exposeServerConfig, error) {
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return exposeServerConfig{}, fmt.Errorf("decode expose server config: %w", err)
	}
	var config exposeServerConfig
	if err := json.Unmarshal(payload, &config); err != nil {
		return exposeServerConfig{}, fmt.Errorf("decode expose server config: %w", err)
	}
	if err := engine.ValidateSessionID(config.SessionID); err != nil {
		return exposeServerConfig{}, fmt.Errorf("invalid expose session ID: %w", err)
	}
	if config.StateDir == "" {
		return exposeServerConfig{}, errors.New("expose server state directory is empty")
	}
	config.Request = config.Request.canonical()
	return config, nil
}

type exposePaths struct {
	Dir    string
	Record string
	Lock   string
	Socket string
	Log    string
}

func exposeStateDirectory() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home directory: %w", err)
		}
		stateHome = filepath.Join(homeDir, ".local", "state")
	}
	dir := filepath.Join(stateHome, "dagger", "expose")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create expose state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("restrict expose state directory: %w", err)
	}
	return dir, nil
}

func makeExposePaths(stateDir, sessionID string) (exposePaths, error) {
	digest := sha256.Sum256([]byte(sessionID))
	paths := exposePaths{
		Dir:    stateDir,
		Record: filepath.Join(stateDir, sessionID+".pid"),
		Lock:   filepath.Join(stateDir, sessionID+".lock"),
		Socket: filepath.Join(stateDir, fmt.Sprintf("%x.sock", digest[:8])),
		Log:    filepath.Join(stateDir, sessionID+".log"),
	}
	if len(paths.Socket) > maxExposeSocketPathLen {
		return exposePaths{}, fmt.Errorf(
			"expose control socket path is too long (%d bytes; maximum %d): %s",
			len(paths.Socket), maxExposeSocketPathLen, paths.Socket,
		)
	}
	return paths, nil
}

func writeExposeRecord(path string, record exposeRecord) (rerr error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".expose-record-*")
	if err != nil {
		return fmt.Errorf("create expose record: %w", err)
	}
	tmpPath := tmp.Name()
	tmpOpen := true
	defer func() {
		if tmpOpen {
			rerr = errors.Join(rerr, tmp.Close())
		}
		if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rerr = errors.Join(rerr, removeErr)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict expose record: %w", err)
	}
	if err := json.NewEncoder(tmp).Encode(record); err != nil {
		return fmt.Errorf("write expose record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync expose record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close expose record: %w", err)
	}
	tmpOpen = false
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace expose record: %w", err)
	}
	return nil
}

func cleanupExposeState(paths exposePaths) error {
	var rerr error
	for _, path := range []string{paths.Record, paths.Socket} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			rerr = errors.Join(rerr, fmt.Errorf("remove %s: %w", path, err))
		}
	}
	return rerr
}

func waitExposeRetry(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

type exposeListenerMonitor struct {
	mu           sync.Mutex
	lost         error
	committed    bool
	shuttingDown bool
	notify       chan struct{}
}

func newExposeListenerMonitor() *exposeListenerMonitor {
	return &exposeListenerMonitor{notify: make(chan struct{}, 1)}
}

func (monitor *exposeListenerMonitor) listenerEnded(addr string, err error) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.shuttingDown || monitor.lost != nil {
		return
	}
	if err == nil {
		err = errors.New("listener stream ended")
	}
	monitor.lost = fmt.Errorf("published listener %s ended: %w", addr, err)
	select {
	case monitor.notify <- struct{}{}:
	default:
	}
}

func (monitor *exposeListenerMonitor) commit() error {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.lost != nil {
		return monitor.lost
	}
	monitor.committed = true
	return nil
}

func (monitor *exposeListenerMonitor) loss() error {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	return monitor.lost
}

func (monitor *exposeListenerMonitor) shutdown() {
	monitor.mu.Lock()
	monitor.shuttingDown = true
	monitor.mu.Unlock()
}

type exposeStartup struct {
	Ports []exposedPort

	mu        sync.Mutex
	lifecycle *os.File
	lock      *os.File
	wait      <-chan error
	paths     exposePaths
	committed bool
	finished  bool
}

func (startup *exposeStartup) Commit() error {
	startup.mu.Lock()
	defer startup.mu.Unlock()
	if startup.finished {
		return errors.New("expose startup already finished")
	}
	if _, err := startup.lifecycle.Write([]byte{exposeCommitByte}); err != nil {
		return fmt.Errorf("commit expose server: %w", err)
	}
	startup.committed = true
	startup.finished = true
	// The commit byte is the acknowledgement boundary. Descriptor-close errors
	// after that write must not turn an acknowledged server into a rollback.
	_ = startup.lifecycle.Close()
	_ = startup.lock.Close()
	return nil
}

func (startup *exposeStartup) Abort() error {
	startup.mu.Lock()
	defer startup.mu.Unlock()
	if startup.committed {
		return errors.New("cannot abort committed expose server")
	}
	if startup.finished {
		return nil
	}
	startup.finished = true
	closeErr := startup.lifecycle.Close()
	waitErr := <-startup.wait
	cleanupErr := cleanupExposeState(startup.paths)
	lockErr := startup.lock.Close()
	return errors.Join(closeErr, waitErr, cleanupErr, lockErr)
}

func writeExposeChildStatus(w io.Writer, status exposeChildStatus) error {
	return json.NewEncoder(w).Encode(status)
}

func readExposeChildStatus(ctx context.Context, r io.Reader) (exposeChildStatus, error) {
	type result struct {
		status exposeChildStatus
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		var status exposeChildStatus
		err := json.NewDecoder(r).Decode(&status)
		resultCh <- result{status: status, err: err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			return exposeChildStatus{}, fmt.Errorf("read expose server status: %w", result.err)
		}
		if result.status.Error != "" {
			return exposeChildStatus{}, errors.New(result.status.Error)
		}
		return result.status, nil
	case <-ctx.Done():
		return exposeChildStatus{}, context.Cause(ctx)
	}
}

func formatExposedPorts(ports []exposedPort) string {
	lines := make([]string, 0, len(ports))
	for _, port := range ports {
		lines = append(lines, fmt.Sprintf("  %s  %s", port.Service, port.URL()))
	}
	return strings.Join(lines, "\n")
}
