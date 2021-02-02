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
	cc         = &dagger.Compiler{}
	inputFlags = &UserConfig{
		Name:        "input",
		Description: "input value",
		Dir:         true,
		Git:         true,
		String:      true,
		Cue:         true,
	}
	sourceFlags = &UserConfig{
		Name:        "source",
		Description: "source config",
		Dir:         true,
		DirInclude:  []string{"*.cue", "cue.mod"},
		Git:         true,
	}
)

var computeCmd = &cobra.Command{
	Use:   "compute CONFIG",
	Short: "Compute a configuration",
	Args:  cobra.ExactArgs(0),
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

		env, err := dagger.NewEnv(cc)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable initialize env")
		}
		if err := env.SetInput(inputFlags.Value()); err != nil {
			lg.Fatal().Err(err).Msg("invalid input")
		}
		// FIXME: harmonize sourceFlags with Env.Updater
		//   (one is a component, the other is a script. Keep component everuwhere)
		if err := env.SetUpdater(sourceFlags.Value().Get("#dagger.compute")); err != nil {
			lg.Fatal().Err(err).Msg("invalid source")
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
	// --input-* : user-specified env input
	inputFlags.Install(computeCmd.Flags())
	// --source-*: user-specified env source
	sourceFlags.Install(computeCmd.Flags())

	if err := viper.BindPFlags(computeCmd.Flags()); err != nil {
		panic(err)
	}
}
