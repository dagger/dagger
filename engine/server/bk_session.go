package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/bklog"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/distconsts"
)

func (srv *Server) newBuildkitSession(ctx context.Context, c *daggerClient) (*bksession.Session, error) {
	sess, err := bksession.NewSession(ctx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}

	builtinStore, err := local.NewStore(distconsts.EngineContainerBuiltinContentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create go sdk content store: %w", err)
	}

	sess.Allow(secretsprovider.NewSecretProvider(c.daggerSession.secretStore))
	sess.Allow(&socketProxy{c, srv.bkSessionManager})
	sess.Allow(&authProxy{c, srv.bkSessionManager})
	sess.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + buildkit.OCIStoreName:               srv.contentStore,
		"oci:" + buildkit.BuiltinContentOCIStoreName: builtinStore,
	}))

	filesyncer, err := client.NewFilesyncer("", "", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("new filesyncer: %w", err)
	}
	sess.Allow(filesyncer.AsSource())
	sess.Allow(filesyncer.AsTarget())

	clientConn, serverConn := net.Pipe()
	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) { //nolint: unparam
		go func() {
			defer serverConn.Close()
			err := srv.bkSessionManager.HandleConn(ctx, serverConn, meta)
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
		// this ctx will be cancelled when Client is closed
		err := sess.Run(context.WithoutCancel(ctx), dialer)
		if err != nil {
			lg := bklog.G(ctx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session ended")
			} else {
				lg.Error("failed to run session")
			}
		}
	}()
	return sess, nil
}
