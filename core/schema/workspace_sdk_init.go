package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) loadWorkspaceSDK(
	ctx context.Context,
	sdkRef string,
) (core.SDK, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}
	root, ok := srv.Root().(dagql.ObjectResult[*core.Query])
	if !ok {
		return nil, fmt.Errorf("dagql root: unexpected type %T", srv.Root())
	}

	loaded, err := coresdk.NewLoader().SDKForModule(ctx, root.Self(), &core.SDKConfig{
		Source: sdkRef,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("load sdk %q: %w", sdkRef, err)
	}
	return loaded, nil
}

func (s *workspaceSchema) currentWorkspaceObject(ctx context.Context) (dagql.ObjectResult[*core.Workspace], error) {
	var workspace dagql.ObjectResult[*core.Workspace]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return workspace, fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &workspace, dagql.Selector{Field: "currentWorkspace"}); err != nil {
		return workspace, fmt.Errorf("current workspace: %w", err)
	}
	return workspace, nil
}

func mergeWorkspaceInitChangeset(ctx context.Context, base *core.Changeset, sdkChanges dagql.ObjectResult[*core.Changeset]) (*core.Changeset, error) {
	if sdkChanges.Self() == nil {
		return base, nil
	}
	merged, err := base.WithChangeset(ctx, sdkChanges.Self(), core.FailEarlyOnConflict)
	if err != nil {
		return nil, fmt.Errorf("merge sdk init changes: %w", err)
	}
	return merged, nil
}
