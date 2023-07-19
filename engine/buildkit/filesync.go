package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/util/bklog"
	filesynctypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// TODO: could make generic func to reduce tons of below boilerplate

// for local dir imports
type fileSyncServerProxy struct {
	c *Client
}

func (p *fileSyncServerProxy) Register(srv *grpc.Server) {
	filesync.RegisterFileSyncServer(srv, p)
}

func (p *fileSyncServerProxy) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	ctx, baseData, err := p.c.GetSessionResourceData(stream)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts := baseData.importLocalDirData
	if opts == nil {
		return fmt.Errorf("expected import local dir opts")
	}
	diffCopyClient, err := filesync.NewFileSyncClient(opts.session.Conn()).DiffCopy(ctx)
	if err != nil {
		return err
	}

	var eg errgroup.Group
	var done bool
	eg.Go(func() (rerr error) {
		defer func() {
			bklog.G(ctx).WithError(rerr).Debug("diffcopy receiver done")
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
			pkt, err := withContext(ctx, func() (*filesynctypes.Packet, error) {
				var pkt filesynctypes.Packet
				err := stream.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := diffCopyClient.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			bklog.G(ctx).WithError(rerr).Debug("diffcopy sender done")
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr == nil {
				done = true
			}
			cancel()
		}()
		for {
			pkt, err := withContext(ctx, func() (*filesynctypes.Packet, error) {
				var pkt filesynctypes.Packet
				err := diffCopyClient.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := stream.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	return eg.Wait()
}

func (p *fileSyncServerProxy) TarStream(stream filesync.FileSync_TarStreamServer) error {
	ctx, baseData, err := p.c.GetSessionResourceData(stream)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts := baseData.importLocalDirData
	if opts == nil {
		return fmt.Errorf("expected import local dir opts")
	}
	tarStreamClient, err := filesync.NewFileSyncClient(opts.session.Conn()).TarStream(ctx)
	if err != nil {
		return err
	}

	var eg errgroup.Group
	eg.Go(func() (rerr error) {
		defer func() {
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			pkt, err := withContext(ctx, func() (*filesynctypes.Packet, error) {
				var pkt filesynctypes.Packet
				err := stream.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := tarStreamClient.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			pkt, err := withContext(ctx, func() (*filesynctypes.Packet, error) {
				var pkt filesynctypes.Packet
				err := tarStreamClient.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := stream.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	return eg.Wait()
}

// for local dir exports
type fileSendServerProxy struct {
	c *Client
}

func (p *fileSendServerProxy) Register(srv *grpc.Server) {
	filesync.RegisterFileSendServer(srv, p)
}

func (p *fileSendServerProxy) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	ctx, baseData, err := p.c.GetSessionResourceData(stream)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts := baseData.exportLocalDirData
	if opts == nil {
		return fmt.Errorf("expected export local dir opts")
	}
	diffCopyClient, err := filesync.NewFileSendClient(opts.session.Conn()).DiffCopy(ctx)
	if err != nil {
		return err
	}

	var eg errgroup.Group
	eg.Go(func() (rerr error) {
		defer func() {
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			pkt, err := withContext(ctx, func() (*filesynctypes.Packet, error) {
				var pkt filesynctypes.Packet
				err := stream.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := diffCopyClient.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			pkt, err := withContext(ctx, func() (*filesynctypes.Packet, error) {
				var pkt filesynctypes.Packet
				err := diffCopyClient.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := stream.SendMsg(pkt); err != nil {
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
	ch := make(chan result)
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
