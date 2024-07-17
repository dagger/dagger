package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func (srv *Server) AddClientResourcesFromID(ctx context.Context, id *call.ID, sourceClientID string, skipTopLevel bool) error {
	destClient, err := srv.clientFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client from context: %w", err)
	}

	return srv.addClientResourcesFromID(ctx, destClient, id, sourceClientID, skipTopLevel)
}

func (srv *Server) addClientResourcesFromID(ctx context.Context, destClient *daggerClient, id *call.ID, sourceClientID string, skipTopLevel bool) error {
	walked, err := dagql.WalkID(id, skipTopLevel)
	if err != nil {
		return fmt.Errorf("failed to walk ID: %w", err)
	}

	secretIDs := dagql.WalkedIDs[*core.Secret](walked)
	socketIDs := dagql.WalkedIDs[*core.Socket](walked)

	// Filter out resources that this client already knows about. This is important for the case
	// where the sourceClientID isn't found, which can happen due to caching skipping the client's
	// execution. In this case, we want to error out only if the returned cached IDs were not
	// ones we already knew about (i.e. they were passed in as arguments).
	var filteredSecretIDs []dagql.ID[*core.Secret]
	for _, secretID := range secretIDs {
		if ok := destClient.secretStore.HasSecret(secretID.ID().Digest()); !ok {
			filteredSecretIDs = append(filteredSecretIDs, secretID)
		}
	}
	secretIDs = filteredSecretIDs

	var filteredSocketIDs []dagql.ID[*core.Socket]
	for _, socketID := range socketIDs {
		if ok := destClient.socketStore.HasSocket(socketID.ID().Digest()); !ok {
			filteredSocketIDs = append(filteredSocketIDs, socketID)
		}
	}
	socketIDs = filteredSocketIDs

	srcClient, ok := srv.clientFromIDs(destClient.daggerSession.sessionID, sourceClientID)
	if !ok {
		var err error
		if len(secretIDs) > 0 {
			err = errors.Join(err, errors.New("cached result contains unknown secret IDs"))
		}
		if len(socketIDs) > 0 {
			err = errors.Join(err, errors.New("cached result contains unknown socket IDs"))
		}
		return err // if nil, that's fine, nothing more to do here
	}

	srcDag, err := srcClient.deps.Schema(ctx)
	if err != nil {
		return fmt.Errorf("failed to get source schema: %w", err)
	}

	if len(secretIDs) > 0 {
		secrets, err := dagql.LoadIDs(ctx, srcDag, secretIDs)
		if err != nil {
			return fmt.Errorf("failed to load secrets: %w", err)
		}
		for _, secret := range secrets {
			if err := destClient.secretStore.AddSecretFromOtherStore(secret, srcClient.secretStore); err != nil {
				return fmt.Errorf("failed to add secret: %w", err)
			}
		}
	}

	if len(socketIDs) > 0 {
		sockets, err := dagql.LoadIDs(ctx, srcDag, socketIDs)
		if err != nil {
			return fmt.Errorf("failed to load sockets: %w", err)
		}
		for _, socket := range sockets {
			if err := destClient.socketStore.AddSocketFromOtherStore(socket, srcClient.socketStore); err != nil {
				return fmt.Errorf("failed to add socket: %w", err)
			}
		}
	}

	return nil
}
