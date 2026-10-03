package drivers

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tunnels are dialed ahead of time once the engine has answered on the
// first one, are then handed out in order without a new dial, and clients
// past them dial on demand.
func TestContainerConnectorWarmsTunnelsOnceEngineAnswers(t *testing.T) {
	t.Parallel()

	backend := &dialingBackend{fail: true}
	connector := containerConnector{backend: backend, host: "engine", warm: newWarmTunnels()}

	// A dial the runtime refuses starts nothing: the next client dials
	// fresh, exactly as it would with no warm tunnels at all.
	_, err := connector.Connect(t.Context())
	require.Error(t, err)
	require.Equal(t, 1, backend.dials())

	backend.setFail(false)
	conn, err := connector.Connect(t.Context())
	require.NoError(t, err)
	// The dial succeeded but the engine has not spoken yet: still nothing.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 2, backend.dials(), "no warm dial before the engine answers")

	_, err = conn.Read(make([]byte, 1))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return backend.dials() == 2+warmTunnelCount
	}, 5*time.Second, time.Millisecond, "the warm tunnels should be in flight once the engine answered")

	for range warmTunnelCount {
		conn, err := connector.Connect(t.Context())
		require.NoError(t, err)
		require.NotNil(t, conn)
	}
	require.Equal(t, 2+warmTunnelCount, backend.dials(), "the warm tunnels should have served every client")

	_, err = connector.Connect(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3+warmTunnelCount, backend.dials(), "past the warm tunnels, a client dials on demand")
}

// A tunnel into an engine that is not answering (still booting, paused)
// opens and then reads EOF. That must not start any warm dials: the
// client's retry dials fresh, as it did before warm tunnels existed.
func TestContainerConnectorDoesNotWarmOnDeadTunnel(t *testing.T) {
	t.Parallel()

	backend := &dialingBackend{dead: true}
	connector := containerConnector{backend: backend, host: "engine", warm: newWarmTunnels()}

	conn, err := connector.Connect(t.Context())
	require.NoError(t, err)
	_, err = conn.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)

	_, err = connector.Connect(t.Context())
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 2, backend.dials(), "each retry dials fresh; nothing warm was started")
}

// A client that stops waiting discards that tunnel; the next client can use
// another warm tunnel without dialing on demand.
func TestContainerConnectorDiscardsTunnelAClientStoppedWaitingFor(t *testing.T) {
	t.Parallel()

	backend := &dialingBackend{holdWarmDials: make(chan struct{})}
	connector := containerConnector{backend: backend, host: "engine", warm: newWarmTunnels()}

	conn, err := connector.Connect(t.Context())
	require.NoError(t, err)
	_, err = conn.Read(make([]byte, 1))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return backend.dials() == 1+warmTunnelCount
	}, 5*time.Second, time.Millisecond, "the warm dials should be in flight, held by the backend")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = connector.Connect(ctx)
	require.ErrorIs(t, err, context.Canceled)

	close(backend.holdWarmDials)
	require.Eventually(t, func() bool {
		return backend.closedDials() == 1
	}, 5*time.Second, time.Millisecond, "the abandoned tunnel should be closed")
	conn, err = connector.Connect(t.Context())
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, 1+warmTunnelCount, backend.dials(), "another warm tunnel should serve the next client")
}

// Closing the connector releases warm tunnels no client claimed, including
// dials that are still in flight when close begins.
func TestContainerConnectorClosesUnusedWarmTunnels(t *testing.T) {
	t.Parallel()

	backend := &dialingBackend{holdWarmDials: make(chan struct{})}
	connector := containerConnector{backend: backend, host: "engine", warm: newWarmTunnels()}

	conn, err := connector.Connect(t.Context())
	require.NoError(t, err)
	_, err = conn.Read(make([]byte, 1))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return backend.dials() == 1+warmTunnelCount
	}, 5*time.Second, time.Millisecond, "the warm tunnels should be in flight")

	require.NoError(t, connector.Close())
	close(backend.holdWarmDials)
	require.Eventually(t, func() bool {
		return backend.closedDials() == warmTunnelCount
	}, 5*time.Second, time.Millisecond, "every unused warm tunnel should close")
	require.NoError(t, conn.Close())
}

// dialingBackend counts dials. It can refuse them (fail), hand out tunnels
// the engine never answers on (dead), or hold every dial after the first
// until holdWarmDials is closed.
type dialingBackend struct {
	captureContainerBackend
	holdWarmDials chan struct{}
	dead          bool

	mu    sync.Mutex
	n     int
	fail  bool
	conns []*tunnelConn
}

func (b *dialingBackend) dials() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}

func (b *dialingBackend) setFail(fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = fail
}

func (b *dialingBackend) closedDials() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var closed int
	for _, conn := range b.conns {
		if conn.closed.Load() {
			closed++
		}
	}
	return closed
}

func (b *dialingBackend) ContainerDial(context.Context, string, []string) (net.Conn, error) {
	b.mu.Lock()
	b.n++
	n, fail := b.n, b.fail
	b.mu.Unlock()
	if n > 1 && b.holdWarmDials != nil {
		<-b.holdWarmDials
	}
	if fail {
		return nil, errors.New("no such container")
	}
	conn := &tunnelConn{dead: b.dead}
	b.mu.Lock()
	b.conns = append(b.conns, conn)
	b.mu.Unlock()
	return conn, nil
}

// tunnelConn is a tunnel the engine answers on, byte by byte, or one it
// never answers on.
type tunnelConn struct {
	net.Conn
	dead   bool
	closed atomic.Bool
}

func (c *tunnelConn) Read(p []byte) (int, error) {
	if c.dead || len(p) == 0 {
		return 0, io.EOF
	}
	p[0] = 'x'
	return 1, nil
}

func (c *tunnelConn) Close() error {
	c.closed.Store(true)
	return nil
}
