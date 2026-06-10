package main

import "github.com/spf13/cobra"

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Interact with the Dagger API (advanced)",
	Long: `Every Dagger command — check, up, generate, even install — ultimately
runs against a GraphQL API served by the Dagger engine, combining
Dagger's core types with schema extensions loaded from modules. The
"api" group surfaces direct access for scripting and advanced
automation. Most users will never type these commands.

See https://docs.dagger.io/api for the full overview.`,
}

func init() {
	apiCmd.AddCommand(apiQueryCmd, apiListenCmd, apiSessionCmd, apiCallCmd.Command(), apiFunctionsCmd)
}
