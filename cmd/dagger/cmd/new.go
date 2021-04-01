package cmd

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var newCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new deployment",
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
		store, err := dagger.DefaultStore()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to load store")
		}

		if viper.GetString("deployment") != "" {
			lg.
				Fatal().
				Msg("cannot use option -d,--deployment for this command")
		}

		name := ""
		if len(args) > 0 {
			name = args[0]
		} else {
			name = getNewDeploymentName(ctx)
		}

		st := &dagger.DeploymentState{
			Name:       name,
			PlanSource: getPlanSource(ctx),
		}

		err = store.CreateDeployment(ctx, st)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to create deployment")
		}
		lg.
			Info().
			Str("deploymentId", st.ID).
			Str("deploymentName", st.Name).
			Msg("deployment created")

		deployment, err := dagger.NewDeployment(st)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to initialize deployment")
		}

		if viper.GetBool("up") {
			common.DeploymentUp(ctx, deployment)
		}
	},
}

func getNewDeploymentName(ctx context.Context) string {
	lg := log.Ctx(ctx)

	workDir, err := os.Getwd()
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to get current working dir")
	}

	currentDir := filepath.Base(workDir)
	if currentDir == "/" {
		return "root"
	}

	return currentDir
}

// FIXME: Implement options: --plan-*
func getPlanSource(ctx context.Context) dagger.Input {
	lg := log.Ctx(ctx)

	wd, err := os.Getwd()
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get current working directory")
	}

	return dagger.DirInput(wd, []string{"*.cue", "cue.mod"})
}

func init() {
	newCmd.Flags().BoolP("up", "u", false, "Bring the deployment online")

	newCmd.Flags().String("plan-dir", "", "Load plan from a local directory")
	newCmd.Flags().String("plan-git", "", "Load plan from a git repository")
	newCmd.Flags().String("plan-package", "", "Load plan from a cue package")
	newCmd.Flags().String("plan-file", "", "Load plan from a cue or json file")

	newCmd.Flags().String("setup", "auto", "Specify whether to prompt user for initial setup (no|yes|auto)")

	if err := viper.BindPFlags(newCmd.Flags()); err != nil {
		panic(err)
	}
}
