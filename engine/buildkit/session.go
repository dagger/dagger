package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/bklog"
)

const (
	// OCIStoreName is the name of the OCI content store used for OCI tarball
	// imports.
	OCIStoreName = "dagger-oci"

	// BuiltinContentOCIStoreName is the name of the OCI content store used for
	// builtins like SDKs that we package with the engine container but still use
	// in LLB.
	BuiltinContentOCIStoreName = "dagger-builtin-content"
)

func (c *Client) newSession() (*bksession.Session, error) {
	sess, err := bksession.NewSession(c.closeCtx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}

	builtinStore, err := local.NewStore(distconsts.EngineContainerBuiltinContentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create go sdk content store: %w", err)
	}

	sess.Allow(secretsprovider.NewSecretProvider(c.SecretStore))
	sess.Allow(&socketProxy{c})
	sess.Allow(&authProxy{c})
	sess.Allow(&client.AnyDirSource{})
	sess.Allow(&client.AnyDirTarget{})
	sess.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + OCIStoreName:               c.Worker.ContentStore(),
		"oci:" + BuiltinContentOCIStoreName: builtinStore,
	}))

	clientConn, serverConn := net.Pipe()
	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) { //nolint: unparam
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
		// this ctx will be cancelled when Client is closed
		err := sess.Run(c.closeCtx, dialer)
		if err != nil {
			lg := bklog.G(c.closeCtx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session ended")
			} else {
				lg.Error("failed to run session")
			}
		}
	}()
	return sess, nil
}

func (c *Client) GetSessionCaller(ctx context.Context, clientID string) (bksession.Caller, error) {
	waitForSession := true
	return c.SessionManager.Get(ctx, clientID, !waitForSession)
}
