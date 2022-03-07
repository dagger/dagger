package project

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/pkg"
)

var sep = string(os.PathSeparator)

var initCmd = &cobra.Command{
	Use:   fmt.Sprintf("init [path%sto%sproject]", sep, sep),
	Short: "Initialize a new empty project",
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
		ctx := lg.WithContext(cmd.Context())

		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}

		name := viper.GetString("name")

		err := pkg.CueModInit(ctx, dir, name)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to initialize project")
		}

		// FIXME: Add telemtry for init
	},
}

func init() {
	initCmd.Flags().StringP("name", "n", "", "project name")
	if err := viper.BindPFlags(initCmd.Flags()); err != nil {
		panic(err)
	}
}
