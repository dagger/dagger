package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine"
)

// WorkspaceClientContext uses the client that supplied a live workspace.
// Value workspaces do not depend on a client filesystem.
func WorkspaceClientContext(ctx context.Context, ws *Workspace) (context.Context, error) {
	if ws.IsValueWorkspace() {
		return ctx, nil
	}
	if ws.ClientID == "" {
		return nil, fmt.Errorf("workspace has no client ID")
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}
	clientMetadata, err := query.SpecificClientMetadata(ctx, ws.ClientID)
	if err != nil {
		return ctx, fmt.Errorf("get client metadata: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}
