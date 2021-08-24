package input

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var boolCmd = &cobra.Command{
	Use:   "bool <TARGET> <true|false>",
	Short: "Add a boolean input",
	Args:  cobra.ExactArgs(2),
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

		updateEnvironmentInput(
			ctx,
			cmd,
			args[0],
			state.BoolInput(readInput(ctx, args[1])),
		)
	},
}

func init() {
	if err := viper.BindPFlags(boolCmd.Flags()); err != nil {
		panic(err)
	}
}
