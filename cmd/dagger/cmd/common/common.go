package common

import (
	"context"
	"fmt"
	"os"

	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

// GetCurrentDeployment returns the current selected deployment based on its abs path
func GetCurrentDeployment(ctx context.Context, store *dagger.Store) *dagger.Deployment {
	lg := log.Ctx(ctx)
	st := GetCurrentDeploymentState(ctx, store)

	deployment, err := dagger.NewDeployment(st)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Interface("deploymentState", st).
			Msg("failed to init deployment")
	}

	return deployment
}

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
	return st
}

// Re-compute a deployment (equivalent to `dagger up`).
// If printOutput is true, print the JSON-encoded computed state to standard output
func DeploymentUp(ctx context.Context, deployment *dagger.Deployment, printOutput bool) {
	lg := log.Ctx(ctx)

	c, err := dagger.NewClient(ctx, "")
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}
	output, err := c.Do(ctx, deployment, func(ctx context.Context, deployment *dagger.Deployment, s dagger.Solver) error {
		log.Ctx(ctx).Debug().Msg("bringing deployment up")
		return deployment.Up(ctx, s, nil)
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to up deployment")
	}
	if printOutput {
		fmt.Println(output.JSON())
	}
}
