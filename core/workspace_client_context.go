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
	clientMetadata, err := workspaceClientMetadata(ctx, query, ws)
	if err != nil {
		return ctx, err
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}

// workspaceClientMetadata returns the metadata of the client that host
// operations on ws go through.
//
// That is normally the client that loaded ws. A workspace that holds its own
// content is shared by cache hits across clients, sessions and engines (see
// ContentIdentity), so the client that loaded it may be closed, outside the
// caller's ancestry, or on another engine. Its content doesn't depend on that
// client, so it goes through the caller's nearest non-module client instead,
// whose loaded modules and credentials are the ones the caller's own workspace
// would use. Fields that depend on those are keyed per client.
func workspaceClientMetadata(ctx context.Context, query *Query, ws *Workspace) (*engine.ClientMetadata, error) {
	if contentIdentifiableSource(ws.Source()) {
		clientMetadata, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return nil, fmt.Errorf("get caller client metadata: %w", err)
		}
		return clientMetadata, nil
	}
	clientMetadata, err := query.SpecificClientMetadata(ctx, ws.ClientID)
	if err != nil {
		return nil, fmt.Errorf("get client metadata: %w", err)
	}
	return clientMetadata, nil
}
