package daggercmd

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var apiClientInitNoGenerate bool

func runAPIClientInitWithSDK(cmd *cobra.Command, sdkName, clientPath, moduleRef string) error {
	if workspaceEnv != "" {
		return fmt.Errorf("client init does not support --env; it scaffolds clients into the base workspace config")
	}
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, func(ctx context.Context, ec *client.Client) error {
		dag := ec.Dagger()
		sdkArgs, err := sdkInitArgsJSON(cmd)
		if err != nil {
			return err
		}
		opts := dagger.WorkspaceWithInitClientOpts{
			NoGenerate: apiClientInitNoGenerate,
		}
		if sdkArgs != "" {
			opts.Args = dagger.JSON(sdkArgs)
		}
		current := dag.CurrentWorkspace()
		// Root-measured for the same reason as module init: the workspace
		// config this edits can sit above the caller, and the apply happens
		// at the workspace root.
		updated := current.WithInitClient(clientPath, sdkName, moduleRef, opts).WithWorkdir(".")
		_, err = handleWorkspaceResponse(ctx, dag, current, updated, autoApply)
		return err
	})
}
