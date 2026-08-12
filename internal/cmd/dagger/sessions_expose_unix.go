//go:build !windows

package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/dagger/dagger/engine/sessionwire"
	"golang.org/x/sys/unix"
)

const (
	exposeChildLockFD      = 3
	exposeChildStatusFD    = 4
	exposeChildLifecycleFD = 5
)

func exposePlatformSupported() bool { return true }

func tryAcquireExposeLock(path string) (*os.File, bool, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open expose lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("acquire expose lock: %w", err)
	}
	return lock, true, nil
}

// inspectLocalExpose returns either the held lifetime lock or a status read
// from the live holder's control socket. A diagnostic record is never read.
func inspectLocalExpose(ctx context.Context, paths exposePaths) (*os.File, *exposeStatus, error) {
	for {
		lock, acquired, err := tryAcquireExposeLock(paths.Lock)
		if err != nil {
			return nil, nil, err
		}
		if acquired {
			if err := cleanupExposeState(paths); err != nil {
				_ = lock.Close()
				return nil, nil, err
			}
			return lock, nil, nil
		}

		response, err := exchangeExposeControl(ctx, paths.Socket, exposeControlRequest{Action: exposeControlStatus})
		if err == nil && response.Status != nil &&
			(response.Status.State == exposeStateStarting || response.Status.State == exposeStateReady) {
			return nil, response.Status, nil
		}
		if err := waitExposeRetry(ctx); err != nil {
			return nil, nil, err
		}
	}
}

func exchangeExposeControl(
	ctx context.Context,
	socketPath string,
	request exposeControlRequest,
) (exposeControlResponse, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(requestCtx, "unix", socketPath)
	if err != nil {
		return exposeControlResponse{}, err
	}
	defer conn.Close()
	if deadline, ok := requestCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return exposeControlResponse{}, fmt.Errorf("write expose control request: %w", err)
	}
	var response exposeControlResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return exposeControlResponse{}, fmt.Errorf("read expose control response: %w", err)
	}
	if response.Error != "" {
		return exposeControlResponse{}, errors.New(response.Error)
	}
	return response, nil
}

func stopLocalExpose(ctx context.Context, paths exposePaths) error {
	lock, err := stopAndAcquireLocalExpose(ctx, paths)
	if err != nil {
		return err
	}
	cleanupErr := cleanupExposeState(paths)
	return errors.Join(cleanupErr, lock.Close())
}

func stopAndAcquireLocalExpose(ctx context.Context, paths exposePaths) (*os.File, error) {
	for {
		lock, status, err := inspectLocalExpose(ctx, paths)
		if err != nil {
			return nil, err
		}
		if lock != nil {
			return lock, nil
		}
		if status == nil {
			continue
		}
		_, _ = exchangeExposeControl(ctx, paths.Socket, exposeControlRequest{Action: exposeControlStop})
		// Acquisition, rather than the stop response, linearizes completion. If
		// a fresh contender wins, the next loop stops that holder as well.
	}
}

func spawnExposeServer(
	ctx context.Context,
	config exposeServerConfig,
	paths exposePaths,
	lock *os.File,
) (_ *exposeStartup, rerr error) {
	return spawnExposeServerWith(ctx, config, paths, lock, startExposeProcess)
}

type exposeProcessStarter func(
	encodedConfig string,
	lock *os.File,
	statusW *os.File,
	lifecycleR *os.File,
	logFile *os.File,
) (<-chan error, error)

func startExposeProcess(
	encodedConfig string,
	lock *os.File,
	statusW *os.File,
	lifecycleR *os.File,
	logFile *os.File,
) (<-chan error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find dagger executable: %w", err)
	}
	cmd := exec.Command(executable, "--_serve-expose="+encodedConfig)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.ExtraFiles = []*os.File{lock, statusW, lifecycleR}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start expose server: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	return waitCh, nil
}

