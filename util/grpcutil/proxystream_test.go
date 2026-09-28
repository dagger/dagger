package grpcutil

import (
	"context"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProxyStreamPreservesTerminalStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stopEntered, releaseStop := make(chan struct{}), make(chan struct{})
		// Go's context cancellation closes the child's Done channel before
		// synchronously calling the parent's AfterFunc stop function. Gate that
		// stop to let the canceled sibling finish before the originating pump
		// can return its NotFound error to errgroup. This deliberately relies on
		// that ordering in the Go context implementation, not a timing delay.
		parent := &proxyStopContext{
			Context: context.Background(), done: make(chan struct{}),
			stopEntered: stopEntered, releaseStop: releaseStop,
		}
		releaseReceive, emitError := make(chan struct{}), make(chan struct{})
		serverEntered, clientEntered := make(chan struct{}), make(chan struct{})
		closeSent := make(chan struct{})
		server := &proxyServerStream{recv: func(any) error {
			close(serverEntered)
			<-releaseReceive
			return io.EOF
		}}
		client := &proxyClientStream{
			recv: func(any) error {
				close(clientEntered)
				<-emitError
				return status.Error(codes.NotFound, "dagger-module.toml does not exist")
			},
			closeSend: func() error { close(closeSent); return nil },
		}
		result := make(chan error, 1)
		finished := make(chan struct{})
		unblockReceive := sync.OnceFunc(func() { close(releaseReceive) })
		unblockStop := sync.OnceFunc(func() { close(releaseStop) })
		unblockError := sync.OnceFunc(func() { close(emitError) })
		defer func() {
			unblockError()
			unblockStop()
			unblockReceive()
			waitProxyEvent(t, finished)
		}()
		go func() {
			defer close(finished)
			result <- ProxyStream[any](parent, client, server)
		}()
		waitProxyEvent(t, serverEntered)
		waitProxyEvent(t, clientEntered)
		synctest.Wait()
		unblockError()
		waitProxyEvent(t, stopEntered)
		synctest.Wait()
		waitProxyEvent(t, closeSent)
		unblockStop()
		waitProxyEvent(t, finished)
		err := <-result
		t.Logf("proxy returned %T: %v", err, err)
		require.Equal(t, codes.NotFound, status.Code(err), "sibling cancellation must not replace the terminal status")
	})
}

type proxyStopContext struct {
	context.Context
	done                     chan struct{}
	stopEntered, releaseStop chan struct{}
}

func (c *proxyStopContext) Done() <-chan struct{} { return c.done }

func (c *proxyStopContext) AfterFunc(func()) func() bool {
	return func() bool {
		close(c.stopEntered)
		<-c.releaseStop
		return true
	}
}

type proxyClientStream struct {
	grpc.ClientStream
	recv      func(any) error
	send      func(any) error
	closeSend func() error
}

func (s *proxyClientStream) RecvMsg(msg any) error { return s.recv(msg) }
func (s *proxyClientStream) SendMsg(msg any) error { return s.send(msg) }
func (s *proxyClientStream) CloseSend() error      { return s.closeSend() }

type proxyServerStream struct {
	grpc.ServerStream
	recv func(any) error
	send func(any) error
}

func (s *proxyServerStream) RecvMsg(msg any) error { return s.recv(msg) }
func (s *proxyServerStream) SendMsg(msg any) error { return s.send(msg) }

func waitProxyEvent(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for proxy stream event")
	}
}

func TestProxyStreamHalfClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closedSend, releaseResponse := make(chan struct{}), make(chan struct{})
		request, response := make(chan string, 1), make(chan string, 1)
		serverReads, clientReads := 0, 0
		server := &proxyServerStream{
			recv: func(msg any) error {
				serverReads++
				if serverReads > 1 {
					return io.EOF
				}
				*msg.(*string) = "request"
				return nil
			},
			send: func(msg any) error { response <- *msg.(*string); return nil },
		}
		client := &proxyClientStream{
			recv: func(msg any) error {
				<-releaseResponse
				clientReads++
				if clientReads > 1 {
					return io.EOF
				}
				*msg.(*string) = "response"
				return nil
			},
			send:      func(msg any) error { request <- *msg.(*string); return nil },
			closeSend: func() error { close(closedSend); return nil },
		}
		finished, result := make(chan struct{}), make(chan error, 1)
		unblock := sync.OnceFunc(func() { close(releaseResponse) })
		defer func() { unblock(); waitProxyEvent(t, finished) }()
		go func() {
			defer close(finished)
			result <- ProxyStream[string](context.Background(), client, server)
		}()
		waitProxyEvent(t, closedSend)
		synctest.Wait()
		select {
		case <-finished:
			t.Fatal("request EOF must only half-close; the response is still pending")
		default:
		}
		unblock()
		waitProxyEvent(t, finished)
		require.NoError(t, <-result)
		require.Equal(t, "request", <-request)
		require.Equal(t, "response", <-response)
	})
}

func TestProxyStreamCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelParent bool
		terminal     error
		want         codes.Code
	}{
		{name: "parent", cancelParent: true, want: codes.Canceled},
		{name: "peer canceled", terminal: status.Error(codes.Canceled, "peer stopped"), want: codes.Canceled},
		{name: "peer deadline", terminal: status.Error(codes.DeadlineExceeded, "peer deadline"), want: codes.DeadlineExceeded},
		{name: "peer EOF", terminal: io.EOF, want: codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				serverEntered, clientEntered := make(chan struct{}), make(chan struct{})
				releaseServer, releaseClient := make(chan struct{}), make(chan struct{})
				client := &proxyClientStream{
					recv:      func(any) error { close(clientEntered); <-releaseClient; return tc.terminal },
					closeSend: func() error { return nil },
				}
				server := &proxyServerStream{recv: func(any) error {
					close(serverEntered)
					<-releaseServer
					return io.EOF
				}}
				finished, result := make(chan struct{}), make(chan error, 1)
				unblockClient := sync.OnceFunc(func() { close(releaseClient) })
				unblockServer := sync.OnceFunc(func() { close(releaseServer) })
				defer func() {
					cancel()
					unblockClient()
					unblockServer()
					waitProxyEvent(t, finished)
				}()
				go func() {
					defer close(finished)
					result <- ProxyStream[any](ctx, client, server)
				}()
				waitProxyEvent(t, serverEntered)
				waitProxyEvent(t, clientEntered)
				synctest.Wait()
				if tc.cancelParent {
					cancel()
				} else {
					unblockClient()
				}
				waitProxyEvent(t, finished)
				err := <-result
				if tc.cancelParent {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.Equal(t, tc.want, status.Code(err))
				}
			})
		})
	}
}

func TestProxyStreamIndependentErrorAfterStop(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal error
		sendErr  error
		want     codes.Code
	}{
		{name: "first error wins", terminal: status.Error(codes.NotFound, "missing"), sendErr: status.Error(codes.PermissionDenied, "write failed"), want: codes.NotFound},
		{name: "EOF does not hide send error", terminal: io.EOF, sendErr: status.Error(codes.PermissionDenied, "write failed"), want: codes.PermissionDenied},
		{name: "EOF does not hide peer canceled", terminal: io.EOF, sendErr: status.Error(codes.Canceled, "write canceled"), want: codes.Canceled},
		{name: "EOF does not hide independent cancellation", terminal: io.EOF, sendErr: context.Canceled, want: codes.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				stopEntered, releaseStop := make(chan struct{}), make(chan struct{})
				parent := &proxyStopContext{
					Context: context.Background(), done: make(chan struct{}),
					stopEntered: stopEntered, releaseStop: releaseStop,
				}
				emitTerminal, releaseSend := make(chan struct{}), make(chan struct{})
				sendEntered, recvEntered := make(chan struct{}), make(chan struct{})
				client := &proxyClientStream{
					recv: func(any) error { close(recvEntered); <-emitTerminal; return tc.terminal },
					send: func(any) error {
						close(sendEntered)
						<-releaseSend
						return tc.sendErr
					},
					closeSend: func() error { return nil },
				}
				server := &proxyServerStream{recv: func(any) error { return nil }}
				finished, result := make(chan struct{}), make(chan error, 1)
				unblockStop := sync.OnceFunc(func() { close(releaseStop) })
				unblockSend := sync.OnceFunc(func() { close(releaseSend) })
				unblockTerminal := sync.OnceFunc(func() { close(emitTerminal) })
				defer func() {
					unblockStop()
					unblockSend()
					unblockTerminal()
					waitProxyEvent(t, finished)
				}()
				go func() {
					defer close(finished)
					result <- ProxyStream[any](parent, client, server)
				}()
				waitProxyEvent(t, sendEntered)
				waitProxyEvent(t, recvEntered)
				unblockTerminal()
				waitProxyEvent(t, stopEntered)
				unblockSend()
				synctest.Wait()
				unblockStop()
				waitProxyEvent(t, finished)
				err := <-result
				require.Equal(t, tc.want, status.Code(err))
				if tc.sendErr == context.Canceled {
					require.ErrorIs(t, err, context.Canceled)
				}
			})
		})
	}
}
