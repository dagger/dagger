package daggercmd

import (
	"context"
	"io"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

func runWorkspaceUpdate(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		return updateWorkspaceLockfile(ctx, cmd.OutOrStdout(), engineClient.Dagger())
	})
}

func updateWorkspaceLockfile(ctx context.Context, outWriter io.Writer, dag *dagger.Client) error {
	current := dag.CurrentWorkspace()
	updated, err := materializeWorkspace(ctx, dag, current.WithUpdatedLock())
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
	return writeWorkspaceLockUpdateResult(outWriter, isEmpty)
}

func writeWorkspaceLockUpdateResult(outWriter io.Writer, isEmpty bool) error {
	if isEmpty {
		_, err := outWriter.Write([]byte("Lockfile already up to date\n"))
		return err
	}

	_, err := outWriter.Write([]byte("Updated dagger.lock\n"))
	return err
}
