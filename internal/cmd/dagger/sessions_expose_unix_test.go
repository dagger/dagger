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

func TestStopAndReplaceLinearizationStopsRivalWinner(t *testing.T) {
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-stop-race-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	paths, err := makeExposePaths(stateDir, testCLIValidSessionID())
	require.NoError(t, err)

	firstLock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	firstControl, err := newExposeControlServer(paths, exposeRequest{})
	require.NoError(t, err)

	firstStopped := make(chan struct{})
	secondStopped := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	stopCount := 0
	afterStop := func() {
		stopCount++
		switch stopCount {
		case 1:
			close(firstStopped)
			<-releaseFirst
		case 2:
			close(secondStopped)
			<-releaseSecond
		}
	}
	type stopResult struct {
		lock *os.File
		err  error
	}
	resultCh := make(chan stopResult, 1)
	go func() {
		lock, err := stopAndAcquireLocalExposeWith(t.Context(), paths, afterStop)
		resultCh <- stopResult{lock: lock, err: err}
	}()

	<-firstStopped
	select {
	case <-firstControl.stop:
	default:
		t.Fatal("first holder did not receive stop")
	}
	require.NoError(t, firstControl.Close())
	if err := os.Remove(paths.Socket); err != nil {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.NoError(t, firstLock.Close())

	rivalLock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired, "stopper acquired before the injected rival")
	rivalControl, err := newExposeControlServer(paths, exposeRequest{})
	require.NoError(t, err)
	close(releaseFirst)

	<-secondStopped
	select {
	case <-rivalControl.stop:
	default:
		t.Fatal("rival winner did not receive stop")
	}
	require.NoError(t, rivalControl.Close())
	if err := os.Remove(paths.Socket); err != nil {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.NoError(t, rivalLock.Close())
	close(releaseSecond)

	result := <-resultCh
	require.NoError(t, result.err)
	require.NotNil(t, result.lock)
	require.Equal(t, 2, stopCount)
	require.NoError(t, result.lock.Close())
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

func TestExposePrecommitChildFailureRetainsCleanupOwnershipAgainstContender(t *testing.T) {
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-precommit-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	paths, err := makeExposePaths(stateDir, testCLIValidSessionID())
	require.NoError(t, err)
	lock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)

	childReported := make(chan struct{})
	childExit := make(chan struct{})
	starter := func(
		_ string,
		lock *os.File,
		statusW *os.File,
		_ *os.File,
		_ *os.File,
	) (<-chan error, error) {
		childLock, err := duplicateExposeFile(lock, "failing-child-lock")
		if err != nil {
			return nil, err
		}
		childStatus, err := duplicateExposeFile(statusW, "failing-child-status")
		if err != nil {
			return nil, err
		}
		wait := make(chan error, 1)
		go func() {
			defer childLock.Close()
			defer childStatus.Close()
			_ = writeExposeRecord(paths.Record, exposeRecord{PID: 123, State: exposeStateStarting})
			_ = os.WriteFile(paths.Socket, []byte("failing child"), 0o600)
			_ = writeExposeChildStatus(childStatus, exposeChildStatus{
				Phase: exposeChildPhaseReady, Error: "injected precommit failure",
			})
			close(childReported)
			<-childExit
			wait <- errors.New("injected child exit")
		}()
		return wait, nil
	}

	type spawnResult struct {
		startup *exposeStartup
		err     error
	}
	spawned := make(chan spawnResult, 1)
	go func() {
		startup, err := spawnExposeServerWith(
			t.Context(),
			exposeServerConfig{SessionID: testCLIValidSessionID(), StateDir: stateDir},
			paths, lock, starter,
		)
		spawned <- spawnResult{startup: startup, err: err}
	}()
	<-childReported
	contender, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.False(t, acquired, "contender acquired before failed-child cleanup")
	require.Nil(t, contender)

	close(childExit)
	result := <-spawned
	require.Nil(t, result.startup)
	require.ErrorContains(t, result.err, "injected precommit failure")
	contender, acquired, err = tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, writeExposeRecord(paths.Record, exposeRecord{PID: 456, State: exposeStateStarting}))
	require.NoError(t, os.WriteFile(paths.Socket, []byte("successor"), 0o600))

	recordBytes, err := os.ReadFile(paths.Record)
	require.NoError(t, err)
	var record exposeRecord
	require.NoError(t, json.Unmarshal(recordBytes, &record))
	require.Equal(t, 456, record.PID, "predecessor cleanup deleted successor record")
	contents, err := os.ReadFile(paths.Socket)
	require.NoError(t, err)
	require.Equal(t, "successor", string(contents), "predecessor cleanup deleted successor socket")
	require.NoError(t, cleanupExposeState(paths))
	require.NoError(t, contender.Close())
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

func TestExposeCommitIgnoresCancellationAfterByte(t *testing.T) {
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-postcommit-cancel-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	paths, err := makeExposePaths(stateDir, testCLIValidSessionID())
	require.NoError(t, err)
	lock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)

	commitReceived := make(chan struct{})
	allowAccepted := make(chan struct{})
	session := &fakeExposePortSession{
		fakeExposeQueryClient: &fakeExposeQueryClient{t: t}, done: make(chan struct{}),
	}
	hooks := exposeServerHooks{
		Connect: func(context.Context, client.Params) (exposePortSession, error) {
			return session, nil
		},
		CommitReceived: func() { close(commitReceived) },
		BeforeAccepted: allowAccepted,
	}
	startup, err := spawnExposeServerWith(
		t.Context(),
		exposeServerConfig{SessionID: testCLIValidSessionID(), StateDir: stateDir, Request: exposeRequest{
			Mappings: []exposePortMapping{{Service: "web", ServiceID: "backend-id", Backend: 80, Protocol: sessionwire.NetworkProtocolTCP}},
		}},
		paths, lock, realExposeProcessStarter(t, hooks),
	)
	require.NoError(t, err)

	commitCtx, cancelCommit := context.WithCancelCause(t.Context())
	commitDone := make(chan error, 1)
	go func() { commitDone <- startup.Commit(commitCtx) }()
	<-commitReceived
	cancelCommit(errors.New("cancel after commit byte"))
	select {
	case err := <-commitDone:
		t.Fatalf("commit returned from caller cancellation after the byte write: %v", err)
	default:
	}
	abortDone := make(chan error, 1)
	go func() { abortDone <- startup.Abort() }()
	select {
	case err := <-abortDone:
		t.Fatalf("abort resolved before the unique commit outcome: %v", err)
	default:
	}
	close(allowAccepted)
	require.NoError(t, <-commitDone)
	require.ErrorContains(t, context.Cause(commitCtx), "cancel after commit byte")
	require.True(t, startup.committed)
	require.True(t, startup.finished)
	require.EqualError(t, <-abortDone, "cannot abort committed expose server")

	close(session.done)
	require.NoError(t, <-startup.wait)
}

