//go:build !windows

package daggercmd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/dagger/dagger/engine/sessionwire"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestExposeSocketIsSoleStatusAuthority(t *testing.T) {
	t.Parallel()
	paths, err := makeExposePaths(t.TempDir(), testCLIValidSessionID())
	require.NoError(t, err)
	holder, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = holder.Close() })
	require.NoError(t, writeExposeRecord(paths.Record, exposeRecord{PID: 999, State: exposeStateReady}))
	request := exposeRequest{Mappings: []exposePortMapping{{Service: "web", ServiceID: "id", Backend: 80, Protocol: sessionwire.NetworkProtocolTCP}}}
	control, err := newExposeControlServer(paths, request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = control.Close() })

	lock, status, err := inspectLocalExpose(t.Context(), paths)
	require.NoError(t, err)
	require.Nil(t, lock)
	require.Equal(t, exposeStateStarting, status.State, "diagnostic ready record was trusted")
	control.ready([]exposedPort{{Service: "web", Frontend: 8080, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP}})
	_, status, err = inspectLocalExpose(t.Context(), paths)
	require.NoError(t, err)
	require.Equal(t, exposeStateReady, status.State)
	require.Equal(t, 8080, status.Ports[0].Frontend)
}

func TestExposeHeldLockWithoutSocketRetriesUntilAcquired(t *testing.T) {
	t.Parallel()
	paths, err := makeExposePaths(t.TempDir(), testCLIValidSessionID())
	require.NoError(t, err)
	holder, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, writeExposeRecord(paths.Record, exposeRecord{PID: 999, State: exposeStateReady}))
	require.NoError(t, os.WriteFile(paths.Socket, []byte("stale"), 0o600))

	type result struct {
		lock *os.File
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		lock, _, err := inspectLocalExpose(t.Context(), paths)
		resultCh <- result{lock: lock, err: err}
	}()
	select {
	case <-resultCh:
		t.Fatal("diagnostic files were treated as a live status response")
	case <-time.After(75 * time.Millisecond):
	}
	require.NoError(t, holder.Close())
	got := <-resultCh
	require.NoError(t, got.err)
	require.NotNil(t, got.lock)
	require.NoError(t, got.lock.Close())
	_, err = os.Stat(paths.Record)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(paths.Socket)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPrepareExposePortsReadyIdempotenceAndDifference(t *testing.T) {
	t.Parallel()
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-ready-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	sessionID := testCLIValidSessionID()
	paths, err := makeExposePaths(stateDir, sessionID)
	require.NoError(t, err)
	holder, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { require.NoError(t, holder.Close()) })
	frontend := 8080
	served := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "web-id", Frontend: &frontend,
		Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}}
	control, err := newExposeControlServer(paths, served)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, control.Close()) })
	control.ready([]exposedPort{{
		Service: "web", Frontend: frontend, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}})

	differentFrontend := 9090
	different := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "web-id", Frontend: &differentFrontend,
		Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}}
	preparation, err := prepareExposePorts(
		t.Context(), sessionID, stateDir, different, true, false, nil,
	)
	require.NoError(t, err)
	require.Nil(t, preparation.Startup)
	require.True(t, preparation.Request.equal(served), "plain expose did not adopt the served request")

	preparation, err = prepareExposePorts(
		t.Context(), sessionID, stateDir, served, false, false, nil,
	)
	require.NoError(t, err)
	require.Nil(t, preparation.Startup)
	require.True(t, preparation.Request.equal(served))

	_, err = prepareExposePorts(
		t.Context(), sessionID, stateDir, different, false, false, nil,
	)
	require.ErrorContains(t, err, "already served with a different port set; use --replace")
}