func spawnExposeServerWith(
	ctx context.Context,
	config exposeServerConfig,
	paths exposePaths,
	lock *os.File,
	start exposeProcessStarter,
) (_ *exposeStartup, rerr error) {
	if err := writeExposeRecord(paths.Record, exposeRecord{
		State: exposeStateStarting, Request: config.Request,
	}); err != nil {
		_ = lock.Close()
		return nil, err
	}

	statusR, statusW, err := os.Pipe()
	if err != nil {
		_ = cleanupExposeState(paths)
		_ = lock.Close()
		return nil, fmt.Errorf("create expose status pipe: %w", err)
	}
	lifecycleR, lifecycleW, err := os.Pipe()
	if err != nil {
		_ = statusR.Close()
		_ = statusW.Close()
		_ = cleanupExposeState(paths)
		_ = lock.Close()
		return nil, fmt.Errorf("create expose lifecycle pipe: %w", err)
	}
	defer func() {
		if rerr != nil {
			_ = statusR.Close()
			_ = statusW.Close()
			_ = lifecycleR.Close()
			_ = lifecycleW.Close()
		}
	}()

	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = cleanupExposeState(paths)
		_ = lock.Close()
		return nil, fmt.Errorf("open expose log: %w", err)
	}
	defer logFile.Close()

	encodedConfig, err := config.encode()
	if err != nil {
		_ = cleanupExposeState(paths)
		_ = lock.Close()
		return nil, err
	}
	waitCh, err := start(encodedConfig, lock, statusW, lifecycleR, logFile)
	if err != nil {
		_ = cleanupExposeState(paths)
		_ = lock.Close()
		return nil, err
	}
	_ = statusW.Close()
	_ = lifecycleR.Close()
	status, statusErr := readExposeChildStatus(ctx, statusR, exposeChildPhaseReady)
	if statusErr != nil {
		_ = statusR.Close()
		_ = lifecycleW.Close()
		<-waitCh
		cleanupErr := cleanupExposeState(paths)
		lockErr := lock.Close()
		if cleanupErr != nil || lockErr != nil {
			return nil, errors.Join(statusErr, cleanupErr, lockErr)
		}
		return nil, statusErr
	}
	return &exposeStartup{
		Ports: status.Ports, lifecycle: lifecycleW, status: statusR, lock: lock,
		wait: waitCh, paths: paths,
	}, nil
}

type exposeControlServer struct {
	listener net.Listener
	stop     chan struct{}
	stopOnce sync.Once

	mu     sync.RWMutex
	status exposeStatus
}

func newExposeControlServer(paths exposePaths, request exposeRequest) (*exposeControlServer, error) {
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		return nil, fmt.Errorf("listen on expose control socket: %w", err)
	}
	server := &exposeControlServer{
		listener: listener,
		stop:     make(chan struct{}),
		status:   exposeStatus{State: exposeStateStarting, Request: request},
	}
	go server.serve()
	return server, nil
}

func (server *exposeControlServer) serve() {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.serveConn(conn)
	}
}

func (server *exposeControlServer) serveConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	var request exposeControlRequest
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		_ = json.NewEncoder(conn).Encode(exposeControlResponse{Error: "malformed expose control request"})
		return
	}
	switch request.Action {
	case exposeControlStatus:
		status := server.snapshot()
		_ = json.NewEncoder(conn).Encode(exposeControlResponse{Status: &status})
	case exposeControlStop:
		_ = json.NewEncoder(conn).Encode(exposeControlResponse{})
		server.stopOnce.Do(func() { close(server.stop) })
	default:
		_ = json.NewEncoder(conn).Encode(exposeControlResponse{Error: "unknown expose control action"})
	}
}

func (server *exposeControlServer) snapshot() exposeStatus {
	server.mu.RLock()
	defer server.mu.RUnlock()
	status := server.status
	status.Ports = append([]exposedPort(nil), status.Ports...)
	return status
}

func (server *exposeControlServer) ready(ports []exposedPort) {
	server.mu.Lock()
	server.status.State = exposeStateReady
	server.status.Ports = append([]exposedPort(nil), ports...)
	server.mu.Unlock()
}

func (server *exposeControlServer) Close() error {
	return server.listener.Close()
}

type exposeServerHooks struct {
	BeforeReady <-chan struct{}
	Connect     func(context.Context, client.Params) (exposePortSession, error)
	WriteRecord func(string, exposeRecord) error
}

func runExposePortServer(ctx context.Context, encodedConfig string) error {
	return runExposePortServerWithHooks(ctx, encodedConfig, exposeServerHooks{})
}

