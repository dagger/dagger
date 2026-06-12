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

type workspaceLockUpdateResponse struct {
	CurrentWorkspace struct {
		Update struct {
			IsEmpty bool
			Export  string
		}
	}
}

func init() {
	lockCmd.AddCommand(lockUpdateCmd)
	setWorkspaceFlagPolicy(lockCmd, workspaceFlagPolicyLocalOnly)
}

var lockCmd = &cobra.Command{
	Use:    "lock",
	Short:  "Manage workspace lockfiles",
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

var lockUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Refresh workspace lock entries",
	Long: `Refresh workspace lock entries.

	Refreshes entries already recorded in dagger.lock.`,
	Args: cobra.NoArgs,
	RunE: runWorkspaceUpdate,
}

func runWorkspaceUpdate(cmd *cobra.Command, moduleNames []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return updateWorkspaceLockfile(ctx, cmd.OutOrStdout(), engineClient.Dagger())
	})
}

func updateWorkspaceLockfile(ctx context.Context, outWriter io.Writer, dag *dagger.Client) error {
	var res workspaceLockUpdateResponse
	if err := dag.Do(ctx, &dagger.Request{Query: workspaceLockUpdateQuery}, &dagger.Response{Data: &res}); err != nil {
		return err
	}

	return writeWorkspaceLockUpdateResult(outWriter, res.CurrentWorkspace.Update.IsEmpty)
}

func writeWorkspaceLockUpdateResult(outWriter io.Writer, isEmpty bool) error {
	if isEmpty {
		_, err := outWriter.Write([]byte("Lockfile already up to date\n"))
		return err
	}

	_, err := outWriter.Write([]byte("Updated dagger.lock\n"))
	return err
}
