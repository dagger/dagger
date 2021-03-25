package cmd

import (
	"fmt"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var queryCmd = &cobra.Command{
	Use:   "query [EXPR] [flags]",
	Short: "Query the contents of a route",
	Args:  cobra.ExactArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()
		// nolint:staticcheck
		ctx := lg.WithContext(cmd.Context())

		routeName := getRouteName(lg, cmd)
		route, err := dagger.LookupRoute(ctx, routeName, nil)
		if err != nil {
			lg.Fatal().Err(err).Str("route-name", routeName).Msg("failed to lookup route")
		}

		expr := args[0]

		out, err := route.Query(ctx, expr, nil)
		if err != nil {
			lg.Fatal().Err(err).Str("route-name", routeName).Str("route-id", route.ID()).Msg("failed to query route")
		}

		fmt.Println(out)
		// TODO: Implement options: --no-*, --format, --revision

	},
}

func init() {
	queryCmd.Flags().String("revision", "latest", "Query a specific version of the route")
	queryCmd.Flags().StringP("format", "f", "", "Output format (json|yaml|cue|text|env)")

	queryCmd.Flags().BoolP("--no-input", "I", false, "Exclude inputs from query")
	queryCmd.Flags().BoolP("--no-output", "O", false, "Exclude outputs from query")
	queryCmd.Flags().BoolP("--no-layout", "L", false, "Exclude outputs from query")

	if err := viper.BindPFlags(queryCmd.Flags()); err != nil {
		panic(err)
	}
}
