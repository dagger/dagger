package input

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var containerCmd = &cobra.Command{
	Use:   "container ID",
	Short: "Add a container image as input artifact",
	Args:  cobra.ExactArgs(1),
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
	if err := viper.BindPFlags(containerCmd.Flags()); err != nil {
		panic(err)
	}
}
