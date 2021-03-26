package cmd

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var newCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new route",
	Args:  cobra.NoArgs,
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		ctx := lg.WithContext(cmd.Context())
		store, err := dagger.DefaultStore()
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to load store")
		}

		st := &dagger.RouteState{
			Name:         getNewRouteName(ctx),
			LayoutSource: getLayoutSource(ctx),
		}

		err = store.CreateRoute(ctx, st)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to create route")
		}
		lg.
			Info().
			Str("routeId", st.ID).
			Str("routeName", st.Name).
			Msg("route created")

		route, err := dagger.NewRoute(st)
		if err != nil {
			lg.
				Fatal().
				Err(err).
				Msg("failed to initialize route")
		}

		if viper.GetBool("up") {
			routeUp(ctx, route)
		}
	},
}

func getNewRouteName(ctx context.Context) string {
	lg := log.Ctx(ctx)

	routeName := viper.GetString("route")
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

// FIXME: Implement options: --layout-*
func getLayoutSource(ctx context.Context) dagger.Input {
	lg := log.Ctx(ctx)

	wd, err := os.Getwd()
	if err != nil {
		lg.Fatal().Err(err).Msg("cannot get current working directory")
	}

	return dagger.DirInput(wd, []string{"*.cue", "cue.mod"})
}

func init() {
	newCmd.Flags().StringP("name", "n", "", "Specify a route name")
	newCmd.Flags().BoolP("up", "u", false, "Bring the route online")

	newCmd.Flags().String("layout-dir", "", "Load layout from a local directory")
	newCmd.Flags().String("layout-git", "", "Load layout from a git repository")
	newCmd.Flags().String("layout-package", "", "Load layout from a cue package")
	newCmd.Flags().String("layout-file", "", "Load layout from a cue or json file")

	newCmd.Flags().String("setup", "auto", "Specify whether to prompt user for initial setup (no|yes|auto)")

	if err := viper.BindPFlags(newCmd.Flags()); err != nil {
		panic(err)
	}
}
