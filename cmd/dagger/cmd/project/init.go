package project

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
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

		// TODO @gerhard suggested maybe eventually having a
		// `dagger project template [list, new]`
		t := viper.GetString("template")
		if len(t) > 0 {
			err := createTemplate(t)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to initialize template")
			}
		}

		name := viper.GetString("name")
		doneCh := common.TrackCommand(ctx, cmd)
		err := pkg.CueModInit(ctx, dir, name)
		<-doneCh
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to initialize project")
		}
	},
}

func init() {
	initCmd.Flags().StringP("name", "n", "", "project name")
	t, err := getTemplateNames()
	if err != nil {
		panic(err)
	}
	initCmd.Flags().StringP("template", "t", "", fmt.Sprintf("Template name %s", t))
	if err := viper.BindPFlags(initCmd.Flags()); err != nil {
		panic(err)
	}
}
