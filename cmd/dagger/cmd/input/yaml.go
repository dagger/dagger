package input

import (
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var yamlCmd = &cobra.Command{
	Use:   "yaml TARGET VALUE",
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

		updateDeploymentInput(ctx, args[0], dagger.YAMLInput(args[1]))
	},
}

func init() {
	if err := viper.BindPFlags(yamlCmd.Flags()); err != nil {
		panic(err)
	}
}
