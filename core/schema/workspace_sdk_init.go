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

func mergeWorkspaceInitChangeset(ctx context.Context, base dagql.ObjectResult[*core.Changeset], sdkChanges dagql.ObjectResult[*core.Changeset]) (dagql.ObjectResult[*core.Changeset], error) {
	if sdkChanges.Self() == nil {
		return base, nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return base, fmt.Errorf("dagql server: %w", err)
	}
	sdkChangesID, err := sdkChanges.ID()
	if err != nil {
		return base, fmt.Errorf("merge sdk init changes: %w", err)
	}
	// Merge through the withChangeset field rather than the raw Go method so
	// the result stays an attached dagql result. Returning a detached
	// *Changeset here breaks later id/Sync resolution with "result
	// *core.Changeset is detached" (see commit "keep generated workspace
	// changesets attached").
	var merged dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, base, &merged, dagql.Selector{
		Field: "withChangeset",
		Args: []dagql.NamedInput{
			{Name: "changes", Value: dagql.NewID[*core.Changeset](sdkChangesID)},
			{Name: "onConflict", Value: FailEarlyOnMergeConflict},
		},
	}); err != nil {
		return base, fmt.Errorf("merge sdk init changes: %w", err)
	}
	return merged, nil
}
