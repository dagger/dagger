package daggercmd

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/spf13/cobra"
)

func artifactSourceFilterEnabled(cmd *cobra.Command) bool {
	uncommitted, _ := cmd.Flags().GetBool("uncommitted")
	return uncommitted || cmd.Flags().Changed("commit") || cmd.Flags().Changed("path")
}

// filterArtifactsBySource uses the same frozen workspace as artifact discovery
// and execution. With Git selectors, paths restrict the changes before matching
// dependencies. Without Git selectors, paths select literal source subtrees.
func filterArtifactsBySource(ctx context.Context, dag *dagger.Client, ws *core.Workspace, selection *core.Artifacts, cmd *cobra.Command) (*core.Artifacts, error) {
	paths := []core.WorkspacePath{}
	if cmd.Flags().Changed("path") {
		values, _ := cmd.Flags().GetStringArray("path")
		cwd, err := ws.Cwd(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve --path working directory: %w", err)
		}
		paths, err = artifactSourcePaths(values, cwd)
		if err != nil {
			return nil, err
		}
	}
	commits, _ := cmd.Flags().GetStringArray("commit")
	for _, revision := range commits {
		if strings.TrimSpace(revision) == "" {
			return nil, fmt.Errorf("--commit requires a non-empty commit")
		}
	}
	query := core.NewQuery(dag)
	changes := []*core.Changeset{}
	uncommitted, _ := cmd.Flags().GetBool("uncommitted")
	if uncommitted {
		id, err := ws.Git().Uncommitted().ID(ctx)
		if err != nil {
			return nil, fmt.Errorf("read --uncommitted changes: %w", err)
		}
		changes = append(changes, core.Ref[*core.Changeset](query, id))
	}
	if len(commits) > 0 {
		repository := ws.Git().Head().AsRepository()
		for _, revision := range commits {
			id, err := repository.Ref(revision).TargetCommit().Changes().ID(ctx)
			if err != nil {
				return nil, fmt.Errorf("read --commit %q changes: %w", revision, err)
			}
			changes = append(changes, core.Ref[*core.Changeset](query, id))
		}
	}
	id, err := selection.FilterAffected(core.ArtifactsFilterAffectedOpts{Paths: paths, Changes: changes}).ID(ctx)
	if err != nil {
		return nil, err
	}
	return core.Ref[*core.Artifacts](query, id), nil
}

func artifactSourcePaths(values []string, cwd string) ([]core.WorkspacePath, error) {
	paths := []core.WorkspacePath{}
	base := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(cwd, `\`, "/")), "/")
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("--path requires a non-empty path")
		}
		clean := path.Clean(strings.ReplaceAll(value, `\`, "/"))
		if !strings.HasPrefix(clean, "/") {
			clean = path.Join(base, clean)
			if clean == ".." || strings.HasPrefix(clean, "../") {
				return nil, fmt.Errorf("--path %q escapes the workspace root", value)
			}
		}
		resolved := core.WorkspacePath(path.Clean("/" + clean))
		if !slices.Contains(paths, resolved) {
			paths = append(paths, resolved)
		}
	}
	return paths, nil
}
