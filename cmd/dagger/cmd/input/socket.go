package input

import (
	"context"
	"os"
	"runtime"

	"github.com/rs/zerolog/log"
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

		updateEnvironmentInput(
			ctx,
			cmd,
			args[0],
			state.SocketInput(detectStreamType(ctx, args[1])),
		)
	},
}

func detectStreamType(ctx context.Context, path string) (string, string) {
	lg := log.Ctx(ctx)

	if runtime.GOOS == "windows" {
		// support the unix format for convenience
		if path == "/var/run/docker.sock" || path == "\\var\\run\\docker.sock" {
			path = "\\\\.\\pipe\\docker_engine"
			lg.Info().Str("path", path).Msg("Windows detected, override unix socket path")
		}

		return path, "npipe"
	}

	st, err := os.Stat(path)
	if err != nil {
		lg.Fatal().Err(err).Str("path", path).Msg("invalid unix socket")
	}

	if st.Mode()&os.ModeSocket == 0 {
		lg.Fatal().Str("path", path).Msg("not a unix socket")
	}

	return path, "unix"
}

func init() {
	if err := viper.BindPFlags(boolCmd.Flags()); err != nil {
		panic(err)
	}
}
