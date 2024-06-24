package server

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func (srv *Server) AddSecretsFromID(ctx context.Context, id *call.ID, sourceClientID string, skipTopLevel bool) error {
	destClient, err := srv.clientFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client from context: %w", err)
	}

	return srv.addSecretsFromID(ctx, destClient, id, sourceClientID, skipTopLevel)
}

// TODO: optimize slightly by accepting multiple IDs
func (srv *Server) addSecretsFromID(ctx context.Context, destClient *daggerClient, id *call.ID, sourceClientID string, skipTopLevel bool) error {
	srcClient, err := srv.clientFromIDs(destClient.daggerSession.sessionID, sourceClientID)
	if err != nil {
		return fmt.Errorf("failed to get source client: %w", err)
	}

	srcDag, err := srcClient.deps.Schema(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source schema: %w", err)
	}
	secrets, err := dagql.ReferencedTypes[*core.Secret](ctx, id, srcDag, skipTopLevel)
	if err != nil {
		return fmt.Errorf("failed to get referenced types: %w", err)
	}

	for _, secret := range secrets {
		srcSecretVal, err := srcClient.secretStore.GetSecret(ctx, secret.Accessor)
		if err != nil {
			return fmt.Errorf("failed to get secret: %w", err)
		}
		if err := destClient.secretStore.AddSecret(ctx, secret.Accessor, srcSecretVal); err != nil {
			return fmt.Errorf("failed to add secret: %w", err)
		}
	}

	return nil
}

// TODO: dedupe with above with generics or callback?
func (srv *Server) AddSocketsFromID(ctx context.Context, id *call.ID, sourceClientID string, skipTopLevel bool) error {
	destClient, err := srv.clientFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client from context: %w", err)
	}

	return srv.addSocketsFromID(ctx, destClient, id, sourceClientID, skipTopLevel)
}

func (srv *Server) addSocketsFromID(ctx context.Context, destClient *daggerClient, id *call.ID, sourceClientID string, skipTopLevel bool) error {
	srcClient, err := srv.clientFromIDs(destClient.daggerSession.sessionID, sourceClientID)
	if err != nil {
		return fmt.Errorf("failed to get source client: %w", err)
	}

	srcDag, err := srcClient.deps.Schema(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source schema: %w", err)
	}
	sockets, err := dagql.ReferencedTypes[*core.Socket](ctx, id, srcDag, skipTopLevel)
	if err != nil {
		return fmt.Errorf("failed to get referenced types: %w", err)
	}

	for _, socket := range sockets {
		srcSocketVal, err := srcClient.socketStore.GetSocket(socket.Name)
		if err != nil {
			return fmt.Errorf("failed to get socket: %w", err)
		}
		if err := destClient.socketStore.AddSocket(socket.Name, srcSocketVal); err != nil {
			return fmt.Errorf("failed to add socket: %w", err)
		}
	}

	return nil
}
