package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a deployment after taking it offline (WARNING: may destroy infrastructure)",
	Args:  cobra.NoArgs,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		// lg := logger.New()
		// ctx := lg.WithContext(cmd.Context())

		panic("not implemented")
	},
}

func init() {
	if err := viper.BindPFlags(deleteCmd.Flags()); err != nil {
		panic(err)
	}
}
