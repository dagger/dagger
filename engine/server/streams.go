package server

import (
	"context"
	"errors"
	"io"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

func proxyStream[T any](ctx context.Context, clientStream grpc.ClientStream, serverStream grpc.ServerStream) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var eg errgroup.Group
	var done bool
	eg.Go(func() (rerr error) {
		defer func() {
			clientStream.CloseSend()
			if rerr == io.EOF {
				rerr = nil
			}
			if errors.Is(rerr, context.Canceled) && done {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			msg, err := withContext(ctx, func() (*T, error) {
				var msg T
				err := serverStream.RecvMsg(&msg)
				return &msg, err
			})
			if err != nil {
				return err
			}
			if err := clientStream.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr == nil {
				done = true
			}
			cancel()
		}()
		for {
			msg, err := withContext(ctx, func() (*T, error) {
				var msg T
				err := clientStream.RecvMsg(&msg)
				return &msg, err
			})
			if err != nil {
				return err
			}
			if err := serverStream.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	return eg.Wait()
}

// withContext adapts a blocking function to a context-aware function. It's
// up to the caller to ensure that the blocking function f will unblock at
// some time, otherwise there can be a goroutine leak.
func withContext[T any](ctx context.Context, f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.v, r.err
	}
}
