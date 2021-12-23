package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/cmd/output"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/mod"
	"go.dagger.io/dagger/plan"
	"go.dagger.io/dagger/plancontext"
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

		if viper.GetBool("europa") {
			err = europaUp(ctx, cl, args...)

			// TODO: rework telemetry
			// <-doneCh

			if err != nil {
				lg.Fatal().Err(err).Msg("failed to up environment")
			}

			return
		}

		project := common.CurrentProject(ctx)
		st := common.CurrentEnvironmentState(ctx, project)

		lg = lg.With().
			Str("environment", st.Name).
			Logger()

		universeUpdateCh := make(chan bool)
		go func() {
			universeUpdateCh <- checkUniverseVersion(ctx, project.Path)
		}()

		doneCh := common.TrackProjectCommand(ctx, cmd, project, st)

		env, err := environment.New(st)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create environment")
		}

		err = cl.Do(ctx, env.Context(), func(ctx context.Context, s solver.Solver) error {
			// check that all inputs are set
			if err := checkInputs(ctx, env); err != nil {
				return err
			}

			if err := env.Up(ctx, s); err != nil {
				return err
			}

			st.Computed = env.Computed().JSON().PrettyString()
			if err := project.Save(ctx, st); err != nil {
				return err
			}

			// FIXME: `ListOutput` is printing to Stdout directly which messes
			// up the TTY logger.
			if tty != nil {
				tty.Stop()
			}
			return output.ListOutputs(ctx, env, term.IsTerminal(int(os.Stdout.Fd())))
		})

		<-doneCh

		if err != nil {
			lg.Fatal().Err(err).Msg("failed to up environment")
		}

		// Warn universe version if out of date
		if update := <-universeUpdateCh; update {
			fmt.Println("A new version of universe is available, please run 'dagger mod get alpha.dagger.io'")
		}
	},
}

func checkUniverseVersion(ctx context.Context, projectPath string) bool {
	lg := log.Ctx(ctx)

	isLatest, err := mod.IsUniverseLatest(ctx, projectPath)
	if err != nil {
		lg.Debug().Err(err).Msg("failed to check universe version")
		return false
	}
	if !isLatest {
		return true
	}
	lg.Debug().Msg("universe is up to date")
	return false
}

func europaUp(ctx context.Context, cl *client.Client, args ...string) error {
	lg := log.Ctx(ctx)

	p, err := plan.Load(ctx, plan.Config{
		Args:   args,
		With:   viper.GetStringSlice("with"),
		Target: viper.GetString("target"),
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

func checkInputs(ctx context.Context, env *environment.Environment) error {
	lg := log.Ctx(ctx)
	warnOnly := viper.GetBool("force")

	notConcreteInputs := []*compiler.Value{}
	inputs, err := env.ScanInputs(ctx, true)
	if err != nil {
		lg.Error().Err(err).Msg("failed to scan inputs")
		return err
	}

	for _, i := range inputs {
		isConcrete := (i.IsConcreteR(cue.Optional(true)) == nil)
		switch {
		case plancontext.IsSecretValue(i):
			if _, err := env.Context().Secrets.FromValue(i); err != nil {
				isConcrete = false
			}
		case plancontext.IsFSValue(i):
			if _, err := env.Context().FS.FromValue(i); err != nil {
				isConcrete = false
			}
		case plancontext.IsServiceValue(i):
			if _, err := env.Context().Services.FromValue(i); err != nil {
				isConcrete = false
			}
		}

		if !isConcrete {
			notConcreteInputs = append(notConcreteInputs, i)
		}
	}

	for _, i := range notConcreteInputs {
		if warnOnly {
			lg.Warn().Str("input", i.Path().String()).Msg("required input is missing")
		} else {
			lg.Error().Str("input", i.Path().String()).Msg("required input is missing")
		}
	}

	if !warnOnly && len(notConcreteInputs) > 0 {
		return errors.New("some required inputs are not set, please re-run with `--force` if you think it's a mistake")
	}

	return nil
}

func init() {
	upCmd.Flags().BoolP("force", "f", false, "Force up, disable inputs check")
	upCmd.Flags().String("output", "", "Write computed output. Prints on stdout if set to-")
	upCmd.Flags().StringArrayP("with", "w", []string{}, "")
	upCmd.Flags().StringP("target", "t", "", "Run a single target of the DAG (for debugging only)")

	if err := viper.BindPFlags(upCmd.Flags()); err != nil {
		panic(err)
	}
}
