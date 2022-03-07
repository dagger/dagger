package project

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var Cmd = &cobra.Command{
	Use:   "project",
	Short: "Manage a Dagger project",
	// Args:  cobra.NoArgs,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
}

func init() {
	if err := viper.BindPFlags(Cmd.Flags()); err != nil {
		panic(err)
	}

	Cmd.AddCommand(
		initCmd,
		updateCmd,
	)
}
