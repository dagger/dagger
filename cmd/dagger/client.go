package main

import "github.com/spf13/cobra"

func init() {
	clientCmd.AddCommand(clientAddCmd)
}

var clientCmd = &cobra.Command{
	Use:    "client",
	Short:  "Access Dagger client subcommands",
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}
