package input

import (
	"fmt"
	"syscall"

	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger/state"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var secretCmd = &cobra.Command{
	Use:   "secret <TARGET> [-f] [<VALUE|PATH>]",
	Short: "Add an encrypted input secret",
	Args:  cobra.RangeArgs(1, 2),
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

		var secret string
		if len(args) == 1 {
			// No value specified: prompt terminal
			fmt.Print("Secret: ")
			data, err := term.ReadPassword(syscall.Stdin)
			if err != nil {
				lg.Fatal().Err(err).Msg("unable to read secret from terminal")
			}
			fmt.Println("")
			secret = string(data)
		} else {
			// value specified: read it
			secret = readInput(ctx, args[1])
		}

		updateEnvironmentInput(
			ctx,
			args[0],
			state.SecretInput(secret),
		)
	},
}

func init() {
	secretCmd.Flags().BoolP("file", "f", false, "Read value from file")

	if err := viper.BindPFlags(secretCmd.Flags()); err != nil {
		panic(err)
	}
}
