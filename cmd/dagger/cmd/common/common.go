package common

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/client"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/solver"
	"go.dagger.io/dagger/state"
)

func CurrentWorkspace(ctx context.Context) *state.Workspace {
	lg := log.Ctx(ctx)

	if workspacePath := viper.GetString("workspace"); workspacePath != "" {
		workspace, err := state.Open(ctx, workspacePath)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Str("path", workspacePath).
				Msg("failed to open workspace")
		}
		return workspace
	}

	workspace, err := state.Current(ctx)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to determine current workspace")
	}
	return workspace
}

func CurrentEnvironmentState(ctx context.Context, workspace *state.Workspace) *state.State {
	lg := log.Ctx(ctx)

	environmentName := viper.GetString("environment")
	if environmentName != "" {
		st, err := workspace.Get(ctx, environmentName)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to load environment")
		}
		return st
	}

	environments, err := workspace.List(ctx)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to list environments")
	}

	if len(environments) == 0 {
		lg.
			Fatal().
			Msg("no environments")
	}

	if len(environments) > 1 {
		envNames := []string{}
		for _, e := range environments {
			envNames = append(envNames, e.Name)
		}
		lg.
			Fatal().
			Err(err).
			Strs("environments", envNames).
			Msg("multiple environments available in the workspace, select one with `--environment`")
	}

	return environments[0]
}

// Re-compute an environment (equivalent to `dagger up`).
func EnvironmentUp(ctx context.Context, state *state.State, noCache bool) *environment.Environment {
	lg := log.Ctx(ctx)

	c, err := client.New(ctx, "", noCache)
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}
	result, err := c.Do(ctx, state, func(ctx context.Context, environment *environment.Environment, s solver.Solver) error {
		log.Ctx(ctx).Debug().Msg("bringing environment up")
		return environment.Up(ctx, s)
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to up environment")
	}
	return result
}
