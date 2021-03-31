package plan

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var packageCmd = &cobra.Command{
	Use:   "package PKG",
	Short: "Load plan from a cue package",
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
	if err := viper.BindPFlags(packageCmd.Flags()); err != nil {
		panic(err)
	}
}
