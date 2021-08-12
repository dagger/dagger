package mod

import (
	"github.com/hashicorp/go-version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
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

		workspace := common.CurrentWorkspace(ctx)
		st := common.CurrentEnvironmentState(ctx, workspace)
		doneCh := common.TrackWorkspaceCommand(ctx, cmd, workspace, st, &telemetry.Property{
			Name:  "packages",
			Value: args,
		})

		// read mod file in the current dir
		modFile, err := readPath(workspace.Path)
		if err != nil {
			lg.Fatal().Err(err).Msg("error loading module file")
		}

		// parse packages to install
		var packages []*require
		var upgrade bool

		if len(args) == 0 {
			lg.Info().Msg("upgrading installed packages...")
			packages = modFile.require
			upgrade = true
		} else {
			for _, arg := range args {
				p, err := parseArgument(arg)
				if err != nil {
					lg.Error().Err(err).Msgf("error parsing package %s", arg)
					continue
				}
				packages = append(packages, p)
			}
		}

		// download packages
		for _, p := range packages {
			isNew, err := modFile.processRequire(p, upgrade)
			if err != nil {
				lg.Error().Err(err).Msgf("error processing package %s", p.repo)
			}

			if isNew {
				lg.Info().Msgf("downloading %s:%v", p.repo, p.version)
			}
		}

		// write to mod file in the current dir
		if err = modFile.write(); err != nil {
			lg.Error().Err(err).Msg("error writing to mod file")
		}

		<-doneCh
	},
}

func compareVersions(reqV1, reqV2 string) (int, error) {
	v1, err := version.NewVersion(reqV1)
	if err != nil {
		return 0, err
	}

	v2, err := version.NewVersion(reqV2)
	if err != nil {
		return 0, err
	}

	if v1.LessThan(v2) {
		return -1, nil
	}

	if v1.Equal(v2) {
		return 0, nil
	}

	return 1, nil
}

func init() {
	getCmd.Flags().String("private-key-file", "", "Private ssh key")
	getCmd.Flags().String("private-key-password", "", "Private ssh key password")

	if err := viper.BindPFlags(getCmd.Flags()); err != nil {
		panic(err)
	}
}
