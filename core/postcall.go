package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/opencontainers/go-digest"
)

func ResourceTransferPostCall(
	ctx context.Context,
	query *Query,
	sourceClientID string,
	ids ...*resource.ID,
) (func(context.Context) error, error) {
	secretsByDgst := map[digest.Digest]dagql.ID[*Secret]{}
	for _, id := range ids {
		walked, err := dagql.WalkID(&id.ID, false)
		if err != nil {
			return nil, fmt.Errorf("failed to walk ID: %w", err)
		}
		secretIDs := dagql.WalkedIDs[*Secret](walked)
		for _, secretID := range secretIDs {
			secretsByDgst[secretID.ID().Digest()] = secretID
		}
	}
	if len(secretsByDgst) == 0 {
		return nopTransfer, nil
	}

	var secretIDs []dagql.ID[*Secret]
	for _, secretID := range secretsByDgst {
		secretIDs = append(secretIDs, secretID)
	}
	// just in case order matters for caching somehow someday
	slices.SortFunc(secretIDs, func(a, b dagql.ID[*Secret]) int {
		return strings.Compare(a.ID().Digest().String(), b.ID().Digest().String())
	})

	// TODO: cleanup
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get source client metadata: %w", err)
	}
	srcClientCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  sourceClientID,
		SessionID: clientMetadata.SessionID,
	})

	srcSecretStore, err := query.Secrets(srcClientCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get source client secret store: %w", err)
	}
	srcDag, err := query.DagqlServer(srcClientCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get source client dagql server: %w", err)
	}

	secrets, err := dagql.LoadIDInstances(srcClientCtx, srcDag, secretIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to load secret instances: %w", err)
	}

	type secretWithPlaintext struct {
		inst      dagql.Instance[*Secret]
		plaintext []byte
	}
	var namedSecrets []secretWithPlaintext
	for _, secret := range secrets {
		isNamed := secret.Self.Name != ""
		if !isNamed {
			continue
		}
		plaintext, err := srcSecretStore.GetSecretPlaintext(ctx, secret.ID().Digest())
		if err != nil {
			return nil, fmt.Errorf("failed to get secret plaintext: %w", err)
		}
		namedSecrets = append(namedSecrets, secretWithPlaintext{inst: secret, plaintext: plaintext})
	}

	callerClientMemo := sync.Map{}
	var postCall func(context.Context) error
	postCall = func(ctx context.Context) error {
		callerClientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to get client metadata: %w", err)
		}
		if callerClientMD.ClientID == sourceClientID {
			// no need to transfer to ourself
			return nil
		}
		if _, alreadyRan := callerClientMemo.LoadOrStore(callerClientMD.ClientID, struct{}{}); alreadyRan {
			// only run this once per calling client, no need to re-add resources
			return nil
		}

		destClientSecretStore, err := query.Secrets(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination client secret store: %w", err)
		}
		destDag, err := query.DagqlServer(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination client dagql server: %w", err)
		}
		for _, secret := range namedSecrets {
			if err := destClientSecretStore.AddSecret(secret.inst); err != nil {
				return fmt.Errorf("failed to add secret: %w", err)
			}
			_, err = destDag.Cache.GetOrInitializeWithPostCall(ctx, secret.inst.ID().Digest(),
				func(ctx context.Context) (dagql.Typed, func(context.Context) error, error) {
					return secret.inst, postCall, nil
				},
			)
			if err != nil {
				return fmt.Errorf("failed to cache secret: %w", err)
			}
		}

		return nil
	}

	return postCall, nil
}

func nopTransfer(ctx context.Context) error {
	return nil
}
