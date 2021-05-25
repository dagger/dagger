package input

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
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

		workspace := common.CurrentWorkspace(ctx)
		st := common.CurrentEnvironmentState(ctx, workspace)
		st.RemoveInputs(args[0])

		if err := workspace.Save(ctx, st); err != nil {
			lg.Fatal().Err(err).Str("environment", st.Name).Msg("cannot update environment")
		}
		lg.Info().Str("environment", st.Name).Msg("updated environment")
	},
}
