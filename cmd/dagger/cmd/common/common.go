package common

import (
	"context"
	"fmt"
	"os"

	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

// getCurrentRoute returns the current selected route based on its abs path
func GetCurrentRoute(ctx context.Context, store *dagger.Store) *dagger.Route {
	lg := log.Ctx(ctx)
	st := GetCurrentRouteState(ctx, store)

	route, err := dagger.NewRoute(st)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Interface("routeState", st).
			Msg("failed to init route")
	}

	return route
}

func GetCurrentRouteState(ctx context.Context, store *dagger.Store) *dagger.RouteState {
	lg := log.Ctx(ctx)

	routeName := viper.GetString("route")
	if routeName != "" {
		st, err := store.LookupRouteByName(ctx, routeName)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Str("routeName", routeName).
				Msg("failed to lookup route by name")
		}
		return st
	}

	wd, err := os.Getwd()
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get current working directory")
	}
	st, err := store.LookupRouteByPath(ctx, wd)
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Str("routePath", wd).
			Msg("failed to lookup route by path")
	}
	return st
}

func RouteUp(ctx context.Context, route *dagger.Route) {
	lg := log.Ctx(ctx)

	c, err := dagger.NewClient(ctx, "")
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to create client")
	}
	output, err := c.Up(ctx, route)
	if err != nil {
		lg.Fatal().Err(err).Msg("failed to compute")
	}
	fmt.Println(output.JSON())
}
