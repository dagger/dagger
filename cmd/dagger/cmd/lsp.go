package cmd

import (
	"github.com/dagger/cuelsp/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"

	// Required to use LSP logger
	_ "github.com/tliron/kutil/logging/simple"
)

var lspCmd = &cobra.Command{
	Use:    "cuelsp",
	Short:  "Run Dagger CUE Language Server",
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

		s, err := server.New(server.WithMode(server.ModeProd))
		if err != nil {
			lg.Fatal().Err(err).Msg("could not init Dagger Language Server")
		}

		lg.Info().Msg("Running Dagger CUE Language Server")
		if err := s.Run(); err != nil {
			lg.Fatal().Err(err).Msg("could not start Dagger Language Server")
		}
	},
}
