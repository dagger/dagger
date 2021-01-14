package cmd

import (
	"fmt"

	"dagger.cloud/go/cmd/dagger/logger"
	"dagger.cloud/go/dagger"

	"github.com/moby/buildkit/util/appcontext"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var computeCmd = &cobra.Command{
	Use:   "compute CONFIG",
	Short: "Compute a configuration",
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
		ctx := lg.WithContext(appcontext.Context())

		c, err := dagger.NewClient(ctx, dagger.ClientConfig{
			Input:   viper.GetString("input"),
			BootDir: args[0],
		})
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}
		// FIXME: configure which config to compute (duh)
		// FIXME: configure inputs
		lg.Info().Msg("running")
		output, err := c.Compute(ctx)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to compute")
		}
		lg.Info().Msg("processing output")
		fmt.Println(output.JSON())
	},
}

func init() {
	computeCmd.Flags().String("input", "", "Input overlay")

	if err := viper.BindPFlags(computeCmd.Flags()); err != nil {
		panic(err)
	}
}