func runExposePortServerWithHooks(
	ctx context.Context,
	encodedConfig string,
	hooks exposeServerHooks,
) (rerr error) {
	config, err := decodeExposeServerConfig(encodedConfig)
	if err != nil {
		return err
	}
	paths, err := makeExposePaths(config.StateDir, config.SessionID)
	if err != nil {
		return err
	}
	lock := os.NewFile(exposeChildLockFD, "expose-lock")
	statusW := os.NewFile(exposeChildStatusFD, "expose-status")
	lifecycleR := os.NewFile(exposeChildLifecycleFD, "expose-lifecycle")
	if lock == nil || statusW == nil || lifecycleR == nil {
		return errors.New("expose server inherited descriptors are unavailable")
	}
	fmt.Fprintf(os.Stderr, "starting port server for detached session %s\n", config.SessionID)
	defer lock.Close()
	defer statusW.Close()
	defer lifecycleR.Close()
	return serveExposePortServer(ctx, config, paths, lock, statusW, lifecycleR, hooks)
}

func serveExposePortServer(
	ctx context.Context,
	config exposeServerConfig,
	paths exposePaths,
	lock *os.File,
	statusW *os.File,
	lifecycleR *os.File,
	hooks exposeServerHooks,
) (rerr error) {
	serverCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	serverCtx, cancel := context.WithCancelCause(serverCtx)
	defer cancel(errors.New("expose server stopped"))

	commitRead := make(chan struct {
		value byte
		err   error
	}, 1)
	go func() {
		var commit [1]byte
		_, err := io.ReadFull(lifecycleR, commit[:])
		if err != nil {
			cancel(fmt.Errorf("expose parent ended before commit: %w", err))
		}
		commitRead <- struct {
			value byte
			err   error
		}{value: commit[0], err: err}
	}()

	readySent := false
	acceptedSent := false
	defer func() {
		if rerr != nil && !acceptedSent {
			phase := exposeChildPhaseReady
			if readySent {
				phase = exposeChildPhaseAccepted
			}
			_ = writeExposeChildStatus(statusW, exposeChildStatus{Phase: phase, Error: rerr.Error()})
		}
	}()
	writeRecord := hooks.WriteRecord
	if writeRecord == nil {
		writeRecord = writeExposeRecord
	}
	if err := writeRecord(paths.Record, exposeRecord{
		PID: os.Getpid(), State: exposeStateStarting, Request: config.Request,
	}); err != nil {
		return err
	}
	control, err := newExposeControlServer(paths, config.Request)
	if err != nil {
		return err
	}
	defer func() {
		if err := control.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintln(os.Stderr, "close expose control socket:", err)
		}
		if err := cleanupExposeState(paths); err != nil {
			fmt.Fprintln(os.Stderr, "clean expose state:", err)
		}
	}()

	monitor := newExposeListenerMonitor()
	fmt.Fprintln(os.Stderr, "attaching to detached session")
	params := client.Params{
		AttachSessionID: config.SessionID,
		RunnerHost:      RunnerHost,
		OnTunnelListenerCompleted: func(completion h2c.TunnelListenerCompletion) {
			monitor.listenerEnded(completion.Addr, completion.Err)
		},
	}
	connect := hooks.Connect
	if connect == nil {
		connect = func(ctx context.Context, params client.Params) (exposePortSession, error) {
			return client.Connect(ctx, params)
		}
	}
	session, err := connect(serverCtx, params)
	if err != nil {
		return fmt.Errorf("attach expose server: %w", err)
	}
	fmt.Fprintln(os.Stderr, "publishing detached service ports")
	defer func() {
		monitor.shutdown()
		if err := session.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close expose attachment:", err)
		}
	}()

	ports, err := publishExposePorts(serverCtx, session, config.Request)
	if err != nil {
		return err
	}
	if loss := monitor.loss(); loss != nil {
		return loss
	}
	if hooks.BeforeReady != nil {
		select {
		case <-hooks.BeforeReady:
		case <-monitor.notify:
			return monitor.loss()
		case <-session.Done():
			return errors.New("session ended before expose server became ready")
		case <-serverCtx.Done():
			return context.Cause(serverCtx)
		}
	}
	if err := writeExposeChildStatus(statusW, exposeChildStatus{Phase: exposeChildPhaseReady, Ports: ports}); err != nil {
		return fmt.Errorf("report expose server readiness: %w", err)
	}
	readySent = true
	fmt.Fprintln(os.Stderr, "all detached service ports are ready; awaiting commit")

	select {
	case commit := <-commitRead:
		if commit.err != nil {
			return commit.err
		}
		if commit.value != exposeCommitByte {
			return fmt.Errorf("invalid expose commit byte %d", commit.value)
		}
		if err := monitor.commit(); err != nil {
			return err
		}
	case <-monitor.notify:
		return monitor.loss()
	case <-session.Done():
		return errors.New("session ended before expose server commit")
	case <-control.stop:
		return errors.New("expose server stopped before commit")
	case <-serverCtx.Done():
		return context.Cause(serverCtx)
	}

	control.ready(ports)
	if err := writeExposeChildStatus(statusW, exposeChildStatus{Phase: exposeChildPhaseAccepted}); err != nil {
		return fmt.Errorf("report expose server commit acceptance: %w", err)
	}
	acceptedSent = true
	if err := writeRecord(paths.Record, exposeRecord{
		PID: os.Getpid(), State: exposeStateReady, Request: config.Request,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "write committed expose record:", err)
	}
	fmt.Fprintln(os.Stderr, "detached service ports committed")

	select {
	case <-session.Done():
		return nil
	case <-monitor.notify:
		return monitor.loss()
	case <-control.stop:
		return nil
	case <-serverCtx.Done():
		return nil
	}
}

