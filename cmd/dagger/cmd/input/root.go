package input

import (
	"context"
	"io"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
	"go.dagger.io/dagger/telemetry"
)

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "input",
	Short: "Manage an environment's inputs",
}

func init() {
	Cmd.AddCommand(
		dirCmd,
		gitCmd,
		secretCmd,
		textCmd,
		jsonCmd,
		yamlCmd,
		listCmd,
		boolCmd,
		socketCmd,
		unsetCmd,
	)
}

func updateEnvironmentInput(ctx context.Context, cmd *cobra.Command, target string, input state.Input) {
	lg := *log.Ctx(ctx)

	project := common.CurrentProject(ctx)
	st := common.CurrentEnvironmentState(ctx, project)

	lg = lg.With().
		Str("environment", st.Name).
		Logger()

	doneCh := common.TrackProjectCommand(ctx, cmd, project, st, &telemetry.Property{
		Name:  "input_target",
		Value: target,
	})

	st.SetInput(target, input)

	env, err := environment.New(st)
	if err != nil {
		lg.Fatal().Msg("unable to create environment")
	}

	cl := common.NewClient(ctx)
	err = cl.Do(ctx, env.Context(), env.Context().Directories.Paths(), func(ctx context.Context, s solver.Solver) error {
		// the inputs are set, check for cue errors by scanning all the inputs
		_, err := env.ScanInputs(ctx, true)
		if err != nil {
			return err
		}
		return nil
	})

	<-doneCh

	if err != nil {
		lg.Fatal().Err(err).Msg("invalid input")
	}

	if err := project.Save(ctx, st); err != nil {
		lg.Fatal().Err(err).Msg("cannot update environment")
	}
}

func readInput(ctx context.Context, source string) string {
	lg := log.Ctx(ctx)

	if !viper.GetBool("file") {
		return source
	}

	if source == "-" {
		// stdin source
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to read input from stdin")
		}
		return string(data)
	}

	// file source
	data, err := os.ReadFile(source)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Str("path", source).
			Msg("failed to read input from file")
	}

	return string(data)
}
