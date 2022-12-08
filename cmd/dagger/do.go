package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func doCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "do",
		Args:   cobra.NoArgs,
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf(`############################################################################################
dagger do has been refactored into a new dagger-cue CLI.                                   #
Please find the latest documentation in the following link: https://docs.dagger.io/sdk/cue #
############################################################################################
		`)
			os.Exit(1)
		},
	}
}

// version holds the complete version number. Filled in at linking time.
