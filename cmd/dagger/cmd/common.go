package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/go/dagger"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// getRouteName returns the selected route name (based on explicit CLI selection or current work dir)
func getRouteName(ctx context.Context, cmd *cobra.Command) string {
	lg := log.Ctx(ctx)

	routeName, err := cmd.Flags().GetString("route")
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Str("flag", "route").
			Msg("unable to resolve flag")
	}

	if routeName != "" {
		return routeName
	}

	workDir, err := os.Getwd()
	if err != nil {
		lg.
			Fatal().
			Err(err).
			Msg("failed to get current working dir")
	}

	currentDir := filepath.Base(workDir)
	if currentDir == "/" {
		return "root"
	}

	return currentDir
}

func routeUp(ctx context.Context, route *dagger.Route) {
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
