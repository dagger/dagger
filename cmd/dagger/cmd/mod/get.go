package mod

import (
	"fmt"
	"os"
	"path"

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

		if len(args) == 0 {
			lg.Fatal().Msg("need to specify package name in command argument")
		}

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
		for _, arg := range args {
			p, err := parseArgument(arg)
			if err != nil {
				lg.Error().Err(err).Msgf("error parsing package %s", arg)
				continue
			}

			packages = append(packages, p)
		}

		// download packages
		for _, p := range packages {
			isNew, err := processRequire(workspace.Path, p, modFile)
			if err != nil {
				lg.Error().Err(err).Msg("error processing package")
			}

			if isNew {
				lg.Info().Msgf("downloading %s:%v", p.repo, p.version)
			}
		}

		// write to mod file in the current dir
		if err = modFile.write(workspace.Path); err != nil {
			lg.Error().Err(err).Msg("error writing to mod file")
		}

		lg.Info().Msg("checking for new versions...")

		<-doneCh
	},
}

func processRequire(workspacePath string, req *require, modFile *file) (bool, error) {
	var isNew bool

	tmpPath := path.Join(workspacePath, tmpBasePath, req.repo)
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return false, fmt.Errorf("error creating tmp dir for cloning package")
	}
	defer os.RemoveAll(tmpPath)

	// clone the repo
	privateKeyFile := viper.GetString("private-key-file")
	privateKeyPassword := viper.GetString("private-key-password")
	r, err := clone(req, tmpPath, privateKeyFile, privateKeyPassword)
	if err != nil {
		return isNew, fmt.Errorf("error downloading package %s: %w", req, err)
	}

	existing := modFile.search(req)
	destPath := path.Join(workspacePath, destBasePath)

	// requirement is new, so we should move the files and add it to the mod file
	if existing == nil {
		if err := move(req, tmpPath, destPath); err != nil {
			return isNew, err
		}
		modFile.require = append(modFile.require, req)
		isNew = true
		return isNew, nil
	}

	c, err := compareVersions(existing.version, req.version)
	if err != nil {
		return isNew, err
	}

	// the existing requirement is newer so we skip installation
	if c > 0 {
		return isNew, nil
	}

	// the new requirement is newer so we checkout the cloned repo to that tag, change the version in the existing
	// requirement and replace the code in the /pkg folder
	existing.version = req.version
	if err = r.checkout(req.version); err != nil {
		return isNew, err
	}
	if err = replace(req, tmpPath, destPath); err != nil {
		return isNew, err
	}
	isNew = true

	return isNew, nil
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

	return 1, nil
}

func init() {
	getCmd.Flags().String("private-key-file", "", "Private ssh key")
	getCmd.Flags().String("private-key-password", "", "Private ssh key password")

	if err := viper.BindPFlags(getCmd.Flags()); err != nil {
		panic(err)
	}
}
