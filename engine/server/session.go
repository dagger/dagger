package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/moby/buildkit/session/grpchijack"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sync/errgroup"
)

func (e *Engine) Session(stream controlapi.Control_SessionServer) (rerr error) {
	defer func() {
		// a panic would indicate a bug, but we don't want to take down the entire server
		if err := recover(); err != nil {
			bklog.G(context.Background()).WithError(fmt.Errorf("%v", err)).Errorf("panic in session call")
			debug.PrintStack()
			rerr = fmt.Errorf("panic in session call, please report a bug: %v %s", err, string(debug.Stack()))
		}
	}()

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	opts, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		bklog.G(ctx).WithError(err).Errorf("failed to get client metadata for session call")
		return fmt.Errorf("failed to get client metadata for session call: %w", err)
	}
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).
		WithField("client_id", opts.ClientID).
		WithField("client_hostname", opts.ClientHostname).
		WithField("server_id", opts.ServerID))

	{
		lg := bklog.G(ctx).WithField("register_client", opts.RegisterClient)
		lgLevel := lg.Trace
		if opts.RegisterClient {
			lgLevel = lg.Debug
		}
		lgLevel("handling session call")
		defer func() {
			if rerr != nil {
				lg.WithError(rerr).Errorf("session call failed")
			} else {
				lgLevel("session call done")
			}
		}()
	}

	conn, _, hijackmd := grpchijack.Hijack(stream)
	return e.HandleConn(ctx, conn, opts, hijackmd)
}

func (e *Engine) HandleConn(ctx context.Context, conn net.Conn, opts *engine.ClientMetadata, hijackmd map[string][]string) (rerr error) {
	if !opts.RegisterClient {
		// retry a few times since an initially connecting client is concurrently registering
		// the server, so this it's okay for this to take a bit to succeed
		srv, err := retry(ctx, 100*time.Millisecond, 20, func() (*DaggerServer, error) {
			e.serverMu.RLock()
			srv, ok := e.servers[opts.ServerID]
			e.serverMu.RUnlock()
			if !ok {
				return nil, fmt.Errorf("server %q not found", opts.ServerID)
			}

			if err := srv.VerifyClient(opts.ClientID, opts.ClientSecretToken); err != nil {
				return nil, fmt.Errorf("failed to verify client: %w", err)
			}
			return srv, nil
		})
		if err != nil {
			return err
		}
		bklog.G(ctx).Trace("forwarding client to server")
		err = srv.ServeClientConn(ctx, opts, conn)
		if errors.Is(err, io.ErrClosedPipe) {
			return nil
		}
		return fmt.Errorf("serve clientConn: %w", err)
	}

	bklog.G(ctx).Trace("registering client")

	eg, egctx := errgroup.WithContext(ctx)
	eg.Go(func() (rerr error) {
		// overwrite the session ID to be our client ID + server ID
		hijackmd[buildkit.BuildkitSessionIDHeader] = []string{opts.BuildkitSessionID()}
		hijackmd[http.CanonicalHeaderKey(buildkit.BuildkitSessionIDHeader)] = []string{opts.BuildkitSessionID()}

		bklog.G(ctx).Debugf("session manager handling conn %s %+v", opts.BuildkitSessionID(), hijackmd)
		defer func() {
			bklog.G(ctx).WithError(rerr).Debugf("session manager handle conn done %s", opts.BuildkitSessionID())
		}()

		err := e.bkSessionManager.HandleConn(egctx, conn, hijackmd)
		slog.Trace("session manager handle conn done", "err", err, "ctxErr", ctx.Err(), "egCtxErr", egctx.Err())
		if err != nil {
			return fmt.Errorf("handleConn: %w", err)
		}
		return nil
	})

	// NOTE: the perServerMu here is used to ensure that we hold a lock
	// specific to only *this server*, so we don't allow creating multiple
	// servers with the same ID at once. This complexity is necessary so we
	// don't hold the global serverMu lock for longer than necessary.
	e.perServerMu.Lock(opts.ServerID)
	e.serverMu.RLock()
	srv, ok := e.servers[opts.ServerID]
	e.serverMu.RUnlock()
	if !ok {
		bklog.G(ctx).Trace("initializing new server")

		var err error
		srv, err = e.newDaggerServer(ctx, opts)
		if err != nil {
			e.perServerMu.Unlock(opts.ServerID)
			return fmt.Errorf("new APIServer: %w", err)
		}
		e.serverMu.Lock()
		e.servers[opts.ServerID] = srv
		e.serverMu.Unlock()

		bklog.G(ctx).Trace("initialized new server")

		// delete the server after the initial client who created it exits
		defer func() {
			bklog.G(ctx).Trace("removing server")
			e.serverMu.Lock()
			delete(e.servers, opts.ServerID)
			e.serverMu.Unlock()

			if err := srv.Close(context.WithoutCancel(ctx)); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to close server")
			}

			time.AfterFunc(time.Second, e.throttledGC)
			bklog.G(ctx).Trace("server removed")
		}()
	}
	e.perServerMu.Unlock(opts.ServerID)

	err := srv.RegisterClient(opts.ClientID, opts.ClientHostname, opts.ClientSecretToken)
	if err != nil {
		return fmt.Errorf("failed to register client: %w", err)
	}

	eg.Go(func() error {
		bklog.G(ctx).Trace("waiting for server")
		err := srv.Wait(egctx)
		bklog.G(ctx).WithError(err).Trace("server done")
		if err != nil {
			return fmt.Errorf("srv.Wait: %w", err)
		}
		return nil
	})
	err = eg.Wait()
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	return nil
}
