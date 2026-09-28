package grpcutil

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

var testStreamDesc = grpc.StreamDesc{
	StreamName:    "Stream",
	ServerStreams: true,
	ClientStreams: true,
}

const testStreamMethod = "/grpcutil.test.Test/Stream"

func serveTestStream(t *testing.T, handler grpc.StreamHandler) *grpc.ClientConn {
	t.Helper()
	srv := grpc.NewServer()
	desc := testStreamDesc
	desc.Handler = handler
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "grpcutil.test.Test",
		HandlerType: (*any)(nil),
		Streams:     []grpc.StreamDesc{desc},
	}, struct{}{})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(l)
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// slowStopCtx delays the first cancellation of a child context by a short
// time. context.WithCancelCause calls the stop function of AfterFunc when its
// cancel function runs, after it closes the child's Done channel. This lets
// the other proxy direction see the cancellation and return first.
type slowStopCtx struct {
	context.Context
	once sync.Once
}

func (c *slowStopCtx) AfterFunc(f func()) func() bool {
	stop := context.AfterFunc(c.Context, f)
	return func() bool {
		c.once.Do(func() { time.Sleep(10 * time.Millisecond) })
		return stop()
	}
}

// TestProxyStreamUpstreamError checks that ProxyStream returns the error of
// the upstream, and not the context.Canceled error that it causes when it
// stops the other direction.
func TestProxyStreamUpstreamError(t *testing.T) {
	upstream := serveTestStream(t, func(_ any, stream grpc.ServerStream) error {
		var msg emptypb.Empty
		if err := stream.RecvMsg(&msg); err != nil {
			return err
		}
		return status.Error(codes.NotFound, "file not found")
	})
	proxy := serveTestStream(t, func(_ any, stream grpc.ServerStream) error {
		ctx := &slowStopCtx{Context: stream.Context()}
		clientStream, err := upstream.NewStream(ctx, &testStreamDesc, testStreamMethod)
		if err != nil {
			return err
		}
		return ProxyStream[emptypb.Empty](ctx, clientStream, stream)
	})

	const calls = 200
	errs := make([]error, calls)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			stream, err := proxy.NewStream(ctx, &testStreamDesc, testStreamMethod)
			if err != nil {
				errs[i] = err
				return
			}
			if err := stream.SendMsg(&emptypb.Empty{}); err != nil {
				errs[i] = err
				return
			}
			var msg emptypb.Empty
			errs[i] = stream.RecvMsg(&msg)
		})
	}
	wg.Wait()

	for i, err := range errs {
		require.Equal(t, codes.NotFound, status.Code(err), "call %d: %v", i, err)
	}
}

// TestProxyStreamParentCanceled checks that ProxyStream still returns
// context.Canceled when its parent context is canceled.
func TestProxyStreamParentCanceled(t *testing.T) {
	started := make(chan struct{})
	upstream := serveTestStream(t, func(_ any, stream grpc.ServerStream) error {
		close(started)
		<-stream.Context().Done()
		return stream.Context().Err()
	})
	proxyErr := make(chan error, 1)
	proxy := serveTestStream(t, func(_ any, stream grpc.ServerStream) error {
		ctx, cancel := context.WithCancel(stream.Context())
		defer cancel()
		clientStream, err := upstream.NewStream(ctx, &testStreamDesc, testStreamMethod)
		if err != nil {
			return err
		}
		go func() {
			<-started
			cancel()
		}()
		err = ProxyStream[emptypb.Empty](ctx, clientStream, stream)
		proxyErr <- err
		return err
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := proxy.NewStream(ctx, &testStreamDesc, testStreamMethod)
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&emptypb.Empty{}))

	select {
	case err := <-proxyErr:
		require.True(t, errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled, "got %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for the proxy to return")
	}
}
