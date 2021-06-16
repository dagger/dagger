package key

import (
	"fmt"
	"strings"

	"go.dagger.io/dagger/cmd/dagger/cmd/common"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/keychain"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var importCmd = &cobra.Command{
	Use:   "import <private key>",
	Short: "Import an encryption key",
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

		privateKey := strings.TrimSpace(common.ReadInput(ctx, args[0]))

		k, err := keychain.Import(ctx, privateKey)
		if err != nil {
			lg.Fatal().Err(err).Msg("failed to import key")
		}
		fmt.Printf("Public key: %s\n", k.Recipient().String())
	},
}

func init() {
	importCmd.Flags().BoolP("file", "f", false, "Read value from file")

	if err := viper.BindPFlags(listCmd.Flags()); err != nil {
		panic(err)
	}
}
