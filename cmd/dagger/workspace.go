package main

import (
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Short:   "Manage the current workspace",
	GroupID: moduleGroup.ID,
}

var workspaceInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show workspace information",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := pathutil.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		ws, err := workspace.DetectLocal(cwd)
		if err != nil {
			return fmt.Errorf("failed to detect workspace: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Root:   %s\n", ws.Root)
		configPath := filepath.Join(ws.Root, workspace.WorkspaceDirName, workspace.ConfigFileName)
		if ws.Config != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Config: %s\n", configPath)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Config: none\n")
		}
		return nil
	},
}

func init() {
	workspaceCmd.AddCommand(workspaceInfoCmd)
}
