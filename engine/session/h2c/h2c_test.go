package h2c

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestTunnelListenerCompletionNotification(t *testing.T) {
	t.Parallel()
	t.Run("successful listener ends", func(t *testing.T) {
		t.Parallel()
		completions := make(chan TunnelListenerCompletion, 2)
		finish := make(chan struct{})
		stream := newTunnelListenerTestStream(t.Context(), finish, &ListenRequest{
			Addr: "127.0.0.1:0", Protocol: "tcp",
		})
		attachable := NewTunnelListenerAttachable(t.Context(), func(completion TunnelListenerCompletion) {
			completions <- completion
		})
		result := make(chan error, 1)
		go func() { result <- attachable.Listen(stream) }()
		<-stream.bound
		close(finish)
		require.NoError(t, <-result)
		completion := <-completions
		require.NotEmpty(t, completion.Addr)
		require.NoError(t, completion.Err)
		select {
		case <-completions:
			t.Fatal("listener completion was reported more than once")
		default:
		}
	})

	t.Run("bind failure", func(t *testing.T) {
		t.Parallel()
		completions := make(chan TunnelListenerCompletion, 1)
		stream := newTunnelListenerTestStream(t.Context(), nil, &ListenRequest{
			Addr: "127.0.0.1:0", Protocol: "not-a-network",
		})
		attachable := NewTunnelListenerAttachable(t.Context(), func(completion TunnelListenerCompletion) {
			completions <- completion
		})
		require.Error(t, attachable.Listen(stream))
		select {
		case <-completions:
			t.Fatal("failed listener bind reported a completion")
		default:
		}
	})

	t.Run("own shutdown", func(t *testing.T) {
		t.Parallel()
		rootCtx, cancel := context.WithCancel(t.Context())
		completions := make(chan TunnelListenerCompletion, 1)
		finish := make(chan struct{})
		stream := newTunnelListenerTestStream(rootCtx, finish, &ListenRequest{
			Addr: "127.0.0.1:0", Protocol: "tcp",
		})
		attachable := NewTunnelListenerAttachable(rootCtx, func(completion TunnelListenerCompletion) {
			completions <- completion
		})
		result := make(chan error, 1)
		go func() { result <- attachable.Listen(stream) }()
		<-stream.bound
		cancel()
		close(finish)
		require.NoError(t, <-result)
		select {
		case <-completions:
			t.Fatal("listener completion was reported during attachable shutdown")
		default:
		}
	})
}

type tunnelListenerTestStream struct {
	ctx     context.Context
	request *ListenRequest
	finish  <-chan struct{}
	bound   chan struct{}
	once    sync.Once
}

func newTunnelListenerTestStream(
	ctx context.Context,
	finish <-chan struct{},
	request *ListenRequest,
) *tunnelListenerTestStream {
	return &tunnelListenerTestStream{
		ctx: ctx, request: request, finish: finish, bound: make(chan struct{}),
	}
}

func (stream *tunnelListenerTestStream) Send(response *ListenResponse) error {
	if response.Addr != "" {
		stream.once.Do(func() { close(stream.bound) })
	}
	return nil
}

func (stream *tunnelListenerTestStream) Recv() (*ListenRequest, error) {
	if stream.request != nil {
		request := stream.request
		stream.request = nil
		return request, nil
	}
	if stream.finish != nil {
		<-stream.finish
	}
	return nil, io.EOF
}

func (stream *tunnelListenerTestStream) SetHeader(metadata.MD) error  { return nil }
func (stream *tunnelListenerTestStream) SendHeader(metadata.MD) error { return nil }
func (stream *tunnelListenerTestStream) SetTrailer(metadata.MD)       {}
func (stream *tunnelListenerTestStream) Context() context.Context     { return stream.ctx }
func (stream *tunnelListenerTestStream) SendMsg(any) error            { return nil }
func (stream *tunnelListenerTestStream) RecvMsg(any) error            { return nil }
