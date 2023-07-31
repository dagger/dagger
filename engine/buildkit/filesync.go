package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/dagger/dagger/engine"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
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
	ctx, baseData, err := p.c.getSessionResourceData(stream)
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
			diffCopyClient.CloseSend() // TODO: make sure all the other streams do this too, including non-filesync ones
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
	ctx, baseData, err := p.c.getSessionResourceData(stream)
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
			tarStreamClient.CloseSend()
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

// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
func (c *Client) newFileSendServerProxySession(ctx context.Context, destPath string) (*bksession.Session, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get requester client metadata: %s", err)
	}
	sess, err := bksession.NewSession(ctx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}
	proxy := &fileSendServerProxy{c: c, destClientID: clientMetadata.ClientID, destPath: destPath}
	sess.Allow(proxy)

	clientConn, serverConn := net.Pipe()
	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) { // nolint: unparam
		go func() {
			defer serverConn.Close()
			err := c.SessionManager.HandleConn(ctx, serverConn, meta)
			if err != nil {
				lg := bklog.G(ctx).WithError(err)
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					lg.Debug("session conn ended")
				} else {
					// TODO: cancel the whole buildkit client
					lg.Error("failed to handle session conn")
				}
			}
		}()
		return clientConn, nil
	}
	go func() {
		defer clientConn.Close()
		defer sess.Close()
		err := sess.Run(ctx, dialer)
		if err != nil {
			lg := bklog.G(ctx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session in dagger frontend ended")
			} else {
				lg.Fatal("failed to run dagger frontend session")
			}
		}
	}()

	return sess, nil
}

// for local dir exports
type fileSendServerProxy struct {
	c *Client
	// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
	destClientID string
	destPath     string
}

func (p *fileSendServerProxy) Register(srv *grpc.Server) {
	filesync.RegisterFileSendServer(srv, p)
}

func (p *fileSendServerProxy) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	ctx := stream.Context()
	var diffCopyClient filesync.FileSend_DiffCopyClient
	var err error
	var useBytesMessageType bool
	if p.destClientID == "" {
		var baseData *sessionStreamResourceData
		ctx, baseData, err = p.c.getSessionResourceData(stream)
		if err != nil {
			return err
		}
		opts := baseData.exportLocalDirData
		if opts == nil {
			return fmt.Errorf("expected export local dir opts")
		}
		diffCopyClient, err = filesync.NewFileSendClient(opts.session.Conn()).DiffCopy(ctx)
		if err != nil {
			return err
		}
	} else {
		// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
		useBytesMessageType = true
		ctx = engine.LocalExportOpts{
			DestClientID: p.destClientID,
			Path:         p.destPath,
			IsFileStream: true,
		}.AppendToOutgoingContext(ctx)

		destCaller, err := p.c.SessionManager.Get(ctx, p.destClientID, false)
		if err != nil {
			return fmt.Errorf("failed to get requester client session: %s", err)
		}
		diffCopyClient, err = filesync.NewFileSendClient(destCaller.Conn()).DiffCopy(ctx)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var eg errgroup.Group
	eg.Go(func() (rerr error) {
		defer func() {
			diffCopyClient.CloseSend()
			if errors.Is(rerr, io.EOF) {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			msg, err := withContext(ctx, func() (any, error) {
				var msg any = &filesynctypes.Packet{}
				if useBytesMessageType {
					msg = &filesync.BytesMessage{}
				}
				err := stream.RecvMsg(msg)
				return msg, err
			})
			if err != nil {
				return err
			}
			if err := diffCopyClient.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			if errors.Is(rerr, io.EOF) {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			msg, err := withContext(ctx, func() (any, error) {
				var msg any = &filesynctypes.Packet{}
				if useBytesMessageType {
					msg = &filesync.BytesMessage{}
				}
				err := diffCopyClient.RecvMsg(msg)
				return msg, err
			})
			if err != nil {
				return err
			}
			if err := stream.SendMsg(msg); err != nil {
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
