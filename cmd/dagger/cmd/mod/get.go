package mod

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/mod"
	"go.dagger.io/dagger/telemetry"
)

var getCmd = &cobra.Command{
	Use:   "get [packages]",
	Short: "download and install dependencies",
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

		project := common.CurrentProject(ctx)
		doneCh := common.TrackProjectCommand(ctx, cmd, project, nil, &telemetry.Property{
			Name:  "packages",
			Value: args,
		})

		var update = viper.GetBool("update")

		var processedRequires []*mod.Require
		var err error
		if update && len(args) == 0 {
			lg.Info().Msg("updating all installed packages...")
			processedRequires, err = mod.UpdateInstalled(ctx, project.Path)
		} else if update && len(args) > 0 {
			lg.Info().Msg("updating specified packages...")
			processedRequires, err = mod.UpdateAll(ctx, project.Path, args)
		} else if !update && len(args) > 0 {
			lg.Info().Msg("installing specified packages...")
			processedRequires, err = mod.InstallAll(ctx, project.Path, args)
		} else {
			lg.Fatal().Msg("unrecognized update/install operation")
		}

		if len(processedRequires) > 0 {
			for _, r := range processedRequires {
				lg.Info().Msgf("installed/updated package %s", r)
			}
		}

		if err != nil {
			lg.Error().Err(err).Msg("error installing/updating packages")
		}

		<-doneCh
	},
}

func init() {
	getCmd.Flags().String("private-key-file", "", "Private ssh key")
	getCmd.Flags().String("private-key-password", "", "Private ssh key password")
	getCmd.Flags().BoolP("update", "u", false, "Update specified package")

	if err := viper.BindPFlags(getCmd.Flags()); err != nil {
		panic(err)
	}
}
