package common

import (
	"context"
	"errors"
	"os"

	"dagger.io/go/dagger"
	"dagger.io/go/dagger/state"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func GetCurrentEnvironmentState(ctx context.Context) *state.State {
	lg := log.Ctx(ctx)

	// 1) If no environment name has been given, look for the current environment
	environment := viper.GetString("environment")
	if environment == "" {
		st, err := state.Current(ctx)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to load environment")
		}
		return st
	}

	// 2) Check if it's an environment path (can be opened directly)
	st, err := state.Open(ctx, environment)
	if err == nil {
		return st
	}
	if !errors.Is(err, os.ErrNotExist) {
		lg.
			Fatal().
			Err(err).
			Str("environmentPath", environment).
			Msg("failed to load environment")
	}

	// At this point, it must be an environment name
	workspace := viper.GetString("workspace")
	if workspace == "" {
		workspace, err = state.CurrentWorkspace(ctx)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to determine current workspace")
		}
	}

	environments, err := state.List(ctx, workspace)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to list environments")
	}
	for _, e := range environments {
		if e.Name == environment {
			return e
		}
	}

	lg.
		Fatal().
		Str("environment", environment).
		Msg("environment not found")

	return nil
}

// Re-compute an environment (equivalent to `dagger up`).
func EnvironmentUp(ctx context.Context, state *state.State, noCache bool) *dagger.Environment {
	lg := log.Ctx(ctx)

	c, err := dagger.NewClient(ctx, "", noCache)
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}
	result, err := c.Do(ctx, state, func(ctx context.Context, environment *dagger.Environment, s dagger.Solver) error {
		log.Ctx(ctx).Debug().Msg("bringing environment up")
		return environment.Up(ctx, s)
	})
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to up environment")
	}
	return result
}
