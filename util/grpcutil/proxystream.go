package grpcutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// ProxyStream proxies messages between a gRPC client stream and server stream.
func ProxyStream[T any](ctx context.Context, clientStream grpc.ClientStream, serverStream grpc.ServerStream) error {
	parentCtx := ctx
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errors.New("proxy stream done"))
	var eg errgroup.Group
	var mu sync.Mutex
	var firstErr error
	var stopped bool
	finish := func(err error, interrupted, stopOnEOF bool, direction string) {
		if err == io.EOF {
			err = nil
		}
		mu.Lock()
		// A sibling woken by our cancellation must not replace the status that
		// caused it. Parent cancellation and independently canceled streams are
		// still errors, including when the other direction completed normally.
		canceledByProxy := interrupted && stopped && parentCtx.Err() == nil
		if err != nil && !canceledByProxy && firstErr == nil {
			firstErr = err
		}
		stop := err != nil || stopOnEOF
		if stop {
			stopped = true
		}
		mu.Unlock()
		// Record the error before waking the other pump, and never call cancel
		// under the mutex: cancellation can run parent-context cleanup callbacks.
		if stop {
			if err != nil {
				cancel(fmt.Errorf("failed to proxy stream %s: %w", direction, err))
			} else {
				cancel(fmt.Errorf("proxy stream %s done", direction))
			}
		}
	}
	eg.Go(func() (rerr error) {
		var interrupted bool
		defer func() {
			clientStream.CloseSend()
			finish(rerr, interrupted, false, "server->client")
		}()
		for {
			msg, err, canceled := withContext(ctx, func() (*T, error) {
				var msg T
				err := serverStream.RecvMsg(&msg)
				return &msg, err
			})
			if err != nil {
				interrupted = canceled
				return err
			}
			if err := clientStream.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		var interrupted bool
		defer func() {
			finish(rerr, interrupted, true, "client->server")
		}()
		for {
			msg, err, canceled := withContext(ctx, func() (*T, error) {
				var msg T
				err := clientStream.RecvMsg(&msg)
				return &msg, err
			})
			if err != nil {
				interrupted = canceled
				return err
			}
			if err := serverStream.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	// Pump return order may differ from termination order because cancel wakes
	// the sibling before the initiating pump returns to errgroup.
	_ = eg.Wait()
	return firstErr
}

// withContext adapts a blocking function to a context-aware function. It's
// up to the caller to ensure that the blocking function f will unblock at
// some time, otherwise there can be a goroutine leak.
// The final result distinguishes interruption from an error returned by f.
func withContext[T any](ctx context.Context, f func() (T, error)) (T, error, bool) {
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
		return zero, ctx.Err(), true
	case r := <-ch:
		return r.v, r.err, false
	}
}

// IncomingToOutgoingContext returns a new outgoing context with the metadata copied from the given contexts
// incoming data
func IncomingToOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.Pairs()
	}
	return metadata.NewOutgoingContext(ctx, md)
}
