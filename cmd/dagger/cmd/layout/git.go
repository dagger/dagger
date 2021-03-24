package layout

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var gitCmd = &cobra.Command{
	Use:   "git REMOTE REF [SUBDIR]",
	Short: "Load layout from a git package",
	Args:  cobra.MinimumNArgs(2),
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
	if err := viper.BindPFlags(gitCmd.Flags()); err != nil {
		panic(err)
	}
}
