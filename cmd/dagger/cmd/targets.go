package cmd

import (
	"context"
	"fmt"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var targetsCmd = &cobra.Command{
	Use:   "targets CONFIG",
	Short: "List targets of a configuration",
	Args:  cobra.ExactArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.
			New().
			Level(zerolog.InfoLevel) // force to level to INFO
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

		c, err := dagger.NewClient(ctx, "")
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create client")
		}
		_, err = c.Session(ctx, env, func(ctx context.Context, s dagger.Solver) (*bkgw.Result, error) {
			if err := env.Update(ctx, s, true); err != nil {
				return nil, err
			}

			targets := env.Targets()
			for _, t := range targets {
				fmt.Println(t)
			}
			return s.Scratch().Result(ctx)
		})
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to compute")
		}
	},
}

func init() {
	if err := viper.BindPFlags(targetsCmd.Flags()); err != nil {
		panic(err)
	}
}
