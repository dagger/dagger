package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

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

// rerootChangesetAtWorkspaceRoot converts a changeset whose paths are
// relative to the caller cwd into one whose paths are workspace-root-relative
// by nesting both sides under cwd. SDK init functions return changesets in
// caller-cwd coordinates — standalone clients apply changesets relative to
// their own cwd — but Workspace.moduleInit merges them with engine changes
// and the CLI applies the result at the workspace root.
//
// Only the changed paths are carried into the re-rooted trees. SDK changesets
// diff two full workspace snapshots, and nesting those wholesale would embed
// the workspace's .git directory inside the merge trees, which breaks the
// git-based changeset merge ("does not have a commit checked out") and can
// leak snapshot races under .git into the diff.
func rerootChangesetAtWorkspaceRoot(ctx context.Context, changes dagql.ObjectResult[*core.Changeset], cwd string) (dagql.ObjectResult[*core.Changeset], error) {
	cwd = filepath.Clean(cwd)
	if changes.Self() == nil || cwd == "" || cwd == "." {
		return changes, nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return changes, fmt.Errorf("dagql server: %w", err)
	}

	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return changes, fmt.Errorf("re-root changeset paths: %w", err)
	}
	seen := map[string]struct{}{}
	include := dagql.ArrayInput[dagql.String]{}
	for _, p := range slices.Concat(paths.Added, paths.Modified, paths.Removed) {
		p = filepath.ToSlash(filepath.Clean(p))
		if p == "." || p == "" {
			continue
		}
		if p == ".git" || strings.HasPrefix(p, ".git/") {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		include = append(include, dagql.String(p))
	}

	nest := func(dir dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		dirID, err := dir.ID()
		if err != nil {
			return dir, fmt.Errorf("directory id: %w", err)
		}
		var out dagql.ObjectResult[*core.Directory]
		err = srv.Select(ctx, srv.Root(), &out,
			dagql.Selector{Field: "directory"},
			dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(cwd)},
					{Name: "source", Value: dagql.NewID[*core.Directory](dirID)},
					{Name: "include", Value: include},
				},
			},
		)
		return out, err
	}

	before, err := nest(changes.Self().Before)
	if err != nil {
		return changes, fmt.Errorf("re-root changeset before: %w", err)
	}
	after, err := nest(changes.Self().After)
	if err != nil {
		return changes, fmt.Errorf("re-root changeset after: %w", err)
	}
	beforeID, err := before.ID()
	if err != nil {
		return changes, fmt.Errorf("re-rooted before id: %w", err)
	}

	var rerooted dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, after, &rerooted, dagql.Selector{
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)},
		},
	}); err != nil {
		return changes, fmt.Errorf("re-root changeset: %w", err)
	}
	return rerooted, nil
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
