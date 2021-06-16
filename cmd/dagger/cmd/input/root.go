package input

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/state"
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

func updateEnvironmentInput(ctx context.Context, target string, input state.Input) {
	lg := log.Ctx(ctx)

	workspace := common.CurrentWorkspace(ctx)
	st := common.CurrentEnvironmentState(ctx, workspace)
	st.SetInput(target, input)

	if err := workspace.Save(ctx, st); err != nil {
		lg.Fatal().Err(err).Str("environment", st.Name).Msg("cannot update environment")
	}
}
