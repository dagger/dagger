package main

import "github.com/spf13/cobra"

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Interact with the Dagger API",
}

func init() {
	apiCmd.AddCommand(apiQueryCmd, apiListenCmd, apiSessionCmd)
}
