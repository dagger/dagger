package daggercmd

import (
	"context"
	"fmt"
	"io"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

func runModuleUpdate(cmd *cobra.Command, names []string) error {
	version, err := cmd.Flags().GetString("version")
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("version") && version == "" {
		return fmt.Errorf("--version must not be empty")
	}
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		modules, cwd, err := installedModulesForSelection(ctx, dag)
		if err != nil {
			return err
		}
		selections, err := workspace.SelectModuleUpdates(modules, ".", cwd, names, version)
		if err != nil {
			return err
		}
		for _, selection := range selections {
			if err := writeModuleSourceMatch(cmd.ErrOrStderr(), selection); err != nil {
				return err
			}
			if selection.Version != "" {
				_, previous, hasVersion, _ := workspace.SplitModuleVersion(selection.Entry.Source)
				if !hasVersion {
					previous = "(default)"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Updating %q: %s -> %s.\n", selection.Name, previous, selection.Version); err != nil {
					return err
				}
			}
		}
		var result struct {
			CurrentWorkspace struct {
				Result struct {
					ID dagger.ID
				}
			}
		}
		if err := dag.Do(ctx, &dagger.Request{
			Query: `query ModuleUpdate($names: [String!]!, $version: String) {
  currentWorkspace {
    result: withUpdatedModules(names: $names, version: $version) { id }
  }
}`,
			Variables: map[string]any{"names": names, "version": version},
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
