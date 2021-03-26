package input

import (
	"dagger.io/go/cmd/dagger/logger"
	"dagger.io/go/dagger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var dirCmd = &cobra.Command{
	Use:   "dir TARGET PATH",
	Short: "Add a local directory as input artifact",
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

		updateRouteInput(ctx, args[0], dagger.DirInput(args[1], []string{}))
	},
}

func init() {
	if err := viper.BindPFlags(dirCmd.Flags()); err != nil {
		panic(err)
	}
}
