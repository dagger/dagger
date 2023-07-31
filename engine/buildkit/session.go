package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/engine"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/bklog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// OCIStoreName is the name of the OCI content store used for OCI tarball
// imports.
const OCIStoreName = "dagger-oci"

func (c *Client) newSession(ctx context.Context) (*bksession.Session, error) {
	sess, err := bksession.NewSession(ctx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}

	sess.Allow(secretsprovider.NewSecretProvider(c.SecretStore))
	sess.Allow(&socketProxy{c})
	sess.Allow(&authProxy{c})
	sess.Allow(&fileSendServerProxy{c: c})
	sess.Allow(&fileSyncServerProxy{c})
	sess.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + OCIStoreName: c.Worker.ContentStore(),
	}))

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
					lg.Error("failed to handle session conn")
				}
			}
		}()
		return clientConn, nil
	}
	go func() {
		defer clientConn.Close()
		defer sess.Close()
		// this ctx is okay because it's from the "main client" caller, so if it's canceled
		// then we want to shutdown anyways
		err := sess.Run(ctx, dialer)
		if err != nil {
			lg := bklog.G(ctx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session in dagger frontend ended")
			} else {
				lg.Error("failed to run dagger frontend session")
			}
		}
	}()
	return sess, nil
}

func (c *Client) GetSessionCaller(ctx context.Context, clientID string) (bksession.Caller, error) {
	waitForSession := true
	return c.SessionManager.Get(ctx, clientID, !waitForSession)
}

type localImportOpts struct {
	*engine.LocalImportOpts
	session bksession.Caller
}

func (c *Client) getLocalImportOpts(stream grpc.ServerStream) (context.Context, *localImportOpts, error) {
	opts, err := engine.LocalImportOptsFromContext(stream.Context())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get local import opts: %w", err)
	}

	if err := DecodeIDHack("local", opts.Path, opts); err != nil {
		return nil, nil, fmt.Errorf("invalid import local dir name: %q", opts.Path)
	}
	sess, err := c.SessionManager.Get(stream.Context(), opts.OwnerClientID, false)
	if err != nil {
		return nil, nil, err
	}
	md := opts.ToGRPCMD()
	ctx := metadata.NewIncomingContext(stream.Context(), md) // TODO: needed too?
	ctx = metadata.NewOutgoingContext(ctx, md)
	return ctx, &localImportOpts{
		LocalImportOpts: opts,
		session:         sess,
	}, nil
}

type localExportOpts struct {
	*engine.LocalExportOpts
	session bksession.Caller
}

func (c *Client) getLocalExportOpts(stream grpc.ServerStream) (context.Context, *localExportOpts, error) {
	opts, err := engine.LocalExportOptsFromContext(stream.Context())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get local export opts: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(stream.Context())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get client metadata: %w", err)
	}

	// for now, require that the requester is the owner of the session, i.e. you
	// can only export to yourself, not to others
	if clientMetadata.ClientID != opts.DestClientID {
		return nil, nil, errors.New("local dir export requester is not the owner of the dest session")
	}

	if opts.Path == "" {
		return nil, nil, fmt.Errorf("missing local dir export path")
	}

	sess, err := c.SessionManager.Get(stream.Context(), opts.DestClientID, false)
	if err != nil {
		return nil, nil, err
	}
	md := opts.ToGRPCMD()
	ctx := metadata.NewIncomingContext(stream.Context(), md) // TODO: needed too?
	ctx = metadata.NewOutgoingContext(ctx, md)
	return ctx, &localExportOpts{
		LocalExportOpts: opts,
		session:         sess,
	}, nil
}
