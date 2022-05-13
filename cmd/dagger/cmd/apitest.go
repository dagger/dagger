package cmd

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/api"
	"go.dagger.io/dagger/cmd/dagger/logger"
)

// FIXME: THIS IS A TEMPORARY TEST FILE, NEEDS TO BE REMOVED.
var apitestCmd = &cobra.Command{
	Use:    "_api_test",
	Short:  "TEST COMMAND",
	Hidden: true,
	Args:   cobra.NoArgs,
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

		log.Ctx(ctx).Info().Msg("sending test request")

		c := api.New()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:8020/private", nil)
		if err != nil {
			panic(err)
		}

		resp, err := c.Do(ctx, req)
		if err != nil {
			log.Ctx(ctx).Fatal().Err(err).Msg("request failed")
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s", body)
	},
}
