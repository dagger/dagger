package main

import (
	"context"
	"io"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

const workspaceLockUpdateQuery = `query {
  currentWorkspace {
    update {
      isEmpty
      export(path: ".")
    }
  }
}
`

const workspaceLockRefreshModulesQuery = `query WorkspaceLockRefreshModules($moduleNames: [String!]!) {
  currentWorkspace {
    refreshModules(moduleNames: $moduleNames) {
      isEmpty
      export(path: ".")
    }
  }
}
`

type workspaceLockUpdateResponse struct {
	CurrentWorkspace struct {
		Update struct {
			IsEmpty bool
			Export  string
		}
	}
}

type workspaceLockRefreshModulesResponse struct {
	CurrentWorkspace struct {
		RefreshModules struct {
			IsEmpty bool
			Export  string
		}
	}
}

func init() {
	lockCmd.AddCommand(lockUpdateCmd)
}

var lockCmd = &cobra.Command{
	Use:     "lock",
	Short:   "Manage workspace lockfiles",
	GroupID: workspaceGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

var lockUpdateCmd = &cobra.Command{
	Use:   "update [module...]",
	Short: "Refresh workspace lock entries",
	Long: `Refresh workspace lock entries.

With no module names, refresh entries already recorded in .dagger/lock.

With module names, refresh only those modules from .dagger/config.toml.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkspaceUpdate(cmd, args)
	},
}

func runWorkspaceUpdate(cmd *cobra.Command, moduleNames []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return updateWorkspaceLockfile(ctx, cmd.OutOrStdout(), engineClient.Dagger(), moduleNames)
	})
}

func updateWorkspaceLockfile(ctx context.Context, outWriter io.Writer, dag *dagger.Client, moduleNames []string) error {
	if len(moduleNames) > 0 {
		return refreshWorkspaceLockModules(ctx, outWriter, dag, moduleNames)
	}

	var res workspaceLockUpdateResponse
	if err := dag.Do(ctx, &dagger.Request{Query: workspaceLockUpdateQuery}, &dagger.Response{Data: &res}); err != nil {
		return err
	}

	return writeWorkspaceLockUpdateResult(outWriter, res.CurrentWorkspace.Update.IsEmpty)
}

func refreshWorkspaceLockModules(ctx context.Context, outWriter io.Writer, dag *dagger.Client, moduleNames []string) error {
	var res workspaceLockRefreshModulesResponse
	if err := dag.Do(ctx, &dagger.Request{
		Query: workspaceLockRefreshModulesQuery,
		Variables: map[string]any{
			"moduleNames": moduleNames,
		},
	}, &dagger.Response{Data: &res}); err != nil {
		return err
	}

	return writeWorkspaceLockUpdateResult(outWriter, res.CurrentWorkspace.RefreshModules.IsEmpty)
}

func writeWorkspaceLockUpdateResult(outWriter io.Writer, isEmpty bool) error {
	if isEmpty {
		_, err := outWriter.Write([]byte("Lockfile already up to date\n"))
		return err
	}

	_, err := outWriter.Write([]byte("Updated .dagger/lock\n"))
	return err
}
