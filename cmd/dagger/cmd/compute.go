package cmd

import (
	"fmt"

	"dagger.cloud/go/cmd/dagger/logger"
	"dagger.cloud/go/dagger"

	"github.com/moby/buildkit/util/appcontext"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	input   *dagger.InputValue
	updater *dagger.InputValue
)

var computeCmd = &cobra.Command{
	Use:   "compute CONFIG",
	Short: "Compute a configuration",
	Args:  cobra.ExactArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		ctx := lg.WithContext(appcontext.Context())

		env, err := dagger.NewEnv()
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to initialize environment")
		}
		if err := updater.SourceFlag().Set(args[0]); err != nil {
			lg.Fatal().Err(err).Msg("invalid local source")
		}

		if err := env.SetUpdater(updater.Value()); err != nil {
			lg.Fatal().Err(err).Msg("invalid updater script")
		}
		lg.Debug().Str("input", input.Value().SourceUnsafe()).Msg("Setting input")
		if err := env.SetInput(input.Value()); err != nil {
			lg.Fatal().Err(err).Msg("invalid input")
		}
		lg.Debug().Str("env state", env.State().SourceUnsafe()).Msg("creating client")
		c, err := dagger.NewClient(ctx, "")
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}
		lg.Info().Msg("running")
		output, err := c.Compute(ctx, env)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to compute")
		}
		lg.Info().Msg("processing output")
		fmt.Println(output.JSON())
	},
}

func init() {
	var err error
	// Setup --input-* flags
	input, err = dagger.NewInputValue("{}")
	if err != nil {
		panic(err)
	}
	computeCmd.Flags().Var(input.StringFlag(), "input-string", "TARGET=STRING")
	computeCmd.Flags().Var(input.DirFlag(), "input-dir", "TARGET=PATH")
	computeCmd.Flags().Var(input.GitFlag(), "input-git", "TARGET=REMOTE#REF")
	computeCmd.Flags().Var(input.CueFlag(), "input-cue", "CUE")

	// Setup (future) --from-* flags
	updater, err = dagger.NewInputValue("[...{do:string, ...}]")
	if err != nil {
		panic(err)
	}

	if err := viper.BindPFlags(computeCmd.Flags()); err != nil {
		panic(err)
	}
}
