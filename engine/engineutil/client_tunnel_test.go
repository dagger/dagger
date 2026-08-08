package engineutil

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

type tunnelRecvResult struct {
	response *h2c.ListenResponse
	err      error
}

type fakeTunnelListenClient struct {
	ctx context.Context

	recvCh      chan tunnelRecvResult
	sendFn      func(*h2c.ListenRequest) error
	closeSendFn func() error
}

func (stream *fakeTunnelListenClient) Send(request *h2c.ListenRequest) error {
	if stream.sendFn == nil {
		return nil
	}
	return stream.sendFn(request)
}

func (stream *fakeTunnelListenClient) Recv() (*h2c.ListenResponse, error) {
	select {
	case result := <-stream.recvCh:
		return result.response, result.err
	case <-stream.ctx.Done():
		return nil, context.Cause(stream.ctx)
	}
}

func (stream *fakeTunnelListenClient) Header() (metadata.MD, error) { return nil, nil }
func (stream *fakeTunnelListenClient) Trailer() metadata.MD         { return nil }
func (stream *fakeTunnelListenClient) Context() context.Context     { return stream.ctx }
func (stream *fakeTunnelListenClient) SendMsg(any) error            { return errors.New("unexpected SendMsg") }
func (stream *fakeTunnelListenClient) RecvMsg(any) error            { return errors.New("unexpected RecvMsg") }

func (stream *fakeTunnelListenClient) CloseSend() error {
	if stream.closeSendFn == nil {
		return nil
	}
	return stream.closeSendFn()
}

func startHostToContainerListenerForTest(
	stream *fakeTunnelListenClient,
	cancelLocal context.CancelCauseFunc,
	dial hostToContainerDialContext,
) *hostToContainerListener {
	listener := &hostToContainerListener{
		done:        make(chan struct{}),
		stream:      stream,
		streamCtx:   stream.Context(),
		cancelLocal: cancelLocal,
		dialContext: dial,
		conns:       map[string]net.Conn{},
	}
	listener.drain.Add(1)
	go listener.run("tcp", "upstream:80")
	return listener
}

func waitTunnelTest[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestHostToContainerListenerRecvFailureIsSticky(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	recvErr := errors.New("receive failed")
	stream := &fakeTunnelListenClient{ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1)}
	stream.recvCh <- tunnelRecvResult{err: recvErr}
	listener := startHostToContainerListenerForTest(stream, cancel, nil)

	require.ErrorIs(t, listener.Wait(), recvErr)
	require.NoError(t, listener.Close())
	require.NoError(t, listener.Close())
	require.ErrorIs(t, listener.Wait(), recvErr)
}

func TestHostToContainerListenerCompletionWinsGuard(t *testing.T) {
	t.Parallel()
	finishLocked := make(chan struct{})
	finishRelease := make(chan struct{})
	listener := &hostToContainerListener{
		done: make(chan struct{}),
		testFinishLocked: func() {
			close(finishLocked)
			<-finishRelease
		},
	}
	listenerErr := errors.New("listener failed")
	finished := make(chan struct{})
	go func() {
		listener.finish(listenerErr)
		close(finished)
	}()
	waitTunnelTest(t, finishLocked, "listener completion lock")

	committed := false
	type guardResult struct {
		completed bool
		err       error
	}
	guarded := make(chan guardResult, 1)
	go func() {
		completed, err := listener.WithCompletionGuard(func() error {
			committed = true
			return nil
		})
		guarded <- guardResult{completed: completed, err: err}
	}()
	close(finishRelease)
	waitTunnelTest(t, finished, "listener completion")
	result := waitTunnelTest(t, guarded, "completion guard")
	require.True(t, result.completed)
	require.ErrorIs(t, result.err, listenerErr)
	require.False(t, committed)
	require.ErrorIs(t, listener.Wait(), listenerErr)
}

