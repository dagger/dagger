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
		containerCmd,
		secretCmd,
		textCmd,
		jsonCmd,
		yamlCmd,
		listCmd,
		unsetCmd,
	)
}

func updateEnvironmentInput(ctx context.Context, cmd *cobra.Command, target string, input state.Input) {
	lg := *log.Ctx(ctx)

	workspace := common.CurrentWorkspace(ctx)
	st := common.CurrentEnvironmentState(ctx, workspace)

	lg = lg.With().
		Str("environment", st.Name).
		Logger()

	doneCh := common.TrackWorkspaceCommand(ctx, cmd, workspace, st, &telemetry.Property{
		Name:  "input_target",
		Value: target,
	})

	cl := common.NewClient(ctx)

	st.SetInput(target, input)

	err := cl.Do(ctx, st, func(ctx context.Context, env *environment.Environment, s solver.Solver) error {
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

	if err := workspace.Save(ctx, st); err != nil {
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
