package common

import (
	"context"
	"os"

	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func GetCurrentDeploymentState(ctx context.Context, store *dagger.Store) *dagger.DeploymentState {
	lg := log.Ctx(ctx)

	deploymentName := viper.GetString("deployment")
	if deploymentName != "" {
		st, err := store.LookupDeploymentByName(ctx, deploymentName)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Str("deploymentName", deploymentName).
				Msg("failed to lookup deployment by name")
		}
		return st
	}

	wd, err := os.Getwd()
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get current working directory")
	}
	st, err := store.LookupDeploymentByPath(ctx, wd)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Str("deploymentPath", wd).
			Msg("failed to lookup deployment by path")
	}
	if len(st) == 0 {
		lg.
			Fatal().
			Err(err).
			Str("deploymentPath", wd).
			Msg("no deployments match the current directory")
	}
	if len(st) > 1 {
		deployments := []string{}
		for _, s := range st {
			deployments = append(deployments, s.Name)
		}
		lg.
			Fatal().
			Err(err).
			Str("deploymentPath", wd).
			Strs("deployments", deployments).
			Msg("multiple deployments match the current directory, select one with `--deployment`")
	}
	return st[0]
}

// Re-compute a deployment (equivalent to `dagger up`).
func DeploymentUp(ctx context.Context, state *dagger.DeploymentState, noCache bool) *dagger.Deployment {
	lg := log.Ctx(ctx)

	c, err := dagger.NewClient(ctx, "", noCache)
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}
	result, err := c.Do(ctx, state, func(ctx context.Context, deployment *dagger.Deployment, s dagger.Solver) error {
		log.Ctx(ctx).Debug().Msg("bringing deployment up")
		return deployment.Up(ctx, s)
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to up deployment")
	}
	return result
}
