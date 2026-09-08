package daggercmd

import (
	"context"
	"fmt"
	"io"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

func runModuleUpdate(cmd *cobra.Command, names []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		var result struct {
			CurrentWorkspace struct {
				Result struct {
					ID dagger.ID
				}
			}
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query: `query ModuleUpdate($names: [String!]!) {
  currentWorkspace {
    result: withUpdatedModules(names: $names) { id }
  }
}`,
			Variables: map[string]any{"names": names},
		}, &dagger.Response{Data: &result}); err != nil {
			return err
		}
		if result.CurrentWorkspace.Result.ID == "" {
			return fmt.Errorf("module update returned no workspace")
		}
		current := dag.CurrentWorkspace().WithWorkdir(".")
		updated := dagger.Ref[*dagger.Workspace](dag, result.CurrentWorkspace.Result.ID).WithWorkdir(".")
		return updateMaterializedWorkspace(ctx, cmd.OutOrStdout(), dag, current, updated)
	})
}

func runWorkspaceUpdate(cmd *cobra.Command, _ []string, noGenerate bool) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return updateWorkspaceLockfile(ctx, cmd.OutOrStdout(), engineClient.Dagger(), noGenerate)
	})
}

func updateWorkspaceLockfile(ctx context.Context, outWriter io.Writer, dag *dagger.Client, noGenerate bool) error {
	current := dag.CurrentWorkspace()
	updated := current.WithUpdatedLock(dagger.WorkspaceWithUpdatedLockOpts{NoGenerate: noGenerate})
	return updateMaterializedWorkspace(ctx, outWriter, dag, current, updated)
}

func updateMaterializedWorkspace(ctx context.Context, outWriter io.Writer, dag *dagger.Client, current, updated *dagger.Workspace) error {
	updated, err := materializeWorkspace(ctx, dag, updated)
	if err != nil {
		return err
	}
	isEmpty, err := updated.Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	if err != nil {
		return err
	}
	if !isEmpty {
		if err := updated.Export(ctx); err != nil {
			return err
		}
	}
	return writeWorkspaceUpdateResult(outWriter, isEmpty)
}

func writeWorkspaceUpdateResult(outWriter io.Writer, isEmpty bool) error {
	if isEmpty {
		_, err := outWriter.Write([]byte("Workspace already up to date\n"))
		return err
	}

	_, err := outWriter.Write([]byte("Updated workspace\n"))
	return err
}