func publishExposePorts(
	ctx context.Context,
	session exposeQueryClient,
	request exposeRequest,
) ([]exposedPort, error) {
	byService := map[string][]exposePortMapping{}
	for _, mapping := range request.Mappings {
		byService[mapping.Service] = append(byService[mapping.Service], mapping)
	}
	serviceNames := make([]string, 0, len(byService))
	for name := range byService {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	var published []exposedPort
	for _, name := range serviceNames {
		mappings := byService[name]
		inputs := make([]map[string]any, len(mappings))
		for i, mapping := range mappings {
			input := map[string]any{
				"backend": mapping.Backend, "protocol": mapping.Protocol,
			}
			if mapping.Frontend != nil {
				input["frontend"] = *mapping.Frontend
			}
			inputs[i] = input
		}
		var started struct {
			Host struct {
				Tunnel struct {
					Start string `json:"start"`
				} `json:"tunnel"`
			} `json:"host"`
		}
		const startQuery = `query StartExposeService($service: ID!, $ports: [PortForward!]!) {
  host {
    tunnel(service: $service, ports: $ports, native: false) {
      start
    }
  }
}`
		if err := session.Do(ctx, startQuery, "StartExposeService", map[string]any{
			"service": mappings[0].ServiceID, "ports": inputs,
		}, &started); err != nil {
			return nil, fmt.Errorf("start port tunnel for %s: %w", name, err)
		}
		if started.Host.Tunnel.Start == "" {
			return nil, fmt.Errorf("start port tunnel for %s: engine returned an empty service ID", name)
		}
		var data struct {
			Node struct {
				Ports []struct {
					Port     int                         `json:"port"`
					Protocol sessionwire.NetworkProtocol `json:"protocol"`
				} `json:"ports"`
			} `json:"node"`
		}
		const portsQuery = `query ExposeServicePorts($service: ID!) {
  node(id: $service) {
    ... on Service { ports { port protocol } }
  }
}`
		if err := session.Do(ctx, portsQuery, "ExposeServicePorts", map[string]any{
			"service": started.Host.Tunnel.Start,
		}, &data); err != nil {
			return nil, fmt.Errorf("publish ports for %s: %w", name, err)
		}
		actual := data.Node.Ports
		if len(actual) != len(mappings) {
			return nil, fmt.Errorf(
				"publish ports for %s: engine returned %d ports for %d mappings",
				name, len(actual), len(mappings),
			)
		}
		for i, port := range actual {
			published = append(published, exposedPort{
				Service: name, Frontend: port.Port, Backend: mappings[i].Backend,
				Protocol: port.Protocol,
			})
		}
	}
	return published, nil
}

type exposeQueryClient interface {
	Do(context.Context, string, string, map[string]any, any) error
}

type exposePortSession interface {
	exposeQueryClient
	Done() <-chan struct{}
	Close() error
}
