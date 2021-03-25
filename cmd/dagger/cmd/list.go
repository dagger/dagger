package cmd

import (
	"fmt"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available routes",
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

		routes, err := dagger.ListRoutes(ctx)
		if err != nil {
			lg.Fatal().Err(err).Msg("cannot list routes")
		}

		for _, name := range routes {
			fmt.Println(name)
		}
	},
}

func init() {
	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
