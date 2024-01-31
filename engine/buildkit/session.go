package buildkit

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/engine/client"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/bklog"
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
	sess.Allow(&client.AnyDirSource{})
	sess.Allow(&client.AnyDirTarget{})
	sess.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + OCIStoreName: c.Worker.ContentStore(),
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
		// this ctx is okay because it's from the "main client" caller, so if it's canceled
		// then we want to shutdown anyways
		err := sess.Run(ctx, dialer)
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

func (c *Client) GetSessionCaller(ctx context.Context, clientID string) (bksession.Caller, error) {
	waitForSession := true
	return c.SessionManager.Get(ctx, clientID, !waitForSession)
}
