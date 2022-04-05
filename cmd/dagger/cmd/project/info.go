package project

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/pkg"
)

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Lists project location on file system",
	Args:  cobra.MaximumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()

		cueModPath, cueModExists := pkg.GetCueModParent()
		if !cueModExists {
			lg.Fatal().Msg("dagger project not found. Run `dagger project init`")
		}

		fmt.Println(fmt.Sprintf("Current dagger project in: %s", cueModPath))

		// TODO: find available plans and if they load successfully
	},
}
