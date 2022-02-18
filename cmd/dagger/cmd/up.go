package cmd

import (
	"context"
	"fmt"
	"os"

	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/plan"
	"go.dagger.io/dagger/solver"
	"golang.org/x/term"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring an environment online with latest plan and inputs",
	Args:  cobra.MaximumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		var (
			lg  = logger.New()
			tty *logger.TTYOutput
			err error
		)

		if f := viper.GetString("log-format"); f == "tty" || f == "auto" && term.IsTerminal(int(os.Stdout.Fd())) {
			tty, err = logger.NewTTYOutput(os.Stderr)
			if err != nil {
				lg.Fatal().Err(err).Msg("failed to initialize TTY logger")
			}
			tty.Start()
			defer tty.Stop()

			lg = lg.Output(tty)
		}

		ctx := lg.WithContext(cmd.Context())
		cl := common.NewClient(ctx)

		err = europaUp(ctx, cl, args...)

		// TODO: rework telemetry
		// <-doneCh

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to up environment")
		}
	},
}

// func checkUniverseVersion(ctx context.Context, projectPath string) bool {
// 	lg := log.Ctx(ctx)

// 	isLatest, err := mod.IsUniverseLatest(ctx, projectPath)
// 	if err != nil {
// 		lg.Debug().Err(err).Msg("failed to check universe version")
// 		return false
// 	}
// 	if !isLatest {
// 		return true
// 	}
// 	lg.Debug().Msg("universe is up to date")
// 	return false
// }

func europaUp(ctx context.Context, cl *client.Client, args ...string) error {
	lg := log.Ctx(ctx)

	p, err := plan.Load(ctx, plan.Config{
		Args:   args,
		With:   viper.GetStringSlice("with"),
		Target: viper.GetString("target"),
		Vendor: !viper.GetBool("no-vendor"),
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load plan")
	}

	return cl.Do(ctx, p.Context(), func(ctx context.Context, s solver.Solver) error {
		computed, err := p.Up(ctx, s)
		if err != nil {
			return err
		}

		if output := viper.GetString("output"); output != "" {
			data := computed.JSON().PrettyString()
			if output == "-" {
				fmt.Println(data)
				return nil
			}
			err := os.WriteFile(output, []byte(data), 0600)
			if err != nil {
				lg.Fatal().Err(err).Str("path", output).Msg("failed to write output")
			}
		}

		return nil
	})
}

func init() {
	upCmd.Flags().BoolP("force", "f", false, "Force up, disable inputs check")
	upCmd.Flags().StringArrayP("with", "w", []string{}, "")
	upCmd.Flags().StringP("target", "t", "", "Run a single target of the DAG (for debugging only)")
	upCmd.Flags().Bool("no-vendor", false, "Force up, disable inputs check")

	if err := viper.BindPFlags(upCmd.Flags()); err != nil {
		panic(err)
	}
}
