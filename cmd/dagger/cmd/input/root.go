package input

import (
	"context"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "input",
	Short: "Manage a route's inputs",
}

func init() {
	Cmd.AddCommand(
		dirCmd,
		gitCmd,
		containerCmd,
		secretCmd,
		textCmd,
	)
}

func updateRouteInput(ctx context.Context, target string, input dagger.Input) {
	lg := log.Ctx(ctx)

	store, err := dagger.DefaultStore()
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load store")
	}

	st := common.GetCurrentRouteState(ctx, store)
	st.AddInput(target, input)

	if err := store.UpdateRoute(ctx, st, nil); err != nil {
		lg.Fatal().Err(err).Str("routeId", st.ID).Str("routeName", st.Name).Msg("cannot update route")
	}
	lg.Info().Str("routeId", st.ID).Str("routeName", st.Name).Msg("updated route")
}
