package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) loadWorkspaceSDK(
	ctx context.Context,
	ws *core.Workspace,
	configDir string,
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
	loader := coresdk.NewLoader()
	sdkConfig := &core.SDKConfig{Source: sdkRef}
	if coresdk.IsBuiltinSDKName(sdkRef) || core.FastModuleSourceKindCheck(sdkRef, "") == core.ModuleSourceKindGit {
		loaded, err := loader.SDKForModule(ctx, root.Self(), sdkConfig, nil)
		if err != nil {
			return nil, fmt.Errorf("load sdk %q: %w", sdkRef, err)
		}
		return loaded, nil
	}

	workspaceRoot, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("load workspace SDK root: %w", err)
	}
	configDir = filepath.ToSlash(cleanWorkspaceRelPath(configDir))
	workspaceSource := &core.ModuleSource{
		ModuleName:        "workspace",
		SourceRootSubpath: configDir,
		ContextDirectory:  workspaceRoot,
		Kind:              core.ModuleSourceKindDir,
		DirSrc: &core.DirModuleSource{
			OriginalContextDir:        workspaceRoot,
			OriginalSourceRootSubpath: configDir,
		},
	}

	loaded, err := loader.SDKForModule(ctx, root.Self(), sdkConfig, workspaceSource)
	if err != nil {
		return nil, fmt.Errorf("load sdk %q: %w", sdkRef, err)
	}
	return loaded, nil
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
