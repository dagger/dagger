package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
)

func SecretTransferPostCall(
	ctx context.Context,
	query *Query,
	sourceClientID string,
	ids ...*resource.ID,
) (func(context.Context) error, error) {
	callerClientMemo := sync.Map{}
	return func(ctx context.Context) error {
		// only run this once per calling client, no need to re-add resources
		callerClientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to get client metadata: %w", err)
		}
		if _, alreadyRan := callerClientMemo.LoadOrStore(callerClientMD.ClientID, struct{}{}); alreadyRan {
			return nil
		}

		for _, id := range ids {
			if err := query.AddClientResourcesFromID(ctx, id, sourceClientID, false); err != nil {
				return fmt.Errorf("failed to add client resources from ID: %w", err)
			}
		}
		return nil
	}, nil
}
