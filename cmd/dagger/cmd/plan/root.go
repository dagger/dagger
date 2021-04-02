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
	Short: "Manage a deployment's plan",
}

func init() {
	Cmd.AddCommand(
		packageCmd,
		dirCmd,
		gitCmd,
		fileCmd,
	)
}

func updateDeploymentPlan(ctx context.Context, planSource dagger.Input) {
	lg := log.Ctx(ctx)

	store, err := dagger.DefaultStore()
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to load store")
	}

	st := common.GetCurrentDeploymentState(ctx, store)
	st.PlanSource = planSource

	if err := store.UpdateDeployment(ctx, st, nil); err != nil {
		lg.Fatal().Err(err).Str("deploymentId", st.ID).Str("deploymentName", st.Name).Msg("cannot update deployment")
	}
	lg.Info().Str("deploymentId", st.ID).Str("deploymentName", st.Name).Msg("updated deployment")
}
