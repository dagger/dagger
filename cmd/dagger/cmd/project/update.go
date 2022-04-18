package project

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/mod"
	"go.dagger.io/dagger/pkg"
)

var updateCmd = &cobra.Command{
	Use:   "update [package]",
	Short: "Download and install dependencies",
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

		var err error

		cueModPath, cueModExists := pkg.GetCueModParent()
		if !cueModExists {
			lg.Fatal().Msg("dagger project not found. Run `dagger project init`")
		}

		if len(args) == 0 {
			lg.Debug().Msg("No package specified, updating all packages")
			if err := pkg.Vendor(ctx, cueModPath); err != nil {
				lg.Log().Err(err).Msg("failed to open vendor file")
			}
			fmt.Println("Project updated")
			return
		}

		var update = viper.GetBool("update")

		doneCh := common.TrackCommand(ctx, cmd)
		var processedRequires []*mod.Require

		if update && len(args) == 0 {
			lg.Info().Msg("updating all installed packages...")
			processedRequires, err = mod.UpdateInstalled(ctx, cueModPath)
		} else if update && len(args) > 0 {
			lg.Info().Msg("updating specified packages...")
			processedRequires, err = mod.UpdateAll(ctx, cueModPath, args)
		} else if !update && len(args) > 0 {
			lg.Info().Msg("installing specified packages...")
			processedRequires, err = mod.InstallAll(ctx, cueModPath, args)
		} else {
			lg.Fatal().Msg("unrecognized update/install operation")
		}

		if len(processedRequires) > 0 {
			for _, r := range processedRequires {
				lg.Info().Msgf("installed/updated package %s", r)
			}
		}

		<-doneCh

		if err != nil {
			lg.Error().Err(err).Msg("error installing/updating packages")
		}

	},
}

func init() {
	updateCmd.Flags().String("private-key-file", "", "Private ssh key")
	updateCmd.Flags().String("private-key-password", "", "Private ssh key password")
	updateCmd.Flags().BoolP("update", "u", false, "Update specified package")

	if err := viper.BindPFlags(updateCmd.Flags()); err != nil {
		panic(err)
	}
}
