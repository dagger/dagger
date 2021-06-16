package key

import (
	"fmt"

	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/keychain"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a new encryption key",
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

		k, err := keychain.Generate(ctx)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to generate key")
		}
		fmt.Printf("Public key: %s\n", k.Recipient().String())
	},
}

func init() {
	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
