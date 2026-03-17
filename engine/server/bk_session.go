package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/internal/buildkit/identity"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	sessioncontent "github.com/dagger/dagger/internal/buildkit/session/content"
	"github.com/dagger/dagger/internal/buildkit/session/secrets/secretsprovider"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/distconsts"
)

func (srv *Server) newBuildkitSession(ctx context.Context, c *daggerClient) (*bksession.Session, error) {
	sess, err := bksession.NewSession(ctx, identity.NewID())
	if err != nil {
		return nil, err
	}

	// for nested clients running the dagger cli, the session attachable connection
	// may not have all of the client metadata (i.e. AllowedLLMModules), so we
	// point to c.clientMetadata here which may be updated with that info by
	// later connections. This matters because dag-ops obtain the client metadata
	// through this particular context.
	ctx = engine.ContextWithClientMetadata(ctx, c.clientMetadata)

	builtinStore, err := local.NewStore(distconsts.EngineContainerBuiltinContentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create go sdk content store: %w", err)
	}

	sess.Allow(secretsprovider.NewSecretProvider(c.secretStore.AsBuildkitSecretStore()))
	sess.Allow(c.socketStore)
	sess.Allow(&authProxy{c, srv.bkSessionManager})
	sess.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + buildkit.OCIStoreName:               srv.contentStore,
		"oci:" + buildkit.BuiltinContentOCIStoreName: builtinStore,
	}))

	filesyncer, err := client.NewFilesyncer()
	if err != nil {
		return nil, fmt.Errorf("new filesyncer: %w", err)
	}
	sess.Allow(filesyncer.AsSource())
	sess.Allow(filesyncer.AsTarget())

	clientConn, serverConn := net.Pipe()
	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) { //nolint: unparam
		go func() {
			defer serverConn.Close()

			// Disable collecting otel metrics on these grpc connections for now. We don't use them and
			// they add noticeable memory allocation overhead, especially for heavy filesync use cases.
			ctx = trace.ContextWithSpan(ctx, trace.SpanFromContext(nil)) //nolint:staticcheck // we have to provide a nil context...

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