func TestExposeCommitStatusEOFAfterByteAllowsRollback(t *testing.T) {
	stateDir, err := os.MkdirTemp("/tmp", "dagger-expose-postcommit-eof-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(stateDir)) })
	paths, err := makeExposePaths(stateDir, testCLIValidSessionID())
	require.NoError(t, err)
	lock, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)

	childExited := errors.New("child exited after commit byte")
	starter := func(
		_ string,
		lock *os.File,
		statusW *os.File,
		lifecycleR *os.File,
		_ *os.File,
	) (<-chan error, error) {
		lockChild, err := duplicateExposeFile(lock, "eof-child-lock")
		if err != nil {
			return nil, err
		}
		statusChild, err := duplicateExposeFile(statusW, "eof-child-status")
		if err != nil {
			return nil, err
		}
		lifecycleChild, err := duplicateExposeFile(lifecycleR, "eof-child-lifecycle")
		if err != nil {
			return nil, err
		}
		wait := make(chan error, 1)
		go func() {
			defer lockChild.Close()
			defer statusChild.Close()
			defer lifecycleChild.Close()
			if err := writeExposeChildStatus(statusChild, exposeChildStatus{Phase: exposeChildPhaseReady}); err != nil {
				wait <- fmt.Errorf("write child ready status: %w", err)
				return
			}
			var commit [1]byte
			if _, err := io.ReadFull(lifecycleChild, commit[:]); err != nil {
				wait <- fmt.Errorf("read child commit byte: %w", err)
				return
			}
			if commit[0] != exposeCommitByte {
				wait <- fmt.Errorf("unexpected child commit byte %d", commit[0])
				return
			}
			wait <- childExited
		}()
		return wait, nil
	}

	startup, err := spawnExposeServerWith(
		t.Context(), exposeServerConfig{SessionID: testCLIValidSessionID(), StateDir: stateDir},
		paths, lock, starter,
	)
	require.NoError(t, err)
	commitErr := startup.Commit(t.Context())
	require.ErrorContains(t, commitErr, "read expose server status: EOF")
	require.False(t, startup.committed)
	abortErr := startup.Abort()
	require.ErrorIs(t, abortErr, childExited)
	contender, acquired, err := tryAcquireExposeLock(paths.Lock)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, contender.Close())
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

func realExposeProcessStarter(t *testing.T, hooks exposeServerHooks) exposeProcessStarter {
	t.Helper()
	return func(
		encodedConfig string,
		lock *os.File,
		statusW *os.File,
		lifecycleR *os.File,
		_ *os.File,
	) (<-chan error, error) {
		config, err := decodeExposeServerConfig(encodedConfig)
		if err != nil {
			return nil, err
		}
		paths, err := makeExposePaths(config.StateDir, config.SessionID)
		if err != nil {
			return nil, err
		}
		lockChild, err := duplicateExposeFile(lock, "real-child-lock")
		if err != nil {
			return nil, err
		}
		statusChild, err := duplicateExposeFile(statusW, "real-child-status")
		if err != nil {
			return nil, err
		}
		lifecycleChild, err := duplicateExposeFile(lifecycleR, "real-child-lifecycle")
		if err != nil {
			return nil, err
		}
		wait := make(chan error, 1)
		go func() {
			defer lockChild.Close()
			defer statusChild.Close()
			defer lifecycleChild.Close()
			wait <- serveExposePortServer(
				t.Context(), config, paths, lockChild, statusChild, lifecycleChild, hooks,
			)
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
