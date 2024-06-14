package server

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// TODO: optimize slightly by accepting multiple IDs
func (srv *Server) AddSecretsFromID(ctx context.Context, id *call.ID, sourceClientID string) error {
	destClient, err := srv.clientFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client from context: %w", err)
	}

	srcClient, err := srv.clientFromIDs(destClient.daggerSession.sessionID, sourceClientID)
	if err != nil {
		return fmt.Errorf("failed to get source client: %w", err)
	}

	srcDag, err := srcClient.deps.Schema(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source schema: %w", err)
	}
	secrets, err := dagql.ReferencedTypes[*core.Secret](ctx, id, srcDag)
	if err != nil {
		return fmt.Errorf("failed to get referenced types: %w", err)
	}

	for _, secret := range secrets {
		srcSecretVal, err := srcClient.daggerSession.secretStore.GetSecret(ctx, secret.Accessor)
		if err != nil {
			return fmt.Errorf("failed to get secret: %w", err)
		}
		if err := destClient.daggerSession.secretStore.AddSecret(ctx, secret.Accessor, srcSecretVal); err != nil {
			return fmt.Errorf("failed to add secret: %w", err)
		}
	}

	return nil
}
