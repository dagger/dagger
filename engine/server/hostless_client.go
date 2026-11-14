package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"

	"github.com/dagger/dagger/engine"
)

// WithHostlessClient runs fn as a fresh host-less child of the client whose
// scope ctx holds: a nested client with inert attachables, like an in-engine
// SDK client serving no module. Under it, host access (host.directory,
// host.file, sockets, host services) is denied, and a secret read for an
// agent asks its owner first (see AuthorizeSecret), whatever the parent could
// read itself. The client is closed when fn returns; results evaluated under
// it outlive it, as an in-engine SDK client's do.
//
// It is how a restored agent's bindings load (see core.LoadBoundObject):
// nothing gains access by being resumed, so the client driving the restored
// loop, a CLI with a host as much as a host-less module client, lends none of
// its own.
func (srv *Server) WithHostlessClient(ctx context.Context, fn func(context.Context) error) (rerr error) {
	parentMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	md := &engine.ClientMetadata{
		ClientID:          rand.Text(),
		ClientSecretToken: rand.Text(),
		SessionID:         parentMD.SessionID,
		ClientStableID:    rand.Text(),
		ClientVersion:     engine.Version,
		AllowedLLMModules: slices.Clone(parentMD.AllowedLLMModules),
	}
	parentClientID, err := engine.NestedClientParentID(ctx, md.SessionID)
	if err != nil {
		return err
	}
	transport, err := srv.RegisterNestedClientTransport(ctx, md, parentClientID)
	if err != nil {
		return fmt.Errorf("register host-less client: %w", err)
	}
	defer transport.Close()

	var lease *engine.ClientLifecycleLease
	client, cleanup, err := srv.getOrInitClient(engine.ContextWithClientMetadata(ctx, md), &ClientInitOpts{
		ClientMetadata:   md,
		NestedTransport:  transport,
		ParentClientID:   parentClientID,
		InertAttachables: true,
		requestOwner:     "host-less client",
		requestLeaseOut:  &lease,
	})
	if err != nil {
		return fmt.Errorf("initialize host-less client: %w", err)
	}
	defer func() {
		rerr = errors.Join(rerr, cleanup())
	}()
	if lease == nil || !lease.Held() {
		return fmt.Errorf("host-less client %q has no lifecycle lease", client.clientID)
	}
	scope, err := engine.NewClientScope(client.clientMetadata, lease)
	if err != nil {
		return fmt.Errorf("host-less client scope: %w", err)
	}
	childCtx, err := engine.ContextWithClientScope(engine.ContextWithClientMetadata(ctx, client.clientMetadata), scope)
	if err != nil {
		return fmt.Errorf("install host-less client scope: %w", err)
	}
	return fn(childCtx)
}
