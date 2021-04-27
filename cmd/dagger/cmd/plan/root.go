package plan

import (
	"context"

	"dagger.io/go/cmd/dagger/cmd/common"
	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Cmd exposes the top-level command
var Cmd = &cobra.Command{
	Use:   "plan",
	Short: "Manage an environment's plan",
}

func init() {
	Cmd.AddCommand(
		packageCmd,
		dirCmd,
		gitCmd,
		fileCmd,
	)
}

func updateEnvironmentPlan(ctx context.Context, planSource dagger.Input) {
	lg := log.Ctx(ctx)

	store, err := dagger.DefaultStore()
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load store")
	}

	st := common.GetCurrentEnvironmentState(ctx, store)
	st.PlanSource = planSource

	if err := store.UpdateEnvironment(ctx, st, nil); err != nil {
		lg.Fatal().Err(err).Str("environmentId", st.ID).Str("environmentName", st.Name).Msg("cannot update environment")
	}
	lg.Info().Str("environmentId", st.ID).Str("environmentName", st.Name).Msg("updated environment")
}
