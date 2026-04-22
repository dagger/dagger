package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:     "env",
	Short:   "Manage workspace environments",
	GroupID: workspaceGroup.ID,
}

var envListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace environments",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			names, err := workspaceEnvList(ctx, engineClient.Dagger())
			if err != nil {
				return err
			}
			for _, name := range names {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), name); err != nil {
					return err
				}
			}
			return nil
		})
	},
}

var envCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a workspace environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return workspaceEnvCreate(ctx, engineClient.Dagger(), args[0])
		})
	},
}

var envRmCmd = &cobra.Command{
	Use:   "rm NAME",
	Short: "Remove a workspace environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return workspaceEnvRemove(ctx, engineClient.Dagger(), args[0])
		})
	},
}

func init() {
	envCmd.AddCommand(envListCmd, envCreateCmd, envRmCmd)

	setWorkspaceFlagPolicy(envCreateCmd, workspaceFlagPolicyLocalOnly)
	setWorkspaceFlagPolicy(envRmCmd, workspaceFlagPolicyLocalOnly)
}

func workspaceEnvList(ctx context.Context, dag *dagger.Client) ([]string, error) {
	var res struct {
		CurrentWorkspace struct {
			EnvList []string
		}
	}

	err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { envList } }`,
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, err
	}
	return res.CurrentWorkspace.EnvList, nil
}

func workspaceEnvCreate(ctx context.Context, dag *dagger.Client, name string) error {
	var res struct {
		CurrentWorkspace struct {
			EnvCreate string
		}
	}

	return dag.Do(ctx, &dagger.Request{
		Query: `query($name: String!) { currentWorkspace { envCreate(name: $name) } }`,
		Variables: map[string]any{
			"name": name,
		},
	}, &dagger.Response{
		Data: &res,
	})
}

func workspaceEnvRemove(ctx context.Context, dag *dagger.Client, name string) error {
	var res struct {
		CurrentWorkspace struct {
			EnvRemove string
		}
	}

	return dag.Do(ctx, &dagger.Request{
		Query: `query($name: String!) { currentWorkspace { envRemove(name: $name) } }`,
		Variables: map[string]any{
			"name": name,
		},
	}, &dagger.Response{
		Data: &res,
	})
}
