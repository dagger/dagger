package common

import (
	"context"
	"os"

	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

func GetCurrentEnvironmentState(ctx context.Context, store *dagger.Store) *dagger.EnvironmentState {
	lg := log.Ctx(ctx)

	environmentName := viper.GetString("environment")
	if environmentName != "" {
		st, err := store.LookupEnvironmentByName(ctx, environmentName)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Str("environmentName", environmentName).
				Msg("failed to lookup environment by name")
		}
		return st
	}

	wd, err := os.Getwd()
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get current working directory")
	}
	st, err := store.LookupEnvironmentByPath(ctx, wd)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Str("environmentPath", wd).
			Msg("failed to lookup environment by path")
	}
	if len(st) == 0 {
		lg.
			Fatal().
			Err(err).
			Str("environmentPath", wd).
			Msg("no environments match the current directory")
	}
	if len(st) > 1 {
		environments := []string{}
		for _, s := range st {
			environments = append(environments, s.Name)
		}
		lg.
			Fatal().
			Err(err).
			Str("environmentPath", wd).
			Strs("environments", environments).
			Msg("multiple environments match the current directory, select one with `--environment`")
	}
	return st[0]
}

// Re-compute an environment (equivalent to `dagger up`).
func EnvironmentUp(ctx context.Context, state *dagger.EnvironmentState, noCache bool) *dagger.Environment {
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