func TestStopLocalExposeWithoutHolderIsIdempotent(t *testing.T) {
	t.Parallel()
	paths, err := makeExposePaths(t.TempDir(), testCLIValidSessionID())
	require.NoError(t, err)
	require.NoError(t, writeExposeRecord(paths.Record, exposeRecord{PID: 999, State: exposeStateReady}))
	require.NoError(t, os.WriteFile(paths.Socket, []byte("stale"), 0o600))
	require.NoError(t, stopLocalExpose(t.Context(), paths))
	require.NoError(t, stopLocalExpose(t.Context(), paths))
	_, err = os.Stat(paths.Record)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(paths.Socket)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExposeControlStopRequest(t *testing.T) {
	t.Parallel()
	paths, err := makeExposePaths(t.TempDir(), testCLIValidSessionID())
	require.NoError(t, err)
	control, err := newExposeControlServer(paths, exposeRequest{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = control.Close() })
	_, err = exchangeExposeControl(t.Context(), paths.Socket, exposeControlRequest{Action: exposeControlStop})
	require.NoError(t, err)
	select {
	case <-control.stop:
	case <-time.After(time.Second):
		t.Fatal("control stop was not delivered")
	}
}

func TestExposeReadyCommitLivenessAndLockHandoff(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		commit bool
	}{
		{name: "parent dies before commit"},
		{name: "commit transfers lifetime lock", commit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-test-")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
			paths, err := makeExposePaths(stateDir, testCLIValidSessionID())
			require.NoError(t, err)
			lock, acquired, err := tryAcquireExposeLock(paths.Lock)
			require.NoError(t, err)
			require.True(t, acquired)
			serverExit := make(chan struct{})
			starter := fakeExposeProcessStarter(t, paths, serverExit)
			config := exposeServerConfig{SessionID: testCLIValidSessionID(), StateDir: paths.Dir}
			startup, err := spawnExposeServerWith(t.Context(), config, paths, lock, starter)
			require.NoError(t, err)
			require.Equal(t, 8080, startup.Ports[0].Frontend)
			contender, acquired, err := tryAcquireExposeLock(paths.Lock)
			require.NoError(t, err)
			require.False(t, acquired)
			require.Nil(t, contender)

			if test.commit {
				require.NoError(t, startup.Commit(t.Context()))
				contender, acquired, err = tryAcquireExposeLock(paths.Lock)
				require.NoError(t, err)
				require.False(t, acquired, "parent close dropped the child's inherited lock")
				close(serverExit)
				require.Eventually(t, func() bool {
					contender, acquired, err = tryAcquireExposeLock(paths.Lock)
					return err == nil && acquired
				}, time.Second, 10*time.Millisecond)
				require.NoError(t, contender.Close())
			} else {
				close(serverExit)
				require.NoError(t, startup.Abort())
				contender, acquired, err = tryAcquireExposeLock(paths.Lock)
				require.NoError(t, err)
				require.True(t, acquired)
				require.NoError(t, contender.Close())
				_, err = os.Stat(paths.Record)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestExposeCommitCanceledBeforeLinearization(t *testing.T) {
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-cancel-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	paths, err := makeExposePaths(stateDir, testCLIValidSessionID())
	require.NoError(t, err)
	lock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	serverExit := make(chan struct{})
	startup, err := spawnExposeServerWith(
		t.Context(),
		exposeServerConfig{SessionID: testCLIValidSessionID(), StateDir: paths.Dir},
		paths,
		lock,
		fakeExposeProcessStarter(t, paths, serverExit),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("commit canceled"))
	require.EqualError(t, startup.Commit(ctx), "commit canceled")
	require.NoError(t, startup.Abort())
}

func TestExposeServerRejectsListenerLossAfterReady(t *testing.T) {
	harness := newExposeServerHarness(t)
	var listenerCompleted func(h2c.TunnelListenerCompletion)
	harness.hooks.Connect = func(_ context.Context, params client.Params) (exposePortSession, error) {
		listenerCompleted = params.OnTunnelListenerCompleted
		return harness.session, nil
	}
	harness.start()

	ready, err := readExposeChildStatus(t.Context(), harness.statusR, exposeChildPhaseReady)
	require.NoError(t, err)
	require.Len(t, ready.Ports, 1)
	require.NotNil(t, listenerCompleted)
	listenerCompleted(h2c.TunnelListenerCompletion{Addr: "127.0.0.1:8080", Err: errors.New("backend exited")})
	_, err = readExposeChildStatus(t.Context(), harness.statusR, exposeChildPhaseAccepted)
	require.ErrorContains(t, err, "backend exited")
	require.ErrorContains(t, <-harness.wait, "backend exited")
}

func TestExposeServerCommittedRecordFailureIsDiagnostic(t *testing.T) {
	harness := newExposeServerHarness(t)
	harness.hooks.Connect = func(_ context.Context, _ client.Params) (exposePortSession, error) {
		return harness.session, nil
	}
	recordFailed := make(chan struct{})
	recordWrites := 0
	harness.hooks.WriteRecord = func(path string, record exposeRecord) error {
		recordWrites++
		if recordWrites == 2 {
			close(recordFailed)
			return errors.New("injected ready-record failure")
		}
		return writeExposeRecord(path, record)
	}
	harness.start()

	_, err := readExposeChildStatus(t.Context(), harness.statusR, exposeChildPhaseReady)
	require.NoError(t, err)
	_, err = harness.lifecycleW.Write([]byte{exposeCommitByte})
	require.NoError(t, err)
	_, err = readExposeChildStatus(t.Context(), harness.statusR, exposeChildPhaseAccepted)
	require.NoError(t, err)
	<-recordFailed
	select {
	case err := <-harness.wait:
		t.Fatalf("committed server exited after diagnostic record failure: %v", err)
	default:
	}
	close(harness.session.done)
	require.NoError(t, <-harness.wait)
}

type exposeServerHarness struct {
	t          *testing.T
	config     exposeServerConfig
	paths      exposePaths
	lock       *os.File
	statusR    *os.File
	statusW    *os.File
	lifecycleR *os.File
	lifecycleW *os.File
	session    *fakeExposePortSession
	hooks      exposeServerHooks
	wait       chan error
}

func newExposeServerHarness(t *testing.T) *exposeServerHarness {
	t.Helper()
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-server-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	sessionID := testCLIValidSessionID()
	paths, err := makeExposePaths(stateDir, sessionID)
	require.NoError(t, err)
	lock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	statusR, statusW, err := os.Pipe()
	require.NoError(t, err)
	lifecycleR, lifecycleW, err := os.Pipe()
	require.NoError(t, err)
	frontend := 8080
	request := exposeRequest{Mappings: []exposePortMapping{{
		Service: "web", ServiceID: "backend-id", Frontend: &frontend,
		Backend: 80, Protocol: sessionwire.NetworkProtocolTCP,
	}}}
	harness := &exposeServerHarness{
		t: t, config: exposeServerConfig{SessionID: sessionID, StateDir: stateDir, Request: request},
		paths: paths, lock: lock, statusR: statusR, statusW: statusW,
		lifecycleR: lifecycleR, lifecycleW: lifecycleW,
		session: &fakeExposePortSession{
			fakeExposeQueryClient: &fakeExposeQueryClient{t: t}, done: make(chan struct{}),
		},
		wait: make(chan error, 1),
	}
	t.Cleanup(func() {
		_ = harness.statusR.Close()
		_ = harness.statusW.Close()
		_ = harness.lifecycleR.Close()
		_ = harness.lifecycleW.Close()
		_ = harness.lock.Close()
	})
	return harness
}

func (harness *exposeServerHarness) start() {
	harness.t.Helper()
	go func() {
		harness.wait <- serveExposePortServer(
			harness.t.Context(), harness.config, harness.paths,
			harness.lock, harness.statusW, harness.lifecycleR, harness.hooks,
		)
	}()
}

type fakeExposePortSession struct {
	*fakeExposeQueryClient
	done chan struct{}
}

func (session *fakeExposePortSession) Done() <-chan struct{} { return session.done }
func (session *fakeExposePortSession) Close() error          { return nil }

func fakeExposeProcessStarter(
	t *testing.T,
	paths exposePaths,
	serverExit <-chan struct{},
) exposeProcessStarter {
	t.Helper()
	return func(
		_ string,
		lock *os.File,
		statusW *os.File,
		lifecycleR *os.File,
		_ *os.File,
	) (<-chan error, error) {
		lockChild, err := duplicateExposeFile(lock, "child-lock")
		if err != nil {
			return nil, err
		}
		statusChild, err := duplicateExposeFile(statusW, "child-status")
		if err != nil {
			return nil, err
		}
		lifecycleChild, err := duplicateExposeFile(lifecycleR, "child-lifecycle")
		if err != nil {
			return nil, err
		}
		wait := make(chan error, 1)
		go func() {
			defer lockChild.Close()
			defer statusChild.Close()
			defer lifecycleChild.Close()
			if err := writeExposeRecord(paths.Record, exposeRecord{PID: 123, State: exposeStateStarting}); err != nil {
				wait <- err
				return
			}
			if err := writeExposeChildStatus(statusChild, exposeChildStatus{Phase: exposeChildPhaseReady, Ports: []exposedPort{{Service: "web", Frontend: 8080}}}); err != nil {
				wait <- err
				return
			}
			var commit [1]byte
			_, err := io.ReadFull(lifecycleChild, commit[:])
			if err != nil {
				_ = cleanupExposeState(paths)
				wait <- nil
				return
			}
			if commit[0] != exposeCommitByte {
				wait <- errors.New("invalid commit")
				return
			}
			if err := writeExposeChildStatus(statusChild, exposeChildStatus{Phase: exposeChildPhaseAccepted}); err != nil {
				wait <- err
				return
			}
			if err := writeExposeRecord(paths.Record, exposeRecord{PID: 123, State: exposeStateReady}); err != nil {
				wait <- err
				return
			}
			<-serverExit
			_ = cleanupExposeState(paths)
			wait <- nil
		}()
		return wait, nil
	}
}

func duplicateExposeFile(file *os.File, name string) (*os.File, error) {
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func TestExposeMalformedSocketResponseRetriesLock(t *testing.T) {
	t.Parallel()
	paths, err := makeExposePaths(t.TempDir(), testCLIValidSessionID())
	require.NoError(t, err)
	holder, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	listener, err := net.Listen("unix", paths.Socket)
	require.NoError(t, err)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_, _ = io.WriteString(conn, "not-json\n")
			_ = conn.Close()
		}
		_ = listener.Close()
		_ = holder.Close()
	}()
	lock, status, err := inspectLocalExpose(t.Context(), paths)
	require.NoError(t, err)
	require.Nil(t, status)
	require.NotNil(t, lock)
	require.NoError(t, lock.Close())
}
