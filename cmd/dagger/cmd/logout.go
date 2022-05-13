package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/api/auth"
	"go.dagger.io/dagger/cmd/dagger/logger"
)

var logoutCmd = &cobra.Command{
	Use:    "logout",
	Short:  "Log out of your dagger account",
	Hidden: true,
	Args:   cobra.NoArgs,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()

		if err := auth.Logout(); err != nil {
			lg.Fatal().Err(err).Msg("logout failed")
		}
		lg.Info().Msg("you are now logged out")
	},
}
