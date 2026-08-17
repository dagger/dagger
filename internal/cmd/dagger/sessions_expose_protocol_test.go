package daggercmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sessionwire"
	"github.com/stretchr/testify/require"
)

func TestExposeServerConfigAndSocketPath(t *testing.T) {
	t.Parallel()
	frontend := 8080
	config := exposeServerConfig{
		SessionID: testCLIValidSessionID(), StateDir: t.TempDir(),
		Request: exposeRequest{Mappings: []exposePortMapping{
			{Service: "web", ServiceID: "service-id", Frontend: &frontend, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP},
		}},
	}
	encoded, err := config.encode()
	require.NoError(t, err)
	decoded, err := decodeExposeServerConfig(encoded)
	require.NoError(t, err)
	require.True(t, config.Request.equal(decoded.Request))
	require.Equal(t, config.SessionID, decoded.SessionID)

	paths, err := makeExposePaths(config.StateDir, config.SessionID)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(config.StateDir, config.SessionID+".pid"), paths.Record)
	require.LessOrEqual(t, len(paths.Socket), maxExposeSocketPathLen)
	pathsAgain, err := makeExposePaths(config.StateDir, config.SessionID)
	require.NoError(t, err)
	require.Equal(t, paths.Socket, pathsAgain.Socket)

	_, err = makeExposePaths(strings.Repeat("x", maxExposeSocketPathLen), config.SessionID)
	require.ErrorContains(t, err, "control socket path is too long")
}

func TestExposeRequestEqualityPreservesRandomFrontends(t *testing.T) {
	t.Parallel()
	fixed := 8080
	left := exposeRequest{Mappings: []exposePortMapping{
		{Service: "db", ServiceID: "db-id", Frontend: nil, Backend: 5432, Protocol: sessionwire.NetworkProtocolTCP},
		{Service: "web", ServiceID: "web-id", Frontend: &fixed, Backend: 80, Protocol: sessionwire.NetworkProtocolTCP},
	}}
	right := exposeRequest{Mappings: []exposePortMapping{left.Mappings[1], left.Mappings[0]}}
	require.True(t, left.equal(right))
	randomBecameFixed := right
	randomBecameFixed.Mappings = append([]exposePortMapping(nil), right.Mappings...)
	randomBecameFixed.Mappings[1].Frontend = &fixed
	require.False(t, left.equal(randomBecameFixed))
}

func TestExposeRecordAtomicReplacementAndCleanup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	paths, err := makeExposePaths(dir, testCLIValidSessionID())
	require.NoError(t, err)
	record := exposeRecord{PID: 42, State: exposeStateReady}
	require.NoError(t, writeExposeRecord(paths.Record, record))
	contents, err := os.ReadFile(paths.Record)
	require.NoError(t, err)
	require.Contains(t, string(contents), `"pid":42`)
	require.NoError(t, os.WriteFile(paths.Socket, []byte("stale"), 0o600))
	require.NoError(t, cleanupExposeState(paths))
	_, err = os.Stat(paths.Record)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(paths.Socket)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExposeListenerMonitorCommitAndShutdown(t *testing.T) {
	t.Parallel()
	monitor := newExposeListenerMonitor()
	monitor.listenerEnded("127.0.0.1:8080", nil)
	require.ErrorContains(t, monitor.commit(), "listener stream ended")

	monitor = newExposeListenerMonitor()
	require.NoError(t, monitor.commit())
	monitor.listenerEnded("127.0.0.1:8080", errors.New("backend exited"))
	require.ErrorContains(t, monitor.loss(), "backend exited")

	monitor = newExposeListenerMonitor()
	monitor.shutdown()
	monitor.listenerEnded("127.0.0.1:8080", errors.New("expected close"))
	require.NoError(t, monitor.loss())

	monitor = newExposeListenerMonitor()
	commitLocked := make(chan struct{})
	releaseCommit := make(chan struct{})
	monitor.testCommitLocked = func() {
		close(commitLocked)
		<-releaseCommit
	}
	commitDone := make(chan error, 1)
	go func() { commitDone <- monitor.commit() }()
	<-commitLocked
	lossDone := make(chan struct{})
	go func() {
		monitor.listenerEnded("127.0.0.1:8080", errors.New("concurrent loss"))
		close(lossDone)
	}()
	close(releaseCommit)
	require.NoError(t, <-commitDone)
	<-lossDone
	require.ErrorContains(t, monitor.loss(), "concurrent loss")
}

func TestExposeChildStatusProtocol(t *testing.T) {
	t.Parallel()
	var ready bytes.Buffer
	require.NoError(t, writeExposeChildStatus(&ready, exposeChildStatus{
		Phase: exposeChildPhaseReady, Ports: []exposedPort{{Service: "web", Frontend: 8080}},
	}))
	status, err := readExposeChildStatus(t.Context(), &ready, exposeChildPhaseReady)
	require.NoError(t, err)
	require.Equal(t, 8080, status.Ports[0].Frontend)

	var failed bytes.Buffer
	require.NoError(t, writeExposeChildStatus(&failed, exposeChildStatus{
		Phase: exposeChildPhaseReady, ErrorCode: engine.SessionErrorAlreadyAttached, Error: "bind failed",
	}))
	_, err = readExposeChildStatus(t.Context(), &failed, exposeChildPhaseReady)
	require.EqualError(t, err, "bind failed")
	var childErr *exposeChildError
	require.ErrorAs(t, err, &childErr)
	require.Equal(t, engine.SessionErrorAlreadyAttached, childErr.Code)

	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("status canceled"))
	reader, writer := io.Pipe()
	_, err = readExposeChildStatus(ctx, reader, exposeChildPhaseReady)
	require.EqualError(t, err, "status canceled")
	require.NoError(t, reader.Close())
	require.NoError(t, writer.Close())
}

func TestExposedPortURLSchemes(t *testing.T) {
	t.Parallel()
	require.Equal(t, "http://localhost:8080", (exposedPort{Backend: 8080, Frontend: 8080}).URL())
	require.Equal(t, "https://localhost:8443", (exposedPort{Backend: 443, Frontend: 8443}).URL())
	require.Equal(t, "tcp://localhost:15432", (exposedPort{Backend: 5432, Frontend: 15432}).URL())
}
