package cmd

import (
	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger/state"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring an environment online with latest plan and inputs",
	Args:  cobra.NoArgs,
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

		st := common.GetCurrentEnvironmentState(ctx)
		result := common.EnvironmentUp(ctx, st, viper.GetBool("no-cache"))

		st.Computed = result.Computed().JSON().PrettyString()
		if err := state.Save(ctx, st); err != nil {
			lg.Fatal().Err(err).Msg("failed to update environment")
		}
	},
}

func init() {
	upCmd.Flags().Bool("no-cache", false, "Disable all run cache")

	if err := viper.BindPFlags(upCmd.Flags()); err != nil {
		panic(err)
	}
}
