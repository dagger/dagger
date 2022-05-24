package cmd

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/api"
	"go.dagger.io/dagger/cmd/dagger/logger"
)

// TODO: remove as soon as there is a higher-level integration with the API
var apiTestCmd = &cobra.Command{
	Use:    "_api_test",
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

		apiURL, err := url.Parse(viper.GetString("url"))
		if err != nil {
			panic(err)
		}
		apiURL.Path = "/private"

		log.Ctx(ctx).Info().Msg(fmt.Sprintf("Testing %s", apiURL))

		c := api.New()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
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
		fmt.Printf("%s\n", body)
	},
}

func init() {
	apiTestCmd.Flags().StringP("url", "u", "http://localhost:8020", "API base URL, e.g. https://api.dagger.io")

	if err := viper.BindPFlags(docCmd.Flags()); err != nil {
		panic(err)
	}
}
