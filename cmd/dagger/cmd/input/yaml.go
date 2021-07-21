package input

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var yamlCmd = &cobra.Command{
	Use:   "yaml <TARGET> [-f] <VALUE|PATH>",
	Short: "Add a YAML input",
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
			state.YAMLInput(readInput(ctx, args[1])),
		)
	},
}

func init() {
	yamlCmd.Flags().BoolP("file", "f", false, "Read value from file")

	if err := viper.BindPFlags(yamlCmd.Flags()); err != nil {
		panic(err)
	}
}
