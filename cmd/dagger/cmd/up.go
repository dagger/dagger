package cmd

import (
	"context"
	"errors"
	"os"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/cmd/output"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
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
	Args:  cobra.NoArgs,
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

		project := common.CurrentProject(ctx)
		st := common.CurrentEnvironmentState(ctx, project)

		lg = lg.With().
			Str("environment", st.Name).
			Logger()

		doneCh := common.TrackProjectCommand(ctx, cmd, project, st)

		cl := common.NewClient(ctx)

		if viper.GetBool("europa") {
			err = europaUp(ctx, cl, project.Path)

			<-doneCh

			if err != nil {
				lg.Fatal().Err(err).Msg("failed to up environment")
			}

			return
		}

		env, err := environment.New(st)
		if err != nil {
			lg.Fatal().Err(err).Msg("unable to create environment")
		}

		err = cl.Do(ctx, env.Context(), env.Context().Directories.Paths(), func(ctx context.Context, s solver.Solver) error {
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
	},
}

func europaUp(ctx context.Context, cl *client.Client, path string) error {
	lg := log.Ctx(ctx)

	p, err := plan.Load(ctx, path, "")
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load plan")
	}

	localdirs, err := p.LocalDirectories()
	if err != nil {
		return err
	}
	return cl.Do(ctx, p.Context(), localdirs, func(ctx context.Context, s solver.Solver) error {
		if err := p.Up(ctx, s); err != nil {
			return err
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
		if i.IsConcreteR(cue.Optional(true)) != nil {
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

	if err := viper.BindPFlags(upCmd.Flags()); err != nil {
		panic(err)
	}
}