func TestHostToContainerListenerGuardWinsCompletion(t *testing.T) {
	t.Parallel()
	guardLocked := make(chan struct{})
	listener := &hostToContainerListener{
		done: make(chan struct{}),
		testCompletionGuardLocked: func() {
			close(guardLocked)
		},
	}
	commitRelease := make(chan struct{})
	type guardResult struct {
		completed bool
		err       error
	}
	guarded := make(chan guardResult, 1)
	go func() {
		completed, err := listener.WithCompletionGuard(func() error {
			<-commitRelease
			return nil
		})
		guarded <- guardResult{completed: completed, err: err}
	}()
	waitTunnelTest(t, guardLocked, "guarded publication")

	listenerErr := errors.New("listener failed after publication")
	finished := make(chan struct{})
	go func() {
		listener.finish(listenerErr)
		close(finished)
	}()
	close(commitRelease)
	result := waitTunnelTest(t, guarded, "completion guard")
	require.False(t, result.completed)
	require.NoError(t, result.err)
	waitTunnelTest(t, finished, "listener completion")
	require.ErrorIs(t, listener.Wait(), listenerErr)
}

func TestHostToContainerListenerSendFailureIsSticky(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	sendErr := errors.New("send failed")
	stream := &fakeTunnelListenClient{
		ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1),
		sendFn: func(*h2c.ListenRequest) error { return sendErr },
	}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection-local dial failure")
	})

	require.ErrorIs(t, listener.Wait(), sendErr)
	require.NoError(t, listener.Close())
	require.ErrorIs(t, listener.Wait(), sendErr)
}

func TestHostToContainerListenerDialFailureIsConnectionLocal(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	closeSent := make(chan struct{})
	stream := &fakeTunnelListenClient{
		ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1),
		sendFn: func(request *h2c.ListenRequest) error {
			if request.Close {
				close(closeSent)
			}
			return nil
		},
	}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	})
	waitTunnelTest(t, closeSent, "connection close message")
	select {
	case <-listener.done:
		t.Fatal("connection-local dial failure completed the listener")
	default:
	}

	require.NoError(t, listener.Close())
	require.NoError(t, listener.Wait())
}

type failingTunnelConn struct {
	readEntered chan struct{}
	readRelease <-chan struct{}
	readErr     error
	writeErr    error
	closed      chan struct{}
	readOnce    sync.Once
	closeOnce   sync.Once
}

func (conn *failingTunnelConn) Read([]byte) (int, error) {
	conn.readOnce.Do(func() { close(conn.readEntered) })
	select {
	case <-conn.readRelease:
		return 0, conn.readErr
	case <-conn.closed:
		return 0, net.ErrClosed
	}
}

func (conn *failingTunnelConn) Write([]byte) (int, error) { return 0, conn.writeErr }
func (conn *failingTunnelConn) Close() error {
	conn.closeOnce.Do(func() { close(conn.closed) })
	return nil
}
func (*failingTunnelConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*failingTunnelConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*failingTunnelConn) SetDeadline(time.Time) error      { return nil }
func (*failingTunnelConn) SetReadDeadline(time.Time) error  { return nil }
func (*failingTunnelConn) SetWriteDeadline(time.Time) error { return nil }

func TestHostToContainerListenerWriteFailureIsConnectionLocal(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	closeSent := make(chan struct{}, 1)
	var closeCount atomic.Int32
	stream := &fakeTunnelListenClient{
		ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 2),
		sendFn: func(request *h2c.ListenRequest) error {
			if request.Close {
				closeCount.Add(1)
				closeSent <- struct{}{}
			}
			return nil
		},
	}
	conn := &failingTunnelConn{
		readEntered: make(chan struct{}),
		writeErr:    errors.New("write failed"),
		closed:      make(chan struct{}),
	}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(context.Context, string, string) (net.Conn, error) {
		return conn, nil
	})
	waitTunnelTest(t, conn.readEntered, "connection reader")
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn", Data: []byte("payload")}}
	waitTunnelTest(t, closeSent, "connection close message")
	select {
	case <-listener.done:
		t.Fatal("connection-local write failure completed the listener")
	default:
	}

	require.NoError(t, listener.Close())
	require.Equal(t, int32(1), closeCount.Load())
	require.NoError(t, listener.Wait())
}

func TestHostToContainerListenerReadFailureIsConnectionLocal(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	closeSent := make(chan struct{}, 1)
	var closeCount atomic.Int32
	stream := &fakeTunnelListenClient{
		ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1),
		sendFn: func(request *h2c.ListenRequest) error {
			if request.Close {
				closeCount.Add(1)
				closeSent <- struct{}{}
			}
			return nil
		},
	}
	readRelease := make(chan struct{})
	conn := &failingTunnelConn{
		readEntered: make(chan struct{}),
		readRelease: readRelease,
		readErr:     errors.New("read failed"),
		closed:      make(chan struct{}),
	}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(context.Context, string, string) (net.Conn, error) {
		return conn, nil
	})
	waitTunnelTest(t, conn.readEntered, "connection reader")
	close(readRelease)
	waitTunnelTest(t, closeSent, "connection close message")
	select {
	case <-listener.done:
		t.Fatal("connection-local read failure completed the listener")
	default:
	}

	require.NoError(t, listener.Close())
	require.Equal(t, int32(1), closeCount.Load())
	require.NoError(t, listener.Wait())
}

