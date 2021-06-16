package key

import (
	"fmt"

	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/keychain"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the available encryption keys",
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

		keys, err := keychain.List(ctx)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to list keys")
		}

		for _, k := range keys {
			fmt.Println(k.Recipient().String())
		}
	},
}

func init() {
	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
