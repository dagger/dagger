package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Take an environment offline (WARNING: may destroy infrastructure)",
	Args:  cobra.NoArgs,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		panic("not implemented")
	},
	// Remove hidden flag once command has been implemented
	Hidden: true,
}

func init() {
	downCmd.Flags().Bool("no-cache", false, "Disable all run cache")

	if err := viper.BindPFlags(downCmd.Flags()); err != nil {
		panic(err)
	}
}
