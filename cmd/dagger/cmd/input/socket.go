package input

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/state"
)

var socketCmd = &cobra.Command{
	Use:   "socket <TARGET> <UNIX path>",
	Short: "Add a socket input",
	Args:  cobra.ExactArgs(2),
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

		unix := args[1]

		st, err := os.Stat(unix)
		if err != nil {
			lg.Fatal().Err(err).Str("path", unix).Msg("invalid unix socket")
		}

		if st.Mode()&os.ModeSocket == 0 {
			lg.Fatal().Str("path", unix).Msg("not a unix socket")
		}

		updateEnvironmentInput(
			ctx,
			cmd,
			args[0],
			state.SocketInput(unix),
		)
	},
}

func init() {
	if err := viper.BindPFlags(boolCmd.Flags()); err != nil {
		panic(err)
	}
}
