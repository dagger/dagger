package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new empty workspace",
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

		dir := viper.GetString("workspace")
		if dir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				lg.
					Fatal().
					Err(err).
					Msg("failed to get current working dir")
			}
			dir = cwd
		}

		ws, err := state.Init(ctx, dir)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to initialize workspace")
		}

		lg.Info().Str("path", ws.DaggerDir()).Msg("initialized new empty workspace")
	},
}

func init() {
	if err := viper.BindPFlags(initCmd.Flags()); err != nil {
		panic(err)
	}
}
