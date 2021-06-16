package key

import (
	"fmt"

	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/keychain"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var exportCmd = &cobra.Command{
	Use:   "export <public key>",
	Short: "Export a private key",
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
		ctx := lg.WithContext(cmd.Context())

		key, err := keychain.Get(ctx, args[0])
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to list keys")
		}

		fmt.Println(key.String())
	},
}

func init() {
	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
