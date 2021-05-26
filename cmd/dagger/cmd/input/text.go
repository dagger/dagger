package input

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var textCmd = &cobra.Command{
	Use:   "text <TARGET> [-f] <VALUE|PATH>",
	Short: "Add a text input",
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
			args[0],
			state.TextInput(readInput(ctx, args[1])),
		)
	},
}

func init() {
	textCmd.Flags().BoolP("file", "f", false, "Read value from file")

	if err := viper.BindPFlags(textCmd.Flags()); err != nil {
		panic(err)
	}
}
