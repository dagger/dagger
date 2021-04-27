package cmd

import (
	"context"
	"net/url"
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
	Short: "Create a new environment",
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

		if viper.GetString("environment") != "" {
			lg.
				Fatal().
				Msg("cannot use option -d,--environment for this command")
		}

		name := ""
		if len(args) > 0 {
			name = args[0]
		} else {
			name = getNewEnvironmentName(ctx)
		}

		st := &dagger.EnvironmentState{
			Name:       name,
			PlanSource: getPlanSource(ctx),
		}

		err = store.CreateEnvironment(ctx, st)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to create environment")
		}
		lg.
			Info().
			Str("environmentId", st.ID).
			Str("environmentName", st.Name).
			Msg("environment created")

		if viper.GetBool("up") {
			common.EnvironmentUp(ctx, st, false)
		}
	},
}

func getNewEnvironmentName(ctx context.Context) string {
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

func getPlanSource(ctx context.Context) dagger.Input {
	lg := log.Ctx(ctx)

	src := dagger.Input{}
	checkFirstSet := func() {
		if src.Type != dagger.InputTypeEmpty {
			lg.Fatal().Msg("only one of those options can be set: --plan-dir, --plan-git, --plan-package, --plan-file")
		}
	}

	planDir := viper.GetString("plan-dir")
	planGit := viper.GetString("plan-git")

	if planDir != "" {
		checkFirstSet()

		src = dagger.DirInput(planDir, []string{"*.cue", "cue.mod"})
	}

	if planGit != "" {
		checkFirstSet()

		u, err := url.Parse(planGit)
		if err != nil {
			lg.Fatal().Err(err).Str("url", planGit).Msg("cannot get current working directory")
		}
		ref := u.Fragment // eg. #main
		u.Fragment = ""
		remote := u.String()

		src = dagger.GitInput(remote, ref, "")
	}

	if src.Type == dagger.InputTypeEmpty {
		var err error
		wd, err := os.Getwd()
		if err != nil {
			lg.Fatal().Err(err).Msg("cannot get current working directory")
		}
		return dagger.DirInput(wd, []string{"*.cue", "cue.mod"})
	}

	return src
}

func init() {
	newCmd.Flags().BoolP("up", "u", false, "Bring the environment online")

	newCmd.Flags().String("plan-dir", "", "Load plan from a local directory")
	newCmd.Flags().String("plan-git", "", "Load plan from a git repository")
	newCmd.Flags().String("plan-package", "", "Load plan from a cue package")
	newCmd.Flags().String("plan-file", "", "Load plan from a cue or json file")

	newCmd.Flags().String("setup", "auto", "Specify whether to prompt user for initial setup (no|yes|auto)")

	if err := viper.BindPFlags(newCmd.Flags()); err != nil {
		panic(err)
	}
}
