package server

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

func (srv *Server) AddClientResourcesFromID(ctx context.Context, id *resource.ID, sourceClientID string, skipTopLevel bool) error {
	destClient, err := srv.clientFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client from context: %w", err)
	}

	return srv.addClientResourcesFromID(ctx, destClient, id, sourceClientID, skipTopLevel)
}

func (srv *Server) addClientResourcesFromID(ctx context.Context, destClient *daggerClient, id *resource.ID, sourceClientID string, skipTopLevel bool) error {
	walked, err := dagql.WalkID(&id.ID, skipTopLevel)
	if err != nil {
		return fmt.Errorf("failed to walk ID: %w", err)
	}

	secretIDs := dagql.WalkedIDs[*core.Secret](walked)
	socketIDs := dagql.WalkedIDs[*core.Socket](walked)

	// Filter out resources that this client already knows about. If the source client
	// is unavailable (e.g. cache skipped execution and source caller disconnected), we
	// can safely skip transfer for already-known resources.
	var filteredSecretIDs []dagql.ID[*core.Secret]
	for _, secretID := range secretIDs {
		if ok := destClient.secretStore.HasSecret(core.SecretIDDigest(secretID.ID())); !ok {
			filteredSecretIDs = append(filteredSecretIDs, secretID)
		}
	}
	secretIDs = filteredSecretIDs

	var filteredSocketIDs []dagql.ID[*core.Socket]
	for _, socketID := range socketIDs {
		if ok := destClient.socketStore.HasSocket(core.SocketIDDigest(socketID.ID())); !ok {
			filteredSocketIDs = append(filteredSocketIDs, socketID)
		}
	}
	socketIDs = filteredSocketIDs

	srcClient, err := srv.clientFromIDs(destClient.daggerSession.sessionID, sourceClientID)
	if err != nil {
		if len(secretIDs) > 0 || len(socketIDs) > 0 {
			slog.Warn("skipping client resource transfer; source client unavailable",
				"err", err,
				"destClientID", destClient.clientID,
				"sourceClientID", sourceClientID,
				"numUnknownSecretIDs", len(secretIDs),
				"numUnknownSocketIDs", len(socketIDs),
				"idOptional", id.Optional,
			)
		}
		return nil
	}

	// Load IDs in the source client's metadata context so any cache-miss re-evaluation
	// (e.g. host.unixSocket post-call side effects) targets the correct source client.
	srcClientCtx := engine.ContextWithClientMetadata(ctx, srcClient.clientMetadata)
	// Ensure the query context is set for schema building. The ctx from
	// initializeDaggerClient may not have it (it's set later), but
	// lazilyLoadSchema needs it for dag.Select operations like __schemaJSONFile.
	if srcClient.dagqlRoot != nil {
		srcClientCtx = core.ContextWithQuery(srcClientCtx, srcClient.dagqlRoot)
	}

	srcDag, err := srcClient.deps.Schema(srcClientCtx)
	if err != nil {
		return fmt.Errorf("failed to get source schema: %w", err)
	}

	if len(secretIDs) > 0 {
		secrets, err := dagql.LoadIDResults(srcClientCtx, srcDag, secretIDs)
		if err != nil && !id.Optional {
			return fmt.Errorf("failed to load secrets: %w", err)
		}
		for _, secret := range secrets {
			if secret.Self() == nil {
				continue
			}
			// try to add the secret, if it's not found continue on as worst case an error will just
			// be hit later if/when the secret is attempted to be used
			if err := destClient.secretStore.AddSecretFromOtherStore(srcClient.secretStore, secret); err != nil {
				slog.Error("failed to add secret from other store", "err", err, "srcClientID", srcClient.clientID)
				continue
			}
		}
	}

	if len(socketIDs) > 0 {
		sockets, err := dagql.LoadIDs(srcClientCtx, srcDag, socketIDs)
		if err != nil && !id.Optional {
			return fmt.Errorf("failed to load sockets: %w", err)
		}
		for _, socket := range sockets {
			if socket == nil {
				continue
			}
			// try to add the socket, if it's not found continue on as worst case an error will just
			// be hit later if/when the socket is attempted to be used
			if err := destClient.socketStore.AddSocketFromOtherStore(socket, srcClient.socketStore); err != nil {
				slog.Error("failed to add socket from other store", "err", err, "srcClientID", srcClient.clientID)
				continue
			}
		}
	}

	return nil
}
