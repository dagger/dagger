package cmd

import (
	"fmt"
	"os"

	"dagger.io/go/dagger"

	"github.com/spf13/cobra"
)

var hackCmd = &cobra.Command{
	Use:   "hack",
	Short: "internal dev cmd",
	Run: func(cmd *cobra.Command, args []string) {
		err := dagger.Hack(args)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

