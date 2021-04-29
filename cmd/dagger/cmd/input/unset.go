package input

import (
	"dagger.io/go/cmd/dagger/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var unsetCmd = &cobra.Command{
	Use:   "unset [TARGET]",
	Short: "Remove input of an environment",
	Args:  cobra.ExactArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		ctx := lg.WithContext(cmd.Context())

		removeEnvironmentInput(ctx, args[0])
	},
}