func TestHostToContainerListenerCloseCancelsBlockedSendBeforeCloseSend(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	sendEntered := make(chan struct{})
	closeSendCalled := make(chan struct{})
	stream := &fakeTunnelListenClient{
		ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1),
		sendFn: func(*h2c.ListenRequest) error {
			close(sendEntered)
			<-streamCtx.Done()
			return context.Cause(streamCtx)
		},
		closeSendFn: func() error {
			close(closeSendCalled)
			return nil
		},
	}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("dial failed")
	})
	waitTunnelTest(t, sendEntered, "blocked stream send")

	closeResult := make(chan error, 1)
	go func() { closeResult <- listener.Close() }()
	require.NoError(t, waitTunnelTest(t, closeResult, "listener close"))
	waitTunnelTest(t, closeSendCalled, "CloseSend")
	require.NoError(t, listener.Wait())
}

func TestHostToContainerListenerCloseCancelsBlockedDial(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	dialEntered := make(chan struct{})
	stream := &fakeTunnelListenClient{ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1)}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialEntered)
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})
	waitTunnelTest(t, dialEntered, "blocked dial")

	closeResult := make(chan error, 1)
	go func() { closeResult <- listener.Close() }()
	require.NoError(t, waitTunnelTest(t, closeResult, "listener close"))
	require.NoError(t, listener.Wait())
}

func TestHostToContainerListenerExternalCancellationCompletesBlockedDial(t *testing.T) {
	t.Parallel()
	streamCtx, cancel := context.WithCancelCause(t.Context())
	dialEntered := make(chan struct{})
	stream := &fakeTunnelListenClient{ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1)}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	listener := startHostToContainerListenerForTest(stream, cancel, func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialEntered)
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})
	waitTunnelTest(t, dialEntered, "blocked dial")

	streamErr := errors.New("source stream canceled")
	cancel(streamErr)
	require.ErrorIs(t, listener.Wait(), streamErr)
	require.NoError(t, listener.Close())
	require.ErrorIs(t, listener.Wait(), streamErr)
}

type closeTrackingConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (conn *closeTrackingConn) Close() error {
	conn.once.Do(func() { close(conn.closed) })
	return conn.Conn.Close()
}

func TestHostToContainerListenerClosesDialReturningAfterClosingGate(t *testing.T) {
	t.Parallel()
	streamCtx := context.Background()
	_, cancelLocal := context.WithCancelCause(t.Context())
	dialEntered := make(chan struct{})
	dialRelease := make(chan struct{})
	closeSendCalled := make(chan struct{})
	stream := &fakeTunnelListenClient{
		ctx: streamCtx, recvCh: make(chan tunnelRecvResult, 1),
		closeSendFn: func() error {
			close(closeSendCalled)
			return nil
		},
	}
	stream.recvCh <- tunnelRecvResult{response: &h2c.ListenResponse{ConnId: "conn"}}
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })
	tracked := &closeTrackingConn{Conn: serverConn, closed: make(chan struct{})}
	listener := startHostToContainerListenerForTest(stream, cancelLocal, func(context.Context, string, string) (net.Conn, error) {
		close(dialEntered)
		<-dialRelease
		return tracked, nil
	})
	waitTunnelTest(t, dialEntered, "late dial")

	closeResult := make(chan error, 1)
	go func() { closeResult <- listener.Close() }()
	waitTunnelTest(t, closeSendCalled, "CloseSend")
	close(dialRelease)
	waitTunnelTest(t, tracked.closed, "late connection close")
	require.NoError(t, waitTunnelTest(t, closeResult, "listener close"))
	require.NoError(t, listener.Wait())
}

var _ h2c.TunnelListener_ListenClient = (*fakeTunnelListenClient)(nil)
var _ net.Conn = (*closeTrackingConn)(nil)
var _ net.Conn = (*failingTunnelConn)(nil)
